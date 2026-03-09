package rpcserver

import (
	"path/filepath"
	"testing"
	"time"

	pb "github.com/btcsuite/btcwallet/rpc/agentwalletrpc"
	"github.com/btcsuite/btcwallet/walletdb"
	_ "github.com/btcsuite/btcwallet/walletdb/bdb"
)

func testAgentWalletDB(t *testing.T) walletdb.DB {
	t.Helper()

	db, err := walletdb.Create(
		"bdb",
		filepath.Join(t.TempDir(), "agentwallet.db"),
		true,
		10*time.Second,
		false,
	)
	if err != nil {
		t.Fatalf("unable to create agent wallet db: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("unable to close agent wallet db: %v", err)
		}
	})

	return db
}

func TestAgentWalletPersistentStoreRoundTrip(t *testing.T) {
	db := testAgentWalletDB(t)
	store := newAgentWalletPersistentStore(db)

	originalOp := &pb.Operation{
		OperationId:          "op_1",
		Kind:                 operationKindRenewPreview,
		State:                operationStateDraft,
		WalletId:             "default",
		Outpoints:            []string{"txid:0"},
		CreatorPrincipal:     "agent:bot",
		CreatorCapabilityId:  "cap_1",
		CreateIdempotencyKey: "idem_1",
		Summary: &pb.TransactionSummary{
			TargetAddress:   "bcrt1test",
			TargetAmountSat: 1234,
		},
		EffectivePolicy: &pb.ExpiryPolicy{
			WindowBlocks:             100,
			ExpiringThresholdBlocks:  10,
			ProjectedReclaimRatioBps: 7000,
			Source:                   "chaincfg",
		},
		LatestPolicySnapshot: &pb.PolicySnapshot{
			Verdict: "ok",
			EffectivePolicy: &pb.ExpiryPolicy{
				WindowBlocks: 100,
				Source:       "chaincfg",
			},
			Warnings:         []string{"snapshot warning"},
			TipHeight:        120,
			TargetAmountSat:  1234,
			FeeRateSatPerKb:  99,
			MinConfirmations: 1,
			ReservationId:    "res_1",
			ExpiryRisks:      []*pb.ExpiryRisk{{Outpoint: "txid:0"}},
		},
		LatestSignerProof: &pb.SignerProof{
			ProofId:            "proof_1",
			BackendId:          "remote",
			BackendMode:        "remote",
			RemoteEndpoint:     "127.0.0.1:10009",
			SignerSessionId:    "sess_1",
			CapabilityId:       "cap_1",
			Principal:          "agent:bot",
			ProofType:          signerProofTypePsbtFinalize,
			UnsignedPsbtSha256: "u1",
			SignedPsbtSha256:   "s1",
			SignedTxSha256:     "t1",
			SignedAtUnix:       102,
		},
		History: []*pb.OperationEvent{{
			EventId:       "evt_1",
			Action:        operationActionPreviewCreated,
			ToState:       operationStateDraft,
			RequestId:     "req_1",
			Principal:     "agent:bot",
			CapabilityId:  "cap_1",
			Warnings:      []string{"preview warning"},
			CreatedAtUnix: 101,
		}},
		DecisionLog: []*pb.DecisionLogEntry{{
			EntryId:       "dlg_1",
			Stage:         decisionLogStagePreview,
			RequestId:     "req_1",
			Principal:     "agent:bot",
			CapabilityId:  "cap_1",
			Verdict:       "ok",
			Reasons:       []string{"selected_outpoints=1"},
			Warnings:      []string{"preview warning"},
			TipHeight:     120,
			CreatedAtUnix: 101,
			PolicySnapshot: &pb.PolicySnapshot{
				Verdict: "ok",
			},
			SignerProof: &pb.SignerProof{
				ProofId: "proof_1",
			},
		}},
	}
	originalReservation := &agentReservationRecord{
		ReservationID: "res_1",
		WalletID:      "default",
		AccountNumber: 0,
		Outpoints:     []string{"txid:0"},
		ExpiresAtUnix: 111,
		CreatedAtUnix: 100,
		UpdatedAtUnix: 100,
	}
	originalArtifacts := &agentOperationArtifacts{
		OperationID:       "op_1",
		UnsignedPsbt:      []byte{0x01, 0x02},
		SignedPsbt:        []byte{0x03, 0x04},
		SignedTransaction: []byte{0x05, 0x06},
	}
	originalCapability := &agentCapabilityRecord{
		CapabilityID:  "cap_1",
		WalletID:      "default",
		Principal:     "agent:bot",
		Permissions:   []string{capabilityPermissionRenewalSubmit},
		CreatedAtUnix: 100,
		ExpiresAtUnix: 200,
		UpdatedAtUnix: 100,
	}
	originalSession := &agentSignerSessionRecord{
		SignerSessionID: "sess_1",
		WalletID:        "default",
		CapabilityID:    "cap_1",
		Principal:       "agent:bot",
		Permissions:     []string{capabilityPermissionRenewalSubmit},
		CreatedAtUnix:   100,
		ExpiresAtUnix:   120,
		UpdatedAtUnix:   100,
	}

	if err := store.putOperationBundle(originalOp, originalArtifacts); err != nil {
		t.Fatalf("putOperationBundle error: %v", err)
	}
	if err := store.putReservation(originalReservation); err != nil {
		t.Fatalf("putReservation error: %v", err)
	}
	if err := store.putCapability(originalCapability); err != nil {
		t.Fatalf("putCapability error: %v", err)
	}
	if err := store.putSignerSession(originalSession); err != nil {
		t.Fatalf("putSignerSession error: %v", err)
	}

	operations, reservations, artifacts, capabilities, sessions, err :=
		store.load()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	loadedOp, ok := operations["op_1"]
	if !ok {
		t.Fatalf("expected operation to be loaded")
	}
	if loadedOp.GetSummary().GetTargetAmountSat() != 1234 {
		t.Fatalf("unexpected loaded operation amount: %d",
			loadedOp.GetSummary().GetTargetAmountSat())
	}
	if loadedOp.GetEffectivePolicy().GetSource() != "chaincfg" {
		t.Fatalf("unexpected loaded effective policy source: %s",
			loadedOp.GetEffectivePolicy().GetSource())
	}
	if loadedOp.GetCreatorPrincipal() != "agent:bot" {
		t.Fatalf("unexpected loaded creator principal: %s",
			loadedOp.GetCreatorPrincipal())
	}
	if len(loadedOp.GetHistory()) != 1 ||
		loadedOp.GetHistory()[0].GetAction() != operationActionPreviewCreated {

		t.Fatalf("unexpected loaded history: %#v", loadedOp.GetHistory())
	}
	if loadedOp.GetLatestPolicySnapshot().GetReservationId() != "res_1" {
		t.Fatalf("unexpected loaded latest policy snapshot: %#v",
			loadedOp.GetLatestPolicySnapshot())
	}
	if loadedOp.GetLatestSignerProof().GetRemoteEndpoint() != "127.0.0.1:10009" {
		t.Fatalf("unexpected loaded latest signer proof: %#v",
			loadedOp.GetLatestSignerProof())
	}
	if len(loadedOp.GetDecisionLog()) != 1 ||
		loadedOp.GetDecisionLog()[0].GetEntryId() != "dlg_1" {
		t.Fatalf("unexpected loaded decision log: %#v",
			loadedOp.GetDecisionLog())
	}

	loadedReservation, ok := reservations["res_1"]
	if !ok {
		t.Fatalf("expected reservation to be loaded")
	}
	if loadedReservation.ExpiresAtUnix != 111 {
		t.Fatalf("unexpected reservation expiry: %d",
			loadedReservation.ExpiresAtUnix)
	}
	if len(loadedReservation.Outpoints) != 1 ||
		loadedReservation.Outpoints[0] != "txid:0" {

		t.Fatalf("unexpected reservation outpoints: %#v",
			loadedReservation.Outpoints)
	}

	loadedArtifacts, ok := artifacts["op_1"]
	if !ok {
		t.Fatalf("expected artifacts to be loaded")
	}
	if len(loadedArtifacts.SignedTransaction) != 2 ||
		loadedArtifacts.SignedTransaction[0] != 0x05 {

		t.Fatalf("unexpected signed transaction artifacts: %#v",
			loadedArtifacts.SignedTransaction)
	}

	loadedCapability, ok := capabilities["cap_1"]
	if !ok {
		t.Fatalf("expected capability to be loaded")
	}
	if loadedCapability.Principal != "agent:bot" {
		t.Fatalf("unexpected capability principal: %s",
			loadedCapability.Principal)
	}

	loadedSession, ok := sessions["sess_1"]
	if !ok {
		t.Fatalf("expected signer session to be loaded")
	}
	if loadedSession.CapabilityID != "cap_1" {
		t.Fatalf("unexpected signer session capability: %s",
			loadedSession.CapabilityID)
	}
}

func TestAgentWalletServerLoadsPersistedState(t *testing.T) {
	db := testAgentWalletDB(t)
	store := newAgentWalletPersistentStore(db)

	if err := store.putOperation(&pb.Operation{
		OperationId: "op_1",
		Kind:        operationKindRenewSubmit,
		State:       operationStatePublished,
		Txid:        "txid_1",
	}); err != nil {
		t.Fatalf("putOperation error: %v", err)
	}
	if err := store.putReservation(&agentReservationRecord{
		ReservationID: "res_1",
		WalletID:      "default",
		CreatedAtUnix: 100,
		UpdatedAtUnix: 100,
	}); err != nil {
		t.Fatalf("putReservation error: %v", err)
	}

	server := &agentWalletServer{
		persistence:    store,
		operations:     make(map[string]*pb.Operation),
		artifacts:      make(map[string]*agentOperationArtifacts),
		reservations:   make(map[string]*agentReservationRecord),
		capabilities:   make(map[string]*agentCapabilityRecord),
		signerSessions: make(map[string]*agentSignerSessionRecord),
	}

	if err := server.ensurePersistenceLoaded(); err != nil {
		t.Fatalf("ensurePersistenceLoaded error: %v", err)
	}

	if server.operations["op_1"].GetTxid() != "txid_1" {
		t.Fatalf("unexpected loaded txid: %s",
			server.operations["op_1"].GetTxid())
	}
	if server.reservations["res_1"].WalletID != "default" {
		t.Fatalf("unexpected loaded reservation wallet id: %s",
			server.reservations["res_1"].WalletID)
	}
}

func TestMarkReservationReleasedPersistsState(t *testing.T) {
	db := testAgentWalletDB(t)
	store := newAgentWalletPersistentStore(db)

	if err := store.putReservation(&agentReservationRecord{
		ReservationID: "res_1",
		WalletID:      "default",
		CreatedAtUnix: 100,
		UpdatedAtUnix: 100,
	}); err != nil {
		t.Fatalf("putReservation error: %v", err)
	}

	server := &agentWalletServer{
		persistence:    store,
		operations:     make(map[string]*pb.Operation),
		artifacts:      make(map[string]*agentOperationArtifacts),
		reservations:   make(map[string]*agentReservationRecord),
		capabilities:   make(map[string]*agentCapabilityRecord),
		signerSessions: make(map[string]*agentSignerSessionRecord),
	}

	if err := server.ensurePersistenceLoaded(); err != nil {
		t.Fatalf("ensurePersistenceLoaded error: %v", err)
	}
	if err := server.markReservationReleased("res_1", 222); err != nil {
		t.Fatalf("markReservationReleased error: %v", err)
	}

	_, reservations, _, _, _, err := store.load()
	if err != nil {
		t.Fatalf("reload error: %v", err)
	}

	record := reservations["res_1"]
	if !record.Released {
		t.Fatalf("expected reservation to be marked released")
	}
	if record.ReleasedAtUnix != 222 {
		t.Fatalf("unexpected released_at_unix: %d", record.ReleasedAtUnix)
	}
	if record.UpdatedAtUnix != 222 {
		t.Fatalf("unexpected updated_at_unix: %d", record.UpdatedAtUnix)
	}
}

func TestEnsurePersistenceLoadedClosesRecoveredSignerSessions(t *testing.T) {
	db := testAgentWalletDB(t)
	store := newAgentWalletPersistentStore(db)

	if err := store.putSignerSession(&agentSignerSessionRecord{
		SignerSessionID: "sess_1",
		WalletID:        "default",
		CapabilityID:    "cap_1",
		Principal:       "agent:bot",
		Permissions:     []string{capabilityPermissionRenewalSubmit},
		CreatedAtUnix:   100,
		ExpiresAtUnix:   500,
		UpdatedAtUnix:   100,
	}); err != nil {
		t.Fatalf("putSignerSession error: %v", err)
	}

	server := &agentWalletServer{
		persistence:    store,
		operations:     make(map[string]*pb.Operation),
		artifacts:      make(map[string]*agentOperationArtifacts),
		reservations:   make(map[string]*agentReservationRecord),
		capabilities:   make(map[string]*agentCapabilityRecord),
		signerSessions: make(map[string]*agentSignerSessionRecord),
	}

	if err := server.ensurePersistenceLoaded(); err != nil {
		t.Fatalf("ensurePersistenceLoaded error: %v", err)
	}

	session := server.signerSessions["sess_1"]
	if !session.Closed {
		t.Fatalf("expected recovered signer session to be closed")
	}
	if session.CloseReason != signerSessionCloseReasonServiceRestart {
		t.Fatalf("unexpected close reason: %s", session.CloseReason)
	}
}
