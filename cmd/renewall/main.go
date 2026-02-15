package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/jessevdk/go-flags"
)

var walletDataDirectory = btcutil.AppDataDir("btcwallet", false)

var opts = struct {
	RPCConnect     string  `long:"connect" description:"Hostname[:port] of wallet RPC server" default:"localhost:8332"`
	RPCUsername    string  `long:"rpcuser" description:"Wallet RPC username" required:"true"`
	RPCPassword    string  `long:"rpcpass" description:"Wallet RPC password" required:"true"`
	RPCCertificate string  `long:"cafile" description:"Wallet RPC TLS certificate"`
	Amount         float64 `long:"amount" description:"Renew amount in BTC per selected UTXO" required:"true"`
	FetchLimit     int     `long:"fetchlimit" description:"Max rows fetched from obtc.getexpiry" default:"1000"`
	BatchLimit     int     `long:"limit" description:"Max renew tx to send in one run (0 means all selected)" default:"10"`
	IncludeExpired bool    `long:"include-expired" description:"Include expired items (default only expiring)"`
	TargetAddress  string  `long:"target-address" description:"Optional target address for renew outputs"`
	MaxFeeRate     float64 `long:"maxfeerate" description:"Optional max fee rate in BTC/KB (0 means wallet default)" default:"0"`
	MinConf        int32   `long:"minconf" description:"Optional minimum confirmations" default:"0"`
	DryRun         bool    `long:"dry-run" description:"Only print selected outpoints without sending tx"`
}{
	RPCCertificate: filepath.Join(walletDataDirectory, "rpc.cert"),
}

type getExpiryItem struct {
	OutPoint string `json:"outpoint"`
	Status   string `json:"status"`
}

type getExpiryResult struct {
	Items []getExpiryItem `json:"items"`
}

type renewResult struct {
	TxID string `json:"txid"`
}

func shouldRenew(status string, includeExpired bool) bool {
	if status == "expiring" {
		return true
	}
	return includeExpired && status == "expired"
}

func selectOutpoints(items []getExpiryItem, limit int, includeExpired bool) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if !shouldRenew(it.Status, includeExpired) {
			continue
		}
		out = append(out, it.OutPoint)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func mustRaw(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func buildRenewParamMessages(outpoint string, amount float64, targetAddress string,
	maxFeeRate float64, minConf int32) []json.RawMessage {
	var target interface{} = nil
	if targetAddress != "" {
		target = targetAddress
	}
	var maxFee interface{} = nil
	if maxFeeRate > 0 {
		maxFee = maxFeeRate
	}
	return []json.RawMessage{
		mustRaw([]string{outpoint}),
		mustRaw(amount),
		mustRaw(target),
		mustRaw(maxFee),
		mustRaw(minConf),
	}
}

func loadCert(path string) ([]byte, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("failed to parse cert")
	}
	_ = tls.Config{RootCAs: pool}
	return pem, nil
}

func main() {
	if _, err := flags.Parse(&opts); err != nil {
		os.Exit(1)
	}
	if opts.Amount <= 0 {
		fmt.Fprintln(os.Stderr, "amount must be > 0")
		os.Exit(1)
	}
	if opts.BatchLimit < 0 {
		fmt.Fprintln(os.Stderr, "limit must be >= 0")
		os.Exit(1)
	}
	if opts.FetchLimit <= 0 {
		fmt.Fprintln(os.Stderr, "fetchlimit must be > 0")
		os.Exit(1)
	}

	certs, err := loadCert(opts.RPCCertificate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load cert: %v\n", err)
		os.Exit(1)
	}

	connCfg := &rpcclient.ConnConfig{
		Host:         opts.RPCConnect,
		User:         opts.RPCUsername,
		Pass:         opts.RPCPassword,
		Certificates: certs,
		HTTPPostMode: true,
		DisableTLS:   false,
	}
	client, err := rpcclient.New(connCfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rpc connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Shutdown()

	resp, err := client.RawRequest("obtc.getexpiry", []json.RawMessage{mustRaw(opts.FetchLimit)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "obtc.getexpiry failed: %v\n", err)
		os.Exit(1)
	}
	var expiry getExpiryResult
	if err := json.Unmarshal(resp, &expiry); err != nil {
		fmt.Fprintf(os.Stderr, "decode getexpiry: %v\n", err)
		os.Exit(1)
	}

	selected := selectOutpoints(expiry.Items, opts.BatchLimit, opts.IncludeExpired)
	if len(selected) == 0 {
		fmt.Println("no renew candidates selected")
		return
	}

	if opts.DryRun {
		fmt.Printf("selected %d outpoints:\n", len(selected))
		for _, op := range selected {
			fmt.Println(op)
		}
		return
	}

	for i, op := range selected {
		params := buildRenewParamMessages(op, opts.Amount, opts.TargetAddress, opts.MaxFeeRate, opts.MinConf)
		raw, err := client.RawRequest("obtc.renew", params)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] renew failed for %s: %v\n", i+1, len(selected), op, err)
			continue
		}
		var result renewResult
		if err := json.Unmarshal(raw, &result); err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] decode renew result failed for %s: %v\n", i+1, len(selected), op, err)
			continue
		}
		fmt.Printf("[%d/%d] renewed %s txid=%s\n", i+1, len(selected), op, result.TxID)
	}
}
