package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcwallet/wallet/txrules"
)

type recordedRPCCall struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func TestRPCClientCallSendsBasicAuthAndPayload(t *testing.T) {
	var gotAuth string
	var gotCall recordedRPCCall

	walletRPC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotCall); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"result":"tx123","error":null,"id":"walletapp-1"}`))
	}))
	defer walletRPC.Close()

	client, err := newRPCClient(walletRPC.URL, "walletuser", "walletpass", time.Second)
	if err != nil {
		t.Fatalf("newRPCClient: %v", err)
	}

	var txid string
	if err := client.call(context.Background(), "sendtoaddress", []any{"addr", 0.5}, &txid); err != nil {
		t.Fatalf("call: %v", err)
	}
	if txid != "tx123" {
		t.Fatalf("unexpected result: %q", txid)
	}
	if gotCall.Method != "sendtoaddress" {
		t.Fatalf("method = %q, want sendtoaddress", gotCall.Method)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("walletuser:walletpass"))
	if gotAuth != wantAuth {
		t.Fatalf("Authorization = %q, want %q", gotAuth, wantAuth)
	}
}

func TestAPIRejectsMissingToken(t *testing.T) {
	walletRPC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("wallet RPC should not be called without API token")
	}))
	defer walletRPC.Close()

	client, err := newRPCClient(walletRPC.URL, "", "", time.Second)
	if err != nil {
		t.Fatalf("newRPCClient: %v", err)
	}
	app, err := newAppServer(client, "secret")
	if err != nil {
		t.Fatalf("newAppServer: %v", err)
	}
	mux := http.NewServeMux()
	app.routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestSendHandlerCallsSendToAddress(t *testing.T) {
	var gotCall recordedRPCCall
	handler := func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotCall); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"result":"sendtx","error":null,"id":"walletapp-1"}`))
	}
	rec := performAPICall(t, handler, http.MethodPost, "/api/send", `{"address":"obtcaddr","amount":0.25}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if gotCall.Method != "sendtoaddress" {
		t.Fatalf("method = %q, want sendtoaddress", gotCall.Method)
	}
	var params []any
	if err := json.Unmarshal(gotCall.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if len(params) != 2 || params[0] != "obtcaddr" || params[1] != 0.25 {
		t.Fatalf("params = %#v, want address and amount", params)
	}
}

func TestStateHandlerIncludesRenewFeeRate(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		var gotCall recordedRPCCall
		if err := json.NewDecoder(r.Body).Decode(&gotCall); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch gotCall.Method {
		case "walletislocked":
			_, _ = w.Write([]byte(`{"result":false,"error":null,"id":"walletapp-1"}`))
		case "getbalance":
			_, _ = w.Write([]byte(`{"result":1.25,"error":null,"id":"walletapp-1"}`))
		case "getunconfirmedbalance":
			_, _ = w.Write([]byte(`{"result":0.5,"error":null,"id":"walletapp-1"}`))
		case "getblockcount":
			_, _ = w.Write([]byte(`{"result":42,"error":null,"id":"walletapp-1"}`))
		case "listtransactions":
			_, _ = w.Write([]byte(`{"result":[],"error":null,"id":"walletapp-1"}`))
		default:
			t.Fatalf("unexpected method %q", gotCall.Method)
		}
	}
	rec := performAPICall(t, handler, http.MethodGet, "/api/state", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp stateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RenewFeeRateSatPerKB != int64(txrules.DefaultRelayFeePerKb) {
		t.Fatalf("renew fee rate = %d, want %d",
			resp.RenewFeeRateSatPerKB, txrules.DefaultRelayFeePerKb)
	}
}

func TestRenewHandlerUsesMinimalParamsWithoutOptionals(t *testing.T) {
	var gotCall recordedRPCCall
	handler := func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotCall); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"result":{"txid":"renewtx","input_count":1,"output_count":2,"fee_rate_sat_per_kb":1000,"target_address":"target"},"error":null,"id":"walletapp-1"}`))
	}
	rec := performAPICall(t, handler, http.MethodPost, "/api/renew", `{"outpoints":["abc:0"]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if gotCall.Method != "obtc.renew" {
		t.Fatalf("method = %q, want obtc.renew", gotCall.Method)
	}
	var params []any
	if err := json.Unmarshal(gotCall.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if len(params) != 2 {
		t.Fatalf("params len = %d, want 2: %#v", len(params), params)
	}
	if params[1] != defaultRenewTargetAmount {
		t.Fatalf("renew amount = %#v, want default %v", params[1], defaultRenewTargetAmount)
	}
}

func TestRenewHandlerKeepsPositionalOptionals(t *testing.T) {
	var gotCall recordedRPCCall
	handler := func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotCall); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"result":{"txid":"renewtx","input_count":1,"output_count":2,"fee_rate_sat_per_kb":1000,"target_address":"target"},"error":null,"id":"walletapp-1"}`))
	}
	rec := performAPICall(t, handler, http.MethodPost, "/api/renew", `{"outpoints":["abc:0"],"amount":0.1,"min_conf":2}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var params []any
	if err := json.Unmarshal(gotCall.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if len(params) != 5 {
		t.Fatalf("params len = %d, want 5: %#v", len(params), params)
	}
	if params[2] != nil || params[3] != nil || params[4] != float64(2) {
		t.Fatalf("params = %#v, want nil target, nil fee, minconf 2", params)
	}
}

func TestExpiryHandlerPassesLimit(t *testing.T) {
	var gotCall recordedRPCCall
	handler := func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotCall); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"result":{"tip_height":7,"window_blocks":10,"items":[]},"error":null,"id":"walletapp-1"}`))
	}
	rec := performAPICall(t, handler, http.MethodGet, "/api/expiry?limit=12", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if gotCall.Method != "obtc.getexpiry" {
		t.Fatalf("method = %q, want obtc.getexpiry", gotCall.Method)
	}
	var params []any
	if err := json.Unmarshal(gotCall.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if len(params) != 1 || params[0] != float64(12) {
		t.Fatalf("params = %#v, want [12]", params)
	}
}

func performAPICall(t *testing.T, rpcHandler http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	walletRPC := httptest.NewServer(rpcHandler)
	t.Cleanup(walletRPC.Close)

	client, err := newRPCClient(walletRPC.URL, "", "", time.Second)
	if err != nil {
		t.Fatalf("newRPCClient: %v", err)
	}
	app, err := newAppServer(client, "secret")
	if err != nil {
		t.Fatalf("newAppServer: %v", err)
	}
	mux := http.NewServeMux()
	app.routes(mux)

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(apiTokenHeader, "secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}
