package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcwallet/wallet/txrules"
)

//go:embed static/*
var staticFiles embed.FS

const (
	defaultListen            = "127.0.0.1:19580"
	defaultWalletRPC         = "http://127.0.0.1:19554/"
	defaultRPCTimeout        = 45 * time.Second
	defaultRenewTargetAmount = 0.0001
	apiTokenHeader           = "X-Wallet-App-Token"
	indexTokenMarker         = "__WALLET_APP_TOKEN__"
	indexEndpointMarker      = "__WALLET_RPC_ENDPOINT__"
)

type appConfig struct {
	listen           string
	walletRPC        string
	walletUser       string
	walletPass       string
	rpcTimeout       time.Duration
	allowNonLoopback bool
}

type rpcClient struct {
	endpoint string
	username string
	password string
	client   *http.Client
	nextID   atomic.Uint64
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("wallet RPC error %d: %s", e.Code, e.Message)
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
	ID     json.RawMessage `json:"id"`
}

type appServer struct {
	rpc           *rpcClient
	apiToken      string
	walletRPCInfo string
	indexHTML     string
	staticFS      http.Handler
}

type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Message string `json:"message"`
}

type stateResponse struct {
	WalletRPC            string            `json:"wallet_rpc"`
	RenewFeeRateSatPerKB int64             `json:"renew_fee_rate_sat_per_kb"`
	Locked               *bool             `json:"locked,omitempty"`
	Balance              *float64          `json:"balance,omitempty"`
	SpendableBalance     *float64          `json:"spendable_balance,omitempty"`
	UnconfirmedBalance   *float64          `json:"unconfirmed_balance,omitempty"`
	BlockCount           *int64            `json:"block_count,omitempty"`
	RecentTransactions   []json.RawMessage `json:"recent_transactions,omitempty"`
	PartialErrors        map[string]string `json:"partial_errors,omitempty"`
	GeneratedAtUnixMilli int64             `json:"generated_at_unix_milli"`
}

type newAddressRequest struct {
	AddressType string `json:"address_type"`
}

type newAddressResponse struct {
	Address string `json:"address"`
}

type unlockRequest struct {
	Passphrase     string `json:"passphrase"`
	TimeoutSeconds int64  `json:"timeout_seconds"`
}

type sendRequest struct {
	Address string  `json:"address"`
	Amount  float64 `json:"amount"`
}

type sendResponse struct {
	TxID string `json:"txid"`
}

type renewRequest struct {
	Outpoints     []string `json:"outpoints"`
	Amount        *float64 `json:"amount"`
	TargetAddress string   `json:"target_address"`
	MaxFeeRate    *float64 `json:"max_fee_rate"`
	MinConf       *int32   `json:"min_conf"`
}

type renewResponse struct {
	TxID            string `json:"txid"`
	InputCount      int    `json:"input_count"`
	OutputCount     int    `json:"output_count"`
	FeeRateSatPerKB int64  `json:"fee_rate_sat_per_kb"`
	TargetAddress   string `json:"target_address"`
}

func main() {
	cfg := parseConfig(os.Args[1:])
	rpc, err := newRPCClient(cfg.walletRPC, cfg.walletUser, cfg.walletPass, cfg.rpcTimeout)
	if err != nil {
		log.Fatalf("invalid wallet RPC endpoint: %v", err)
	}

	apiToken, err := newAPIToken()
	if err != nil {
		log.Fatalf("create API token: %v", err)
	}

	server, err := newAppServer(rpc, apiToken)
	if err != nil {
		log.Fatalf("create wallet app: %v", err)
	}

	mux := http.NewServeMux()
	server.routes(mux)

	httpServer := &http.Server{
		Addr:              cfg.listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("walletapp listening on http://%s", cfg.listen)
	log.Printf("proxying wallet legacy RPC at %s", rpc.safeEndpoint())
	if cfg.walletUser == "" && cfg.walletPass == "" {
		log.Printf("wallet RPC credentials are empty; use --wallet-user/--wallet-pass if the wallet requires Basic Auth")
	}

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func parseConfig(args []string) appConfig {
	cfg := appConfig{
		listen:     envOr("OBTC_WALLETAPP_LISTEN", defaultListen),
		walletRPC:  envOr("OBTC_WALLET_RPC", defaultWalletRPC),
		walletUser: os.Getenv("OBTC_WALLET_RPC_USER"),
		walletPass: os.Getenv("OBTC_WALLET_RPC_PASS"),
		rpcTimeout: defaultRPCTimeout,
	}

	flags := flag.NewFlagSet("walletapp", flag.ExitOnError)
	flags.StringVar(&cfg.listen, "listen", cfg.listen, "HTTP listen address")
	flags.StringVar(&cfg.walletRPC, "wallet-rpc", cfg.walletRPC, "obtcwallet legacy JSON-RPC URL")
	flags.StringVar(&cfg.walletUser, "wallet-user", cfg.walletUser, "obtcwallet legacy JSON-RPC username")
	flags.StringVar(&cfg.walletPass, "wallet-pass", cfg.walletPass, "obtcwallet legacy JSON-RPC password")
	flags.DurationVar(&cfg.rpcTimeout, "rpc-timeout", cfg.rpcTimeout, "wallet RPC request timeout")
	flags.BoolVar(&cfg.allowNonLoopback, "allow-non-loopback", cfg.allowNonLoopback, "allow walletapp to listen on a non-loopback address")
	_ = flags.Parse(args)

	if !cfg.allowNonLoopback && !hostIsLoopback(cfg.listen) {
		log.Fatalf("refusing to listen on non-loopback address %q without --allow-non-loopback", cfg.listen)
	}

	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func newRPCClient(endpoint, username, password string, timeout time.Duration) (*rpcClient, error) {
	if timeout <= 0 {
		return nil, errors.New("timeout must be > 0")
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("missing host")
	}
	if username == "" && u.User != nil {
		username = u.User.Username()
	}
	if password == "" && u.User != nil {
		if p, ok := u.User.Password(); ok {
			password = p
		}
	}
	u.User = nil

	return &rpcClient{
		endpoint: u.String(),
		username: username,
		password: password,
		client: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func (c *rpcClient) safeEndpoint() string {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return c.endpoint
	}
	u.User = nil
	return u.String()
}

func (c *rpcClient) call(ctx context.Context, method string, params []any, result any) error {
	if params == nil {
		params = []any{}
	}
	id := c.nextID.Add(1)
	payload := map[string]any{
		"jsonrpc": "1.0",
		"id":      fmt.Sprintf("walletapp-%d", id),
		"method":  method,
		"params":  params,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wallet RPC HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var envelope rpcResponse
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("decode wallet RPC response: %w", err)
	}
	if envelope.Error != nil {
		return envelope.Error
	}
	if result == nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("decode wallet RPC result for %s: %w", method, err)
	}
	return nil
}

func newAPIToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func newAppServer(rpc *rpcClient, apiToken string) (*appServer, error) {
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}
	indexBytes, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		return nil, err
	}
	indexHTML := strings.ReplaceAll(string(indexBytes), indexTokenMarker, apiToken)
	indexHTML = strings.ReplaceAll(indexHTML, indexEndpointMarker, rpc.safeEndpoint())

	return &appServer{
		rpc:           rpc,
		apiToken:      apiToken,
		walletRPCInfo: rpc.safeEndpoint(),
		indexHTML:     indexHTML,
		staticFS:      http.FileServer(http.FS(static)),
	}, nil
}

func (s *appServer) routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/state", s.withAPIAuth(s.handleState))
	mux.HandleFunc("/api/address/new", s.withAPIAuth(s.handleNewAddress))
	mux.HandleFunc("/api/unlock", s.withAPIAuth(s.handleUnlock))
	mux.HandleFunc("/api/lock", s.withAPIAuth(s.handleLock))
	mux.HandleFunc("/api/send", s.withAPIAuth(s.handleSend))
	mux.HandleFunc("/api/expiry", s.withAPIAuth(s.handleExpiry))
	mux.HandleFunc("/api/renew", s.withAPIAuth(s.handleRenew))
	mux.HandleFunc("/api/transactions", s.withAPIAuth(s.handleTransactions))
	mux.HandleFunc("/", s.handleStatic)
}

func (s *appServer) withAPIAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(apiTokenHeader)), []byte(s.apiToken)) != 1 {
			writeError(w, http.StatusForbidden, errors.New("invalid wallet app token"))
			return
		}
		next(w, r)
	}
}

func (s *appServer) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	securityHeaders(w)
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, s.indexHTML)
		return
	}
	s.staticFS.ServeHTTP(w, r)
}

func (s *appServer) handleState(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	ctx := r.Context()
	resp := stateResponse{
		WalletRPC:            s.walletRPCInfo,
		RenewFeeRateSatPerKB: int64(txrules.DefaultRelayFeePerKb),
		PartialErrors:        map[string]string{},
		GeneratedAtUnixMilli: time.Now().UnixMilli(),
	}

	var locked bool
	if err := s.rpc.call(ctx, "walletislocked", nil, &locked); err != nil {
		resp.PartialErrors["walletislocked"] = err.Error()
	} else {
		resp.Locked = &locked
	}

	var spendable float64
	if err := s.rpc.call(ctx, "getbalance", []any{"*", 1}, &spendable); err != nil {
		resp.PartialErrors["getbalance_spendable"] = err.Error()
	} else {
		resp.SpendableBalance = &spendable
	}

	var balance float64
	if err := s.rpc.call(ctx, "getbalance", []any{"*", 0}, &balance); err != nil {
		resp.PartialErrors["getbalance_total"] = err.Error()
	} else {
		resp.Balance = &balance
	}

	var unconfirmed float64
	if err := s.rpc.call(ctx, "getunconfirmedbalance", nil, &unconfirmed); err != nil {
		resp.PartialErrors["getunconfirmedbalance"] = err.Error()
	} else {
		resp.UnconfirmedBalance = &unconfirmed
	}

	var blockCount int64
	if err := s.rpc.call(ctx, "getblockcount", nil, &blockCount); err != nil {
		resp.PartialErrors["getblockcount"] = err.Error()
	} else {
		resp.BlockCount = &blockCount
	}

	var txs []json.RawMessage
	if err := s.rpc.call(ctx, "listtransactions", []any{"*", 8, 0}, &txs); err != nil {
		resp.PartialErrors["listtransactions"] = err.Error()
	} else {
		resp.RecentTransactions = txs
	}

	if len(resp.PartialErrors) == 0 {
		resp.PartialErrors = nil
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *appServer) handleNewAddress(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var req newAddressRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.AddressType == "" {
		req.AddressType = "legacy"
	}
	switch req.AddressType {
	case "legacy", "p2sh-segwit", "bech32":
	default:
		writeError(w, http.StatusBadRequest, errors.New("address_type must be legacy, p2sh-segwit, or bech32"))
		return
	}

	var address string
	if err := s.rpc.call(r.Context(), "getnewaddress", []any{"default", req.AddressType}, &address); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, newAddressResponse{Address: address})
}

func (s *appServer) handleUnlock(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var req unlockRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Passphrase = strings.TrimSpace(req.Passphrase)
	if req.Passphrase == "" {
		writeError(w, http.StatusBadRequest, errors.New("passphrase is required"))
		return
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 300
	}
	if req.TimeoutSeconds > 86400 {
		writeError(w, http.StatusBadRequest, errors.New("timeout_seconds must be <= 86400"))
		return
	}

	if err := s.rpc.call(r.Context(), "walletpassphrase", []any{req.Passphrase, req.TimeoutSeconds}, nil); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *appServer) handleLock(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if err := s.rpc.call(r.Context(), "walletlock", nil, nil); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *appServer) handleSend(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var req sendRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Address = strings.TrimSpace(req.Address)
	if req.Address == "" {
		writeError(w, http.StatusBadRequest, errors.New("address is required"))
		return
	}
	if err := validatePositiveAmount(req.Amount, "amount"); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var txid string
	if err := s.rpc.call(r.Context(), "sendtoaddress", []any{req.Address, req.Amount}, &txid); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, sendResponse{TxID: txid})
}

func (s *appServer) handleExpiry(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	limit := parseIntQuery(r, "limit", 100)
	if limit < 0 {
		writeError(w, http.StatusBadRequest, errors.New("limit must be >= 0"))
		return
	}

	params := []any{limit}
	if beforeHeight := strings.TrimSpace(r.URL.Query().Get("before_height")); beforeHeight != "" {
		height, err := strconv.ParseInt(beforeHeight, 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("before_height must be an integer"))
			return
		}
		params = append(params, int32(height))
	}

	var result json.RawMessage
	if err := s.rpc.call(r.Context(), "obtc.getexpiry", params, &result); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeRawResult(w, result)
}

func (s *appServer) handleRenew(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var req renewRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Outpoints) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("at least one outpoint is required"))
		return
	}
	for i, outpoint := range req.Outpoints {
		req.Outpoints[i] = strings.TrimSpace(outpoint)
		if req.Outpoints[i] == "" {
			writeError(w, http.StatusBadRequest, errors.New("outpoints must not contain empty values"))
			return
		}
	}
	renewAmount := defaultRenewTargetAmount
	if req.Amount != nil {
		if err := validatePositiveAmount(*req.Amount, "amount"); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		renewAmount = *req.Amount
	}
	if req.MaxFeeRate != nil {
		if err := validatePositiveAmount(*req.MaxFeeRate, "max_fee_rate"); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if req.MinConf != nil && *req.MinConf < 0 {
		writeError(w, http.StatusBadRequest, errors.New("min_conf must be >= 0"))
		return
	}

	params := []any{req.Outpoints, renewAmount}
	if req.MinConf != nil {
		if req.TargetAddress != "" {
			params = append(params, strings.TrimSpace(req.TargetAddress))
		} else {
			params = append(params, nil)
		}
		if req.MaxFeeRate != nil {
			params = append(params, *req.MaxFeeRate)
		} else {
			params = append(params, nil)
		}
		params = append(params, *req.MinConf)
	} else if req.MaxFeeRate != nil {
		if req.TargetAddress != "" {
			params = append(params, strings.TrimSpace(req.TargetAddress))
		} else {
			params = append(params, nil)
		}
		params = append(params, *req.MaxFeeRate)
	} else if req.TargetAddress != "" {
		params = append(params, strings.TrimSpace(req.TargetAddress))
	}

	var result renewResponse
	if err := s.rpc.call(r.Context(), "obtc.renew", params, &result); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *appServer) handleTransactions(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	count := parseIntQuery(r, "count", 50)
	if count < 1 || count > 500 {
		writeError(w, http.StatusBadRequest, errors.New("count must be between 1 and 500"))
		return
	}

	var txs []json.RawMessage
	if err := s.rpc.call(r.Context(), "listtransactions", []any{"*", count, 0}, &txs); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transactions": txs})
}

func parseIntQuery(r *http.Request, key string, fallback int) int {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return n
}

func validatePositiveAmount(value float64, field string) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return fmt.Errorf("%s must be > 0", field)
	}
	return nil
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	return false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	securityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeRawResult(w http.ResponseWriter, raw json.RawMessage) {
	securityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if len(raw) == 0 {
		raw = []byte("null")
	}
	_, _ = w.Write(raw)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, apiError{Error: apiErrorBody{Message: err.Error()}})
}

func securityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func hostIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
