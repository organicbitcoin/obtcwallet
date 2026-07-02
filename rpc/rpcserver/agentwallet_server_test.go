package rpcserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcwallet/chain"
	pb "github.com/btcsuite/btcwallet/rpc/agentwalletrpc"
	"github.com/btcsuite/btcwallet/waddrmgr"
	"github.com/btcsuite/btcwallet/wallet"
	"github.com/btcsuite/btcwallet/walletdb"
	"github.com/btcsuite/btcwallet/wtxmgr"
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

func (b *testSignerBackend) SignerProofMetadata() agentSignerProofMetadata {
	return agentSignerProofMetadata{}
}

func (b *testSignerBackend) OpenSession(sessionID string, _ []byte,
	_ time.Duration, _ string, _ func()) error {

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

func (b *testSignerBackend) CloseSession(sessionID, _ string) error {
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

type unprotectedFinalizingSignerBackend struct {
	testSignerBackend

	wallet *wallet.Wallet
}

func (b *unprotectedFinalizingSignerBackend) FinalizePsbt(sessionID string,
	_ uint32, packet *psbt.Packet) error {

	if err := b.ValidateSession(sessionID); err != nil {
		return err
	}

	tx := packet.UnsignedTx
	prevFetcher := wallet.PsbtPrevOutputFetcher(packet)
	sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)
	for idx, txIn := range tx.TxIn {
		prevOut := prevFetcher.FetchPrevOutput(txIn.PreviousOutPoint)
		if prevOut == nil {
			return status.Errorf(codes.InvalidArgument,
				"input %d missing previous output", idx)
		}

		_, addrs, _, err := txscript.ExtractPkScriptAddrs(
			prevOut.PkScript, b.wallet.ChainParams(),
		)
		if err != nil || len(addrs) == 0 {
			return status.Errorf(codes.InvalidArgument,
				"input %d has unsupported previous output", idx)
		}
		privKey, err := b.wallet.PrivKeyForAddress(addrs[0])
		if err != nil {
			return err
		}

		witness, err := txscript.WitnessSignature(
			tx, sigHashes, idx, prevOut.Value, prevOut.PkScript,
			txscript.SigHashAll, privKey, true,
		)
		if err != nil {
			return err
		}

		var witnessBytes bytes.Buffer
		if err := psbt.WriteTxWitness(&witnessBytes, witness); err != nil {
			return err
		}
		packet.Inputs[idx].FinalScriptWitness = witnessBytes.Bytes()
	}

	return psbt.MaybeFinalizeAll(packet)
}

type testRPCChainClient struct {
	blockStamp *waddrmgr.BlockStamp
}

var _ chain.Interface = (*testRPCChainClient)(nil)

func (c *testRPCChainClient) Start(context.Context) error { return nil }
func (c *testRPCChainClient) Stop()                       {}
func (c *testRPCChainClient) WaitForShutdown()            {}
func (c *testRPCChainClient) GetBestBlock() (*chainhash.Hash, int32, error) {
	if c.blockStamp != nil {
		return &c.blockStamp.Hash, c.blockStamp.Height, nil
	}
	return &chainhash.Hash{}, 0, nil
}
func (c *testRPCChainClient) GetBlock(*chainhash.Hash) (*wire.MsgBlock, error) {
	return nil, nil
}
func (c *testRPCChainClient) GetBlockHash(int64) (*chainhash.Hash, error) {
	return &chainhash.Hash{}, nil
}
func (c *testRPCChainClient) GetBlockHeader(*chainhash.Hash) (
	*wire.BlockHeader, error) {

	return &wire.BlockHeader{Timestamp: time.Unix(1234, 0)}, nil
}
func (c *testRPCChainClient) IsCurrent() bool { return true }
func (c *testRPCChainClient) FilterBlocks(*chain.FilterBlocksRequest) (
	*chain.FilterBlocksResponse, error) {

	return nil, nil
}
func (c *testRPCChainClient) BlockStamp() (*waddrmgr.BlockStamp, error) {
	if c.blockStamp != nil {
		stamp := *c.blockStamp
		return &stamp, nil
	}
	return &waddrmgr.BlockStamp{}, nil
}
func (c *testRPCChainClient) SendRawTransaction(*wire.MsgTx, bool) (
	*chainhash.Hash, error) {

	return &chainhash.Hash{}, nil
}
func (c *testRPCChainClient) Rescan(*chainhash.Hash, []btcutil.Address,
	map[wire.OutPoint]btcutil.Address) error {

	return nil
}
func (c *testRPCChainClient) NotifyReceived([]btcutil.Address) error {
	return nil
}
func (c *testRPCChainClient) NotifyBlocks() error { return nil }
func (c *testRPCChainClient) Notifications() <-chan interface{} {
	return make(chan interface{})
}
func (c *testRPCChainClient) BackEnd() string { return "test" }
func (c *testRPCChainClient) TestMempoolAccept([]*wire.MsgTx,
	float64) ([]*btcjson.TestMempoolAcceptResult, error) {

	return nil, nil
}
func (c *testRPCChainClient) MapRPCErr(err error) error { return err }

func testAgentWallet(t *testing.T, params *chaincfg.Params,
	tipHeight int32) *wallet.Wallet {

	t.Helper()

	seed, err := hdkeychain.GenerateSeed(hdkeychain.MinSeedBytes)
	if err != nil {
		t.Fatalf("unable to generate wallet seed: %v", err)
	}
	pubPass := []byte("public")
	privPass := []byte("private")

	loader := wallet.NewLoader(
		params, t.TempDir(), true, 10*time.Second, 250,
		wallet.WithWalletSyncRetryInterval(10*time.Millisecond),
	)
	w, err := loader.CreateNewWallet(pubPass, privPass, seed, time.Now())
	if err != nil {
		t.Fatalf("unable to create wallet: %v", err)
	}
	if err := w.Unlock(privPass, time.After(10*time.Minute)); err != nil {
		t.Fatalf("unable to unlock wallet: %v", err)
	}

	w.SynchronizeRPC(&testRPCChainClient{
		blockStamp: &waddrmgr.BlockStamp{
			Height: tipHeight,
			Hash:   chainhash.Hash{},
		},
	})
	w.SetChainSynced(true)

	t.Cleanup(func() {
		w.Stop()
		w.WaitForShutdown()
	})

	return w
}

func setAgentWalletSyncedTo(t *testing.T, w *wallet.Wallet, height int32) {
	t.Helper()

	err := walletdb.Update(w.Database(), func(tx walletdb.ReadWriteTx) error {
		ns := tx.ReadWriteBucket([]byte("waddrmgr"))
		return w.Manager.SetSyncedTo(ns, &waddrmgr.BlockStamp{
			Height: height,
			Hash:   chainhash.Hash{},
		})
	})
	if err != nil {
		t.Fatalf("unable to set wallet sync height: %v", err)
	}
}

func addAgentWalletUtxo(t *testing.T, w *wallet.Wallet, tx *wire.MsgTx,
	height int32) {

	t.Helper()

	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		t.Fatalf("unable to serialize tx: %v", err)
	}
	rec, err := wtxmgr.NewTxRecord(buf.Bytes(), time.Now())
	if err != nil {
		t.Fatalf("unable to create tx record: %v", err)
	}

	var blockHash chainhash.Hash
	binary.LittleEndian.PutUint32(blockHash[:4], uint32(height))
	block := &wtxmgr.BlockMeta{
		Block: wtxmgr.Block{
			Hash:   blockHash,
			Height: height,
		},
		Time: time.Unix(1234, 0),
	}

	err = walletdb.Update(w.Database(), func(dbtx walletdb.ReadWriteTx) error {
		ns := dbtx.ReadWriteBucket([]byte("wtxmgr"))
		if err := w.TxStore.InsertTx(ns, rec, block); err != nil {
			return err
		}
		for idx := range tx.TxOut {
			if err := w.TxStore.AddCredit(
				ns, rec, block, uint32(idx), false,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unable to add wallet UTXO: %v", err)
	}
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

func TestBuildExpiryRiskItemsLifecycleFields(t *testing.T) {
	outputs := []*wallet.TransactionOutput{
		{
			OutPoint: wire.OutPoint{
				Hash:  chainhash.Hash{0x01},
				Index: 0,
			},
			Output: wire.TxOut{Value: 10_000},
			ContainingBlock: wallet.BlockIdentity{
				Height: 120,
			},
		},
		{
			OutPoint: wire.OutPoint{
				Hash:  chainhash.Hash{0x02},
				Index: 0,
			},
			Output: wire.TxOut{Value: 10_000},
			ContainingBlock: wallet.BlockIdentity{
				Height: 70,
			},
		},
		{
			OutPoint: wire.OutPoint{
				Hash:  chainhash.Hash{0x03},
				Index: 0,
			},
			Output: wire.TxOut{Value: 500},
			ContainingBlock: wallet.BlockIdentity{
				Height: 40,
			},
		},
	}
	policy := agentExpiryPolicy{
		WindowBlocks:             100,
		ExpiringThresholdBlocks:  20,
		RenewWarningBlocks:       12,
		DustThresholdSat:         wallet.CompatibilityDustThresholdSat,
		ProjectedReclaimRatioBps: wallet.DefaultProjectedReclaimRatioBps,
		Source:                   "test",
	}

	items, err := buildExpiryRiskItems(outputs, 150, policy, 0, nil)
	if err != nil {
		t.Fatalf("buildExpiryRiskItems error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	byOutpoint := make(map[string]*pb.ExpiryRisk, len(items))
	for _, item := range items {
		byOutpoint[item.GetOutpoint()] = item
	}

	ok := byOutpoint[outputs[0].OutPoint.String()]
	if ok.GetAmountSat() != 10_000 || ok.GetCreateHeight() != 120 ||
		ok.GetExpiryHeight() != 220 || ok.GetBlocksToExpiry() != 70 ||
		ok.GetDaysToExpiry() != wallet.EstimateDaysToExpiry(70) ||
		ok.GetStatus() != string(wallet.ExpiryStatusOK) ||
		ok.GetDustRisk() {

		t.Fatalf("unexpected ok expiry risk: %+v", ok)
	}

	expiring := byOutpoint[outputs[1].OutPoint.String()]
	if expiring.GetAmountSat() != 10_000 ||
		expiring.GetCreateHeight() != 70 ||
		expiring.GetExpiryHeight() != 170 ||
		expiring.GetBlocksToExpiry() != 20 ||
		expiring.GetStatus() != string(wallet.ExpiryStatusExpiring) ||
		expiring.GetDustRisk() {

		t.Fatalf("unexpected expiring risk: %+v", expiring)
	}

	expired := byOutpoint[outputs[2].OutPoint.String()]
	if expired.GetAmountSat() != 500 ||
		expired.GetCreateHeight() != 40 ||
		expired.GetExpiryHeight() != 140 ||
		expired.GetBlocksToExpiry() != 0 ||
		expired.GetDaysToExpiry() != 0 ||
		expired.GetStatus() != string(wallet.ExpiryStatusExpired) ||
		!expired.GetDustRisk() {

		t.Fatalf("unexpected expired risk: %+v", expired)
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

func TestWarningsFromExpiryRisksNearExpiryBoundaries(t *testing.T) {
	items := []*pb.ExpiryRisk{
		{Status: string(wallet.ExpiryStatusExpiring), BlocksToExpiry: 13},
	}
	warnings := warningsFromExpiryRisks(items)
	if len(warnings) != 1 ||
		!strings.Contains(warnings[0], "inside the expiring window") {

		t.Fatalf("unexpected non-near warnings: %#v", warnings)
	}

	items = []*pb.ExpiryRisk{
		{Status: string(wallet.ExpiryStatusExpiring), BlocksToExpiry: 12},
	}
	warnings = warningsFromExpiryRisks(items)
	if len(warnings) != 2 ||
		!strings.Contains(warnings[0], "near expiry") ||
		!strings.Contains(warnings[1], "inside the expiring window") {

		t.Fatalf("unexpected near-expiry warnings: %#v", warnings)
	}

	items = []*pb.ExpiryRisk{
		{Status: string(wallet.ExpiryStatusExpiring), BlocksToExpiry: 1},
	}
	warnings = warningsFromExpiryRisks(items)
	if len(warnings) != 2 ||
		!strings.Contains(warnings[0], "too close to expiry") {

		t.Fatalf("unexpected too-late-next-block warnings: %#v", warnings)
	}

	items = []*pb.ExpiryRisk{
		{Status: string(wallet.ExpiryStatusExpired), BlocksToExpiry: 0},
		{Status: string(wallet.ExpiryStatusExpiring), BlocksToExpiry: 1},
	}
	warnings = warningsFromExpiryRisks(items)
	if len(warnings) != 1 ||
		!strings.Contains(warnings[0], "expired UTXOs") {

		t.Fatalf("expired warning should dominate, got %#v", warnings)
	}
}

func TestBuildPreviewDecisionReasonsAreAuditable(t *testing.T) {
	reasons := buildPreviewDecisionReasons(opSummaryDecisionContext{
		outpointCount:      2,
		policySource:       "request_override",
		reservationID:      "res_1",
		targetAmountSat:    1_000_000,
		feeRateSatPerKB:    5_000,
		minConfirmations:   6,
		targetAddress:      "tb1qtarget",
		walletDryRunNotice: true,
	})
	joined := strings.Join(reasons, "\n")
	for _, want := range []string{
		"selected_outpoints=2",
		"policy_source=request_override",
		"target_amount_sat=1000000",
		"fee_rate_sat_per_kb=5000",
		"min_confirmations=6",
		"target_address=tb1qtarget",
		"reservation_id=res_1",
		"preview_only=true",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("decision reasons missing %q: %#v", want, reasons)
		}
	}
	for _, forbidden := range []string{"passphrase", "private", "secret"} {
		if strings.Contains(strings.ToLower(joined), forbidden) {
			t.Fatalf("decision reasons leak sensitive term %q: %#v",
				forbidden, reasons)
		}
	}
}

func TestParseOutPointStringsUniqueErrorsAreActionable(t *testing.T) {
	_, err := parseOutPointStringsUnique(nil)
	if err == nil || !strings.Contains(err.Error(), "outpoints must not be empty") {
		t.Fatalf("unexpected empty outpoints error: %v", err)
	}

	_, err = parseOutPointStringsUnique([]string{"not-an-outpoint"})
	if err == nil || !strings.Contains(err.Error(), "invalid outpoint format") {
		t.Fatalf("unexpected invalid outpoint error: %v", err)
	}

	outpoint := chainhash.Hash{0x01}.String() + ":0"
	_, err = parseOutPointStringsUnique([]string{outpoint, outpoint})
	if err == nil || !strings.Contains(err.Error(), "duplicate outpoint") ||
		!strings.Contains(err.Error(), outpoint) {

		t.Fatalf("unexpected duplicate outpoint error: %v", err)
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
		LatestPolicySnapshot: &pb.PolicySnapshot{
			Verdict:  "ok",
			Warnings: []string{"snapshot warning"},
			EffectivePolicy: &pb.ExpiryPolicy{
				WindowBlocks: 100,
			},
			ExpiryRisks: []*pb.ExpiryRisk{{Outpoint: "snap:0"}},
		},
		LatestSignerProof: &pb.SignerProof{
			ProofId:          "proof_1",
			BackendId:        "remote",
			SignerSessionId:  "sess_1",
			SignedPsbtSha256: "abc",
		},
		DecisionLog: []*pb.DecisionLogEntry{{
			EntryId:   "dlg_1",
			Stage:     decisionLogStageSign,
			Reasons:   []string{"reason"},
			Warnings:  []string{"decision warning"},
			Verdict:   "ok",
			TipHeight: 123,
			PolicySnapshot: &pb.PolicySnapshot{
				Verdict: "ok",
			},
			SignerProof: &pb.SignerProof{
				ProofId: "proof_1",
			},
		}},
	}

	cloned := cloneOperation(original)
	cloned.Warnings[0] = "changed"
	cloned.Summary.TargetAmountSat = 456
	cloned.ExpiryRisks[0].Outpoint = "mutated"
	cloned.EffectivePolicy.Source = "override"
	cloned.History[0].Warnings[0] = "changed history"
	cloned.LatestPolicySnapshot.Warnings[0] = "changed snapshot"
	cloned.LatestSignerProof.SignedPsbtSha256 = "def"
	cloned.DecisionLog[0].Reasons[0] = "changed reason"

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
	if original.LatestPolicySnapshot.Warnings[0] != "snapshot warning" {
		t.Fatalf("latest policy snapshot was not cloned")
	}
	if original.LatestSignerProof.SignedPsbtSha256 != "abc" {
		t.Fatalf("latest signer proof was not cloned")
	}
	if original.DecisionLog[0].Reasons[0] != "reason" {
		t.Fatalf("decision log was not cloned")
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

func TestGetExpiryRiskRejectsUnsyncedWallet(t *testing.T) {
	server := &agentWalletServer{
		wallet: &wallet.Wallet{},
	}

	_, err := server.GetExpiryRisk(context.Background(),
		&pb.GetExpiryRiskRequest{},
	)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected failed precondition, got %v", err)
	}
	if err == nil || err.Error() != "rpc error: code = FailedPrecondition desc = wallet chain state is not synced at height 0" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPreviewRenewalRejectsUnsyncedWallet(t *testing.T) {
	server := &agentWalletServer{
		wallet:       &wallet.Wallet{},
		operations:   make(map[string]*pb.Operation),
		artifacts:    make(map[string]*agentOperationArtifacts),
		reservations: make(map[string]*agentReservationRecord),
	}
	server.persistenceLoadOnce.Do(func() {})

	_, err := server.PreviewRenewal(context.Background(),
		&pb.PreviewRenewalRequest{
			TargetAmountSat: 1,
		},
	)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected failed precondition, got %v", err)
	}
}

func TestSubmitRenewalRejectsUnsyncedDraftOperation(t *testing.T) {
	server := &agentWalletServer{
		wallet: &wallet.Wallet{},
		operations: map[string]*pb.Operation{
			"op_1": {
				OperationId:         "op_1",
				WalletId:            defaultAgentWalletID,
				Kind:                operationKindRenewSubmit,
				State:               operationStateDraft,
				CreatorPrincipal:    "agent:bot",
				CreatorCapabilityId: "cap_1",
				Summary: &pb.TransactionSummary{
					TargetAmountSat: 2000,
				},
			},
		},
		artifacts: map[string]*agentOperationArtifacts{
			"op_1": {OperationID: "op_1"},
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
		signerSessions: map[string]*agentSignerSessionRecord{
			"sess_1": {
				SignerSessionID: "sess_1",
				WalletID:        defaultAgentWalletID,
				CapabilityID:    "cap_1",
				Principal:       "agent:bot",
				Permissions:     []string{capabilityPermissionRenewalSubmit},
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

	_, err := server.SubmitRenewal(context.Background(),
		&pb.SubmitRenewalRequest{
			Meta: &pb.RequestMeta{
				RequestId:    "req_1",
				Principal:    "agent:bot",
				CapabilityId: "cap_1",
			},
			OperationId:     "op_1",
			SignerSessionId: "sess_1",
		},
	)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected failed precondition, got %v", err)
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

func TestSignPsbtRejectsRemoteSignerWithoutOBTCReplayProtection(t *testing.T) {
	params := &chaincfg.ObtcTestNetParams
	activeAt := chaincfg.GetOBTCReplayProtectionHeight(params)

	w := testAgentWallet(t, params, activeAt)
	setAgentWalletSyncedTo(t, w, activeAt-2)

	sourceAddr, err := w.CurrentAddress(0, waddrmgr.KeyScopeBIP0084)
	if err != nil {
		t.Fatalf("CurrentAddress error: %v", err)
	}
	sourceScript, err := txscript.PayToAddrScript(sourceAddr)
	if err != nil {
		t.Fatalf("source script error: %v", err)
	}
	targetAddr, err := w.NewAddress(0, waddrmgr.KeyScopeBIP0084)
	if err != nil {
		t.Fatalf("NewAddress error: %v", err)
	}

	incomingTx := &wire.MsgTx{
		TxIn: []*wire.TxIn{{}},
		TxOut: []*wire.TxOut{{
			Value:    100000,
			PkScript: sourceScript,
		}},
	}
	addAgentWalletUtxo(t, w, incomingTx, activeAt-20)
	selected := wire.OutPoint{
		Hash:  incomingTx.TxHash(),
		Index: 0,
	}

	store := newAgentWalletPersistentStore(testAgentWalletDB(t))
	server := &agentWalletServer{
		wallet:               w,
		persistence:          store,
		expiryPolicyProvider: compatibilityExpiryPolicyProvider{},
		operations: map[string]*pb.Operation{
			"op_1": {
				OperationId:         "op_1",
				WalletId:            defaultAgentWalletID,
				Kind:                operationKindRenewSign,
				State:               operationStateDraft,
				Outpoints:           []string{selected.String()},
				AccountNumber:       0,
				MinConfirmations:    1,
				CreatorPrincipal:    "agent:bot",
				CreatorCapabilityId: "cap_1",
				Summary: &pb.TransactionSummary{
					TargetAddress:   targetAddr.EncodeAddress(),
					TargetAmountSat: 50000,
					FeeRateSatPerKb: 1000,
					InputCount:      1,
					TotalInputSat:   100000,
				},
			},
		},
		artifacts: map[string]*agentOperationArtifacts{
			"op_1": {OperationID: "op_1"},
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
		signerBackend: &unprotectedFinalizingSignerBackend{
			testSignerBackend: testSignerBackend{
				activeSessions: map[string]struct{}{
					"sess_1": {},
				},
			},
			wallet: w,
		},
	}
	server.persistenceLoadOnce.Do(func() {})

	_, err = server.SignPsbt(context.Background(), &pb.SignPsbtRequest{
		Meta: &pb.RequestMeta{
			RequestId:    "req_1",
			Principal:    "agent:bot",
			CapabilityId: "cap_1",
		},
		OperationId:     "op_1",
		SignerSessionId: "sess_1",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected failed precondition, got %v", err)
	}
	if !strings.Contains(err.Error(), "replay-protection validation") {
		t.Fatalf("unexpected error: %v", err)
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
