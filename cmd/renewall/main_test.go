package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/btcsuite/btcwallet/rpc/agentwalletrpc"
)

type fakeAgentWalletClient struct {
	calls []string

	getWalletStateReqs    []*pb.GetWalletStateRequest
	getExpiryRiskReqs     []*pb.GetExpiryRiskRequest
	issueCapabilityReqs   []*pb.IssueCapabilityRequest
	revokeCapabilityReqs  []*pb.RevokeCapabilityRequest
	openSignerSessionReqs []*pb.OpenSignerSessionRequest
	closeSessionReqs      []*pb.CloseSignerSessionRequest
	previewRenewalReqs    []*pb.PreviewRenewalRequest
	submitRenewalReqs     []*pb.SubmitRenewalRequest

	getWalletStateResp    *pb.GetWalletStateResponse
	getWalletStateErr     error
	getExpiryRiskResp     *pb.GetExpiryRiskResponse
	getExpiryRiskErr      error
	issueCapabilityResp   *pb.IssueCapabilityResponse
	issueCapabilityErr    error
	revokeCapabilityResp  *pb.RevokeCapabilityResponse
	revokeCapabilityErr   error
	openSignerSessionResp *pb.OpenSignerSessionResponse
	openSignerSessionErr  error
	closeSessionResp      *pb.CloseSignerSessionResponse
	closeSessionErr       error
	previewRenewalResp    *pb.PreviewRenewalResponse
	previewRenewalErr     error
	previewRenewalResps   []*pb.PreviewRenewalResponse
	previewRenewalErrs    []error
	submitRenewalResp     *pb.SubmitRenewalResponse
	submitRenewalErr      error
}

func (c *fakeAgentWalletClient) GetWalletState(_ context.Context,
	req *pb.GetWalletStateRequest) (*pb.GetWalletStateResponse, error) {

	c.calls = append(c.calls, "GetWalletState")
	c.getWalletStateReqs = append(c.getWalletStateReqs, req)
	return c.getWalletStateResp, c.getWalletStateErr
}

func (c *fakeAgentWalletClient) GetExpiryRisk(_ context.Context,
	req *pb.GetExpiryRiskRequest) (*pb.GetExpiryRiskResponse, error) {

	c.calls = append(c.calls, "GetExpiryRisk")
	c.getExpiryRiskReqs = append(c.getExpiryRiskReqs, req)
	return c.getExpiryRiskResp, c.getExpiryRiskErr
}

func (c *fakeAgentWalletClient) IssueCapability(_ context.Context,
	req *pb.IssueCapabilityRequest) (*pb.IssueCapabilityResponse, error) {

	c.calls = append(c.calls, "IssueCapability")
	c.issueCapabilityReqs = append(c.issueCapabilityReqs, req)
	return c.issueCapabilityResp, c.issueCapabilityErr
}

func (c *fakeAgentWalletClient) RevokeCapability(_ context.Context,
	req *pb.RevokeCapabilityRequest) (*pb.RevokeCapabilityResponse, error) {

	c.calls = append(c.calls, "RevokeCapability")
	c.revokeCapabilityReqs = append(c.revokeCapabilityReqs, req)
	return c.revokeCapabilityResp, c.revokeCapabilityErr
}

func (c *fakeAgentWalletClient) OpenSignerSession(_ context.Context,
	req *pb.OpenSignerSessionRequest) (*pb.OpenSignerSessionResponse, error) {

	c.calls = append(c.calls, "OpenSignerSession")
	c.openSignerSessionReqs = append(c.openSignerSessionReqs,
		cloneOpenSignerSessionRequest(req))
	return c.openSignerSessionResp, c.openSignerSessionErr
}

func (c *fakeAgentWalletClient) CloseSignerSession(_ context.Context,
	req *pb.CloseSignerSessionRequest) (*pb.CloseSignerSessionResponse, error) {

	c.calls = append(c.calls, "CloseSignerSession")
	c.closeSessionReqs = append(c.closeSessionReqs, req)
	return c.closeSessionResp, c.closeSessionErr
}

func (c *fakeAgentWalletClient) PreviewRenewal(_ context.Context,
	req *pb.PreviewRenewalRequest) (*pb.PreviewRenewalResponse, error) {

	c.calls = append(c.calls, "PreviewRenewal")
	c.previewRenewalReqs = append(c.previewRenewalReqs, req)
	callIndex := len(c.previewRenewalReqs) - 1
	if callIndex < len(c.previewRenewalResps) ||
		callIndex < len(c.previewRenewalErrs) {

		var resp *pb.PreviewRenewalResponse
		if callIndex < len(c.previewRenewalResps) {
			resp = c.previewRenewalResps[callIndex]
		}
		var err error
		if callIndex < len(c.previewRenewalErrs) {
			err = c.previewRenewalErrs[callIndex]
		}
		return resp, err
	}
	return c.previewRenewalResp, c.previewRenewalErr
}

func (c *fakeAgentWalletClient) SubmitRenewal(_ context.Context,
	req *pb.SubmitRenewalRequest) (*pb.SubmitRenewalResponse, error) {

	c.calls = append(c.calls, "SubmitRenewal")
	c.submitRenewalReqs = append(c.submitRenewalReqs, req)
	return c.submitRenewalResp, c.submitRenewalErr
}

func cloneOpenSignerSessionRequest(
	req *pb.OpenSignerSessionRequest) *pb.OpenSignerSessionRequest {

	if req == nil {
		return nil
	}

	cloned := *req
	if req.Meta != nil {
		meta := *req.Meta
		cloned.Meta = &meta
	}
	cloned.Passphrase = append([]byte(nil), req.Passphrase...)
	return &cloned
}

func useTestOpts(t *testing.T) {
	t.Helper()

	saved := opts
	t.Cleanup(func() {
		opts = saved
	})

	opts.RPCConnect = defaultAgentRPCConnect
	opts.RPCCertificate = ""
	opts.DisableTLS = true
	opts.WalletPass = ""
	opts.Principal = defaultPrincipal
	opts.WalletID = defaultWalletID
	opts.AccountNumber = 0
	opts.CapabilityTTL = defaultCapabilityTTLSeconds
	opts.SessionTTL = defaultSessionTTLSeconds
	opts.Label = ""
	opts.Amount = 0.01
	opts.FetchLimit = 1000
	opts.BatchLimit = 10
	opts.IncludeExpired = false
	opts.IncludeNearExpiry = false
	opts.NearExpiryBlocks = 12
	opts.TargetAddress = ""
	opts.MaxFeeRate = 0
	opts.MinConf = 0
	opts.DryRun = false
	opts.WindowStart = -1
	opts.WindowEnd = -1
	opts.Interval = ""
	opts.Runs = 1
}

func TestRPCConnectDefaultMatchesAgentPort(t *testing.T) {
	field, ok := reflect.TypeOf(opts).FieldByName("RPCConnect")
	if !ok {
		t.Fatalf("RPCConnect option field missing")
	}
	if got := field.Tag.Get("default"); got != defaultAgentRPCConnect {
		t.Fatalf("unexpected default connect tag: got %q want %q",
			got, defaultAgentRPCConnect)
	}
}

func TestShouldRenewDirect(t *testing.T) {
	if !shouldRenew("expiring", false) {
		t.Fatalf("expiring should be selected")
	}
	if shouldRenew("expired", false) {
		t.Fatalf("expired should not be selected when includeExpired=false")
	}
	if !shouldRenew("expired", true) {
		t.Fatalf("expired should be selected when includeExpired=true")
	}
}

func TestNewRenewFilterDirect(t *testing.T) {
	if _, err := newRenewFilter(false, false, 100, -1, 12); err == nil {
		t.Fatalf("expected error when only one window bound is set")
	}
	if _, err := newRenewFilter(false, false, 50, 100, 12); err == nil {
		t.Fatalf("expected error when window-start < window-end")
	}
	if _, err := newRenewFilter(false, false, -1, -1, -1); err == nil {
		t.Fatalf("expected error when near-expiry-blocks is negative")
	}

	f, err := newRenewFilter(true, false, -1, -1, 12)
	if err != nil {
		t.Fatalf("unexpected error for disabled window filter: %v", err)
	}
	if f.useWindow {
		t.Fatalf("expected window filter disabled")
	}

	f, err = newRenewFilter(true, false, 100, 50, 12)
	if err != nil {
		t.Fatalf("unexpected error for valid window filter: %v", err)
	}
	if !f.useWindow || f.windowStart != 100 || f.windowEnd != 50 {
		t.Fatalf("unexpected window filter value: %+v", f)
	}
}

func TestSelectOutpointsDirect(t *testing.T) {
	items := []getExpiryItem{
		{OutPoint: "a:0", Status: "ok", BlocksToExpiry: 300},
		{OutPoint: "b:0", Status: "expiring", BlocksToExpiry: 120},
		{OutPoint: "c:0", Status: "expired", BlocksToExpiry: -5},
		{OutPoint: "d:0", Status: "ok", BlocksToExpiry: 80},
	}

	statusFilter, err := newRenewFilter(true, true, -1, -1, 12)
	if err != nil {
		t.Fatalf("new status filter: %v", err)
	}
	got := selectOutpoints(items, 0, statusFilter)
	if len(got) != 2 || got[0] != "b:0" || got[1] != "c:0" {
		t.Fatalf("unexpected status selection: %v", got)
	}

	windowFilter, err := newRenewFilter(false, true, 150, 50, 12)
	if err != nil {
		t.Fatalf("new window filter: %v", err)
	}
	got = selectOutpoints(items, 0, windowFilter)
	if len(got) != 2 || got[0] != "b:0" || got[1] != "d:0" {
		t.Fatalf("unexpected window selection: %v", got)
	}

	got = selectOutpoints(items, 1, windowFilter)
	if len(got) != 1 || got[0] != "b:0" {
		t.Fatalf("unexpected window selection with limit=1: %v", got)
	}
}

func TestSelectOutpointsSkipsNearExpiryByDefault(t *testing.T) {
	items := []getExpiryItem{
		{OutPoint: "safe:0", Status: "expiring", BlocksToExpiry: 13},
		{OutPoint: "boundary:0", Status: "expiring", BlocksToExpiry: 12},
		{OutPoint: "next-block-too-late:0", Status: "expiring", BlocksToExpiry: 1},
		{OutPoint: "expired:0", Status: "expired", BlocksToExpiry: 0},
	}

	filter, err := newRenewFilter(true, false, -1, -1, 12)
	if err != nil {
		t.Fatalf("new renew filter: %v", err)
	}
	got := selectOutpoints(items, 0, filter)
	if !reflect.DeepEqual(got, []string{"safe:0", "expired:0"}) {
		t.Fatalf("unexpected default near-expiry selection: %v", got)
	}

	filter, err = newRenewFilter(true, true, -1, -1, 12)
	if err != nil {
		t.Fatalf("new renew filter: %v", err)
	}
	got = selectOutpoints(items, 0, filter)
	if !reflect.DeepEqual(got, []string{
		"safe:0",
		"boundary:0",
		"next-block-too-late:0",
		"expired:0",
	}) {
		t.Fatalf("unexpected include-near-expiry selection: %v", got)
	}
}

func TestParseLoopConfigDirect(t *testing.T) {
	cfg, err := parseLoopConfig("", 1)
	if err != nil {
		t.Fatalf("unexpected run-once config error: %v", err)
	}
	if cfg.interval != 0 || cfg.runs != 0 {
		t.Fatalf("unexpected run-once config value: %+v", cfg)
	}

	if _, err := parseLoopConfig("", 2); err == nil {
		t.Fatalf("expected runs-without-interval error")
	}
	if _, err := parseLoopConfig("10m", -1); err == nil {
		t.Fatalf("expected negative-runs error")
	}
	if _, err := parseLoopConfig("not-a-duration", 1); err == nil {
		t.Fatalf("expected invalid-interval error")
	}

	cfg, err = parseLoopConfig("10s", 3)
	if err != nil {
		t.Fatalf("unexpected scheduled config error: %v", err)
	}
	if cfg.interval != 10*time.Second || cfg.runs != 3 {
		t.Fatalf("unexpected scheduled config value: %+v", cfg)
	}
}

func TestRunRenewAllOnceDryRunUsesAgentRiskQuery(t *testing.T) {
	useTestOpts(t)
	opts.DryRun = true
	opts.BatchLimit = 1

	filter, err := newRenewFilter(false, false, -1, -1, 12)
	if err != nil {
		t.Fatalf("new renew filter: %v", err)
	}

	client := &fakeAgentWalletClient{
		getWalletStateResp: &pb.GetWalletStateResponse{
			Meta: &pb.ResponseMeta{},
			State: &pb.WalletState{
				WalletId:    opts.WalletID,
				ChainSynced: true,
				SignerBackend: &pb.SignerBackendInfo{
					Mode: "local",
				},
			},
		},
		getExpiryRiskResp: &pb.GetExpiryRiskResponse{
			Meta: &pb.ResponseMeta{},
			Items: []*pb.ExpiryRisk{
				{Outpoint: "expiring:0", Status: "expiring", BlocksToExpiry: 13},
				{Outpoint: "expired:0", Status: "expired", BlocksToExpiry: -2},
			},
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runRenewAllOnce(client, filter, &stdout, &stderr); err != nil {
		t.Fatalf("runRenewAllOnce: %v", err)
	}

	if got := stdout.String(); !strings.Contains(got, "selected 1 outpoints:\nexpiring:0\n") {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}

	wantCalls := []string{"GetWalletState", "GetExpiryRisk"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("unexpected call order: got %v want %v", client.calls, wantCalls)
	}
	if len(client.previewRenewalReqs) != 0 || len(client.submitRenewalReqs) != 0 {
		t.Fatalf("dry-run should not preview or submit renewals")
	}
}

func TestRunRenewAllOnceRejectsUnsyncedWalletState(t *testing.T) {
	useTestOpts(t)
	opts.DryRun = true

	filter, err := newRenewFilter(false, false, -1, -1, 12)
	if err != nil {
		t.Fatalf("new renew filter: %v", err)
	}

	client := &fakeAgentWalletClient{
		getWalletStateResp: &pb.GetWalletStateResponse{
			Meta: &pb.ResponseMeta{},
			State: &pb.WalletState{
				WalletId:           opts.WalletID,
				ChainSynced:        false,
				CurrentBlockHeight: 481,
				SignerBackend: &pb.SignerBackendInfo{
					Mode: "local",
				},
			},
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = runRenewAllOnce(client, filter, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "wallet chain state is not synced at height 481") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.getExpiryRiskReqs) != 0 {
		t.Fatalf("unsynced wallet should stop before GetExpiryRisk")
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("unexpected output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunRenewAllOnceExecutesViaAgentFlow(t *testing.T) {
	useTestOpts(t)
	opts.DryRun = false
	opts.WalletPass = "topsecret"
	opts.AccountNumber = 7
	opts.BatchLimit = 1
	opts.Label = "renewall-agent"

	filter, err := newRenewFilter(false, false, -1, -1, 12)
	if err != nil {
		t.Fatalf("new renew filter: %v", err)
	}

	client := &fakeAgentWalletClient{
		getWalletStateResp: &pb.GetWalletStateResponse{
			Meta: &pb.ResponseMeta{},
			State: &pb.WalletState{
				WalletId:    opts.WalletID,
				ChainSynced: true,
				SignerBackend: &pb.SignerBackendInfo{
					Mode: "local",
				},
			},
		},
		getExpiryRiskResp: &pb.GetExpiryRiskResponse{
			Meta: &pb.ResponseMeta{},
			Items: []*pb.ExpiryRisk{
				{Outpoint: "abc:1", Status: "expiring", BlocksToExpiry: 15},
				{Outpoint: "def:0", Status: "expiring", BlocksToExpiry: 10},
			},
		},
		issueCapabilityResp: &pb.IssueCapabilityResponse{
			Meta: &pb.ResponseMeta{},
			Capability: &pb.Capability{
				CapabilityId: "cap-1",
			},
		},
		openSignerSessionResp: &pb.OpenSignerSessionResponse{
			Meta: &pb.ResponseMeta{},
			Session: &pb.SignerSession{
				SignerSessionId: "sess-1",
			},
		},
		previewRenewalResp: &pb.PreviewRenewalResponse{
			Meta: &pb.ResponseMeta{},
			Operation: &pb.Operation{
				OperationId: "op-1",
			},
		},
		submitRenewalResp: &pb.SubmitRenewalResponse{
			Meta: &pb.ResponseMeta{},
			Operation: &pb.Operation{
				OperationId: "op-1",
			},
			Txid: "tx-1",
		},
		closeSessionResp: &pb.CloseSignerSessionResponse{
			Meta: &pb.ResponseMeta{},
		},
		revokeCapabilityResp: &pb.RevokeCapabilityResponse{
			Meta: &pb.ResponseMeta{},
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runRenewAllOnce(client, filter, &stdout, &stderr); err != nil {
		t.Fatalf("runRenewAllOnce: %v", err)
	}

	wantCalls := []string{
		"GetWalletState",
		"GetExpiryRisk",
		"IssueCapability",
		"OpenSignerSession",
		"PreviewRenewal",
		"SubmitRenewal",
		"CloseSignerSession",
		"RevokeCapability",
	}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("unexpected call order: got %v want %v", client.calls, wantCalls)
	}

	if len(client.issueCapabilityReqs) != 1 {
		t.Fatalf("expected one IssueCapability request, got %d",
			len(client.issueCapabilityReqs))
	}
	if got := client.issueCapabilityReqs[0].GetPermissions(); !reflect.DeepEqual(got,
		[]string{"renewal.submit"}) {

		t.Fatalf("unexpected capability permissions: %v", got)
	}

	if len(client.openSignerSessionReqs) != 1 {
		t.Fatalf("expected one OpenSignerSession request, got %d",
			len(client.openSignerSessionReqs))
	}
	if got := string(client.openSignerSessionReqs[0].GetPassphrase()); got != "topsecret" {
		t.Fatalf("unexpected passphrase: %q", got)
	}
	if got := client.openSignerSessionReqs[0].GetCapabilityId(); got != "cap-1" {
		t.Fatalf("unexpected open session capability id: %q", got)
	}

	if len(client.previewRenewalReqs) != 1 {
		t.Fatalf("expected one PreviewRenewal request, got %d",
			len(client.previewRenewalReqs))
	}
	previewReq := client.previewRenewalReqs[0]
	if got := previewReq.GetOutpoints(); !reflect.DeepEqual(got, []string{"abc:1"}) {
		t.Fatalf("unexpected preview outpoints: %v", got)
	}
	if got := previewReq.GetAccountNumber(); got != 7 {
		t.Fatalf("unexpected preview account number: %d", got)
	}
	if got := previewReq.GetTargetAmountSat(); got != 1_000_000 {
		t.Fatalf("unexpected preview target amount: %d", got)
	}
	if got := previewReq.GetMeta().GetCapabilityId(); got != "cap-1" {
		t.Fatalf("unexpected preview capability id: %q", got)
	}

	if len(client.submitRenewalReqs) != 1 {
		t.Fatalf("expected one SubmitRenewal request, got %d",
			len(client.submitRenewalReqs))
	}
	submitReq := client.submitRenewalReqs[0]
	if got := submitReq.GetOperationId(); got != "op-1" {
		t.Fatalf("unexpected submit operation id: %q", got)
	}
	if got := submitReq.GetSignerSessionId(); got != "sess-1" {
		t.Fatalf("unexpected submit signer session id: %q", got)
	}
	if got := submitReq.GetLabel(); got != "renewall-agent" {
		t.Fatalf("unexpected submit label: %q", got)
	}
	if got := submitReq.GetMeta().GetCapabilityId(); got != "cap-1" {
		t.Fatalf("unexpected submit capability id: %q", got)
	}

	if len(client.closeSessionReqs) != 1 || len(client.revokeCapabilityReqs) != 1 {
		t.Fatalf("expected close/revoke on successful execution")
	}

	if got := stdout.String(); !strings.Contains(got,
		"[1/1] renewed abc:1 txid=tx-1 operation_id=op-1\n") {

		t.Fatalf("unexpected stdout: %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunRenewAllOnceScansPastPreviewFailure(t *testing.T) {
	useTestOpts(t)
	opts.WalletPass = "topsecret"
	opts.BatchLimit = 1

	filter, err := newRenewFilter(false, true, -1, -1, 12)
	if err != nil {
		t.Fatalf("new renew filter: %v", err)
	}

	client := &fakeAgentWalletClient{
		getWalletStateResp: &pb.GetWalletStateResponse{
			Meta: &pb.ResponseMeta{},
			State: &pb.WalletState{
				WalletId:    opts.WalletID,
				ChainSynced: true,
				SignerBackend: &pb.SignerBackendInfo{
					Mode: "local",
				},
			},
		},
		getExpiryRiskResp: &pb.GetExpiryRiskResponse{
			Meta: &pb.ResponseMeta{},
			Items: []*pb.ExpiryRisk{
				{Outpoint: "stale:0", Status: "expiring", BlocksToExpiry: 1},
				{Outpoint: "renewable:0", Status: "expiring", BlocksToExpiry: 2},
			},
		},
		issueCapabilityResp: &pb.IssueCapabilityResponse{
			Meta:       &pb.ResponseMeta{},
			Capability: &pb.Capability{CapabilityId: "cap-1"},
		},
		openSignerSessionResp: &pb.OpenSignerSessionResponse{
			Meta:    &pb.ResponseMeta{},
			Session: &pb.SignerSession{SignerSessionId: "sess-1"},
		},
		previewRenewalResps: []*pb.PreviewRenewalResponse{
			nil,
			{
				Meta:      &pb.ResponseMeta{},
				Operation: &pb.Operation{OperationId: "op-1"},
			},
		},
		previewRenewalErrs: []error{
			errors.New("outpoint no longer spendable"),
			nil,
		},
		submitRenewalResp: &pb.SubmitRenewalResponse{
			Meta:      &pb.ResponseMeta{},
			Operation: &pb.Operation{OperationId: "op-1"},
			Txid:      "tx-1",
		},
		closeSessionResp:     &pb.CloseSignerSessionResponse{Meta: &pb.ResponseMeta{}},
		revokeCapabilityResp: &pb.RevokeCapabilityResponse{Meta: &pb.ResponseMeta{}},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runRenewAllOnce(client, filter, &stdout, &stderr); err != nil {
		t.Fatalf("runRenewAllOnce: %v", err)
	}

	if len(client.previewRenewalReqs) != 2 {
		t.Fatalf("expected preview to continue after failure, got %d calls",
			len(client.previewRenewalReqs))
	}
	if len(client.submitRenewalReqs) != 1 {
		t.Fatalf("expected one successful submit, got %d",
			len(client.submitRenewalReqs))
	}
	if got := stdout.String(); !strings.Contains(got,
		"[2/2] renewed renewable:0 txid=tx-1 operation_id=op-1\n") {

		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestRunRenewAllOnceRejectsPublishOnlySignerBackend(t *testing.T) {
	useTestOpts(t)

	filter, err := newRenewFilter(false, false, -1, -1, 12)
	if err != nil {
		t.Fatalf("new renew filter: %v", err)
	}

	client := &fakeAgentWalletClient{
		getWalletStateResp: &pb.GetWalletStateResponse{
			Meta: &pb.ResponseMeta{},
			State: &pb.WalletState{
				WalletId:    opts.WalletID,
				ChainSynced: true,
				SignerBackend: &pb.SignerBackendInfo{
					Mode: "publish_only",
				},
			},
		},
		getExpiryRiskResp: &pb.GetExpiryRiskResponse{
			Meta: &pb.ResponseMeta{},
			Items: []*pb.ExpiryRisk{
				{Outpoint: "abc:1", Status: "expiring", BlocksToExpiry: 15},
			},
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = runRenewAllOnce(client, filter, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "publish_only signer backend") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.issueCapabilityReqs) != 0 || len(client.previewRenewalReqs) != 0 {
		t.Fatalf("publish_only path should stop before capability or preview")
	}
}

func TestRunRenewAllOnceLocalSignerRequiresWalletPass(t *testing.T) {
	useTestOpts(t)

	filter, err := newRenewFilter(false, false, -1, -1, 12)
	if err != nil {
		t.Fatalf("new renew filter: %v", err)
	}

	client := &fakeAgentWalletClient{
		getWalletStateResp: &pb.GetWalletStateResponse{
			Meta: &pb.ResponseMeta{},
			State: &pb.WalletState{
				WalletId:    opts.WalletID,
				ChainSynced: true,
				SignerBackend: &pb.SignerBackendInfo{
					Mode: "local",
				},
			},
		},
		getExpiryRiskResp: &pb.GetExpiryRiskResponse{
			Meta: &pb.ResponseMeta{},
			Items: []*pb.ExpiryRisk{
				{Outpoint: "abc:1", Status: "expiring", BlocksToExpiry: 15},
			},
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = runRenewAllOnce(client, filter, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "walletpass is required") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.issueCapabilityReqs) != 0 || len(client.openSignerSessionReqs) != 0 {
		t.Fatalf("local path without walletpass should stop before session open")
	}
}
