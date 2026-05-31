// Copyright (c) 2026 The OBTC developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
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
