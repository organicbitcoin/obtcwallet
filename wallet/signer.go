// Copyright (c) 2020 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package wallet

import (
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcwallet/waddrmgr"
)

// ScriptForOutput returns the address, witness program and redeem script for a
// given UTXO. An error is returned if the UTXO does not belong to our wallet or
// it is not a managed public key address.
func (w *Wallet) ScriptForOutput(output *wire.TxOut) (
	waddrmgr.ManagedPubKeyAddress, []byte, []byte, error) {

	// First make sure we can sign for the input by making sure the script
	// in the UTXO belongs to our wallet and we have the private key for it.
	walletAddr, err := w.fetchOutputAddr(output.PkScript)
	if err != nil {
		return nil, nil, nil, err
	}

	pubKeyAddr, ok := walletAddr.(waddrmgr.ManagedPubKeyAddress)
	if !ok {
		return nil, nil, nil, fmt.Errorf("address %s is not a "+
			"managed public key address", walletAddr.Address())
	}

	var (
		witnessProgram []byte
		sigScript      []byte
	)

	switch {
	// If we're spending p2wkh output nested within a p2sh output, then
	// we'll need to attach a sigScript in addition to witness data.
	case walletAddr.AddrType() == waddrmgr.NestedWitnessPubKey:
		pubKey := pubKeyAddr.PubKey()
		pubKeyHash := btcutil.Hash160(pubKey.SerializeCompressed())

		// Next, we'll generate a valid sigScript that will allow us to
		// spend the p2sh output. The sigScript will contain only a
		// single push of the p2wkh witness program corresponding to
		// the matching public key of this address.
		p2wkhAddr, err := btcutil.NewAddressWitnessPubKeyHash(
			pubKeyHash, w.chainParams,
		)
		if err != nil {
			return nil, nil, nil, err
		}
		witnessProgram, err = txscript.PayToAddrScript(p2wkhAddr)
		if err != nil {
			return nil, nil, nil, err
		}

		bldr := txscript.NewScriptBuilder()
		bldr.AddData(witnessProgram)
		sigScript, err = bldr.Script()
		if err != nil {
			return nil, nil, nil, err
		}

	// Otherwise, this is a regular p2pkh, p2wkh or p2tr output. Return the
	// output script for the signer to use as the subscript.
	default:
		witnessProgram = output.PkScript
	}

	return pubKeyAddr, witnessProgram, sigScript, nil
}

// PrivKeyTweaker is a function type that can be used to pass in a callback for
// tweaking a private key before it's used to sign an input.
type PrivKeyTweaker func(*btcec.PrivateKey) (*btcec.PrivateKey, error)

// ComputeInputScript generates a complete InputScript for the passed
// transaction with the signature as defined within the passed SignDescriptor.
// This method is capable of generating the proper input script for p2pkh,
// regular p2wkh and p2wkh outputs nested within a regular p2sh output.
func (w *Wallet) ComputeInputScript(tx *wire.MsgTx, output *wire.TxOut,
	inputIndex int, sigHashes *txscript.TxSigHashes,
	hashType txscript.SigHashType, tweaker PrivKeyTweaker) (wire.TxWitness,
	[]byte, error) {

	hashType = w.signatureHashType(hashType)
	walletAddr, witnessProgram, sigScript, err := w.ScriptForOutput(output)
	if err != nil {
		return nil, nil, err
	}

	privKey, err := walletAddr.PrivKey()
	if err != nil {
		return nil, nil, err
	}

	// If we need to maybe tweak our private key, do it now.
	if tweaker != nil {
		privKey, err = tweaker(privKey)
		if err != nil {
			return nil, nil, err
		}
	}

	// We need to produce a Schnorr signature for p2tr key spend addresses.
	if txscript.IsPayToTaproot(output.PkScript) {
		var opts []txscript.TaprootSigHashOption
		if hashType&txscript.SigHashOBTCReplayProtection != 0 {
			opts = append(opts, txscript.WithOBTCReplayProtectionSighash())
		}
		// We can now generate a valid witness which will allow us to
		// spend this output.
		witnessScript, err := txscript.TaprootWitnessSignature(
			tx, sigHashes, inputIndex, output.Value,
			output.PkScript, hashType, privKey, opts...,
		)
		if err != nil {
			return nil, nil, err
		}

		return witnessScript, nil, nil
	}

	if txscript.IsPayToPubKeyHash(output.PkScript) {
		signatureScript, err := txscript.SignatureScript(
			tx, inputIndex, output.PkScript, hashType, privKey,
			walletAddr.Compressed(),
		)
		if err != nil {
			return nil, nil, err
		}

		return nil, signatureScript, nil
	}

	// Generate a valid witness stack for the input.
	witnessScript, err := txscript.WitnessSignature(
		tx, sigHashes, inputIndex, output.Value, witnessProgram,
		hashType, privKey, walletAddr.Compressed(),
	)
	if err != nil {
		return nil, nil, err
	}

	return witnessScript, sigScript, nil
}
