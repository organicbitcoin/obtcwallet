// Copyright (c) 2020 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcwallet/waddrmgr"
)

// TestComputeInputScript checks that the wallet can create the full input
// script for supported wallet outputs.
func TestComputeInputScript(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name               string
		scope              waddrmgr.KeyScope
		expectedScriptLen  int
		expectedWitnessLen int
	}{{
		name:               "BIP084 P2WKH",
		scope:              waddrmgr.KeyScopeBIP0084,
		expectedScriptLen:  0,
		expectedWitnessLen: 2,
	}, {
		name:               "BIP049 nested P2WKH",
		scope:              waddrmgr.KeyScopeBIP0049Plus,
		expectedScriptLen:  23,
		expectedWitnessLen: 2,
	}, {
		name:               "BIP044 P2PKH",
		scope:              waddrmgr.KeyScopeBIP0044,
		expectedScriptLen:  -1,
		expectedWitnessLen: 0,
	}}

	w, cleanup := testWallet(t)
	defer cleanup()

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runTestCase(
				t, w, tc.scope, tc.expectedScriptLen,
				tc.expectedWitnessLen,
			)
		})
	}
}

func runTestCase(t *testing.T, w *Wallet, scope waddrmgr.KeyScope,
	scriptLen, witnessLen int) {

	// Create an address we can use to send some coins to.
	addr, err := w.CurrentAddress(0, scope)
	if err != nil {
		t.Fatalf("unable to get current address: %v", addr)
	}
	p2shAddr, err := txscript.PayToAddrScript(addr)
	if err != nil {
		t.Fatalf("unable to convert wallet address to p2sh: %v", err)
	}

	// Add an output paying to the wallet's address to the database.
	utxOut := wire.NewTxOut(100000, p2shAddr)
	incomingTx := &wire.MsgTx{
		TxIn:  []*wire.TxIn{{}},
		TxOut: []*wire.TxOut{utxOut},
	}
	addUtxo(t, w, incomingTx)

	// Create a transaction that spends the UTXO created above and spends to
	// the same address again.
	prevOut := wire.OutPoint{
		Hash:  incomingTx.TxHash(),
		Index: 0,
	}
	outgoingTx := &wire.MsgTx{
		TxIn: []*wire.TxIn{{
			PreviousOutPoint: prevOut,
		}},
		TxOut: []*wire.TxOut{utxOut},
	}
	fetcher := txscript.NewCannedPrevOutputFetcher(
		utxOut.PkScript, utxOut.Value,
	)
	sigHashes := txscript.NewTxSigHashes(outgoingTx, fetcher)

	// Compute the input script to spend the UTXO now.
	witness, script, err := w.ComputeInputScript(
		outgoingTx, utxOut, 0, sigHashes, txscript.SigHashAll, nil,
	)
	if err != nil {
		t.Fatalf("error computing input script: %v", err)
	}
	if scriptLen < 0 && len(script) == 0 {
		t.Fatal("expected non-empty signature script")
	}
	if scriptLen >= 0 && len(script) != scriptLen {
		t.Fatalf("unexpected script length, got %d wanted %d",
			len(script), scriptLen)
	}
	if len(witness) != witnessLen {
		t.Fatalf("unexpected witness stack length, got %d, wanted %d",
			len(witness), witnessLen)
	}

	// Finally verify that the created witness is valid.
	outgoingTx.TxIn[0].Witness = witness
	outgoingTx.TxIn[0].SignatureScript = script
	err = validateMsgTx(
		outgoingTx, [][]byte{utxOut.PkScript}, []btcutil.Amount{100000},
	)
	if err != nil {
		t.Fatalf("error validating tx: %v", err)
	}
}
