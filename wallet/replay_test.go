// Copyright (c) 2026 The OBTC developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package wallet

import (
	"errors"
	"testing"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcwallet/waddrmgr"
	"github.com/stretchr/testify/require"
)

func TestSignatureHashTypeForHeight(t *testing.T) {
	obtcParams := &chaincfg.ObtcTestNetParams
	activeAt := chaincfg.GetOBTCReplayProtectionHeight(obtcParams)

	tests := []struct {
		name     string
		params   *chaincfg.Params
		height   int32
		hashType txscript.SigHashType
		want     txscript.SigHashType
	}{
		{
			name:     "obtc before activation",
			params:   obtcParams,
			height:   activeAt - 1,
			hashType: txscript.SigHashAll,
			want:     txscript.SigHashAll,
		},
		{
			name:     "obtc after activation",
			params:   obtcParams,
			height:   activeAt,
			hashType: txscript.SigHashAll,
			want: txscript.SigHashAll |
				txscript.SigHashOBTCReplayProtection,
		},
		{
			name:     "obtc taproot default after activation",
			params:   obtcParams,
			height:   activeAt,
			hashType: txscript.SigHashDefault,
			want: txscript.SigHashAll |
				txscript.SigHashOBTCReplayProtection,
		},
		{
			name:     "bitcoin network unchanged",
			params:   &chaincfg.TestNet3Params,
			height:   activeAt,
			hashType: txscript.SigHashAll,
			want:     txscript.SigHashAll,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := signatureHashTypeForHeight(
				test.params, test.height, test.hashType,
			)
			if got != test.want {
				t.Fatalf("unexpected sighash type: got %v want %v",
					got, test.want)
			}
		})
	}
}

func TestScriptVerifyFlagsForHeight(t *testing.T) {
	obtcParams := &chaincfg.ObtcTestNetParams
	activeAt := chaincfg.GetOBTCReplayProtectionHeight(obtcParams)

	before := scriptVerifyFlagsForHeight(obtcParams, activeAt-1)
	if before&txscript.ScriptVerifyOBTCReplayProtection != 0 {
		t.Fatal("replay protection unexpectedly enabled before activation")
	}

	after := scriptVerifyFlagsForHeight(obtcParams, activeAt)
	if after&txscript.ScriptVerifyOBTCReplayProtection == 0 {
		t.Fatal("replay protection not enabled at activation")
	}

	bitcoin := scriptVerifyFlagsForHeight(&chaincfg.TestNet3Params, activeAt)
	if bitcoin&txscript.ScriptVerifyOBTCReplayProtection != 0 {
		t.Fatal("replay protection unexpectedly enabled for bitcoin testnet")
	}
}

func TestSigningPolicyUsesBackendTargetHeight(t *testing.T) {
	t.Parallel()

	params := &chaincfg.ObtcTestNetParams
	activeAt := chaincfg.GetOBTCReplayProtectionHeight(params)

	w, cleanup := testWalletWithChainParams(t, params)
	defer cleanup()

	setWalletSyncedTo(t, w, activeAt-2)
	w.chainClient = &mockChainClient{
		blockStamp: &waddrmgr.BlockStamp{Height: activeAt},
	}

	hashType, flags, err := w.signingPolicy(txscript.SigHashAll)
	require.NoError(t, err)
	require.NotZero(t, hashType&txscript.SigHashOBTCReplayProtection)
	require.NotZero(t, flags&txscript.ScriptVerifyOBTCReplayProtection)
}

func TestSigningPolicyRefusesUnknownFreshnessBelowActivation(t *testing.T) {
	t.Parallel()

	params := &chaincfg.ObtcTestNetParams
	activeAt := chaincfg.GetOBTCReplayProtectionHeight(params)

	w, cleanup := testWalletWithChainParams(t, params)
	defer cleanup()

	setWalletSyncedTo(t, w, activeAt-2)
	w.chainClient = nil

	_, err := w.signatureHashType(txscript.SigHashAll)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrOBTCReplayProtectionTargetUnknown))

	setWalletSyncedTo(t, w, activeAt)
	hashType, err := w.signatureHashType(txscript.SigHashAll)
	require.NoError(t, err)
	require.NotZero(t, hashType&txscript.SigHashOBTCReplayProtection)
}

func TestSignTransactionUsesBackendReplayTarget(t *testing.T) {
	t.Parallel()

	params := &chaincfg.ObtcTestNetParams
	activeAt := chaincfg.GetOBTCReplayProtectionHeight(params)

	w, cleanup := testWalletWithChainParams(t, params)
	defer cleanup()

	setWalletSyncedTo(t, w, activeAt-2)
	w.chainClient = &mockChainClient{
		blockStamp: &waddrmgr.BlockStamp{Height: activeAt},
	}

	addr, err := w.CurrentAddress(0, waddrmgr.KeyScopeBIP0044)
	require.NoError(t, err)
	pkScript, err := txscript.PayToAddrScript(addr)
	require.NoError(t, err)

	utxo := wire.NewTxOut(100000, pkScript)
	incomingTx := &wire.MsgTx{
		TxIn:  []*wire.TxIn{{}},
		TxOut: []*wire.TxOut{utxo},
	}
	addUtxoAtHeight(t, w, incomingTx, activeAt-20)

	spendTx := &wire.MsgTx{
		TxIn: []*wire.TxIn{{
			PreviousOutPoint: wire.OutPoint{
				Hash:  incomingTx.TxHash(),
				Index: 0,
			},
		}},
		TxOut: []*wire.TxOut{wire.NewTxOut(90000, pkScript)},
	}

	signErrors, err := w.SignTransaction(
		spendTx, txscript.SigHashAll, nil, nil, nil,
	)
	require.NoError(t, err)
	require.Empty(t, signErrors)

	sigScript := spendTx.TxIn[0].SignatureScript
	require.NotEmpty(t, sigScript)
	sigLen := int(sigScript[0])
	require.LessOrEqual(t, sigLen+1, len(sigScript))
	sig := sigScript[1 : sigLen+1]
	require.NotZero(
		t, sig[len(sig)-1]&byte(txscript.SigHashOBTCReplayProtection),
	)

	err = validateMsgTxWithFlags(
		spendTx, [][]byte{utxo.PkScript},
		[]btcutil.Amount{btcutil.Amount(utxo.Value)},
		scriptVerifyFlagsForHeight(params, activeAt+1),
	)
	require.NoError(t, err)
}
