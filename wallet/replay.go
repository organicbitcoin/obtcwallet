// Copyright (c) 2026 The OBTC developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package wallet

import (
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
)

var (
	// ErrOBTCReplayProtectionTargetUnknown is returned when an OBTC wallet
	// cannot prove the target block height for a signing operation that
	// would otherwise produce a pre-activation, non-replay-protected
	// signature.
	ErrOBTCReplayProtectionTargetUnknown = errors.New(
		"obtc replay-protection signing target height is unknown",
	)
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

func (w *Wallet) backendSigningTargetHeight() (int32, error) {
	chainClient := w.ChainClient()
	if chainClient == nil {
		return 0, errors.New("blockchain RPC is inactive")
	}

	blockStamp, err := chainClient.BlockStamp()
	if err == nil && blockStamp != nil {
		return blockStamp.Height + 1, nil
	}

	_, bestHeight, bestErr := chainClient.GetBestBlock()
	if bestErr == nil {
		return bestHeight + 1, nil
	}
	if err != nil {
		return 0, err
	}

	return 0, bestErr
}

func (w *Wallet) signingTargetHeight() (int32, error) {
	params := w.ChainParams()
	walletTarget := w.Manager.SyncedTo().Height + 1
	if !chaincfg.IsOBTC(params) {
		return walletTarget, nil
	}

	activationHeight := chaincfg.GetOBTCReplayProtectionHeight(params)
	if activationHeight < 0 {
		return walletTarget, nil
	}

	backendTarget, err := w.backendSigningTargetHeight()
	if err == nil {
		if backendTarget > walletTarget {
			return backendTarget, nil
		}

		return walletTarget, nil
	}

	if chaincfg.IsOBTCReplayProtectionActive(params, walletTarget) {
		return walletTarget, nil
	}

	return 0, fmt.Errorf("%w: wallet target height %d is before OBTC "+
		"replay activation height %d and backend height is unavailable: %v",
		ErrOBTCReplayProtectionTargetUnknown, walletTarget,
		activationHeight, err)
}

func (w *Wallet) signatureHashType(
	hashType txscript.SigHashType) (txscript.SigHashType, error) {

	targetHeight, err := w.signingTargetHeight()
	if err != nil {
		return 0, err
	}

	return signatureHashTypeForHeight(w.ChainParams(), targetHeight, hashType),
		nil
}

func scriptVerifyFlagsForHeight(params *chaincfg.Params,
	height int32) txscript.ScriptFlags {

	flags := txscript.StandardVerifyFlags
	if chaincfg.IsOBTCReplayProtectionActive(params, height) {
		flags |= txscript.ScriptVerifyOBTCReplayProtection
	}
	return flags
}

func (w *Wallet) scriptVerifyFlags() (txscript.ScriptFlags, error) {
	targetHeight, err := w.signingTargetHeight()
	if err != nil {
		return 0, err
	}

	return scriptVerifyFlagsForHeight(w.ChainParams(), targetHeight), nil
}

func (w *Wallet) signingPolicy(hashType txscript.SigHashType) (
	txscript.SigHashType, txscript.ScriptFlags, error) {

	targetHeight, err := w.signingTargetHeight()
	if err != nil {
		return 0, 0, err
	}

	return signatureHashTypeForHeight(
			w.ChainParams(), targetHeight, hashType,
		),
		scriptVerifyFlagsForHeight(w.ChainParams(), targetHeight), nil
}
