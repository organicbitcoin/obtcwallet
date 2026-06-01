package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcwallet/internal/zero"
	pb "github.com/btcsuite/btcwallet/rpc/agentwalletrpc"
	"github.com/jessevdk/go-flags"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultCapabilityTTLSeconds = 900
	defaultPrincipal            = "cli:renewall"
	defaultRPCTimeout           = 30 * time.Second
	defaultSessionTTLSeconds    = 300
	defaultWalletID             = "default"
)

var walletDataDirectory = btcutil.AppDataDir("btcwallet", false)

var opts = struct {
	RPCConnect     string  `long:"connect" description:"Hostname[:port] of the experimental agent gRPC server" default:"localhost:8332"`
	RPCUsername    string  `long:"rpcuser" description:"Deprecated: ignored by the agent gRPC path"`
	RPCPassword    string  `long:"rpcpass" description:"Deprecated: ignored by the agent gRPC path"`
	RPCCertificate string  `long:"cafile" description:"Wallet RPC TLS certificate"`
	DisableTLS     bool    `long:"notls" description:"Disable TLS for the agent gRPC client -- NOTE: This is only allowed if connect points to localhost"`
	WalletPass     string  `long:"walletpass" default-mask:"-" description:"Wallet private passphrase for local signer mode, or remote signer auth bytes for remote signer mode"`
	Principal      string  `long:"principal" description:"Principal recorded in agent wallet audit fields" default:"cli:renewall"`
	WalletID       string  `long:"walletid" description:"Wallet identifier used by AgentWalletService" default:"default"`
	AccountNumber  uint32  `long:"account" description:"Wallet account number used for renewal planning" default:"0"`
	CapabilityTTL  int64   `long:"capabilityttl" description:"Capability TTL in seconds for execution runs" default:"900"`
	SessionTTL     int64   `long:"sessionttl" description:"Signer session TTL in seconds for execution runs" default:"300"`
	Label          string  `long:"label" description:"Optional label used when publishing renewal transactions"`
	Amount         float64 `long:"amount" description:"Renew amount in BTC per selected UTXO" required:"true"`
	FetchLimit     int     `long:"fetchlimit" description:"Max rows fetched from GetExpiryRisk" default:"1000"`
	BatchLimit     int     `long:"limit" description:"Max renew tx to send in one run (0 means all selected)" default:"10"`
	IncludeExpired bool    `long:"include-expired" description:"Include expired items (default only expiring)"`
	TargetAddress  string  `long:"target-address" description:"Optional target address for renew outputs"`
	MaxFeeRate     float64 `long:"maxfeerate" description:"Optional max fee rate in BTC/KB (0 means wallet default)" default:"0"`
	MinConf        int32   `long:"minconf" description:"Optional minimum confirmations" default:"0"`
	DryRun         bool    `long:"dry-run" description:"Only print selected outpoints without signing or publishing"`
	WindowStart    int32   `long:"window-start" description:"Optional upper bound of blocks_to_expiry (requires --window-end)" default:"-1"`
	WindowEnd      int32   `long:"window-end" description:"Optional lower bound of blocks_to_expiry (requires --window-start)" default:"-1"`
	Interval       string  `long:"interval" description:"Optional repeat interval, e.g. 30m (empty means run once)"`
	Runs           int     `long:"runs" description:"Number of scheduled runs when --interval is set (0 means forever)" default:"1"`
}{
	Principal:      defaultPrincipal,
	RPCCertificate: filepath.Join(walletDataDirectory, "rpc.cert"),
	WalletID:       defaultWalletID,
}

type getExpiryItem struct {
	OutPoint       string
	Status         string
	BlocksToExpiry int32
}

type renewFilter struct {
	includeExpired bool
	useWindow      bool
	windowStart    int32
	windowEnd      int32
}

type loopConfig struct {
	interval time.Duration
	runs     int
}

type agentWalletClient interface {
	GetWalletState(context.Context, *pb.GetWalletStateRequest) (*pb.GetWalletStateResponse, error)
	GetExpiryRisk(context.Context, *pb.GetExpiryRiskRequest) (*pb.GetExpiryRiskResponse, error)
	IssueCapability(context.Context, *pb.IssueCapabilityRequest) (*pb.IssueCapabilityResponse, error)
	RevokeCapability(context.Context, *pb.RevokeCapabilityRequest) (*pb.RevokeCapabilityResponse, error)
	OpenSignerSession(context.Context, *pb.OpenSignerSessionRequest) (*pb.OpenSignerSessionResponse, error)
	CloseSignerSession(context.Context, *pb.CloseSignerSessionRequest) (*pb.CloseSignerSessionResponse, error)
	PreviewRenewal(context.Context, *pb.PreviewRenewalRequest) (*pb.PreviewRenewalResponse, error)
	SubmitRenewal(context.Context, *pb.SubmitRenewalRequest) (*pb.SubmitRenewalResponse, error)
}

type grpcAgentWalletClient struct {
	client pb.AgentWalletServiceClient
}

func (c grpcAgentWalletClient) GetWalletState(ctx context.Context,
	req *pb.GetWalletStateRequest) (*pb.GetWalletStateResponse, error) {
	return c.client.GetWalletState(ctx, req)
}

func (c grpcAgentWalletClient) GetExpiryRisk(ctx context.Context,
	req *pb.GetExpiryRiskRequest) (*pb.GetExpiryRiskResponse, error) {
	return c.client.GetExpiryRisk(ctx, req)
}

func (c grpcAgentWalletClient) IssueCapability(ctx context.Context,
	req *pb.IssueCapabilityRequest) (*pb.IssueCapabilityResponse, error) {
	return c.client.IssueCapability(ctx, req)
}

func (c grpcAgentWalletClient) RevokeCapability(ctx context.Context,
	req *pb.RevokeCapabilityRequest) (*pb.RevokeCapabilityResponse, error) {
	return c.client.RevokeCapability(ctx, req)
}

func (c grpcAgentWalletClient) OpenSignerSession(ctx context.Context,
	req *pb.OpenSignerSessionRequest) (*pb.OpenSignerSessionResponse, error) {
	return c.client.OpenSignerSession(ctx, req)
}

func (c grpcAgentWalletClient) CloseSignerSession(ctx context.Context,
	req *pb.CloseSignerSessionRequest) (*pb.CloseSignerSessionResponse, error) {
	return c.client.CloseSignerSession(ctx, req)
}

func (c grpcAgentWalletClient) PreviewRenewal(ctx context.Context,
	req *pb.PreviewRenewalRequest) (*pb.PreviewRenewalResponse, error) {
	return c.client.PreviewRenewal(ctx, req)
}

func (c grpcAgentWalletClient) SubmitRenewal(ctx context.Context,
	req *pb.SubmitRenewalRequest) (*pb.SubmitRenewalResponse, error) {
	return c.client.SubmitRenewal(ctx, req)
}

type executionSession struct {
	capabilityID  string
	signerSession string
}

func shouldRenew(status string, includeExpired bool) bool {
	if status == "expiring" {
		return true
	}
	return includeExpired && status == "expired"
}

func newRenewFilter(includeExpired bool, windowStart, windowEnd int32) (renewFilter, error) {
	hasStart := windowStart >= 0
	hasEnd := windowEnd >= 0

	if hasStart != hasEnd {
		return renewFilter{}, fmt.Errorf("window-start and window-end must be set together")
	}

	if !hasStart {
		return renewFilter{includeExpired: includeExpired}, nil
	}

	if windowStart < windowEnd {
		return renewFilter{}, fmt.Errorf("window-start must be >= window-end")
	}

	return renewFilter{
		includeExpired: includeExpired,
		useWindow:      true,
		windowStart:    windowStart,
		windowEnd:      windowEnd,
	}, nil
}

func (f renewFilter) shouldRenewItem(it getExpiryItem) bool {
	if f.useWindow {
		if it.BlocksToExpiry > f.windowStart || it.BlocksToExpiry < f.windowEnd {
			return false
		}
		if !f.includeExpired && it.Status == "expired" {
			return false
		}
		return true
	}

	return shouldRenew(it.Status, f.includeExpired)
}

func selectOutpoints(items []getExpiryItem, limit int, filter renewFilter) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if !filter.shouldRenewItem(it) {
			continue
		}
		out = append(out, it.OutPoint)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func parseLoopConfig(intervalRaw string, runs int) (loopConfig, error) {
	if runs < 0 {
		return loopConfig{}, fmt.Errorf("runs must be >= 0")
	}

	if intervalRaw == "" {
		if runs != 1 {
			return loopConfig{}, fmt.Errorf("runs requires interval")
		}
		return loopConfig{}, nil
	}

	interval, err := time.ParseDuration(intervalRaw)
	if err != nil {
		return loopConfig{}, fmt.Errorf("invalid interval: %w", err)
	}
	if interval <= 0 {
		return loopConfig{}, fmt.Errorf("interval must be > 0")
	}

	return loopConfig{interval: interval, runs: runs}, nil
}

func maxFeeRateSatPerKB() (int64, error) {
	if opts.MaxFeeRate <= 0 {
		return 0, nil
	}

	feeRateSat, err := btcutil.NewAmount(opts.MaxFeeRate)
	if err != nil {
		return 0, fmt.Errorf("invalid max fee rate: %w", err)
	}
	return int64(feeRateSat), nil
}

func targetAmountSat() (int64, error) {
	amountSat, err := btcutil.NewAmount(opts.Amount)
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %w", err)
	}
	return int64(amountSat), nil
}

func isLoopbackHostPort(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func dialAgentWalletClient() (agentWalletClient, func() error, error) {
	if opts.DisableTLS && !isLoopbackHostPort(opts.RPCConnect) {
		return nil, nil, fmt.Errorf("--notls is only allowed for localhost connections")
	}

	var creds credentials.TransportCredentials
	if opts.DisableTLS {
		creds = insecure.NewCredentials()
	} else {
		var err error
		creds, err = credentials.NewClientTLSFromFile(opts.RPCCertificate, "")
		if err != nil {
			return nil, nil, fmt.Errorf("load cert: %w", err)
		}
	}

	conn, err := grpc.Dial(
		opts.RPCConnect,
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("grpc connect: %w", err)
	}

	return grpcAgentWalletClient{
			client: pb.NewAgentWalletServiceClient(conn),
		},
		conn.Close, nil
}

func withRPCTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), defaultRPCTimeout)
}

func newRequestMeta(kind, capabilityID string) *pb.RequestMeta {
	return &pb.RequestMeta{
		RequestId:      fmt.Sprintf("renewall-%s-%d", kind, time.Now().UnixNano()),
		IdempotencyKey: fmt.Sprintf("renewall-%s-%d", kind, time.Now().UnixNano()),
		Principal:      opts.Principal,
		CapabilityId:   capabilityID,
	}
}

func expiryItemsFromResponse(items []*pb.ExpiryRisk) []getExpiryItem {
	out := make([]getExpiryItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, getExpiryItem{
			OutPoint:       item.GetOutpoint(),
			Status:         item.GetStatus(),
			BlocksToExpiry: item.GetBlocksToExpiry(),
		})
	}
	return out
}

func printWarnings(dst io.Writer, prefix string, warnings []string) {
	for _, warning := range warnings {
		if warning == "" {
			continue
		}
		fmt.Fprintf(dst, "%s%s\n", prefix, warning)
	}
}

func requireExecutionBackend(state *pb.WalletState) error {
	if state == nil {
		return fmt.Errorf("wallet state not returned by agent API")
	}

	backend := state.GetSignerBackend()
	switch backend.GetMode() {
	case "publish_only":
		return fmt.Errorf("publish_only signer backend requires external signed PSBT; renewall CLI does not support that path yet")
	case "local":
		if opts.WalletPass == "" {
			return fmt.Errorf("walletpass is required when signer backend mode is local")
		}
	}

	return nil
}

func requireChainSynced(state *pb.WalletState) error {
	if state == nil {
		return fmt.Errorf("wallet state not returned by agent API")
	}
	if state.GetChainSynced() {
		return nil
	}

	return fmt.Errorf("wallet chain state is not synced at height %d",
		state.GetCurrentBlockHeight())
}

func openExecutionSession(client agentWalletClient, stderr io.Writer) (*executionSession, error) {
	ctx, cancel := withRPCTimeout()
	defer cancel()

	issueResp, err := client.IssueCapability(ctx, &pb.IssueCapabilityRequest{
		Meta:        newRequestMeta("issue-capability", ""),
		WalletId:    opts.WalletID,
		Principal:   opts.Principal,
		Permissions: []string{"renewal.submit"},
		TtlSeconds:  opts.CapabilityTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("IssueCapability failed: %w", err)
	}
	printWarnings(stderr, "[capability] ", issueResp.GetMeta().GetWarnings())

	capabilityID := issueResp.GetCapability().GetCapabilityId()
	passphrase := []byte(opts.WalletPass)
	defer zero.Bytes(passphrase)

	ctx, cancel = withRPCTimeout()
	defer cancel()

	openResp, err := client.OpenSignerSession(ctx, &pb.OpenSignerSessionRequest{
		Meta:         newRequestMeta("open-session", capabilityID),
		WalletId:     opts.WalletID,
		CapabilityId: capabilityID,
		Passphrase:   passphrase,
		TtlSeconds:   opts.SessionTTL,
		Reason:       "renewall CLI execution",
	})
	if err != nil {
		_ = revokeCapability(client, capabilityID, stderr)
		return nil, fmt.Errorf("OpenSignerSession failed: %w", err)
	}
	printWarnings(stderr, "[session] ", openResp.GetMeta().GetWarnings())

	return &executionSession{
		capabilityID:  capabilityID,
		signerSession: openResp.GetSession().GetSignerSessionId(),
	}, nil
}

func closeExecutionSession(client agentWalletClient, session *executionSession,
	stderr io.Writer) {

	if session == nil {
		return
	}

	if session.signerSession != "" {
		ctx, cancel := withRPCTimeout()
		closeResp, err := client.CloseSignerSession(ctx,
			&pb.CloseSignerSessionRequest{
				Meta:            newRequestMeta("close-session", session.capabilityID),
				SignerSessionId: session.signerSession,
				Reason:          "renewall CLI finished",
			},
		)
		cancel()
		if err != nil {
			fmt.Fprintf(stderr, "[session] close failed for %s: %v\n",
				session.signerSession, err)
		} else {
			printWarnings(stderr, "[session] ",
				closeResp.GetMeta().GetWarnings())
		}
	}

	_ = revokeCapability(client, session.capabilityID, stderr)
}

func revokeCapability(client agentWalletClient, capabilityID string,
	stderr io.Writer) error {

	if capabilityID == "" {
		return nil
	}

	ctx, cancel := withRPCTimeout()
	defer cancel()

	resp, err := client.RevokeCapability(ctx, &pb.RevokeCapabilityRequest{
		Meta:         newRequestMeta("revoke-capability", capabilityID),
		CapabilityId: capabilityID,
		Reason:       "renewall CLI finished",
	})
	if err != nil {
		fmt.Fprintf(stderr, "[capability] revoke failed for %s: %v\n",
			capabilityID, err)
		return err
	}
	printWarnings(stderr, "[capability] ", resp.GetMeta().GetWarnings())
	return nil
}

func runRenewAllOnce(client agentWalletClient, filter renewFilter,
	stdout, stderr io.Writer) error {

	ctx, cancel := withRPCTimeout()
	stateResp, err := client.GetWalletState(ctx, &pb.GetWalletStateRequest{
		Meta:     newRequestMeta("wallet-state", ""),
		WalletId: opts.WalletID,
	})
	cancel()
	if err != nil {
		return fmt.Errorf("GetWalletState failed: %w", err)
	}
	printWarnings(stderr, "[wallet-state] ", stateResp.GetMeta().GetWarnings())
	if err := requireChainSynced(stateResp.GetState()); err != nil {
		return err
	}

	ctx, cancel = withRPCTimeout()
	expiryResp, err := client.GetExpiryRisk(ctx, &pb.GetExpiryRiskRequest{
		Meta:             newRequestMeta("get-expiry-risk", ""),
		WalletId:         opts.WalletID,
		AccountNumber:    opts.AccountNumber,
		MinConfirmations: opts.MinConf,
		Limit:            uint32(opts.FetchLimit),
	})
	cancel()
	if err != nil {
		return fmt.Errorf("GetExpiryRisk failed: %w", err)
	}
	printWarnings(stderr, "[expiry-risk] ", expiryResp.GetMeta().GetWarnings())

	candidates := selectOutpoints(
		expiryItemsFromResponse(expiryResp.GetItems()),
		0, filter,
	)
	if len(candidates) == 0 {
		fmt.Fprintln(stdout, "no renew candidates selected")
		return nil
	}

	if opts.DryRun {
		selected := candidates
		if opts.BatchLimit > 0 && len(selected) > opts.BatchLimit {
			selected = selected[:opts.BatchLimit]
		}
		fmt.Fprintf(stdout, "selected %d outpoints:\n", len(selected))
		for _, op := range selected {
			fmt.Fprintln(stdout, op)
		}
		return nil
	}

	if err := requireExecutionBackend(stateResp.GetState()); err != nil {
		return err
	}

	targetAmount, err := targetAmountSat()
	if err != nil {
		return err
	}
	maxFeeRate, err := maxFeeRateSatPerKB()
	if err != nil {
		return err
	}

	session, err := openExecutionSession(client, stderr)
	if err != nil {
		return err
	}
	defer closeExecutionSession(client, session, stderr)

	renewed := 0
	for i, outpoint := range candidates {
		if opts.BatchLimit > 0 && renewed >= opts.BatchLimit {
			break
		}

		ctx, cancel = withRPCTimeout()
		previewResp, err := client.PreviewRenewal(ctx, &pb.PreviewRenewalRequest{
			Meta: &pb.RequestMeta{
				RequestId:      fmt.Sprintf("renewall-preview-%d-%d", i, time.Now().UnixNano()),
				IdempotencyKey: fmt.Sprintf("renewall-preview-%s", outpoint),
				Principal:      opts.Principal,
				CapabilityId:   session.capabilityID,
			},
			WalletId:           opts.WalletID,
			AccountNumber:      opts.AccountNumber,
			Outpoints:          []string{outpoint},
			DryRun:             false,
			MaxFeeRateSatPerKb: maxFeeRate,
			TargetAmountSat:    targetAmount,
			TargetAddress:      opts.TargetAddress,
			MinConfirmations:   opts.MinConf,
		})
		cancel()
		if err != nil {
			fmt.Fprintf(stderr, "[%d/%d] preview failed for %s: %v\n",
				i+1, len(candidates), outpoint, err)
			continue
		}
		printWarnings(stderr, fmt.Sprintf("[%d/%d] preview warning for %s: ",
			i+1, len(candidates), outpoint), previewResp.GetMeta().GetWarnings())

		ctx, cancel = withRPCTimeout()
		submitResp, err := client.SubmitRenewal(ctx, &pb.SubmitRenewalRequest{
			Meta: &pb.RequestMeta{
				RequestId:      fmt.Sprintf("renewall-submit-%d-%d", i, time.Now().UnixNano()),
				IdempotencyKey: fmt.Sprintf("renewall-submit-%s", outpoint),
				Principal:      opts.Principal,
				CapabilityId:   session.capabilityID,
			},
			OperationId:     previewResp.GetOperation().GetOperationId(),
			Label:           opts.Label,
			SignerSessionId: session.signerSession,
		})
		cancel()
		if err != nil {
			fmt.Fprintf(stderr, "[%d/%d] renew failed for %s: %v\n",
				i+1, len(candidates), outpoint, err)
			continue
		}
		printWarnings(stderr, fmt.Sprintf("[%d/%d] submit warning for %s: ",
			i+1, len(candidates), outpoint), submitResp.GetMeta().GetWarnings())
		fmt.Fprintf(stdout, "[%d/%d] renewed %s txid=%s operation_id=%s\n",
			i+1, len(candidates), outpoint, submitResp.GetTxid(),
			submitResp.GetOperation().GetOperationId())
		renewed++
	}

	return nil
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

	filter, err := newRenewFilter(opts.IncludeExpired, opts.WindowStart, opts.WindowEnd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid renew filter: %v\n", err)
		os.Exit(1)
	}
	loopCfg, err := parseLoopConfig(opts.Interval, opts.Runs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid loop config: %v\n", err)
		os.Exit(1)
	}

	client, closeFn, err := dialAgentWalletClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer closeFn()

	for run := 1; ; run++ {
		if err := runRenewAllOnce(client, filter, os.Stdout, os.Stderr); err != nil {
			if loopCfg.interval == 0 {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "[run %d] %v\n", run, err)
		}

		if loopCfg.interval == 0 {
			return
		}
		if loopCfg.runs > 0 && run >= loopCfg.runs {
			return
		}

		time.Sleep(loopCfg.interval)
	}
}
