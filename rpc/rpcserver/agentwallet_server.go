package rpcserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	pb "github.com/btcsuite/btcwallet/rpc/agentwalletrpc"
	"github.com/btcsuite/btcwallet/waddrmgr"
	"github.com/btcsuite/btcwallet/wallet"
	"github.com/btcsuite/btcwallet/wallet/txrules"
	"github.com/btcsuite/btcwallet/wtxmgr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultAgentWalletID               = "default"
	defaultReservationTTLSeconds int64 = 300
	defaultExpiryResultLimit           = 100
	defaultListOperationsLimit         = 100

	operationKindRenewPreview = "renewal_preview"
	operationKindRenewSign    = "renewal_sign"
	operationKindRenewPublish = "renewal_publish"
	operationKindRenewSubmit  = "renewal_submit"
	operationStateDraft       = "DRAFT"
	operationStateSigned      = "SIGNED"
	operationStatePublished   = "PUBLISHED"

	expiryPolicySourceRequestOverride = "request_override"

	operationActionPreviewCreated = "preview_created"
	operationActionPsbtSigned     = "psbt_signed"
	operationActionTxPublished    = "transaction_published"

	decisionLogStagePreview = "preview"
	decisionLogStageSign    = "sign"
	decisionLogStagePublish = "publish"

	signerProofTypePsbtFinalize       = "psbt_finalize"
	signerProofTypeExternalSignedPsbt = "external_signed_psbt"
)

// AgentExpiryPolicyProvider resolves the expiry policy used by the agent API.
type AgentExpiryPolicyProvider interface {
	PolicyForWallet(wallet *wallet.Wallet) (agentExpiryPolicy, []string, error)
}

// AgentWalletOptions configures the agent gRPC service.
type AgentWalletOptions struct {
	ExpiryPolicyProvider AgentExpiryPolicyProvider
	SignerBackend        AgentSignerBackend
}

type agentExpiryPolicy struct {
	WindowBlocks             uint64
	ExpiringThresholdBlocks  int32
	RenewWarningBlocks       int32
	DustThresholdSat         int64
	ProjectedReclaimRatioBps uint32
	Source                   string
}

type compatibilityExpiryPolicyProvider struct{}

func (compatibilityExpiryPolicyProvider) PolicyForWallet(
	w *wallet.Wallet) (agentExpiryPolicy, []string, error) {

	resolvedPolicy, warnings := wallet.ResolveExpiryPolicy(w.ChainParams())
	return agentExpiryPolicy{
		WindowBlocks:             resolvedPolicy.WindowBlocks,
		ExpiringThresholdBlocks:  resolvedPolicy.ExpiringThresholdBlocks,
		RenewWarningBlocks:       resolvedPolicy.RenewWarningBlocks,
		DustThresholdSat:         resolvedPolicy.DustThresholdSat,
		ProjectedReclaimRatioBps: resolvedPolicy.ProjectedReclaimRatioBps,
		Source:                   resolvedPolicy.Source,
	}, warnings, nil
}

type agentWalletServer struct {
	pb.UnimplementedAgentWalletServiceServer

	wallet               *wallet.Wallet
	expiryPolicyProvider AgentExpiryPolicyProvider
	signerBackend        AgentSignerBackend
	persistence          *agentWalletPersistentStore

	mu                  sync.Mutex
	nextOperationID     uint64
	nextReservationID   uint64
	nextEventID         uint64
	nextDecisionLogID   uint64
	nextCapabilityID    uint64
	nextSignerSessionID uint64
	operations          map[string]*pb.Operation
	artifacts           map[string]*agentOperationArtifacts
	reservations        map[string]*agentReservationRecord
	capabilities        map[string]*agentCapabilityRecord
	signerSessions      map[string]*agentSignerSessionRecord
	persistenceLoadOnce sync.Once
	persistenceLoadErr  error
}

// StartAgentWalletService registers AgentWalletService on the gRPC server.
func StartAgentWalletService(server *grpc.Server, wallet *wallet.Wallet) {
	StartAgentWalletServiceWithOptions(server, wallet, AgentWalletOptions{})
}

// StartAgentWalletServiceWithOptions registers the agent wallet gRPC service.
func StartAgentWalletServiceWithOptions(server *grpc.Server,
	wallet *wallet.Wallet, opts AgentWalletOptions) {

	if opts.ExpiryPolicyProvider == nil {
		opts.ExpiryPolicyProvider = compatibilityExpiryPolicyProvider{}
	}
	if opts.SignerBackend == nil {
		if wallet.Manager.WatchOnly() {
			opts.SignerBackend = newPublishOnlyAgentSignerBackend()
		} else {
			opts.SignerBackend = newLocalAgentSignerBackend(wallet)
		}
	}

	service := &agentWalletServer{
		wallet:               wallet,
		expiryPolicyProvider: opts.ExpiryPolicyProvider,
		signerBackend:        opts.SignerBackend,
		persistence:          newAgentWalletPersistentStore(wallet.Database()),
		operations:           make(map[string]*pb.Operation),
		artifacts:            make(map[string]*agentOperationArtifacts),
		reservations:         make(map[string]*agentReservationRecord),
		capabilities:         make(map[string]*agentCapabilityRecord),
		signerSessions:       make(map[string]*agentSignerSessionRecord),
	}
	pb.RegisterAgentWalletServiceServer(server, service)
}

func (s *agentWalletServer) GetWalletState(_ context.Context,
	req *pb.GetWalletStateRequest) (*pb.GetWalletStateResponse, error) {

	syncedTo := s.wallet.SyncedTo()
	return &pb.GetWalletStateResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
		},
		State: &pb.WalletState{
			WalletId:           normalizeWalletID(req.GetWalletId()),
			ActiveNetwork:      uint32(s.wallet.ChainParams().Net),
			Loaded:             true,
			ChainSynced:        s.wallet.ChainSynced(),
			Locked:             s.wallet.Locked(),
			CurrentBlockHeight: syncedTo.Height,
			CurrentBlockHash:   syncedTo.Hash[:],
			SignerBackend:      s.signerBackend.Info(),
		},
	}, nil
}

func (s *agentWalletServer) ListUtxos(_ context.Context,
	req *pb.ListUtxosRequest) (*pb.ListUtxosResponse, error) {

	if err := validateAccountAndMinConfs(
		req.AccountNumber, req.MinConfirmations,
	); err != nil {
		return nil, err
	}

	outputs, err := s.wallet.UnspentOutputs(wallet.OutputSelectionPolicy{
		Account:               req.AccountNumber,
		RequiredConfirmations: req.MinConfirmations,
	})
	if err != nil {
		return nil, translateError(err)
	}

	leasedByOutpoint, err := s.leasedOutputsByOutpoint()
	if err != nil {
		return nil, translateError(err)
	}

	utxos := make([]*pb.Utxo, 0, len(outputs))
	for _, output := range outputs {
		addr := extractFirstAddress(output.Output.PkScript, s.wallet.ChainParams())
		lease, leased := leasedByOutpoint[output.OutPoint.String()]

		utxos = append(utxos, &pb.Utxo{
			Outpoint:           output.OutPoint.String(),
			AmountSat:          output.Output.Value,
			FromCoinbase:       output.OutputKind == wallet.OutputKindCoinbase,
			BlockHeight:        output.ContainingBlock.Height,
			ReceiveTimeUnix:    output.ReceiveTime.Unix(),
			Address:            addr,
			Leased:             leased,
			LeaseExpiresAtUnix: leaseExpirationUnix(lease, leased),
		})
	}

	return &pb.ListUtxosResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
		},
		Utxos: utxos,
	}, nil
}

func (s *agentWalletServer) GetExpiryRisk(_ context.Context,
	req *pb.GetExpiryRiskRequest) (*pb.GetExpiryRiskResponse, error) {

	if err := s.ensureChainSynced(); err != nil {
		return nil, err
	}
	if err := validateAccountAndMinConfs(
		req.AccountNumber, req.MinConfirmations,
	); err != nil {
		return nil, err
	}

	outputs, err := s.lookupOutputs(
		req.AccountNumber, req.MinConfirmations, req.Outpoints, true,
	)
	if err != nil {
		return nil, err
	}

	effectivePolicy, warnings, err := s.resolveExpiryPolicy(req.Policy)
	if err != nil {
		return nil, err
	}

	var beforeHeight *int32
	if req.BeforeHeight > 0 {
		beforeHeight = &req.BeforeHeight
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = defaultExpiryResultLimit
	}

	tipHeight := s.wallet.SyncedTo().Height
	items, err := buildExpiryRiskItems(
		outputs, tipHeight, effectivePolicy, limit, beforeHeight,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to build expiry risk view: %v", err)
	}

	return &pb.GetExpiryRiskResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
			Warnings:  warnings,
		},
		TipHeight:       tipHeight,
		EffectivePolicy: toPBExpiryPolicy(effectivePolicy),
		Items:           items,
	}, nil
}

func (s *agentWalletServer) PreviewRenewal(_ context.Context,
	req *pb.PreviewRenewalRequest) (*pb.PreviewRenewalResponse, error) {

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}
	if err := s.ensureChainSynced(); err != nil {
		return nil, err
	}
	if err := validateAccountAndMinConfs(
		req.AccountNumber, req.MinConfirmations,
	); err != nil {
		return nil, err
	}
	if req.TargetAmountSat <= 0 {
		return nil, status.Error(codes.InvalidArgument,
			"target_amount_sat must be > 0")
	}

	selectedOutputs, err := s.lookupOutputs(
		req.AccountNumber, req.MinConfirmations, req.Outpoints, false,
	)
	if err != nil {
		return nil, err
	}

	selectedOutpoints := make([]wire.OutPoint, 0, len(selectedOutputs))
	for _, output := range selectedOutputs {
		selectedOutpoints = append(selectedOutpoints, output.OutPoint)
	}

	if err := s.validateRequestedReservation(
		selectedOutpoints, req.ReservationId,
	); err != nil {
		return nil, err
	}

	targetAddr, targetAddressWarning, err := s.resolveRenewalTargetAddress(
		req.AccountNumber, req.TargetAddress,
	)
	if err != nil {
		return nil, err
	}

	pkScript, err := txscript.PayToAddrScript(targetAddr)
	if err != nil {
		return nil, translateError(err)
	}

	inputs := make([]*wire.OutPoint, 0, len(selectedOutpoints))
	sequences := make([]uint32, 0, len(selectedOutpoints))
	for i := range selectedOutpoints {
		outpoint := selectedOutpoints[i]
		inputs = append(inputs, &outpoint)
		sequences = append(sequences, wire.MaxTxInSequenceNum)
	}

	packet, err := psbt.New(
		inputs,
		[]*wire.TxOut{{
			Value:    req.TargetAmountSat,
			PkScript: pkScript,
		}},
		wire.TxVersion,
		0,
		sequences,
	)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"unable to create renewal PSBT skeleton: %v", err)
	}

	feeRate := txrules.DefaultRelayFeePerKb
	if req.MaxFeeRateSatPerKb > 0 {
		feeRate = btcutil.Amount(req.MaxFeeRateSatPerKb)
	}

	changeIndex, err := s.wallet.FundPsbt(
		packet,
		nil, // Allow selected outpoints from any key scope in the account.
		req.MinConfirmations,
		req.AccountNumber,
		feeRate,
		wallet.CoinSelectionLargest,
	)
	if err != nil {
		return nil, translateError(err)
	}

	summary, err := summarizePsbt(
		packet, changeIndex, pkScript, targetAddr.EncodeAddress(),
		req.TargetAmountSat, int64(feeRate),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to summarize renewal PSBT: %v", err)
	}

	rawPSBT, b64PSBT, err := serializePsbt(packet)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to serialize renewal PSBT: %v", err)
	}

	effectivePolicy, warnings, err := s.resolveExpiryPolicy(req.ExpiryPolicy)
	if err != nil {
		return nil, err
	}

	tipHeight := s.wallet.SyncedTo().Height
	expiryRisks, err := buildExpiryRiskItems(
		selectedOutputs, tipHeight, effectivePolicy, len(selectedOutputs), nil,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to analyze renewal expiry risk: %v", err)
	}

	if targetAddressWarning != "" {
		warnings = append(warnings, targetAddressWarning)
	}
	if !req.DryRun {
		warnings = append(warnings,
			"PreviewRenewal is always dry-run and does not publish transactions")
	}
	warnings = append(warnings, warningsFromExpiryRisks(expiryRisks)...)

	now := time.Now().Unix()

	s.mu.Lock()
	operationID := s.newOperationIDLocked()
	policySnapshot := newPolicySnapshot(
		policyVerdictFromExpiryRisks(expiryRisks), effectivePolicy,
		expiryRisks, warnings, tipHeight, summary.TargetAmountSat,
		summary.FeeRateSatPerKb, req.MinConfirmations, req.ReservationId,
	)
	op := &pb.Operation{
		OperationId:   operationID,
		Kind:          operationKindRenewPreview,
		State:         operationStateDraft,
		WalletId:      normalizeWalletID(req.GetWalletId()),
		AccountNumber: req.AccountNumber,
		Outpoints:     outpointStrings(selectedOutpoints),
		PolicyVerdict: policyVerdictFromExpiryRisks(expiryRisks),
		Warnings:      append([]string(nil), warnings...),
		CreatedAtUnix: now,
		UpdatedAtUnix: now,
		ReservationId: req.ReservationId,
		Summary:       cloneSummary(summary),
		ExpiryRisks:   cloneExpiryRisks(expiryRisks),
		EffectivePolicy: toPBExpiryPolicy(
			effectivePolicy,
		),
		MinConfirmations:     req.MinConfirmations,
		CreatorPrincipal:     req.GetMeta().GetPrincipal(),
		CreatorCapabilityId:  req.GetMeta().GetCapabilityId(),
		CreateIdempotencyKey: req.GetMeta().GetIdempotencyKey(),
		LatestPolicySnapshot: clonePolicySnapshot(policySnapshot),
		History: []*pb.OperationEvent{
			s.newOperationEventLocked(
				req.GetMeta(),
				operationActionPreviewCreated,
				"",
				operationStateDraft,
				warnings,
				"",
				now,
			),
		},
		DecisionLog: []*pb.DecisionLogEntry{
			s.newDecisionLogEntryLocked(
				req.GetMeta(), decisionLogStagePreview,
				buildPreviewDecisionReasons(opSummaryDecisionContext{
					outpointCount:      len(selectedOutpoints),
					policySource:       effectivePolicy.Source,
					reservationID:      req.ReservationId,
					targetAmountSat:    summary.TargetAmountSat,
					feeRateSatPerKB:    summary.FeeRateSatPerKb,
					minConfirmations:   req.MinConfirmations,
					targetAddress:      summary.TargetAddress,
					walletDryRunNotice: true,
				}),
				warnings, tipHeight, "", nil, policySnapshot, now,
			),
		},
	}
	artifacts := &agentOperationArtifacts{
		OperationID:  operationID,
		UnsignedPsbt: append([]byte(nil), rawPSBT...),
	}
	persistErr := s.persistence.putOperationBundle(op, artifacts)
	if persistErr == nil {
		s.operations[operationID] = cloneOperation(op)
		s.artifacts[operationID] = cloneOperationArtifacts(artifacts)
	}
	s.mu.Unlock()
	if persistErr != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to persist renewal operation: %v", persistErr)
	}

	return &pb.PreviewRenewalResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
			Warnings:  warnings,
		},
		Operation:       cloneOperation(op),
		UnsignedPsbt:    rawPSBT,
		UnsignedPsbtB64: b64PSBT,
		Summary:         cloneSummary(summary),
		EffectivePolicy: toPBExpiryPolicy(effectivePolicy),
		ExpiryRisks:     cloneExpiryRisks(expiryRisks),
	}, nil
}

func (s *agentWalletServer) SignPsbt(_ context.Context,
	req *pb.SignPsbtRequest) (*pb.SignPsbtResponse, error) {

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}
	if req.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument,
			"operation_id must not be empty")
	}

	op, artifacts, warnings, err := s.signRenewalOperation(
		req.OperationId, operationKindRenewSign, req.GetMeta(),
		req.GetSignerSessionId(), capabilityPermissionRenewalSign,
		capabilityPermissionRenewalSubmit,
	)
	if err != nil {
		return nil, err
	}

	signedPsbtB64, err := psbtB64FromBytes(artifacts.SignedPsbt)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to encode signed PSBT: %v", err)
	}

	return &pb.SignPsbtResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
			Warnings:  warnings,
		},
		Operation:         cloneOperation(op),
		SignedPsbt:        append([]byte(nil), artifacts.SignedPsbt...),
		SignedPsbtB64:     signedPsbtB64,
		SignedTransaction: append([]byte(nil), artifacts.SignedTransaction...),
		Summary:           cloneSummary(op.Summary),
	}, nil
}

func (s *agentWalletServer) PublishTransaction(_ context.Context,
	req *pb.PublishTransactionRequest) (*pb.PublishTransactionResponse, error) {

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}
	if req.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument,
			"operation_id must not be empty")
	}

	op, artifacts, warnings, err := s.publishRenewalOperation(
		req.OperationId, req.SignedPsbt, req.Label,
		operationKindRenewPublish, "agentwallet.publish_transaction",
		req.GetMeta(), req.GetSignerSessionId(),
		capabilityPermissionRenewalPublish, capabilityPermissionRenewalSubmit,
	)
	if err != nil {
		return nil, err
	}

	return &pb.PublishTransactionResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
			Warnings:  warnings,
		},
		Operation:       cloneOperation(op),
		Txid:            op.Txid,
		RawTransaction:  append([]byte(nil), artifacts.SignedTransaction...),
		Summary:         cloneSummary(op.Summary),
		EffectivePolicy: cloneExpiryPolicy(op.EffectivePolicy),
		ExpiryRisks:     cloneExpiryRisks(op.ExpiryRisks),
	}, nil
}

func (s *agentWalletServer) SubmitRenewal(_ context.Context,
	req *pb.SubmitRenewalRequest) (*pb.SubmitRenewalResponse, error) {

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}
	if req.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument,
			"operation_id must not be empty")
	}

	signWarnings := []string(nil)
	op, _, err := s.loadRenewalOperation(req.OperationId)
	if err != nil {
		return nil, err
	}
	if op.State == operationStateDraft {
		_, _, signWarnings, err = s.signRenewalOperation(
			req.OperationId, operationKindRenewSubmit, req.GetMeta(),
			req.GetSignerSessionId(), capabilityPermissionRenewalSubmit,
		)
		if err != nil {
			return nil, err
		}
	}

	op, artifacts, publishWarnings, err := s.publishRenewalOperation(
		req.OperationId, nil, req.Label, operationKindRenewSubmit,
		"agentwallet.submit_renewal", req.GetMeta(),
		req.GetSignerSessionId(), capabilityPermissionRenewalSubmit,
	)
	if err != nil {
		return nil, err
	}

	warnings := mergeWarnings(signWarnings, publishWarnings)
	return &pb.SubmitRenewalResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
			Warnings:  warnings,
		},
		Operation:       cloneOperation(op),
		Txid:            op.Txid,
		RawTransaction:  append([]byte(nil), artifacts.SignedTransaction...),
		Summary:         cloneSummary(op.Summary),
		EffectivePolicy: cloneExpiryPolicy(op.EffectivePolicy),
		ExpiryRisks:     cloneExpiryRisks(op.ExpiryRisks),
	}, nil
}

func (s *agentWalletServer) ReserveUtxos(_ context.Context,
	req *pb.ReserveUtxosRequest) (*pb.ReserveUtxosResponse, error) {

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}
	if err := validateAccountAndMinConfs(
		req.AccountNumber, req.MinConfirmations,
	); err != nil {
		return nil, err
	}

	selectedOutputs, err := s.lookupOutputs(
		req.AccountNumber, req.MinConfirmations, req.Outpoints, false,
	)
	if err != nil {
		return nil, err
	}

	ttlSeconds := req.TtlSeconds
	if ttlSeconds <= 0 {
		ttlSeconds = defaultReservationTTLSeconds
	}

	s.mu.Lock()
	reservationID := s.newReservationIDLocked()
	s.mu.Unlock()

	lockID := reservationLockID(reservationID)
	duration := time.Duration(ttlSeconds) * time.Second

	acquired := make([]wire.OutPoint, 0, len(selectedOutputs))
	var expiresAt time.Time
	for _, output := range selectedOutputs {
		expiry, err := s.wallet.LeaseOutput(lockID, output.OutPoint, duration)
		if err != nil {
			releaseErr := releaseLeasedOutpoints(s.wallet, lockID, acquired)
			if releaseErr != nil {
				return nil, status.Errorf(codes.Internal,
					"reservation failed for %s: %v (rollback failed: %v)",
					output.OutPoint, err, releaseErr)
			}
			return nil, reservationError(output.OutPoint, err)
		}

		if expiry.After(expiresAt) {
			expiresAt = expiry
		}
		acquired = append(acquired, output.OutPoint)
	}

	now := time.Now().Unix()
	record := &agentReservationRecord{
		ReservationID: reservationID,
		WalletID:      normalizeWalletID(req.GetWalletId()),
		AccountNumber: req.AccountNumber,
		Outpoints:     outpointStrings(acquired),
		ExpiresAtUnix: expiresAt.Unix(),
		CreatedAtUnix: now,
		UpdatedAtUnix: now,
	}
	if err := s.persistence.putReservation(record); err != nil {
		releaseErr := releaseLeasedOutpoints(s.wallet, lockID, acquired)
		if releaseErr != nil {
			return nil, status.Errorf(codes.Internal,
				"reservation metadata persistence failed: %v (rollback failed: %v)",
				err, releaseErr)
		}
		return nil, status.Errorf(codes.Internal,
			"failed to persist reservation metadata: %v", err)
	}

	s.mu.Lock()
	s.reservations[reservationID] = cloneReservationRecord(record)
	s.mu.Unlock()

	return &pb.ReserveUtxosResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
		},
		ReservationId: reservationID,
		ExpiresAtUnix: expiresAt.Unix(),
		Outpoints:     outpointStrings(acquired),
	}, nil
}

func (s *agentWalletServer) ReleaseReservation(_ context.Context,
	req *pb.ReleaseReservationRequest) (*pb.ReleaseReservationResponse, error) {

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}
	if req.ReservationId == "" {
		return nil, status.Error(codes.InvalidArgument,
			"reservation_id must not be empty")
	}

	lockID := reservationLockID(req.ReservationId)
	leasedOutputs, err := s.wallet.ListLeasedOutputs()
	if err != nil {
		return nil, translateError(err)
	}

	released := false
	for _, output := range leasedOutputs {
		if output.LockID != lockID {
			continue
		}

		if err := s.wallet.ReleaseOutput(lockID, output.Outpoint); err != nil {
			return nil, translateError(err)
		}
		released = true
	}

	warnings := make([]string, 0, 1)
	if released {
		if err := s.markReservationReleased(
			req.ReservationId, time.Now().Unix(),
		); err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"reservation released but metadata persistence failed: %v",
				err,
			))
		}
	}

	return &pb.ReleaseReservationResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
			Warnings:  warnings,
		},
		ReservationId: req.ReservationId,
		Released:      released,
	}, nil
}

func (s *agentWalletServer) GetOperation(_ context.Context,
	req *pb.GetOperationRequest) (*pb.GetOperationResponse, error) {

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}
	if req.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument,
			"operation_id must not be empty")
	}

	s.mu.Lock()
	op, ok := s.operations[req.OperationId]
	s.mu.Unlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound,
			"operation %s not found", req.OperationId)
	}

	return &pb.GetOperationResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
		},
		Operation: cloneOperation(op),
	}, nil
}

func (s *agentWalletServer) ListOperations(_ context.Context,
	req *pb.ListOperationsRequest) (*pb.ListOperationsResponse, error) {

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}

	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = defaultListOperationsLimit
	}

	newestFirst := true
	if req.NewestFirst != nil {
		newestFirst = req.GetNewestFirst()
	}

	filterWalletID := ""
	if req.GetWalletId() != "" {
		filterWalletID = normalizeWalletID(req.GetWalletId())
	}

	s.mu.Lock()
	operations := make([]*pb.Operation, 0, len(s.operations))
	for _, op := range s.operations {
		if op == nil {
			continue
		}
		if filterWalletID != "" && op.GetWalletId() != filterWalletID {
			continue
		}
		if req.GetState() != "" && op.GetState() != req.GetState() {
			continue
		}
		if req.GetKind() != "" && op.GetKind() != req.GetKind() {
			continue
		}
		if req.GetCreatorPrincipal() != "" &&
			op.GetCreatorPrincipal() != req.GetCreatorPrincipal() {

			continue
		}

		operations = append(operations, cloneOperation(op))
	}
	s.mu.Unlock()

	sort.Slice(operations, func(i, j int) bool {
		left := operations[i]
		right := operations[j]
		leftTime := left.GetUpdatedAtUnix()
		rightTime := right.GetUpdatedAtUnix()
		if leftTime == 0 {
			leftTime = left.GetCreatedAtUnix()
		}
		if rightTime == 0 {
			rightTime = right.GetCreatedAtUnix()
		}
		if leftTime != rightTime {
			if newestFirst {
				return leftTime > rightTime
			}
			return leftTime < rightTime
		}
		if newestFirst {
			return left.GetOperationId() > right.GetOperationId()
		}
		return left.GetOperationId() < right.GetOperationId()
	})

	if len(operations) > limit {
		operations = operations[:limit]
	}

	return &pb.ListOperationsResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
		},
		Operations: operations,
	}, nil
}

func (s *agentWalletServer) ensurePersistenceLoaded() error {
	s.persistenceLoadOnce.Do(func() {
		if s.persistence == nil {
			s.persistenceLoadErr = fmt.Errorf(
				"agent wallet persistence store is not configured",
			)
			return
		}

		operations, reservations, artifacts, capabilities,
			signerSessions, err := s.persistence.load()
		if err != nil {
			s.persistenceLoadErr = err
			return
		}

		s.mu.Lock()
		s.operations = operations
		s.reservations = reservations
		s.artifacts = artifacts
		s.capabilities = capabilities
		s.signerSessions = signerSessions
		s.mu.Unlock()

		if err := s.reconcileRecoveredSignerSessions(); err != nil {
			s.persistenceLoadErr = err
		}
	})

	if s.persistenceLoadErr != nil {
		return status.Errorf(codes.Aborted,
			"agent wallet persistence unavailable: %v",
			s.persistenceLoadErr)
	}

	return nil
}

func (s *agentWalletServer) ensureChainSynced() error {
	if s.wallet == nil {
		return status.Error(codes.FailedPrecondition,
			"wallet is unavailable")
	}
	if s.wallet.ChainSynced() {
		return nil
	}

	var height int32
	if s.wallet.Manager != nil {
		height = s.wallet.SyncedTo().Height
	}

	return status.Errorf(codes.FailedPrecondition,
		"wallet chain state is not synced at height %d",
		height)
}

func (s *agentWalletServer) markReservationReleased(reservationID string,
	releasedAtUnix int64) error {

	if reservationID == "" {
		return nil
	}

	s.mu.Lock()
	record, ok := s.reservations[reservationID]
	s.mu.Unlock()
	if !ok {
		return nil
	}

	updated := cloneReservationRecord(record)
	updated.Released = true
	if updated.ReleasedAtUnix == 0 {
		updated.ReleasedAtUnix = releasedAtUnix
	}
	updated.UpdatedAtUnix = releasedAtUnix

	if err := s.persistence.putReservation(updated); err != nil {
		return err
	}

	s.mu.Lock()
	s.reservations[reservationID] = updated
	s.mu.Unlock()
	return nil
}

func (s *agentWalletServer) loadRenewalOperation(
	operationID string) (*pb.Operation, *agentOperationArtifacts, error) {

	s.mu.Lock()
	op, ok := s.operations[operationID]
	artifacts := s.artifacts[operationID]
	s.mu.Unlock()
	if !ok {
		return nil, nil, status.Errorf(codes.NotFound,
			"operation %s not found", operationID)
	}

	clonedOp := cloneOperation(op)
	if !isRenewalOperationKind(clonedOp.Kind) {
		return nil, nil, status.Errorf(codes.FailedPrecondition,
			"operation %s is not a renewal operation", operationID)
	}
	if clonedOp.Summary == nil {
		return nil, nil, status.Errorf(codes.FailedPrecondition,
			"operation %s is missing renewal summary", operationID)
	}
	if len(clonedOp.Outpoints) == 0 {
		return nil, nil, status.Errorf(codes.FailedPrecondition,
			"operation %s has no selected outpoints", operationID)
	}
	if clonedOp.Summary.TargetAmountSat <= 0 {
		return nil, nil, status.Errorf(codes.FailedPrecondition,
			"operation %s has invalid target amount", operationID)
	}

	return clonedOp, cloneOperationArtifacts(artifacts), nil
}

func (s *agentWalletServer) selectedOutputsForOperation(op *pb.Operation,
	validateReservation bool) ([]*wallet.TransactionOutput, []wire.OutPoint,
	btcutil.Address, []byte, error) {

	targetAddress := op.GetSummary().GetTargetAddress()
	if targetAddress == "" {
		return nil, nil, nil, nil, status.Errorf(codes.FailedPrecondition,
			"operation %s is missing target address", op.OperationId)
	}

	targetAddr, err := decodeAddress(targetAddress, s.wallet.ChainParams())
	if err != nil {
		return nil, nil, nil, nil, status.Errorf(codes.FailedPrecondition,
			"operation %s has invalid target address: %v",
			op.OperationId, err)
	}

	selectedOutputs, err := s.lookupOutputs(
		op.AccountNumber, op.MinConfirmations, op.Outpoints, false,
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	selectedOutpoints := make([]wire.OutPoint, 0, len(selectedOutputs))
	for _, output := range selectedOutputs {
		selectedOutpoints = append(selectedOutpoints, output.OutPoint)
	}

	if validateReservation {
		if err := s.validateRequestedReservation(
			selectedOutpoints, op.ReservationId,
		); err != nil {
			return nil, nil, nil, nil, err
		}
	}

	pkScript, err := txscript.PayToAddrScript(targetAddr)
	if err != nil {
		return nil, nil, nil, nil, translateError(err)
	}

	return selectedOutputs, selectedOutpoints, targetAddr, pkScript, nil
}

func (s *agentWalletServer) buildUnsignedRenewalPsbt(op *pb.Operation,
	selectedOutpoints []wire.OutPoint,
	targetPkScript []byte) (*psbt.Packet, int64, error) {

	inputs := make([]*wire.OutPoint, 0, len(selectedOutpoints))
	sequences := make([]uint32, 0, len(selectedOutpoints))
	for i := range selectedOutpoints {
		outpoint := selectedOutpoints[i]
		inputs = append(inputs, &outpoint)
		sequences = append(sequences, wire.MaxTxInSequenceNum)
	}

	packet, err := psbt.New(
		inputs,
		[]*wire.TxOut{{
			Value:    op.Summary.TargetAmountSat,
			PkScript: targetPkScript,
		}},
		wire.TxVersion,
		0,
		sequences,
	)
	if err != nil {
		return nil, 0, status.Errorf(codes.InvalidArgument,
			"unable to create renewal PSBT skeleton: %v", err)
	}

	feeRate := txrules.DefaultRelayFeePerKb
	if op.Summary.FeeRateSatPerKb > 0 {
		feeRate = btcutil.Amount(op.Summary.FeeRateSatPerKb)
	}

	_, err = s.wallet.FundPsbt(
		packet,
		nil,
		op.MinConfirmations,
		op.AccountNumber,
		feeRate,
		wallet.CoinSelectionLargest,
	)
	if err != nil {
		return nil, 0, translateError(err)
	}

	return packet, int64(feeRate), nil
}

func (s *agentWalletServer) signRenewalOperation(operationID,
	finalKind string, meta *pb.RequestMeta, signerSessionID string,
	requiredPermissions ...string) (*pb.Operation,
	*agentOperationArtifacts, []string, error) {

	op, artifacts, err := s.loadRenewalOperation(operationID)
	if err != nil {
		return nil, nil, nil, err
	}

	if _, _, err := s.requireSignerSessionForOperation(
		op, meta, signerSessionID, requiredPermissions...,
	); err != nil {
		return nil, nil, nil, err
	}

	switch op.State {
	case operationStateSigned:
		if artifacts != nil && len(artifacts.SignedPsbt) > 0 {
			return op, artifacts, []string{
				"operation already signed; returning recorded signed PSBT",
			}, nil
		}
		return nil, nil, nil, status.Errorf(codes.FailedPrecondition,
			"operation %s is marked signed but signed PSBT is unavailable",
			operationID)

	case operationStatePublished:
		if artifacts != nil && len(artifacts.SignedPsbt) > 0 {
			return op, artifacts, []string{
				"operation already published; returning recorded signed PSBT",
			}, nil
		}
		return nil, nil, nil, status.Errorf(codes.FailedPrecondition,
			"operation %s is marked published but signed PSBT is unavailable",
			operationID)

	case operationStateDraft:
		// Continue with signing.

	default:
		return nil, nil, nil, status.Errorf(codes.FailedPrecondition,
			"operation %s is in unsupported state %s",
			operationID, op.State)
	}

	if err := s.ensureChainSynced(); err != nil {
		return nil, nil, nil, err
	}
	if s.wallet.Manager.WatchOnly() {
		return nil, nil, nil, status.Error(codes.FailedPrecondition,
			"wallet is watch-only; use PreviewRenewal and an external signing flow")
	}

	selectedOutputs, selectedOutpoints, _, targetPkScript, err :=
		s.selectedOutputsForOperation(op, true)
	if err != nil {
		return nil, nil, nil, err
	}

	effectivePolicy, warnings, err := s.resolveSubmitExpiryPolicy(op)
	if err != nil {
		return nil, nil, nil, err
	}

	tipHeight := s.wallet.SyncedTo().Height
	expiryRisks, err := buildExpiryRiskItems(
		selectedOutputs, tipHeight, effectivePolicy, len(selectedOutputs), nil,
	)
	if err != nil {
		return nil, nil, nil, status.Errorf(codes.Internal,
			"failed to analyze renewal expiry risk: %v", err)
	}
	warnings = append(warnings, warningsFromExpiryRisks(expiryRisks)...)

	if artifacts == nil {
		artifacts = &agentOperationArtifacts{
			OperationID: operationID,
		}
	}

	var packet *psbt.Packet
	if len(artifacts.UnsignedPsbt) > 0 {
		packet, err = parsePsbtBytes(artifacts.UnsignedPsbt)
		if err != nil {
			return nil, nil, nil, status.Errorf(codes.InvalidArgument,
				"stored unsigned PSBT is invalid: %v", err)
		}
	} else {
		packet, _, err = s.buildUnsignedRenewalPsbt(
			op, selectedOutpoints, targetPkScript,
		)
		if err != nil {
			return nil, nil, nil, err
		}

		unsignedPsbt, _, err := serializePsbt(packet)
		if err != nil {
			return nil, nil, nil, status.Errorf(codes.Internal,
				"failed to serialize unsigned PSBT: %v", err)
		}
		artifacts.UnsignedPsbt = unsignedPsbt
	}

	if err := s.signerBackend.FinalizePsbt(
		signerSessionID, op.AccountNumber, packet,
	); err != nil {
		if status.Code(err) != codes.Unknown {
			return nil, nil, nil, err
		}
		return nil, nil, nil, translateError(err)
	}
	if err := s.wallet.ValidateFinalizedPsbt(packet); err != nil {
		return nil, nil, nil, status.Errorf(codes.FailedPrecondition,
			"signed PSBT failed local replay-protection validation: %v",
			err)
	}

	signedPsbt, _, err := serializePsbt(packet)
	if err != nil {
		return nil, nil, nil, status.Errorf(codes.Internal,
			"failed to serialize signed PSBT: %v", err)
	}

	tx, err := psbt.Extract(packet)
	if err != nil {
		return nil, nil, nil, status.Errorf(codes.Internal,
			"failed to extract signed transaction: %v", err)
	}

	signedTx, err := serializeTransaction(tx)
	if err != nil {
		return nil, nil, nil, status.Errorf(codes.Internal,
			"failed to serialize signed transaction: %v", err)
	}

	now := time.Now().Unix()
	fromState := op.State
	policyVerdict := policyVerdictFromExpiryRisks(expiryRisks)
	policySnapshot := newPolicySnapshot(
		policyVerdict, effectivePolicy, expiryRisks,
		mergeWarnings(op.Warnings, warnings), tipHeight,
		op.GetSummary().GetTargetAmountSat(), op.GetSummary().GetFeeRateSatPerKb(),
		op.MinConfirmations, op.ReservationId,
	)
	signerProof := s.newSignerProof(
		meta, signerSessionID, signerProofTypePsbtFinalize,
		artifacts.UnsignedPsbt, signedPsbt, signedTx, now,
	)
	op.Kind = finalKind
	op.State = operationStateSigned
	op.PolicyVerdict = policyVerdict
	op.Warnings = mergeWarnings(op.Warnings, warnings)
	op.UpdatedAtUnix = now
	op.ExpiryRisks = cloneExpiryRisks(expiryRisks)
	op.EffectivePolicy = toPBExpiryPolicy(effectivePolicy)
	op.LatestPolicySnapshot = clonePolicySnapshot(policySnapshot)
	op.LatestSignerProof = cloneSignerProof(signerProof)
	s.mu.Lock()
	op.History = append(op.History, s.newOperationEventLocked(
		meta, operationActionPsbtSigned, fromState, operationStateSigned,
		warnings, "", now,
	))
	op.DecisionLog = append(op.DecisionLog, s.newDecisionLogEntryLocked(
		meta, decisionLogStageSign,
		buildSignDecisionReasons(op, signerProof),
		warnings, tipHeight, "", signerProof, policySnapshot, now,
	))
	s.mu.Unlock()

	artifacts.SignedPsbt = signedPsbt
	artifacts.SignedTransaction = signedTx

	if err := s.persistence.putOperationBundle(op, artifacts); err != nil {
		return nil, nil, nil, status.Errorf(codes.Internal,
			"failed to persist signed renewal artifacts: %v", err)
	}

	s.mu.Lock()
	s.operations[operationID] = cloneOperation(op)
	s.artifacts[operationID] = cloneOperationArtifacts(artifacts)
	s.mu.Unlock()

	return cloneOperation(op), cloneOperationArtifacts(artifacts),
		warnings, nil
}

func (s *agentWalletServer) publishRenewalOperation(operationID string,
	signedPsbtOverride []byte, label string, finalKind,
	defaultLabel string, meta *pb.RequestMeta, signerSessionID string,
	requiredPermissions ...string) (*pb.Operation,
	*agentOperationArtifacts, []string, error) {

	op, artifacts, err := s.loadRenewalOperation(operationID)
	if err != nil {
		return nil, nil, nil, err
	}

	if _, err := s.requirePublishAuthorization(
		op, meta, signerSessionID, requiredPermissions...,
	); err != nil {
		return nil, nil, nil, err
	}

	if op.State == operationStatePublished {
		return op, artifacts, []string{
			"operation already published; returning recorded transaction metadata",
		}, nil
	}
	if op.State != operationStateDraft && op.State != operationStateSigned {
		return nil, nil, nil, status.Errorf(codes.FailedPrecondition,
			"operation %s is in unsupported state %s",
			operationID, op.State)
	}

	if err := s.ensureChainSynced(); err != nil {
		return nil, nil, nil, err
	}
	selectedOutputs, selectedOutpoints, targetAddr, targetPkScript, err :=
		s.selectedOutputsForOperation(op, false)
	if err != nil {
		return nil, nil, nil, err
	}

	effectivePolicy, warnings, err := s.resolveSubmitExpiryPolicy(op)
	if err != nil {
		return nil, nil, nil, err
	}

	tipHeight := s.wallet.SyncedTo().Height
	expiryRisks, err := buildExpiryRiskItems(
		selectedOutputs, tipHeight, effectivePolicy, len(selectedOutputs), nil,
	)
	if err != nil {
		return nil, nil, nil, status.Errorf(codes.Internal,
			"failed to analyze renewal expiry risk: %v", err)
	}
	warnings = append(warnings, warningsFromExpiryRisks(expiryRisks)...)

	if artifacts == nil {
		artifacts = &agentOperationArtifacts{
			OperationID: operationID,
		}
	}

	var packet *psbt.Packet
	switch {
	case len(signedPsbtOverride) > 0:
		packet, err = parsePsbtBytes(signedPsbtOverride)
		if err != nil {
			return nil, nil, nil, status.Errorf(codes.InvalidArgument,
				"invalid signed_psbt: %v", err)
		}

	case len(artifacts.SignedPsbt) > 0:
		packet, err = parsePsbtBytes(artifacts.SignedPsbt)
		if err != nil {
			return nil, nil, nil, status.Errorf(codes.InvalidArgument,
				"stored signed PSBT is invalid: %v", err)
		}

	default:
		return nil, nil, nil, status.Errorf(codes.FailedPrecondition,
			"signed PSBT not available; call SignPsbt or provide signed_psbt")
	}

	if err := psbt.MaybeFinalizeAll(packet); err != nil {
		return nil, nil, nil, status.Errorf(codes.FailedPrecondition,
			"signed PSBT is not finalized: %v", err)
	}
	if err := s.wallet.ValidateFinalizedPsbt(packet); err != nil {
		return nil, nil, nil, status.Errorf(codes.FailedPrecondition,
			"signed PSBT failed local replay-protection validation: %v",
			err)
	}

	normalizedSignedPsbt, _, err := serializePsbt(packet)
	if err != nil {
		return nil, nil, nil, status.Errorf(codes.Internal,
			"failed to serialize signed PSBT: %v", err)
	}

	tx, err := psbt.Extract(packet)
	if err != nil {
		return nil, nil, nil, status.Errorf(codes.Internal,
			"failed to extract signed transaction: %v", err)
	}

	rawTx, err := serializeTransaction(tx)
	if err != nil {
		return nil, nil, nil, status.Errorf(codes.Internal,
			"failed to serialize published transaction: %v", err)
	}

	summary, err := summarizePublishedTransaction(
		tx, selectedOutputs, targetPkScript, targetAddr.EncodeAddress(),
		op.Summary.TargetAmountSat, op.Summary.FeeRateSatPerKb,
	)
	if err != nil {
		return nil, nil, nil, status.Errorf(codes.Internal,
			"failed to summarize published renewal transaction: %v", err)
	}

	if label == "" {
		label = defaultLabel
	}
	if err := s.wallet.PublishTransaction(tx, label); err != nil {
		return nil, nil, nil, translateError(err)
	}

	if op.ReservationId != "" {
		if releaseErr := releaseLeasedOutpoints(
			s.wallet, reservationLockID(op.ReservationId), selectedOutpoints,
		); releaseErr != nil {
			warnings = append(warnings, fmt.Sprintf(
				"transaction published but reservation release failed: %v",
				releaseErr,
			))
		} else if persistErr := s.markReservationReleased(
			op.ReservationId, time.Now().Unix(),
		); persistErr != nil {
			warnings = append(warnings, fmt.Sprintf(
				"transaction published but reservation metadata persistence failed: %v",
				persistErr,
			))
		}
	}

	now := time.Now().Unix()
	fromState := op.State
	policyVerdict := policyVerdictFromExpiryRisks(expiryRisks)
	policySnapshot := newPolicySnapshot(
		policyVerdict, effectivePolicy, expiryRisks,
		mergeWarnings(op.Warnings, warnings), tipHeight,
		summary.TargetAmountSat, summary.FeeRateSatPerKb,
		op.MinConfirmations, op.ReservationId,
	)
	signerProof := cloneSignerProof(op.LatestSignerProof)
	if signerProof == nil || len(signedPsbtOverride) > 0 {
		signerProof = s.newSignerProof(
			meta, signerSessionID, signerProofTypeExternalSignedPsbt,
			artifacts.UnsignedPsbt, normalizedSignedPsbt, rawTx, now,
		)
	}
	op.Kind = finalKind
	op.State = operationStatePublished
	op.PolicyVerdict = policyVerdict
	op.Warnings = mergeWarnings(op.Warnings, warnings)
	op.UpdatedAtUnix = now
	op.Summary = cloneSummary(summary)
	op.ExpiryRisks = cloneExpiryRisks(expiryRisks)
	op.EffectivePolicy = toPBExpiryPolicy(effectivePolicy)
	op.LatestPolicySnapshot = clonePolicySnapshot(policySnapshot)
	op.LatestSignerProof = cloneSignerProof(signerProof)
	op.Txid = tx.TxHash().String()
	s.mu.Lock()
	op.History = append(op.History, s.newOperationEventLocked(
		meta, operationActionTxPublished, fromState, operationStatePublished,
		warnings, op.Txid, now,
	))
	op.DecisionLog = append(op.DecisionLog, s.newDecisionLogEntryLocked(
		meta, decisionLogStagePublish,
		buildPublishDecisionReasons(op, len(signedPsbtOverride) > 0),
		warnings, tipHeight, op.Txid, signerProof, policySnapshot, now,
	))
	s.mu.Unlock()

	artifacts.SignedPsbt = normalizedSignedPsbt
	artifacts.SignedTransaction = rawTx

	if err := s.persistence.putOperationBundle(op, artifacts); err != nil {
		warnings = append(warnings, fmt.Sprintf(
			"transaction published but operation persistence failed: %v",
			err,
		))
		op.Warnings = mergeWarnings(op.Warnings, warnings)
	}

	s.mu.Lock()
	s.operations[operationID] = cloneOperation(op)
	s.artifacts[operationID] = cloneOperationArtifacts(artifacts)
	s.mu.Unlock()

	return cloneOperation(op), cloneOperationArtifacts(artifacts),
		warnings, nil
}

func isRenewalOperationKind(kind string) bool {
	switch kind {
	case operationKindRenewPreview,
		operationKindRenewSign,
		operationKindRenewPublish,
		operationKindRenewSubmit:

		return true

	default:
		return false
	}
}

func (s *agentWalletServer) resolveExpiryPolicy(
	override *pb.ExpiryPolicy) (agentExpiryPolicy, []string, error) {

	policy, warnings, err := s.expiryPolicyProvider.PolicyForWallet(s.wallet)
	if err != nil {
		return agentExpiryPolicy{}, nil, translateError(err)
	}

	if override != nil {
		overridden := false
		if override.WindowBlocks > 0 {
			policy.WindowBlocks = override.WindowBlocks
			overridden = true
		}
		if override.ExpiringThresholdBlocks != 0 {
			policy.ExpiringThresholdBlocks = override.ExpiringThresholdBlocks
			overridden = true
		}
		if override.DustThresholdSat != 0 {
			policy.DustThresholdSat = override.DustThresholdSat
			overridden = true
		}
		if override.ProjectedReclaimRatioBps != 0 {
			policy.ProjectedReclaimRatioBps = override.ProjectedReclaimRatioBps
			overridden = true
		}
		if overridden {
			policy.Source = joinPolicySources(
				policy.Source, expiryPolicySourceRequestOverride,
			)
			warnings = append(warnings,
				"request overrides server expiry policy defaults")
		}
	}

	if err := validateExpiryPolicy(policy); err != nil {
		return agentExpiryPolicy{}, nil, status.Errorf(
			codes.InvalidArgument, "invalid expiry policy: %v", err,
		)
	}

	return policy, warnings, nil
}

func (s *agentWalletServer) resolveSubmitExpiryPolicy(
	op *pb.Operation) (agentExpiryPolicy, []string, error) {

	if op != nil && op.EffectivePolicy != nil {
		policy := agentExpiryPolicy{
			WindowBlocks:             op.EffectivePolicy.WindowBlocks,
			ExpiringThresholdBlocks:  op.EffectivePolicy.ExpiringThresholdBlocks,
			RenewWarningBlocks:       wallet.DefaultRenewWarningBlocks,
			DustThresholdSat:         op.EffectivePolicy.DustThresholdSat,
			ProjectedReclaimRatioBps: op.EffectivePolicy.ProjectedReclaimRatioBps,
			Source:                   op.EffectivePolicy.Source,
		}
		if err := validateExpiryPolicy(policy); err == nil {
			return policy, nil, nil
		}
	}

	return s.resolveExpiryPolicy(nil)
}

func (s *agentWalletServer) resolveRenewalTargetAddress(account uint32,
	targetAddress string) (btcutil.Address, string, error) {

	if targetAddress != "" {
		addr, err := decodeAddress(targetAddress, s.wallet.ChainParams())
		if err != nil {
			return nil, "", status.Errorf(codes.InvalidArgument,
				"invalid target_address: %v", err)
		}
		return addr, "", nil
	}

	addr, err := s.wallet.CurrentAddress(account, waddrmgr.KeyScopeBIP0044)
	if err == nil {
		return addr,
			"target_address not provided; preview reused the current wallet address",
			nil
	}

	addr, err = s.wallet.NewAddress(account, waddrmgr.KeyScopeBIP0044)
	if err != nil {
		return nil, "", translateError(err)
	}

	return addr,
		"target_address not provided; preview allocated a new wallet address",
		nil
}

func (s *agentWalletServer) validateRequestedReservation(
	outpoints []wire.OutPoint, reservationID string) error {

	leasedByOutpoint, err := s.leasedOutputsByOutpoint()
	if err != nil {
		return translateError(err)
	}

	if reservationID == "" {
		for _, outpoint := range outpoints {
			lease, ok := leasedByOutpoint[outpoint.String()]
			if !ok {
				continue
			}
			return status.Errorf(codes.Aborted,
				"outpoint %s is reserved until %d; provide reservation_id to use it",
				outpoint, lease.Expiration.Unix())
		}
		return nil
	}

	expectedLockID := reservationLockID(reservationID)
	for _, outpoint := range outpoints {
		lease, ok := leasedByOutpoint[outpoint.String()]
		if !ok {
			return status.Errorf(codes.FailedPrecondition,
				"outpoint %s is not reserved by %s",
				outpoint, reservationID)
		}
		if lease.LockID != expectedLockID {
			return status.Errorf(codes.Aborted,
				"outpoint %s is reserved by another reservation",
				outpoint)
		}
	}

	return nil
}

func (s *agentWalletServer) leasedOutputsByOutpoint() (
	map[string]*wallet.ListLeasedOutputResult, error) {

	leasedOutputs, err := s.wallet.ListLeasedOutputs()
	if err != nil {
		return nil, err
	}

	leasedByOutpoint := make(
		map[string]*wallet.ListLeasedOutputResult, len(leasedOutputs),
	)
	for _, output := range leasedOutputs {
		leasedByOutpoint[output.Outpoint.String()] = output
	}

	return leasedByOutpoint, nil
}

func (s *agentWalletServer) lookupOutputs(account uint32, minConfs int32,
	outpoints []string, includeExpired bool) ([]*wallet.TransactionOutput, error) {

	if err := validateAccountAndMinConfs(account, minConfs); err != nil {
		return nil, err
	}

	outputs, err := s.wallet.UnspentOutputs(wallet.OutputSelectionPolicy{
		Account:               account,
		RequiredConfirmations: minConfs,
		IncludeExpired:        includeExpired,
	})
	if err != nil {
		return nil, translateError(err)
	}

	if len(outpoints) == 0 {
		return outputs, nil
	}

	parsedOutpoints, err := parseOutPointStringsUnique(outpoints)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"invalid outpoints: %v", err)
	}

	outputsByOutpoint := make(map[string]*wallet.TransactionOutput, len(outputs))
	for _, output := range outputs {
		outputsByOutpoint[output.OutPoint.String()] = output
	}

	selectedOutputs := make([]*wallet.TransactionOutput, 0, len(parsedOutpoints))
	for _, outpoint := range parsedOutpoints {
		output, ok := outputsByOutpoint[outpoint.String()]
		if !ok {
			return nil, status.Errorf(codes.NotFound,
				"outpoint %s not found in wallet account %d",
				outpoint, account)
		}
		selectedOutputs = append(selectedOutputs, output)
	}

	return selectedOutputs, nil
}

func (s *agentWalletServer) newOperationIDLocked() string {
	s.nextOperationID++
	return fmt.Sprintf("op_%d_%d", time.Now().UnixNano(), s.nextOperationID)
}

func (s *agentWalletServer) newReservationIDLocked() string {
	s.nextReservationID++
	return fmt.Sprintf("res_%d_%d", time.Now().UnixNano(), s.nextReservationID)
}

func (s *agentWalletServer) newEventIDLocked() string {
	s.nextEventID++
	return fmt.Sprintf("evt_%d_%d", time.Now().UnixNano(), s.nextEventID)
}

func (s *agentWalletServer) newDecisionLogIDLocked() string {
	s.nextDecisionLogID++
	return fmt.Sprintf("dlg_%d_%d", time.Now().UnixNano(),
		s.nextDecisionLogID)
}

func (s *agentWalletServer) newOperationEventLocked(meta *pb.RequestMeta,
	action, fromState, toState string, warnings []string, txid string,
	createdAtUnix int64) *pb.OperationEvent {

	return &pb.OperationEvent{
		EventId:       s.newEventIDLocked(),
		Action:        action,
		FromState:     fromState,
		ToState:       toState,
		RequestId:     requestID(meta),
		Principal:     meta.GetPrincipal(),
		CapabilityId:  meta.GetCapabilityId(),
		Warnings:      append([]string(nil), warnings...),
		CreatedAtUnix: createdAtUnix,
		Txid:          txid,
	}
}

func (s *agentWalletServer) newDecisionLogEntryLocked(meta *pb.RequestMeta,
	stage string, reasons, warnings []string, tipHeight int32, txid string,
	signerProof *pb.SignerProof, policySnapshot *pb.PolicySnapshot,
	createdAtUnix int64) *pb.DecisionLogEntry {

	return &pb.DecisionLogEntry{
		EntryId:         s.newDecisionLogIDLocked(),
		Stage:           stage,
		RequestId:       requestID(meta),
		Principal:       meta.GetPrincipal(),
		CapabilityId:    meta.GetCapabilityId(),
		SignerSessionId: signerSessionIDFromProof(signerProof),
		Verdict:         policySnapshotVerdict(policySnapshot),
		Reasons:         append([]string(nil), reasons...),
		Warnings:        append([]string(nil), warnings...),
		TipHeight:       tipHeight,
		Txid:            txid,
		CreatedAtUnix:   createdAtUnix,
		PolicySnapshot:  clonePolicySnapshot(policySnapshot),
		SignerProof:     cloneSignerProof(signerProof),
	}
}

func (s *agentWalletServer) newSignerProof(meta *pb.RequestMeta,
	signerSessionID, proofType string, unsignedPsbt, signedPsbt,
	signedTx []byte, signedAtUnix int64) *pb.SignerProof {

	info := &pb.SignerBackendInfo{}
	metadata := agentSignerProofMetadata{}
	if s.signerBackend != nil {
		info = s.signerBackend.Info()
		metadata = s.signerBackend.SignerProofMetadata()
	}

	proof := &pb.SignerProof{
		ProofId:            fmt.Sprintf("proof_%d_%s", signedAtUnix, proofType),
		BackendId:          info.GetBackendId(),
		BackendMode:        info.GetMode(),
		BackendDescription: info.GetDescription(),
		RemoteEndpoint:     metadata.RemoteEndpoint,
		SignerSessionId:    signerSessionID,
		CapabilityId:       meta.GetCapabilityId(),
		Principal:          meta.GetPrincipal(),
		ProofType:          proofType,
		UnsignedPsbtSha256: sha256Hex(unsignedPsbt),
		SignedPsbtSha256:   sha256Hex(signedPsbt),
		SignedTxSha256:     sha256Hex(signedTx),
		SignedAtUnix:       signedAtUnix,
	}
	if proof.BackendId == "" && proofType == signerProofTypeExternalSignedPsbt {
		proof.BackendId = "external"
		proof.BackendMode = "external"
		proof.BackendDescription = "signed PSBT supplied externally"
	}
	return proof
}

type opSummaryDecisionContext struct {
	outpointCount      int
	policySource       string
	reservationID      string
	targetAmountSat    int64
	feeRateSatPerKB    int64
	minConfirmations   int32
	targetAddress      string
	walletDryRunNotice bool
}

func buildPreviewDecisionReasons(ctx opSummaryDecisionContext) []string {
	reasons := []string{
		fmt.Sprintf("selected_outpoints=%d", ctx.outpointCount),
		fmt.Sprintf("policy_source=%s", ctx.policySource),
		fmt.Sprintf("target_amount_sat=%d", ctx.targetAmountSat),
		fmt.Sprintf("fee_rate_sat_per_kb=%d", ctx.feeRateSatPerKB),
		fmt.Sprintf("min_confirmations=%d", ctx.minConfirmations),
	}
	if ctx.targetAddress != "" {
		reasons = append(reasons,
			fmt.Sprintf("target_address=%s", ctx.targetAddress))
	}
	if ctx.reservationID != "" {
		reasons = append(reasons,
			fmt.Sprintf("reservation_id=%s", ctx.reservationID))
	}
	if ctx.walletDryRunNotice {
		reasons = append(reasons,
			"preview_only=true")
	}
	return reasons
}

func buildSignDecisionReasons(op *pb.Operation,
	signerProof *pb.SignerProof) []string {

	if op == nil {
		return nil
	}

	reasons := []string{
		fmt.Sprintf("operation_state=%s", op.State),
		fmt.Sprintf("policy_verdict=%s", op.PolicyVerdict),
		fmt.Sprintf("expiry_items=%d", len(op.ExpiryRisks)),
	}
	if signerProof != nil {
		reasons = append(reasons,
			fmt.Sprintf("signer_backend=%s", signerProof.BackendId))
		if signerProof.SignerSessionId != "" {
			reasons = append(reasons,
				fmt.Sprintf("signer_session_id=%s",
					signerProof.SignerSessionId))
		}
	}
	return reasons
}

func buildPublishDecisionReasons(op *pb.Operation,
	overrideSignedPsbt bool) []string {

	if op == nil {
		return nil
	}

	reasons := []string{
		fmt.Sprintf("operation_state=%s", op.State),
		fmt.Sprintf("policy_verdict=%s", op.PolicyVerdict),
	}
	if overrideSignedPsbt {
		reasons = append(reasons, "signed_psbt_source=request_override")
	} else {
		reasons = append(reasons, "signed_psbt_source=stored_artifact")
	}
	if op.ReservationId != "" {
		reasons = append(reasons,
			fmt.Sprintf("reservation_id=%s", op.ReservationId))
	}
	return reasons
}

func newPolicySnapshot(verdict string, effectivePolicy agentExpiryPolicy,
	expiryRisks []*pb.ExpiryRisk, warnings []string, tipHeight int32,
	targetAmountSat, feeRateSatPerKB int64, minConfirmations int32,
	reservationID string) *pb.PolicySnapshot {

	return &pb.PolicySnapshot{
		Verdict:          verdict,
		EffectivePolicy:  toPBExpiryPolicy(effectivePolicy),
		ExpiryRisks:      cloneExpiryRisks(expiryRisks),
		Warnings:         append([]string(nil), warnings...),
		TipHeight:        tipHeight,
		TargetAmountSat:  targetAmountSat,
		FeeRateSatPerKb:  feeRateSatPerKB,
		MinConfirmations: minConfirmations,
		ReservationId:    reservationID,
	}
}

func signerSessionIDFromProof(proof *pb.SignerProof) string {
	if proof == nil {
		return ""
	}
	return proof.SignerSessionId
}

func policySnapshotVerdict(snapshot *pb.PolicySnapshot) string {
	if snapshot == nil {
		return ""
	}
	return snapshot.Verdict
}

func requestID(meta *pb.RequestMeta) string {
	if meta == nil {
		return ""
	}
	return meta.RequestId
}

func normalizeWalletID(walletID string) string {
	if walletID == "" {
		return defaultAgentWalletID
	}
	return walletID
}

func extractFirstAddress(pkScript []byte, params *chaincfg.Params) string {
	_, addrs, _, err := txscript.ExtractPkScriptAddrs(pkScript, params)
	if err != nil || len(addrs) == 0 {
		return ""
	}
	return addrs[0].EncodeAddress()
}

func buildExpiryRiskItems(outputs []*wallet.TransactionOutput, tipHeight int32,
	policy agentExpiryPolicy, limit int, beforeHeight *int32) (
	[]*pb.ExpiryRisk, error) {

	items := make([]*pb.ExpiryRisk, 0, len(outputs))
	for _, output := range outputs {
		createHeight := output.ContainingBlock.Height
		if createHeight < 0 {
			continue
		}

		amountSat := output.Output.Value
		projectedReclaimSat := projectedReclaimAmount(
			amountSat, policy.ProjectedReclaimRatioBps,
		)
		info, err := wallet.BuildExpiryInfoWithRenewWarning(
			createHeight, tipHeight, policy.WindowBlocks,
			policy.ExpiringThresholdBlocks, policy.RenewWarningBlocks, amountSat,
			projectedReclaimSat, policy.DustThresholdSat,
		)
		if err != nil {
			return nil, err
		}
		if beforeHeight != nil && info.ExpiryHeight > *beforeHeight {
			continue
		}

		items = append(items, &pb.ExpiryRisk{
			Outpoint:       output.OutPoint.String(),
			AmountSat:      amountSat,
			CreateHeight:   info.CreateHeight,
			ExpiryHeight:   info.ExpiryHeight,
			BlocksToExpiry: info.BlocksToExpiry,
			DaysToExpiry:   info.DaysToExpiry,
			Status:         string(info.Status),
			DustRisk:       info.DustRisk,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].ExpiryHeight != items[j].ExpiryHeight {
			return items[i].ExpiryHeight < items[j].ExpiryHeight
		}
		return items[i].Outpoint < items[j].Outpoint
	})

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	return items, nil
}

func validateExpiryPolicy(policy agentExpiryPolicy) error {
	switch {
	case policy.WindowBlocks == 0:
		return fmt.Errorf("window_blocks must be > 0")
	case policy.ExpiringThresholdBlocks < 0:
		return fmt.Errorf("expiring_threshold_blocks must be >= 0")
	case policy.RenewWarningBlocks < 0:
		return fmt.Errorf("renew_warning_blocks must be >= 0")
	case policy.DustThresholdSat < 0:
		return fmt.Errorf("dust_threshold_sat must be >= 0")
	case policy.ProjectedReclaimRatioBps == 0:
		return fmt.Errorf("projected_reclaim_ratio_bps must be > 0")
	case policy.ProjectedReclaimRatioBps > 10000:
		return fmt.Errorf("projected_reclaim_ratio_bps must be <= 10000")
	default:
		return nil
	}
}

func validateAccountAndMinConfs(account uint32, minConfs int32) error {
	if account == waddrmgr.ImportedAddrAccount {
		return status.Errorf(codes.InvalidArgument,
			"account %d is not supported by phase-1 AgentWalletService",
			account)
	}
	if minConfs < 0 {
		return status.Error(codes.InvalidArgument,
			"min_confirmations must be >= 0")
	}
	return nil
}

func projectedReclaimAmount(amountSat int64, ratioBps uint32) int64 {
	return (amountSat * int64(ratioBps)) / 10000
}

func toPBExpiryPolicy(policy agentExpiryPolicy) *pb.ExpiryPolicy {
	return &pb.ExpiryPolicy{
		WindowBlocks:             policy.WindowBlocks,
		ExpiringThresholdBlocks:  policy.ExpiringThresholdBlocks,
		DustThresholdSat:         policy.DustThresholdSat,
		ProjectedReclaimRatioBps: policy.ProjectedReclaimRatioBps,
		Source:                   policy.Source,
	}
}

func joinPolicySources(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, "+")
}

func reservationLockID(reservationID string) wtxmgr.LockID {
	return sha256.Sum256([]byte("agentwallet/reservation/" + reservationID))
}

func reservationError(outpoint wire.OutPoint, err error) error {
	switch {
	case errors.Is(err, wtxmgr.ErrUnknownOutput):
		return status.Errorf(codes.NotFound,
			"outpoint %s is not spendable by the wallet", outpoint)

	case errors.Is(err, wtxmgr.ErrOutputAlreadyLocked):
		return status.Errorf(codes.Aborted,
			"outpoint %s is already reserved", outpoint)

	default:
		return translateError(err)
	}
}

func releaseLeasedOutpoints(wallet *wallet.Wallet, lockID wtxmgr.LockID,
	outpoints []wire.OutPoint) error {

	for _, outpoint := range outpoints {
		if err := wallet.ReleaseOutput(lockID, outpoint); err != nil {
			return err
		}
	}

	return nil
}

func leaseExpirationUnix(lease *wallet.ListLeasedOutputResult, leased bool) int64 {
	if !leased || lease == nil {
		return 0
	}
	return lease.Expiration.Unix()
}

func outpointStrings(outpoints []wire.OutPoint) []string {
	serialized := make([]string, 0, len(outpoints))
	for _, outpoint := range outpoints {
		serialized = append(serialized, outpoint.String())
	}
	return serialized
}

func policyVerdictFromExpiryRisks(items []*pb.ExpiryRisk) string {
	hasExpiring := false
	for _, item := range items {
		switch item.Status {
		case string(wallet.ExpiryStatusExpired):
			return "attention_expired"
		case string(wallet.ExpiryStatusExpiring):
			hasExpiring = true
		}
	}

	if hasExpiring {
		return "attention_expiring"
	}

	return "ok"
}

func warningsFromExpiryRisks(items []*pb.ExpiryRisk) []string {
	warnings := make([]string, 0, 2)
	hasExpiring := false
	hasNearExpiry := false
	hasTooLateNextBlock := false
	for _, item := range items {
		switch item.Status {
		case string(wallet.ExpiryStatusExpired):
			return append(warnings,
				"selected outpoints include expired UTXOs; renewal semantics should be reviewed before signing")
		case string(wallet.ExpiryStatusExpiring):
			hasExpiring = true
		}
		if item.Status != string(wallet.ExpiryStatusExpired) &&
			item.BlocksToExpiry <= wallet.DefaultRenewWarningBlocks {

			hasNearExpiry = true
			if item.BlocksToExpiry <= 1 {
				hasTooLateNextBlock = true
			}
		}
	}

	if hasTooLateNextBlock {
		warnings = append(warnings,
			"selected outpoints are too close to expiry for the next block to renew them")
	} else if hasNearExpiry {
		warnings = append(warnings,
			"selected outpoints are near expiry; renewal only succeeds if confirmed before expiry height")
	}

	if hasExpiring {
		warnings = append(warnings,
			"selected outpoints are inside the expiring window")
	}

	return warnings
}

func mergeWarnings(base []string, extra []string) []string {
	merged := make([]string, 0, len(base)+len(extra))
	seen := make(map[string]struct{}, len(base)+len(extra))

	for _, warning := range base {
		if warning == "" {
			continue
		}
		if _, ok := seen[warning]; ok {
			continue
		}
		seen[warning] = struct{}{}
		merged = append(merged, warning)
	}

	for _, warning := range extra {
		if warning == "" {
			continue
		}
		if _, ok := seen[warning]; ok {
			continue
		}
		seen[warning] = struct{}{}
		merged = append(merged, warning)
	}

	return merged
}

func summarizePsbt(packet *psbt.Packet, changeIndex int32, targetPkScript []byte,
	targetAddress string, targetAmountSat,
	feeRateSatPerKB int64) (*pb.TransactionSummary, error) {

	totalInput := int64(0)
	for idx, input := range packet.Inputs {
		value, err := inputValue(
			input, packet.UnsignedTx.TxIn[idx].PreviousOutPoint,
		)
		if err != nil {
			return nil, err
		}
		totalInput += value
	}

	totalOutput := int64(0)
	for _, output := range packet.UnsignedTx.TxOut {
		totalOutput += output.Value
	}

	targetOutputIndex := int32(-1)
	for idx, output := range packet.UnsignedTx.TxOut {
		if output.Value != targetAmountSat {
			continue
		}
		if !bytes.Equal(output.PkScript, targetPkScript) {
			continue
		}
		targetOutputIndex = int32(idx)
		break
	}

	return &pb.TransactionSummary{
		InputCount:        int32(len(packet.UnsignedTx.TxIn)),
		OutputCount:       int32(len(packet.UnsignedTx.TxOut)),
		TotalInputSat:     totalInput,
		TotalOutputSat:    totalOutput,
		EstimatedFeeSat:   totalInput - totalOutput,
		ChangeOutputIndex: changeIndex,
		TargetOutputIndex: targetOutputIndex,
		TargetAddress:     targetAddress,
		TargetAmountSat:   targetAmountSat,
		FeeRateSatPerKb:   feeRateSatPerKB,
	}, nil
}

func summarizePublishedTransaction(tx *wire.MsgTx,
	selectedOutputs []*wallet.TransactionOutput, targetPkScript []byte,
	targetAddress string, targetAmountSat,
	feeRateSatPerKB int64) (*pb.TransactionSummary, error) {

	if len(tx.TxIn) != len(selectedOutputs) {
		return nil, fmt.Errorf("published transaction input count mismatch")
	}

	totalInput := int64(0)
	selectedByOutpoint := make(map[wire.OutPoint]*wallet.TransactionOutput,
		len(selectedOutputs))
	for _, output := range selectedOutputs {
		totalInput += output.Output.Value
		selectedByOutpoint[output.OutPoint] = output
	}

	for _, input := range tx.TxIn {
		if _, ok := selectedByOutpoint[input.PreviousOutPoint]; !ok {
			return nil, fmt.Errorf("unexpected input %s in published transaction",
				input.PreviousOutPoint)
		}
	}

	totalOutput := int64(0)
	targetOutputIndex := int32(-1)
	changeOutputIndex := int32(-1)
	for idx, output := range tx.TxOut {
		totalOutput += output.Value

		if output.Value == targetAmountSat &&
			bytes.Equal(output.PkScript, targetPkScript) &&
			targetOutputIndex == -1 {

			targetOutputIndex = int32(idx)
			continue
		}

		if changeOutputIndex == -1 {
			changeOutputIndex = int32(idx)
		}
	}

	return &pb.TransactionSummary{
		InputCount:        int32(len(tx.TxIn)),
		OutputCount:       int32(len(tx.TxOut)),
		TotalInputSat:     totalInput,
		TotalOutputSat:    totalOutput,
		EstimatedFeeSat:   totalInput - totalOutput,
		ChangeOutputIndex: changeOutputIndex,
		TargetOutputIndex: targetOutputIndex,
		TargetAddress:     targetAddress,
		TargetAmountSat:   targetAmountSat,
		FeeRateSatPerKb:   feeRateSatPerKB,
	}, nil
}

func inputValue(input psbt.PInput, prevOut wire.OutPoint) (int64, error) {
	switch {
	case input.WitnessUtxo != nil:
		return input.WitnessUtxo.Value, nil

	case input.NonWitnessUtxo != nil:
		if int(prevOut.Index) >= len(input.NonWitnessUtxo.TxOut) {
			return 0, fmt.Errorf("missing prev output %s", prevOut)
		}
		return input.NonWitnessUtxo.TxOut[prevOut.Index].Value, nil

	default:
		return 0, fmt.Errorf("missing UTXO data for input %s", prevOut)
	}
}

func serializePsbt(packet *psbt.Packet) ([]byte, string, error) {
	var raw bytes.Buffer
	if err := packet.Serialize(&raw); err != nil {
		return nil, "", err
	}

	b64, err := packet.B64Encode()
	if err != nil {
		return nil, "", err
	}

	return raw.Bytes(), b64, nil
}

func parsePsbtBytes(raw []byte) (*psbt.Packet, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("psbt bytes must not be empty")
	}

	packet, err := psbt.NewFromRawBytes(bytes.NewReader(raw), false)
	if err == nil {
		return packet, nil
	}

	packet, b64Err := psbt.NewFromRawBytes(bytes.NewReader(raw), true)
	if b64Err == nil {
		return packet, nil
	}

	return nil, err
}

func psbtB64FromBytes(raw []byte) (string, error) {
	packet, err := parsePsbtBytes(raw)
	if err != nil {
		return "", err
	}

	return packet.B64Encode()
}

func serializeTransaction(tx *wire.MsgTx) ([]byte, error) {
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		return nil, err
	}

	return raw.Bytes(), nil
}

func cloneOperation(op *pb.Operation) *pb.Operation {
	if op == nil {
		return nil
	}
	cp := *op
	cp.Outpoints = append([]string(nil), op.Outpoints...)
	cp.Warnings = append([]string(nil), op.Warnings...)
	cp.Summary = cloneSummary(op.Summary)
	cp.ExpiryRisks = cloneExpiryRisks(op.ExpiryRisks)
	cp.EffectivePolicy = cloneExpiryPolicy(op.EffectivePolicy)
	cp.History = cloneOperationEvents(op.History)
	cp.LatestPolicySnapshot = clonePolicySnapshot(op.LatestPolicySnapshot)
	cp.LatestSignerProof = cloneSignerProof(op.LatestSignerProof)
	cp.DecisionLog = cloneDecisionLogEntries(op.DecisionLog)
	return &cp
}

func cloneSummary(summary *pb.TransactionSummary) *pb.TransactionSummary {
	if summary == nil {
		return nil
	}
	cp := *summary
	return &cp
}

func cloneExpiryPolicy(policy *pb.ExpiryPolicy) *pb.ExpiryPolicy {
	if policy == nil {
		return nil
	}
	cp := *policy
	return &cp
}

func cloneExpiryRisks(items []*pb.ExpiryRisk) []*pb.ExpiryRisk {
	if len(items) == 0 {
		return nil
	}

	cloned := make([]*pb.ExpiryRisk, 0, len(items))
	for _, item := range items {
		if item == nil {
			cloned = append(cloned, nil)
			continue
		}
		cp := *item
		cloned = append(cloned, &cp)
	}
	return cloned
}

func cloneOperationEvents(items []*pb.OperationEvent) []*pb.OperationEvent {
	if len(items) == 0 {
		return nil
	}

	cloned := make([]*pb.OperationEvent, 0, len(items))
	for _, item := range items {
		if item == nil {
			cloned = append(cloned, nil)
			continue
		}
		cp := *item
		cp.Warnings = append([]string(nil), item.Warnings...)
		cloned = append(cloned, &cp)
	}
	return cloned
}

func clonePolicySnapshot(snapshot *pb.PolicySnapshot) *pb.PolicySnapshot {
	if snapshot == nil {
		return nil
	}

	cp := *snapshot
	cp.EffectivePolicy = cloneExpiryPolicy(snapshot.EffectivePolicy)
	cp.ExpiryRisks = cloneExpiryRisks(snapshot.ExpiryRisks)
	cp.Warnings = append([]string(nil), snapshot.Warnings...)
	return &cp
}

func cloneSignerProof(proof *pb.SignerProof) *pb.SignerProof {
	if proof == nil {
		return nil
	}

	cp := *proof
	return &cp
}

func cloneDecisionLogEntries(items []*pb.DecisionLogEntry) []*pb.DecisionLogEntry {
	if len(items) == 0 {
		return nil
	}

	cloned := make([]*pb.DecisionLogEntry, 0, len(items))
	for _, item := range items {
		if item == nil {
			cloned = append(cloned, nil)
			continue
		}
		cp := *item
		cp.Reasons = append([]string(nil), item.Reasons...)
		cp.Warnings = append([]string(nil), item.Warnings...)
		cp.PolicySnapshot = clonePolicySnapshot(item.PolicySnapshot)
		cp.SignerProof = cloneSignerProof(item.SignerProof)
		cloned = append(cloned, &cp)
	}
	return cloned
}

func sha256Hex(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:])
}

func parseOutPointStringsUnique(in []string) ([]wire.OutPoint, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("outpoints must not be empty")
	}

	seen := make(map[string]struct{}, len(in))
	out := make([]wire.OutPoint, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			return nil, fmt.Errorf("duplicate outpoint %q", s)
		}
		seen[s] = struct{}{}

		op, err := parseOutPointString(s)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}

	return out, nil
}

func parseOutPointString(s string) (wire.OutPoint, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return wire.OutPoint{}, fmt.Errorf("invalid outpoint format")
	}

	hash, err := chainhash.NewHashFromStr(parts[0])
	if err != nil {
		return wire.OutPoint{}, fmt.Errorf("invalid txid: %w", err)
	}

	index, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return wire.OutPoint{}, fmt.Errorf("invalid vout: %w", err)
	}

	return wire.OutPoint{
		Hash:  *hash,
		Index: uint32(index),
	}, nil
}

func decodeAddress(addr string, params *chaincfg.Params) (btcutil.Address, error) {
	decoded, err := btcutil.DecodeAddress(addr, params)
	if err != nil {
		return nil, err
	}
	if !decoded.IsForNet(params) {
		return nil, fmt.Errorf("not intended for use on %s", params.Name)
	}
	return decoded, nil
}
