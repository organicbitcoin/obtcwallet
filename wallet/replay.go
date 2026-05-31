// Copyright (c) 2026 The OBTC developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package wallet

import (
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
)

func signatureHashTypeForHeight(params *chaincfg.Params, height int32,
	hashType txscript.SigHashType) txscript.SigHashType {

	if !chaincfg.IsOBTCReplayProtectionActive(params, height) {
		return hashType
	}
	if hashType == txscript.SigHashDefault {
		hashType = txscript.SigHashAll
	}
	return hashType | txscript.SigHashOBTCReplayProtection
}

func (w *Wallet) signatureHashType(hashType txscript.SigHashType) txscript.SigHashType {
	return signatureHashTypeForHeight(
		w.ChainParams(), w.Manager.SyncedTo().Height+1, hashType,
	)
}

func scriptVerifyFlagsForHeight(params *chaincfg.Params,
	height int32) txscript.ScriptFlags {

	flags := txscript.StandardVerifyFlags
	if chaincfg.IsOBTCReplayProtectionActive(params, height) {
		flags |= txscript.ScriptVerifyOBTCReplayProtection
	}
	return flags
}

func (w *Wallet) scriptVerifyFlags() txscript.ScriptFlags {
	return scriptVerifyFlagsForHeight(
		w.ChainParams(), w.Manager.SyncedTo().Height+1,
	)
}
