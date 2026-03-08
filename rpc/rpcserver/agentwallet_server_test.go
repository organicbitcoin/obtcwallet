package rpcserver

import (
	"context"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	pb "github.com/btcsuite/btcwallet/rpc/agentwalletrpc"
	"github.com/btcsuite/btcwallet/wallet"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testSignerBackend struct {
	activeSessions map[string]struct{}
}

func (b *testSignerBackend) Info() *pb.SignerBackendInfo {
	return &pb.SignerBackendInfo{
		BackendId:                   "test",
		Mode:                        "test",
		LocalSigningAvailable:       true,
		ExternalSignedPsbtSupported: true,
		MaxActiveSessions:           1,
	}
}

func (b *testSignerBackend) OpenSession(sessionID string, _ []byte,
	_ time.Duration, _ func()) error {

	if b.activeSessions == nil {
		b.activeSessions = make(map[string]struct{})
	}
	for activeSessionID := range b.activeSessions {
		return status.Errorf(codes.FailedPrecondition,
			"session %s already active", activeSessionID)
	}
	b.activeSessions[sessionID] = struct{}{}
	return nil
}

func (b *testSignerBackend) CloseSession(sessionID string) error {
	if b.activeSessions != nil {
		delete(b.activeSessions, sessionID)
	}
	return nil
}

func (b *testSignerBackend) ValidateSession(sessionID string) error {
	if b.activeSessions == nil {
		return status.Errorf(codes.FailedPrecondition,
			"session %s is not active", sessionID)
	}
	if _, ok := b.activeSessions[sessionID]; !ok {
		return status.Errorf(codes.FailedPrecondition,
			"session %s is not active", sessionID)
	}
	return nil
}

func (b *testSignerBackend) FinalizePsbt(sessionID string, _ uint32,
	_ *psbt.Packet) error {
	return b.ValidateSession(sessionID)
}

func TestReservationLockIDDeterministic(t *testing.T) {
	id1 := reservationLockID("res_1")
	id2 := reservationLockID("res_1")
	id3 := reservationLockID("res_2")

	if id1 != id2 {
		t.Fatalf("expected identical reservation lock IDs")
	}
	if id1 == id3 {
		t.Fatalf("expected distinct reservation lock IDs")
	}
}

func TestBuildExpiryRiskItemsSortsAndFilters(t *testing.T) {
	outputs := []*wallet.TransactionOutput{
		{
			OutPoint: wire.OutPoint{
				Hash:  chainhash.Hash{0x02},
				Index: 1,
			},
			Output: wire.TxOut{Value: 1000},
			ContainingBlock: wallet.BlockIdentity{
				Height: 200,
			},
		},
		{
			OutPoint: wire.OutPoint{
				Hash:  chainhash.Hash{0x01},
				Index: 0,
			},
			Output: wire.TxOut{Value: 2000},
			ContainingBlock: wallet.BlockIdentity{
				Height: 40,
			},
		},
	}

	policy := agentExpiryPolicy{
		WindowBlocks:             100,
		ExpiringThresholdBlocks:  10,
		DustThresholdSat:         546,
		ProjectedReclaimRatioBps: 7000,
	}
	beforeHeight := int32(250)

	items, err := buildExpiryRiskItems(outputs, 150, policy, 1, &beforeHeight)
	if err != nil {
		t.Fatalf("buildExpiryRiskItems error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item after limit, got %d", len(items))
	}
	if items[0].Outpoint != outputs[1].OutPoint.String() {
		t.Fatalf("expected earliest expiry item first, got %s", items[0].Outpoint)
	}
	if items[0].Status != string(wallet.ExpiryStatusExpired) {
		t.Fatalf("expected expired status, got %s", items[0].Status)
	}
}

func TestSummarizePsbt(t *testing.T) {
	targetPkScript := []byte{0x51}
	changePkScript := []byte{0x52}
	packet := &psbt.Packet{
		UnsignedTx: &wire.MsgTx{
			TxIn: []*wire.TxIn{
				{
					PreviousOutPoint: wire.OutPoint{
						Hash:  chainhash.Hash{0x01},
						Index: 0,
					},
				},
				{
					PreviousOutPoint: wire.OutPoint{
						Hash:  chainhash.Hash{0x02},
						Index: 1,
					},
				},
			},
			TxOut: []*wire.TxOut{
				{
					Value:    2000,
					PkScript: targetPkScript,
				},
				{
					Value:    400,
					PkScript: changePkScript,
				},
			},
		},
		Inputs: []psbt.PInput{
			{
				WitnessUtxo: &wire.TxOut{Value: 1000},
			},
			{
				WitnessUtxo: &wire.TxOut{Value: 1500},
			},
		},
	}

	summary, err := summarizePsbt(packet, 1, targetPkScript, "addr", 2000, 1500)
	if err != nil {
		t.Fatalf("summarizePsbt error: %v", err)
	}
	if summary.TotalInputSat != 2500 {
		t.Fatalf("unexpected total input: %d", summary.TotalInputSat)
	}
	if summary.TotalOutputSat != 2400 {
		t.Fatalf("unexpected total output: %d", summary.TotalOutputSat)
	}
	if summary.EstimatedFeeSat != 100 {
		t.Fatalf("unexpected fee: %d", summary.EstimatedFeeSat)
	}
	if summary.TargetOutputIndex != 0 {
		t.Fatalf("unexpected target output index: %d", summary.TargetOutputIndex)
	}
	if summary.ChangeOutputIndex != 1 {
		t.Fatalf("unexpected change output index: %d", summary.ChangeOutputIndex)
	}
}

func TestPolicyVerdictFromExpiryRisks(t *testing.T) {
	items := []*pb.ExpiryRisk{
		{Status: string(wallet.ExpiryStatusOK)},
		{Status: string(wallet.ExpiryStatusExpiring)},
	}

	if verdict := policyVerdictFromExpiryRisks(items); verdict != "attention_expiring" {
		t.Fatalf("unexpected verdict: %s", verdict)
	}

	items = append(items, &pb.ExpiryRisk{Status: string(wallet.ExpiryStatusExpired)})
	if verdict := policyVerdictFromExpiryRisks(items); verdict != "attention_expired" {
		t.Fatalf("unexpected verdict with expired item: %s", verdict)
	}
}

func TestMergeWarningsDeduplicatesAndPreservesOrder(t *testing.T) {
	merged := mergeWarnings(
		[]string{"preview warning", "shared warning"},
		[]string{"shared warning", "submit warning"},
	)

	if len(merged) != 3 {
		t.Fatalf("unexpected warning count: %d", len(merged))
	}
	if merged[0] != "preview warning" || merged[1] != "shared warning" ||
		merged[2] != "submit warning" {

		t.Fatalf("unexpected merged warnings: %#v", merged)
	}
}

func TestSummarizePublishedTransaction(t *testing.T) {
	targetPkScript := []byte{0x51}
	changePkScript := []byte{0x52}
	selectedOutputs := []*wallet.TransactionOutput{
		{
			OutPoint: wire.OutPoint{
				Hash:  chainhash.Hash{0x01},
				Index: 0,
			},
			Output: wire.TxOut{Value: 1000},
		},
		{
			OutPoint: wire.OutPoint{
				Hash:  chainhash.Hash{0x02},
				Index: 1,
			},
			Output: wire.TxOut{Value: 1500},
		},
	}
	tx := &wire.MsgTx{
		TxIn: []*wire.TxIn{
			{PreviousOutPoint: selectedOutputs[0].OutPoint},
			{PreviousOutPoint: selectedOutputs[1].OutPoint},
		},
		TxOut: []*wire.TxOut{
			{
				Value:    2000,
				PkScript: targetPkScript,
			},
			{
				Value:    350,
				PkScript: changePkScript,
			},
		},
	}

	summary, err := summarizePublishedTransaction(
		tx, selectedOutputs, targetPkScript, "addr", 2000, 1500,
	)
	if err != nil {
		t.Fatalf("summarizePublishedTransaction error: %v", err)
	}
	if summary.TotalInputSat != 2500 {
		t.Fatalf("unexpected total input: %d", summary.TotalInputSat)
	}
	if summary.TotalOutputSat != 2350 {
		t.Fatalf("unexpected total output: %d", summary.TotalOutputSat)
	}
	if summary.EstimatedFeeSat != 150 {
		t.Fatalf("unexpected fee: %d", summary.EstimatedFeeSat)
	}
	if summary.TargetOutputIndex != 0 {
		t.Fatalf("unexpected target output index: %d", summary.TargetOutputIndex)
	}
	if summary.ChangeOutputIndex != 1 {
		t.Fatalf("unexpected change output index: %d", summary.ChangeOutputIndex)
	}
}

func testPsbtBytes(t *testing.T) []byte {
	t.Helper()

	packet, err := psbt.New(
		[]*wire.OutPoint{{
			Hash:  chainhash.Hash{0x01},
			Index: 0,
		}},
		[]*wire.TxOut{{
			Value:    1000,
			PkScript: []byte{0x51},
		}},
		wire.TxVersion,
		0,
		[]uint32{wire.MaxTxInSequenceNum},
	)
	if err != nil {
		t.Fatalf("unable to create test PSBT: %v", err)
	}
	packet.Inputs = []psbt.PInput{{
		WitnessUtxo: &wire.TxOut{
			Value:    1200,
			PkScript: []byte{0x51},
		},
	}}

	raw, _, err := serializePsbt(packet)
	if err != nil {
		t.Fatalf("unable to serialize test PSBT: %v", err)
	}

	return raw
}

func testTxBytes(t *testing.T) []byte {
	t.Helper()

	tx := &wire.MsgTx{
		TxIn: []*wire.TxIn{{
			PreviousOutPoint: wire.OutPoint{
				Hash:  chainhash.Hash{0x01},
				Index: 0,
			},
		}},
		TxOut: []*wire.TxOut{{
			Value:    1000,
			PkScript: []byte{0x51},
		}},
	}

	raw, err := serializeTransaction(tx)
	if err != nil {
		t.Fatalf("unable to serialize test tx: %v", err)
	}

	return raw
}

func TestCloneOperationClonesEffectivePolicy(t *testing.T) {
	original := &pb.Operation{
		OperationId:          "op_1",
		CreatorPrincipal:     "agent:bot",
		CreatorCapabilityId:  "cap_1",
		CreateIdempotencyKey: "idem_1",
		Warnings:             []string{"warning"},
		Summary: &pb.TransactionSummary{
			TargetAmountSat: 123,
		},
		ExpiryRisks: []*pb.ExpiryRisk{
			{Outpoint: "tx:0"},
		},
		EffectivePolicy: &pb.ExpiryPolicy{
			WindowBlocks: 100,
			Source:       "chaincfg",
		},
		History: []*pb.OperationEvent{{
			EventId:       "evt_1",
			Action:        operationActionPreviewCreated,
			Warnings:      []string{"history warning"},
			CreatedAtUnix: 100,
		}},
	}

	cloned := cloneOperation(original)
	cloned.Warnings[0] = "changed"
	cloned.Summary.TargetAmountSat = 456
	cloned.ExpiryRisks[0].Outpoint = "mutated"
	cloned.EffectivePolicy.Source = "override"
	cloned.History[0].Warnings[0] = "changed history"

	if original.Warnings[0] != "warning" {
		t.Fatalf("warnings were not cloned")
	}
	if original.Summary.TargetAmountSat != 123 {
		t.Fatalf("summary was not cloned")
	}
	if original.ExpiryRisks[0].Outpoint != "tx:0" {
		t.Fatalf("expiry risks were not cloned")
	}
	if original.EffectivePolicy.Source != "chaincfg" {
		t.Fatalf("effective policy was not cloned")
	}
	if original.History[0].Warnings[0] != "history warning" {
		t.Fatalf("history was not cloned")
	}
}

func TestListOperationsFiltersAndSorts(t *testing.T) {
	db := testAgentWalletDB(t)
	store := newAgentWalletPersistentStore(db)

	operations := []*pb.Operation{
		{
			OperationId:      "op_old",
			WalletId:         "default",
			Kind:             operationKindRenewPreview,
			State:            operationStateDraft,
			CreatorPrincipal: "agent:a",
			CreatedAtUnix:    100,
			UpdatedAtUnix:    100,
		},
		{
			OperationId:      "op_mid",
			WalletId:         "default",
			Kind:             operationKindRenewSubmit,
			State:            operationStatePublished,
			CreatorPrincipal: "agent:b",
			CreatedAtUnix:    200,
			UpdatedAtUnix:    200,
		},
		{
			OperationId:      "op_new",
			WalletId:         "default",
			Kind:             operationKindRenewSubmit,
			State:            operationStatePublished,
			CreatorPrincipal: "agent:a",
			CreatedAtUnix:    300,
			UpdatedAtUnix:    300,
		},
	}
	for _, op := range operations {
		if err := store.putOperation(op); err != nil {
			t.Fatalf("putOperation error: %v", err)
		}
	}

	server := &agentWalletServer{
		persistence:  store,
		operations:   make(map[string]*pb.Operation),
		artifacts:    make(map[string]*agentOperationArtifacts),
		reservations: make(map[string]*agentReservationRecord),
	}

	resp, err := server.ListOperations(context.Background(),
		&pb.ListOperationsRequest{
			Meta:  &pb.RequestMeta{RequestId: "req_1"},
			Limit: 2,
		},
	)
	if err != nil {
		t.Fatalf("ListOperations error: %v", err)
	}
	if len(resp.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(resp.Operations))
	}
	if resp.Operations[0].GetOperationId() != "op_new" ||
		resp.Operations[1].GetOperationId() != "op_mid" {

		t.Fatalf("unexpected newest-first order: %#v", resp.Operations)
	}

	oldestFirst := false
	filteredResp, err := server.ListOperations(context.Background(),
		&pb.ListOperationsRequest{
			State:            operationStatePublished,
			CreatorPrincipal: "agent:a",
			NewestFirst:      &oldestFirst,
		},
	)
	if err != nil {
		t.Fatalf("filtered ListOperations error: %v", err)
	}
	if len(filteredResp.Operations) != 1 {
		t.Fatalf("expected 1 filtered operation, got %d",
			len(filteredResp.Operations))
	}
	if filteredResp.Operations[0].GetOperationId() != "op_new" {
		t.Fatalf("unexpected filtered operation: %s",
			filteredResp.Operations[0].GetOperationId())
	}
}

func TestSubmitRenewalAlreadyPublishedReturnsRecordedMetadata(t *testing.T) {
	psbtBytes := testPsbtBytes(t)
	txBytes := testTxBytes(t)

	server := &agentWalletServer{
		operations: map[string]*pb.Operation{
			"op_1": {
				OperationId:         "op_1",
				Kind:                operationKindRenewSubmit,
				State:               operationStatePublished,
				Txid:                "txid_1",
				Outpoints:           []string{"txid:0"},
				CreatorPrincipal:    "agent:bot",
				CreatorCapabilityId: "cap_1",
				Summary: &pb.TransactionSummary{
					TargetAmountSat: 2000,
				},
				EffectivePolicy: &pb.ExpiryPolicy{
					WindowBlocks:             100,
					ExpiringThresholdBlocks:  10,
					ProjectedReclaimRatioBps: 7000,
				},
			},
		},
		artifacts: map[string]*agentOperationArtifacts{
			"op_1": {
				OperationID:       "op_1",
				SignedPsbt:        psbtBytes,
				SignedTransaction: txBytes,
			},
		},
		capabilities: map[string]*agentCapabilityRecord{
			"cap_1": {
				CapabilityID:  "cap_1",
				WalletID:      defaultAgentWalletID,
				Principal:     "agent:bot",
				Permissions:   []string{capabilityPermissionRenewalSubmit},
				CreatedAtUnix: 100,
				ExpiresAtUnix: time.Now().Add(time.Hour).Unix(),
				UpdatedAtUnix: 100,
			},
		},
		reservations:   make(map[string]*agentReservationRecord),
		signerSessions: make(map[string]*agentSignerSessionRecord),
		signerBackend:  &testSignerBackend{},
	}
	server.persistenceLoadOnce.Do(func() {})

	resp, err := server.SubmitRenewal(context.Background(),
		&pb.SubmitRenewalRequest{
			Meta: &pb.RequestMeta{
				RequestId:    "req_1",
				Principal:    "agent:bot",
				CapabilityId: "cap_1",
			},
			OperationId: "op_1",
		},
	)
	if err != nil {
		t.Fatalf("SubmitRenewal error: %v", err)
	}
	if resp.Txid != "txid_1" {
		t.Fatalf("unexpected txid: %s", resp.Txid)
	}
	if resp.Meta.GetRequestId() != "req_1" {
		t.Fatalf("unexpected request id: %s", resp.Meta.GetRequestId())
	}
	if len(resp.Meta.GetWarnings()) != 1 {
		t.Fatalf("expected idempotency warning, got %d", len(resp.Meta.GetWarnings()))
	}
	if resp.Operation.GetState() != operationStatePublished {
		t.Fatalf("unexpected operation state: %s", resp.Operation.GetState())
	}
	if len(resp.RawTransaction) == 0 {
		t.Fatalf("expected raw transaction bytes")
	}
}

func TestSignPsbtAlreadySignedReturnsRecordedMetadata(t *testing.T) {
	psbtBytes := testPsbtBytes(t)
	txBytes := testTxBytes(t)

	server := &agentWalletServer{
		operations: map[string]*pb.Operation{
			"op_1": {
				OperationId:         "op_1",
				Kind:                operationKindRenewSign,
				State:               operationStateSigned,
				Outpoints:           []string{"txid:0"},
				CreatorPrincipal:    "agent:bot",
				CreatorCapabilityId: "cap_1",
				Summary: &pb.TransactionSummary{
					TargetAmountSat: 2000,
				},
			},
		},
		artifacts: map[string]*agentOperationArtifacts{
			"op_1": {
				OperationID:       "op_1",
				SignedPsbt:        psbtBytes,
				SignedTransaction: txBytes,
			},
		},
		capabilities: map[string]*agentCapabilityRecord{
			"cap_1": {
				CapabilityID:  "cap_1",
				WalletID:      defaultAgentWalletID,
				Principal:     "agent:bot",
				Permissions:   []string{capabilityPermissionRenewalSign},
				CreatedAtUnix: 100,
				ExpiresAtUnix: time.Now().Add(time.Hour).Unix(),
				UpdatedAtUnix: 100,
			},
		},
		signerSessions: map[string]*agentSignerSessionRecord{
			"sess_1": {
				SignerSessionID: "sess_1",
				WalletID:        defaultAgentWalletID,
				CapabilityID:    "cap_1",
				Principal:       "agent:bot",
				Permissions:     []string{capabilityPermissionRenewalSign},
				CreatedAtUnix:   100,
				ExpiresAtUnix:   time.Now().Add(time.Hour).Unix(),
				UpdatedAtUnix:   100,
			},
		},
		reservations: make(map[string]*agentReservationRecord),
		signerBackend: &testSignerBackend{
			activeSessions: map[string]struct{}{
				"sess_1": {},
			},
		},
	}
	server.persistenceLoadOnce.Do(func() {})

	resp, err := server.SignPsbt(context.Background(), &pb.SignPsbtRequest{
		Meta: &pb.RequestMeta{
			RequestId:    "req_1",
			Principal:    "agent:bot",
			CapabilityId: "cap_1",
		},
		OperationId:     "op_1",
		SignerSessionId: "sess_1",
	})
	if err != nil {
		t.Fatalf("SignPsbt error: %v", err)
	}
	if len(resp.Meta.GetWarnings()) != 1 {
		t.Fatalf("expected idempotency warning, got %d",
			len(resp.Meta.GetWarnings()))
	}
	if len(resp.SignedPsbt) == 0 || len(resp.SignedTransaction) == 0 {
		t.Fatalf("expected signed artifacts")
	}
	if resp.Operation.GetState() != operationStateSigned {
		t.Fatalf("unexpected operation state: %s", resp.Operation.GetState())
	}
}

func TestPublishTransactionAlreadyPublishedReturnsRecordedMetadata(t *testing.T) {
	psbtBytes := testPsbtBytes(t)
	txBytes := testTxBytes(t)

	server := &agentWalletServer{
		operations: map[string]*pb.Operation{
			"op_1": {
				OperationId:         "op_1",
				Kind:                operationKindRenewPublish,
				State:               operationStatePublished,
				Txid:                "txid_1",
				Outpoints:           []string{"txid:0"},
				CreatorPrincipal:    "agent:bot",
				CreatorCapabilityId: "cap_1",
				Summary: &pb.TransactionSummary{
					TargetAmountSat: 2000,
				},
			},
		},
		artifacts: map[string]*agentOperationArtifacts{
			"op_1": {
				OperationID:       "op_1",
				SignedPsbt:        psbtBytes,
				SignedTransaction: txBytes,
			},
		},
		capabilities: map[string]*agentCapabilityRecord{
			"cap_1": {
				CapabilityID:  "cap_1",
				WalletID:      defaultAgentWalletID,
				Principal:     "agent:bot",
				Permissions:   []string{capabilityPermissionRenewalPublish},
				CreatedAtUnix: 100,
				ExpiresAtUnix: time.Now().Add(time.Hour).Unix(),
				UpdatedAtUnix: 100,
			},
		},
		reservations:   make(map[string]*agentReservationRecord),
		signerSessions: make(map[string]*agentSignerSessionRecord),
		signerBackend:  &testSignerBackend{},
	}
	server.persistenceLoadOnce.Do(func() {})

	resp, err := server.PublishTransaction(context.Background(),
		&pb.PublishTransactionRequest{
			Meta: &pb.RequestMeta{
				RequestId:    "req_1",
				Principal:    "agent:bot",
				CapabilityId: "cap_1",
			},
			OperationId: "op_1",
		},
	)
	if err != nil {
		t.Fatalf("PublishTransaction error: %v", err)
	}
	if resp.Txid != "txid_1" {
		t.Fatalf("unexpected txid: %s", resp.Txid)
	}
	if len(resp.Meta.GetWarnings()) != 1 {
		t.Fatalf("expected idempotency warning, got %d",
			len(resp.Meta.GetWarnings()))
	}
}

func TestRequireSignerSessionForOperation(t *testing.T) {
	server := &agentWalletServer{
		capabilities: map[string]*agentCapabilityRecord{
			"cap_1": {
				CapabilityID:  "cap_1",
				WalletID:      defaultAgentWalletID,
				Principal:     "agent:bot",
				Permissions:   []string{capabilityPermissionRenewalSubmit},
				ExpiresAtUnix: time.Now().Add(time.Hour).Unix(),
			},
		},
		signerSessions: map[string]*agentSignerSessionRecord{
			"sess_1": {
				SignerSessionID: "sess_1",
				WalletID:        defaultAgentWalletID,
				CapabilityID:    "cap_1",
				Principal:       "agent:bot",
				Permissions:     []string{capabilityPermissionRenewalSubmit},
				ExpiresAtUnix:   time.Now().Add(time.Hour).Unix(),
			},
		},
		signerBackend: &testSignerBackend{
			activeSessions: map[string]struct{}{
				"sess_1": {},
			},
		},
	}

	op := &pb.Operation{
		OperationId:         "op_1",
		WalletId:            defaultAgentWalletID,
		CreatorPrincipal:    "agent:bot",
		CreatorCapabilityId: "cap_1",
	}

	session, capability, err := server.requireSignerSessionForOperation(
		op,
		&pb.RequestMeta{
			Principal:    "agent:bot",
			CapabilityId: "cap_1",
		},
		"sess_1",
		capabilityPermissionRenewalSubmit,
	)
	if err != nil {
		t.Fatalf("requireSignerSessionForOperation error: %v", err)
	}
	if capability.CapabilityID != "cap_1" {
		t.Fatalf("unexpected capability: %s", capability.CapabilityID)
	}
	if session.SignerSessionID != "sess_1" {
		t.Fatalf("unexpected session: %s", session.SignerSessionID)
	}
}

func TestOpenAndCloseSignerSessionUsesSignerBackend(t *testing.T) {
	db := testAgentWalletDB(t)
	store := newAgentWalletPersistentStore(db)
	backend := &testSignerBackend{}

	if err := store.putCapability(&agentCapabilityRecord{
		CapabilityID:  "cap_1",
		WalletID:      defaultAgentWalletID,
		Principal:     "agent:bot",
		Permissions:   []string{capabilityPermissionRenewalSubmit},
		CreatedAtUnix: 100,
		ExpiresAtUnix: time.Now().Add(time.Hour).Unix(),
		UpdatedAtUnix: 100,
	}); err != nil {
		t.Fatalf("putCapability error: %v", err)
	}

	server := &agentWalletServer{
		persistence:    store,
		operations:     make(map[string]*pb.Operation),
		artifacts:      make(map[string]*agentOperationArtifacts),
		reservations:   make(map[string]*agentReservationRecord),
		capabilities:   make(map[string]*agentCapabilityRecord),
		signerSessions: make(map[string]*agentSignerSessionRecord),
		signerBackend:  backend,
	}

	openResp, err := server.OpenSignerSession(context.Background(),
		&pb.OpenSignerSessionRequest{
			Meta: &pb.RequestMeta{
				RequestId:    "req_1",
				Principal:    "agent:bot",
				CapabilityId: "cap_1",
			},
			CapabilityId: "cap_1",
			Passphrase:   []byte("secret"),
		},
	)
	if err != nil {
		t.Fatalf("OpenSignerSession error: %v", err)
	}

	sessionID := openResp.GetSession().GetSignerSessionId()
	if sessionID == "" {
		t.Fatalf("expected signer session id")
	}
	if err := backend.ValidateSession(sessionID); err != nil {
		t.Fatalf("expected backend session to be active: %v", err)
	}

	closeResp, err := server.CloseSignerSession(context.Background(),
		&pb.CloseSignerSessionRequest{
			Meta: &pb.RequestMeta{
				RequestId:    "req_2",
				Principal:    "agent:bot",
				CapabilityId: "cap_1",
			},
			SignerSessionId: sessionID,
		},
	)
	if err != nil {
		t.Fatalf("CloseSignerSession error: %v", err)
	}
	if !closeResp.GetSession().GetClosed() {
		t.Fatalf("expected closed signer session")
	}
	if err := backend.ValidateSession(sessionID); err == nil {
		t.Fatalf("expected backend session to be closed")
	}
}

func TestOpenSignerSessionAllowsEmptyPassphraseForRemoteBackend(t *testing.T) {
	db := testAgentWalletDB(t)
	store := newAgentWalletPersistentStore(db)
	backend := newRemoteAgentSignerBackend(nil)

	if err := store.putCapability(&agentCapabilityRecord{
		CapabilityID:  "cap_1",
		WalletID:      defaultAgentWalletID,
		Principal:     "agent:bot",
		Permissions:   []string{capabilityPermissionRenewalSubmit},
		CreatedAtUnix: 100,
		ExpiresAtUnix: time.Now().Add(time.Hour).Unix(),
		UpdatedAtUnix: 100,
	}); err != nil {
		t.Fatalf("putCapability error: %v", err)
	}

	server := &agentWalletServer{
		persistence:    store,
		operations:     make(map[string]*pb.Operation),
		artifacts:      make(map[string]*agentOperationArtifacts),
		reservations:   make(map[string]*agentReservationRecord),
		capabilities:   make(map[string]*agentCapabilityRecord),
		signerSessions: make(map[string]*agentSignerSessionRecord),
		signerBackend:  backend,
	}

	resp, err := server.OpenSignerSession(context.Background(),
		&pb.OpenSignerSessionRequest{
			Meta: &pb.RequestMeta{
				RequestId:    "req_1",
				Principal:    "agent:bot",
				CapabilityId: "cap_1",
			},
			CapabilityId: "cap_1",
		},
	)
	if err != nil {
		t.Fatalf("OpenSignerSession error: %v", err)
	}

	sessionID := resp.GetSession().GetSignerSessionId()
	if sessionID == "" {
		t.Fatalf("expected signer session id")
	}
	if err := backend.ValidateSession(sessionID); err != nil {
		t.Fatalf("expected remote signer session to be active: %v", err)
	}
}
