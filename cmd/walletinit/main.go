package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcwallet/netparams"
	"github.com/btcsuite/btcwallet/wallet"
	_ "github.com/btcsuite/btcwallet/walletdb/bdb"
)

func networkDir(dataDir string, params *chaincfg.Params) string {
	netName := params.Name
	if params.Net == wire.TestNet3 {
		netName = "testnet"
	}
	return filepath.Join(dataDir, netName)
}

func paramsForNetwork(network string) (*chaincfg.Params, error) {
	switch network {
	case "obtctestnet":
		return netparams.ObtcTestNetParams.Params, nil
	default:
		return nil, fmt.Errorf("unsupported network %q", network)
	}
}

func readSecret(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	secret := []byte(strings.TrimSpace(string(raw)))
	if len(secret) == 0 {
		return nil, fmt.Errorf("secret file is empty: %s", path)
	}
	return secret, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func readOrCreateSeed(path string) ([]byte, error) {
	if ok, err := fileExists(path); err != nil {
		return nil, err
	} else if ok {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		seed, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			return nil, fmt.Errorf("decode seed hex: %w", err)
		}
		return seed, nil
	}

	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("generate seed: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(seed)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return seed, nil
}

func main() {
	var (
		network         = flag.String("network", "obtctestnet", "network name")
		appData         = flag.String("appdata", "", "wallet appdata directory")
		privatePassFile = flag.String("private-pass-file", "", "file containing the private wallet passphrase")
		publicPassFile  = flag.String("public-pass-file", "", "optional file containing the public wallet passphrase")
		seedHexFile     = flag.String("seed-hex-file", "", "file containing or receiving the wallet seed as hex")
	)
	flag.Parse()

	if *appData == "" || *privatePassFile == "" || *seedHexFile == "" {
		fmt.Fprintln(os.Stderr, "--appdata, --private-pass-file, and --seed-hex-file are required")
		os.Exit(2)
	}

	params, err := paramsForNetwork(*network)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	netDir := networkDir(*appData, params)
	dbPath := filepath.Join(netDir, wallet.WalletDBName)
	exists, err := fileExists(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stat wallet db: %v\n", err)
		os.Exit(1)
	}
	if exists {
		fmt.Printf("wallet_exists appdata=%s db=%s\n", *appData, dbPath)
		return
	}

	privPass, err := readSecret(*privatePassFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read private passphrase: %v\n", err)
		os.Exit(1)
	}

	pubPass := []byte(wallet.InsecurePubPassphrase)
	if *publicPassFile != "" {
		pubPass, err = readSecret(*publicPassFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read public passphrase: %v\n", err)
			os.Exit(1)
		}
	}

	seed, err := readOrCreateSeed(*seedHexFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed setup: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(netDir, 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "create wallet directory: %v\n", err)
		os.Exit(1)
	}

	loader := wallet.NewLoader(params, netDir, true, 60*time.Second, 250)
	w, err := loader.CreateNewWallet(pubPass, privPass, seed, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "create wallet: %v\n", err)
		os.Exit(1)
	}
	w.Manager.Close()
	if err := w.Database().Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close wallet db: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("wallet_created appdata=%s db=%s\n", *appData, dbPath)
}
