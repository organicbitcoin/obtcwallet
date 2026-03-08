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

	operationKindRenewPreview = "renewal_preview"
	operationKindRenewSubmit  = "renewal_submit"
	operationStateDraft       = "DRAFT"
	operationStatePublished   = "PUBLISHED"

	expiryPolicySourceRequestOverride = "request_override"
)

// AgentExpiryPolicyProvider resolves the effective expiry policy used by the
// agent-facing API. The default implementation prefers a real OBTC chaincfg
// source when available and otherwise falls back to built-in/default values.
type AgentExpiryPolicyProvider interface {
	PolicyForWallet(wallet *wallet.Wallet) (agentExpiryPolicy, []string, error)
}

// AgentWalletOptions configures the agent-oriented gRPC service.
type AgentWalletOptions struct {
	ExpiryPolicyProvider AgentExpiryPolicyProvider
}

type agentExpiryPolicy struct {
	WindowBlocks             uint64
	ExpiringThresholdBlocks  int32
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
		DustThresholdSat:         resolvedPolicy.DustThresholdSat,
		ProjectedReclaimRatioBps: resolvedPolicy.ProjectedReclaimRatioBps,
		Source:                   resolvedPolicy.Source,
	}, warnings, nil
}

type agentWalletServer struct {
	pb.UnimplementedAgentWalletServiceServer

	wallet               *wallet.Wallet
	expiryPolicyProvider AgentExpiryPolicyProvider

	mu                sync.Mutex
	nextOperationID   uint64
	nextReservationID uint64
	operations        map[string]*pb.Operation
}

// StartAgentWalletService registers the phase-1 AgentWalletService on the
// experimental gRPC server. This service shares the same wallet core as the
// human-facing CLI and legacy interfaces.
func StartAgentWalletService(server *grpc.Server, wallet *wallet.Wallet) {
	StartAgentWalletServiceWithOptions(server, wallet, AgentWalletOptions{})
}

// StartAgentWalletServiceWithOptions registers the agent wallet gRPC service
// with explicit service options.
func StartAgentWalletServiceWithOptions(server *grpc.Server,
	wallet *wallet.Wallet, opts AgentWalletOptions) {

	if opts.ExpiryPolicyProvider == nil {
		opts.ExpiryPolicyProvider = compatibilityExpiryPolicyProvider{}
	}

	service := &agentWalletServer{
		wallet:               wallet,
		expiryPolicyProvider: opts.ExpiryPolicyProvider,
		operations:           make(map[string]*pb.Operation),
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

	if err := validateAccountAndMinConfs(
		req.AccountNumber, req.MinConfirmations,
	); err != nil {
		return nil, err
	}

	outputs, err := s.lookupOutputs(
		req.AccountNumber, req.MinConfirmations, req.Outpoints,
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
		req.AccountNumber, req.MinConfirmations, req.Outpoints,
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
		MinConfirmations: req.MinConfirmations,
	}
	s.operations[operationID] = op
	s.mu.Unlock()

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

func (s *agentWalletServer) SubmitRenewal(_ context.Context,
	req *pb.SubmitRenewalRequest) (*pb.SubmitRenewalResponse, error) {

	if req.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument,
			"operation_id must not be empty")
	}

	s.mu.Lock()
	existingOp, ok := s.operations[req.OperationId]
	if !ok {
		s.mu.Unlock()
		return nil, status.Errorf(codes.NotFound,
			"operation %s not found", req.OperationId)
	}
	op := cloneOperation(existingOp)
	s.mu.Unlock()

	switch op.Kind {
	case operationKindRenewPreview, operationKindRenewSubmit:
	default:
		return nil, status.Errorf(codes.FailedPrecondition,
			"operation %s is not a renewal operation", req.OperationId)
	}

	if op.Summary == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"operation %s is missing renewal summary", req.OperationId)
	}
	if op.State == operationStatePublished {
		warnings := []string{
			"operation already published; returning recorded transaction metadata",
		}
		return &pb.SubmitRenewalResponse{
			Meta: &pb.ResponseMeta{
				RequestId: requestID(req.GetMeta()),
				Warnings:  warnings,
			},
			Operation:       cloneOperation(op),
			Txid:            op.Txid,
			Summary:         cloneSummary(op.Summary),
			EffectivePolicy: cloneExpiryPolicy(op.EffectivePolicy),
			ExpiryRisks:     cloneExpiryRisks(op.ExpiryRisks),
		}, nil
	}
	if op.State != operationStateDraft {
		return nil, status.Errorf(codes.FailedPrecondition,
			"operation %s is in unsupported state %s",
			req.OperationId, op.State)
	}
	if len(op.Outpoints) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"operation %s has no selected outpoints", req.OperationId)
	}
	if op.Summary.TargetAmountSat <= 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"operation %s has invalid target amount", req.OperationId)
	}
	if s.wallet.Manager.WatchOnly() {
		return nil, status.Error(codes.FailedPrecondition,
			"wallet is watch-only; use PreviewRenewal and an external signing flow")
	}

	selectedOutputs, err := s.lookupOutputs(
		op.AccountNumber, op.MinConfirmations, op.Outpoints,
	)
	if err != nil {
		return nil, err
	}

	selectedOutpoints := make([]wire.OutPoint, 0, len(selectedOutputs))
	for _, output := range selectedOutputs {
		selectedOutpoints = append(selectedOutpoints, output.OutPoint)
	}

	if err := s.validateRequestedReservation(
		selectedOutpoints, op.ReservationId,
	); err != nil {
		return nil, err
	}

	targetAddress := op.Summary.TargetAddress
	if targetAddress == "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"operation %s is missing target address", req.OperationId)
	}

	targetAddr, err := decodeAddress(targetAddress, s.wallet.ChainParams())
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"operation %s has invalid target address: %v",
			req.OperationId, err)
	}

	pkScript, err := txscript.PayToAddrScript(targetAddr)
	if err != nil {
		return nil, translateError(err)
	}

	effectivePolicy, warnings, err := s.resolveSubmitExpiryPolicy(op)
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
	warnings = append(warnings, warningsFromExpiryRisks(expiryRisks)...)

	feeRate := txrules.DefaultRelayFeePerKb
	if op.Summary.FeeRateSatPerKb > 0 {
		feeRate = btcutil.Amount(op.Summary.FeeRateSatPerKb)
	}

	label := req.Label
	if label == "" {
		label = "agentwallet.submit_renewal"
	}

	tx, err := s.wallet.SendOutputsWithInput(
		[]*wire.TxOut{{
			Value:    op.Summary.TargetAmountSat,
			PkScript: pkScript,
		}},
		nil, // Allow selected outpoints from any key scope in the account.
		op.AccountNumber,
		op.MinConfirmations,
		feeRate,
		wallet.CoinSelectionLargest,
		label,
		selectedOutpoints,
	)
	if err != nil {
		return nil, translateError(err)
	}

	summary, err := summarizePublishedTransaction(
		tx, selectedOutputs, pkScript, targetAddress,
		op.Summary.TargetAmountSat, int64(feeRate),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to summarize published renewal transaction: %v", err)
	}

	rawTx, err := serializeTransaction(tx)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to serialize published renewal transaction: %v", err)
	}

	if op.ReservationId != "" {
		if releaseErr := releaseLeasedOutpoints(
			s.wallet, reservationLockID(op.ReservationId), selectedOutpoints,
		); releaseErr != nil {
			warnings = append(warnings, fmt.Sprintf(
				"transaction published but reservation release failed: %v",
				releaseErr,
			))
		}
	}

	now := time.Now().Unix()
	auditWarnings := mergeWarnings(op.Warnings, warnings)
	op.Kind = operationKindRenewSubmit
	op.State = operationStatePublished
	op.PolicyVerdict = policyVerdictFromExpiryRisks(expiryRisks)
	op.Warnings = auditWarnings
	op.UpdatedAtUnix = now
	op.Summary = cloneSummary(summary)
	op.ExpiryRisks = cloneExpiryRisks(expiryRisks)
	op.EffectivePolicy = toPBExpiryPolicy(effectivePolicy)
	op.Txid = tx.TxHash().String()

	s.mu.Lock()
	s.operations[req.OperationId] = cloneOperation(op)
	s.mu.Unlock()

	return &pb.SubmitRenewalResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
			Warnings:  warnings,
		},
		Operation:       cloneOperation(op),
		Txid:            op.Txid,
		RawTransaction:  rawTx,
		Summary:         cloneSummary(summary),
		EffectivePolicy: toPBExpiryPolicy(effectivePolicy),
		ExpiryRisks:     cloneExpiryRisks(expiryRisks),
	}, nil
}

func (s *agentWalletServer) ReserveUtxos(_ context.Context,
	req *pb.ReserveUtxosRequest) (*pb.ReserveUtxosResponse, error) {

	if err := validateAccountAndMinConfs(
		req.AccountNumber, req.MinConfirmations,
	); err != nil {
		return nil, err
	}

	selectedOutputs, err := s.lookupOutputs(
		req.AccountNumber, req.MinConfirmations, req.Outpoints,
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

	return &pb.ReleaseReservationResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
		},
		ReservationId: req.ReservationId,
		Released:      released,
	}, nil
}

func (s *agentWalletServer) GetOperation(_ context.Context,
	req *pb.GetOperationRequest) (*pb.GetOperationResponse, error) {

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
	outpoints []string) ([]*wallet.TransactionOutput, error) {

	if err := validateAccountAndMinConfs(account, minConfs); err != nil {
		return nil, err
	}

	outputs, err := s.wallet.UnspentOutputs(wallet.OutputSelectionPolicy{
		Account:               account,
		RequiredConfirmations: minConfs,
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
		info, err := wallet.BuildExpiryInfo(
			createHeight, tipHeight, policy.WindowBlocks,
			policy.ExpiringThresholdBlocks, amountSat,
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
	for _, item := range items {
		switch item.Status {
		case string(wallet.ExpiryStatusExpired):
			return append(warnings,
				"selected outpoints include expired UTXOs; renewal semantics should be reviewed before signing")
		case string(wallet.ExpiryStatusExpiring):
			hasExpiring = true
		}
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
