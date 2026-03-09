package rpcserver

import (
	"bytes"
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	remotepb "github.com/btcsuite/btcwallet/rpc/remotesignerrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type testRemoteSignerService struct {
	remotepb.UnimplementedRemoteSignerServiceServer

	mu sync.Mutex

	openSessionID   string
	openAuth        []byte
	openTTLSeconds  int64
	openReason      string
	closeSessionID  string
	closeReason     string
	finalizeSession string
	finalizeAccount uint32
	finalizePSBT    []byte
	authorization   string
}

func (s *testRemoteSignerService) OpenSession(ctx context.Context,
	req *remotepb.OpenSessionRequest) (*remotepb.OpenSessionResponse, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.openSessionID = req.GetSessionId()
	s.openAuth = append([]byte(nil), req.GetAuth()...)
	s.openTTLSeconds = req.GetTtlSeconds()
	s.openReason = req.GetReason()
	s.authorization = authorizationHeaderFromContext(ctx)

	return &remotepb.OpenSessionResponse{
		ExpiresAtUnix: time.Now().Add(time.Duration(req.GetTtlSeconds()) *
			time.Second).Unix(),
	}, nil
}

func (s *testRemoteSignerService) CloseSession(ctx context.Context,
	req *remotepb.CloseSessionRequest) (*remotepb.CloseSessionResponse, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.closeSessionID = req.GetSessionId()
	s.closeReason = req.GetReason()
	s.authorization = authorizationHeaderFromContext(ctx)

	return &remotepb.CloseSessionResponse{}, nil
}

func (s *testRemoteSignerService) FinalizePsbt(ctx context.Context,
	req *remotepb.FinalizePsbtRequest) (*remotepb.FinalizePsbtResponse, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.finalizeSession = req.GetSessionId()
	s.finalizeAccount = req.GetAccountNumber()
	s.finalizePSBT = append([]byte(nil), req.GetUnsignedPsbt()...)
	s.authorization = authorizationHeaderFromContext(ctx)

	return &remotepb.FinalizePsbtResponse{
		SignedPsbt: append([]byte(nil), req.GetUnsignedPsbt()...),
	}, nil
}

func authorizationHeaderFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func startTestRemoteSignerService(t *testing.T) (string, *testRemoteSignerService,
	func()) {

	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unable to listen for test remote signer: %v", err)
	}

	server := grpc.NewServer()
	service := &testRemoteSignerService{}
	remotepb.RegisterRemoteSignerServiceServer(server, service)

	go func() {
		if serveErr := server.Serve(lis); serveErr != nil {
			t.Logf("test remote signer serve stopped: %v", serveErr)
		}
	}()

	cleanup := func() {
		server.Stop()
		_ = lis.Close()
	}

	return lis.Addr().String(), service, cleanup
}

func TestNewRemoteAgentSignerBackendWithGRPCTransportRejectsNonLoopbackNoTLS(
	t *testing.T) {

	_, err := NewRemoteAgentSignerBackendWithGRPCTransport(
		GRPCRemoteSignerTransportConfig{
			Address:    "192.0.2.10:10009",
			DisableTLS: true,
		},
	)
	if err == nil {
		t.Fatalf("expected non-loopback notls config to fail")
	}
}

func TestRemoteAgentSignerBackendUsesGRPCTransport(t *testing.T) {
	address, service, cleanup := startTestRemoteSignerService(t)
	defer cleanup()

	backend, err := NewRemoteAgentSignerBackendWithGRPCTransport(
		GRPCRemoteSignerTransportConfig{
			Address:     address,
			DisableTLS:  true,
			AuthToken:   "token-123",
			DialTimeout: time.Second,
		},
	)
	if err != nil {
		t.Fatalf("unable to create remote signer backend: %v", err)
	}

	packet, err := parsePsbtBytes(testPsbtBytes(t))
	if err != nil {
		t.Fatalf("unable to parse test PSBT: %v", err)
	}

	if err := backend.OpenSession(
		"sess_1", []byte("pin-1234"), 2*time.Minute, "renewal-run", nil,
	); err != nil {
		t.Fatalf("OpenSession error: %v", err)
	}
	if err := backend.ValidateSession("sess_1"); err != nil {
		t.Fatalf("ValidateSession error: %v", err)
	}
	if err := backend.FinalizePsbt("sess_1", 7, packet); err != nil {
		t.Fatalf("FinalizePsbt error: %v", err)
	}
	if err := backend.CloseSession("sess_1", "client_requested"); err != nil {
		t.Fatalf("CloseSession error: %v", err)
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	if service.openSessionID != "sess_1" {
		t.Fatalf("unexpected open session id: %s", service.openSessionID)
	}
	if string(service.openAuth) != "pin-1234" {
		t.Fatalf("unexpected open auth: %q", string(service.openAuth))
	}
	if service.openTTLSeconds != 120 {
		t.Fatalf("unexpected TTL seconds: %d", service.openTTLSeconds)
	}
	if service.openReason != "renewal-run" {
		t.Fatalf("unexpected open reason: %s", service.openReason)
	}
	if service.finalizeSession != "sess_1" {
		t.Fatalf("unexpected finalize session: %s", service.finalizeSession)
	}
	if service.finalizeAccount != 7 {
		t.Fatalf("unexpected finalize account: %d", service.finalizeAccount)
	}
	if len(service.finalizePSBT) == 0 {
		t.Fatalf("expected finalize PSBT payload")
	}
	if service.closeSessionID != "sess_1" {
		t.Fatalf("unexpected close session id: %s", service.closeSessionID)
	}
	if service.closeReason != "client_requested" {
		t.Fatalf("unexpected close reason: %s", service.closeReason)
	}
	if service.authorization != "Bearer token-123" {
		t.Fatalf("unexpected authorization header: %q",
			service.authorization)
	}
}

func TestGRPCRemoteAgentSignerTransportFinalizeRoundTrip(t *testing.T) {
	address, service, cleanup := startTestRemoteSignerService(t)
	defer cleanup()

	transport, err := newGRPCRemoteAgentSignerTransport(
		GRPCRemoteSignerTransportConfig{
			Address:     address,
			DisableTLS:  true,
			DialTimeout: time.Second,
		},
	)
	if err != nil {
		t.Fatalf("unable to create transport: %v", err)
	}

	packet, err := parsePsbtBytes(testPsbtBytes(t))
	if err != nil {
		t.Fatalf("unable to parse test PSBT: %v", err)
	}

	if err := transport.FinalizePsbt("sess_9", 42, packet); err != nil {
		t.Fatalf("FinalizePsbt error: %v", err)
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	if service.finalizeSession != "sess_9" {
		t.Fatalf("unexpected finalize session: %s", service.finalizeSession)
	}
	if service.finalizeAccount != 42 {
		t.Fatalf("unexpected finalize account: %d", service.finalizeAccount)
	}
	roundTripPacket, err := psbt.NewFromRawBytes(
		bytes.NewReader(service.finalizePSBT), false,
	)
	if err != nil {
		t.Fatalf("unable to parse round-trip PSBT: %v", err)
	}
	if len(roundTripPacket.UnsignedTx.TxIn) == 0 {
		t.Fatalf("expected round-trip PSBT inputs")
	}
}
