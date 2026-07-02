package wallet

import (
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcwallet/waddrmgr"
	"github.com/stretchr/testify/require"
)

func TestRenewalSelectedOutpointCreatesSignedTransaction(t *testing.T) {
	t.Parallel()

	w, cleanup := testWallet(t)
	defer cleanup()

	const (
		inputAmount  = int64(1_000_000)
		targetAmount = int64(400_000)
		feeRate      = btcutil.Amount(1_000)
	)

	sourceAddr, err := w.CurrentAddress(0, waddrmgr.KeyScopeBIP0084)
	require.NoError(t, err)
	sourceScript, err := txscript.PayToAddrScript(sourceAddr)
	require.NoError(t, err)

	targetAddr, err := w.NewAddress(0, waddrmgr.KeyScopeBIP0084)
	require.NoError(t, err)
	targetScript, err := txscript.PayToAddrScript(targetAddr)
	require.NoError(t, err)

	incomingTx := &wire.MsgTx{
		TxIn:  []*wire.TxIn{{}},
		TxOut: []*wire.TxOut{wire.NewTxOut(inputAmount, sourceScript)},
	}
	addUtxo(t, w, incomingTx)
	selected := wire.OutPoint{Hash: incomingTx.TxHash(), Index: 0}

	authoredTx, err := w.txToOutputs(
		[]*wire.TxOut{{Value: targetAmount, PkScript: targetScript}},
		nil, nil, 0, 1, feeRate, CoinSelectionLargest, false,
		[]wire.OutPoint{selected}, alwaysAllowUtxo,
	)
	require.NoError(t, err)
	require.NotNil(t, authoredTx)
	require.Len(t, authoredTx.Tx.TxIn, 1)
	require.Equal(t, selected, authoredTx.Tx.TxIn[0].PreviousOutPoint)

	var targetFound bool
	var totalOut int64
	for _, txOut := range authoredTx.Tx.TxOut {
		totalOut += txOut.Value
		if txOut.Value == targetAmount &&
			string(txOut.PkScript) == string(targetScript) {

			targetFound = true
		}
	}
	require.True(t, targetFound, "renewal target output missing")
	require.Greater(t, inputAmount-totalOut, int64(0), "fee must be positive")

	require.NoError(t, validateMsgTx(
		authoredTx.Tx, authoredTx.PrevScripts, authoredTx.PrevInputValues,
	))
}

func TestWalletLockedStateObservableForRenewalGuards(t *testing.T) {
	t.Parallel()

	w, cleanup := testWallet(t)
	defer cleanup()

	w.Lock()
	require.Eventually(t, w.Locked, time.Second, 10*time.Millisecond)
}

func TestAutoRenewBuildCandidatesAppliesWindowAmountCapAndOrder(t *testing.T) {
	t.Parallel()

	w, cleanup := testWalletWithChainParams(t, &chaincfg.ObtcRegTestParams)
	defer cleanup()

	const tipHeight = int32(100)
	setWalletSyncedTo(t, w, tipHeight)

	addr, err := w.CurrentAddress(0, waddrmgr.KeyScopeBIP0084)
	require.NoError(t, err)
	pkScript, err := txscript.PayToAddrScript(addr)
	require.NoError(t, err)

	mkTx := func(value int64) *wire.MsgTx {
		return &wire.MsgTx{
			TxIn:  []*wire.TxIn{{}},
			TxOut: []*wire.TxOut{wire.NewTxOut(value, pkScript)},
		}
	}

	firstInWindow := mkTx(5_000)
	secondInWindow := mkTx(6_000)
	outsideWindow := mkTx(7_000)
	tooSmall := mkTx(900)

	addUtxoAtHeight(t, w, firstInWindow, 0)   // blocks_to_expiry=100
	addUtxoAtHeight(t, w, secondInWindow, 10) // blocks_to_expiry=110
	addUtxoAtHeight(t, w, outsideWindow, 30)  // blocks_to_expiry=130
	addUtxoAtHeight(t, w, tooSmall, 5)        // amount <= renew amount

	cfg := DefaultAutoRenewRuntimeConfig()
	cfg.Policy.Enabled = true
	cfg.Policy.WindowStartBlocks = 120
	cfg.Policy.WindowEndBlocks = 80
	cfg.Policy.MaxUtxosPerRun = 2
	cfg.Amount = btcutil.Amount(1_000)
	cfg.ExpiryWindowBlocks = 200
	cfg.ExpiringThresholdBlocks = 25

	candidates, err := w.buildAutoRenewCandidates(cfg)
	require.NoError(t, err)
	require.Len(t, candidates, 2)

	require.Equal(t, wire.OutPoint{
		Hash:  firstInWindow.TxHash(),
		Index: 0,
	}, candidates[0].outpoint)
	require.EqualValues(t, 100, candidates[0].blocksToExpiry)
	require.EqualValues(t, 5_000, candidates[0].amountSat)

	require.Equal(t, wire.OutPoint{
		Hash:  secondInWindow.TxHash(),
		Index: 0,
	}, candidates[1].outpoint)
	require.EqualValues(t, 110, candidates[1].blocksToExpiry)
	require.EqualValues(t, 6_000, candidates[1].amountSat)
}
