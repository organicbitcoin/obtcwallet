package rpcserver

import (
	"context"
	"testing"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	pb "github.com/btcsuite/btcwallet/rpc/agentwalletrpc"
	"github.com/btcsuite/btcwallet/wallet"
)

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

func TestCloneOperationClonesEffectivePolicy(t *testing.T) {
	original := &pb.Operation{
		OperationId: "op_1",
		Warnings:    []string{"warning"},
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
	}

	cloned := cloneOperation(original)
	cloned.Warnings[0] = "changed"
	cloned.Summary.TargetAmountSat = 456
	cloned.ExpiryRisks[0].Outpoint = "mutated"
	cloned.EffectivePolicy.Source = "override"

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
}

func TestSubmitRenewalAlreadyPublishedReturnsRecordedMetadata(t *testing.T) {
	server := &agentWalletServer{
		operations: map[string]*pb.Operation{
			"op_1": {
				OperationId: "op_1",
				Kind:        operationKindRenewSubmit,
				State:       operationStatePublished,
				Txid:        "txid_1",
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
	}

	resp, err := server.SubmitRenewal(context.Background(),
		&pb.SubmitRenewalRequest{
			Meta:        &pb.RequestMeta{RequestId: "req_1"},
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
}
