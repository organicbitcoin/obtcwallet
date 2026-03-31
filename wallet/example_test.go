package wallet

import (
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcwallet/waddrmgr"
	"github.com/btcsuite/btcwallet/walletdb"
)

// defaultDBTimeout specifies the timeout value when opening the wallet
// database.
var defaultDBTimeout = 10 * time.Second

// testWallet creates a test wallet and unlocks it.
func testWallet(t *testing.T) (*Wallet, func()) {
	return testWalletWithChainParams(t, &chaincfg.TestNet3Params)
}

func testWalletWithChainParams(t *testing.T, params *chaincfg.Params) (*Wallet, func()) {
	// Set up a wallet.
	dir := t.TempDir()

	seed, err := hdkeychain.GenerateSeed(hdkeychain.MinSeedBytes)
	if err != nil {
		t.Fatalf("unable to create seed: %v", err)
	}

	pubPass := []byte("hello")
	privPass := []byte("world")

	loader := NewLoader(
		params, dir, true, defaultDBTimeout, 250,
		WithWalletSyncRetryInterval(10*time.Millisecond),
	)
	w, err := loader.CreateNewWallet(pubPass, privPass, seed, time.Now())
	if err != nil {
		t.Fatalf("unable to create wallet: %v", err)
	}
	chainClient := &mockChainClient{}
	w.chainClient = chainClient
	if err := w.Unlock(privPass, time.After(10*time.Minute)); err != nil {
		t.Fatalf("unable to unlock wallet: %v", err)
	}

	return w, func() {}
}

func setWalletSyncedTo(t *testing.T, w *Wallet, height int32) {
	t.Helper()

	err := walletdb.Update(w.db, func(tx walletdb.ReadWriteTx) error {
		addrmgrNs := tx.ReadWriteBucket(waddrmgrNamespaceKey)
		return w.Manager.SetSyncedTo(addrmgrNs, &waddrmgr.BlockStamp{
			Height: height,
			Hash:   chainhash.Hash{},
		})
	})
	if err != nil {
		t.Fatalf("unable to update wallet synced height: %v", err)
	}
}

// testWalletWatchingOnly creates a test watch only wallet and unlocks it.
func testWalletWatchingOnly(t *testing.T) (*Wallet, func()) {
	// Set up a wallet.
	dir := t.TempDir()

	pubPass := []byte("hello")
	loader := NewLoader(
		&chaincfg.TestNet3Params, dir, true, defaultDBTimeout, 250,
		WithWalletSyncRetryInterval(10*time.Millisecond),
	)
	w, err := loader.CreateNewWatchingOnlyWallet(pubPass, time.Now())
	if err != nil {
		t.Fatalf("unable to create wallet: %v", err)
	}
	chainClient := &mockChainClient{}
	w.chainClient = chainClient

	err = walletdb.Update(w.Database(), func(tx walletdb.ReadWriteTx) error {
		ns := tx.ReadWriteBucket(waddrmgrNamespaceKey)
		for scope, schema := range waddrmgr.ScopeAddrMap {
			_, err := w.Manager.NewScopedKeyManager(
				ns, scope, schema,
			)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("unable to create default scopes: %v", err)
	}

	return w, func() {}
}
