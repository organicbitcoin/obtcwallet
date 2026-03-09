package rpcserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	remotepb "github.com/btcsuite/btcwallet/rpc/remotesignerrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const defaultRemoteSignerDialTimeout = 5 * time.Second

// GRPCRemoteSignerTransportConfig describes how the agent wallet should reach
// an external signer transport over gRPC.
type GRPCRemoteSignerTransportConfig struct {
	Address       string
	TLSCertPath   string
	TLSServerName string
	DisableTLS    bool
	AuthToken     string
	DialTimeout   time.Duration
}

type remoteAgentSignerTransport interface {
	Description() string
	Endpoint() string
	OpenSession(sessionID string, auth []byte, ttl time.Duration,
		reason string) error
	CloseSession(sessionID, reason string) error
	FinalizePsbt(sessionID string, account uint32, packet *psbt.Packet) error
}

type grpcRemoteAgentSignerTransport struct {
	cfg GRPCRemoteSignerTransportConfig
}

// NewRemoteAgentSignerBackendWithGRPCTransport builds a remote agent signer
// backend that delegates signer session lifecycle and PSBT finalization to a
// gRPC remote signer service.
func NewRemoteAgentSignerBackendWithGRPCTransport(
	cfg GRPCRemoteSignerTransportConfig) (AgentSignerBackend, error) {

	transport, err := newGRPCRemoteAgentSignerTransport(cfg)
	if err != nil {
		return nil, err
	}

	return newRemoteAgentSignerBackend(transport), nil
}

func newGRPCRemoteAgentSignerTransport(
	cfg GRPCRemoteSignerTransportConfig) (*grpcRemoteAgentSignerTransport, error) {

	if cfg.Address == "" {
		return nil, status.Error(codes.InvalidArgument,
			"remote signer address must not be empty")
	}
	if _, _, err := net.SplitHostPort(cfg.Address); err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"remote signer address must be host:port: %v", err)
	}
	if cfg.DisableTLS && !remoteSignerAddressIsLoopback(cfg.Address) {
		return nil, status.Error(codes.InvalidArgument,
			"remote signer TLS may only be disabled for loopback addresses")
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = defaultRemoteSignerDialTimeout
	}

	return &grpcRemoteAgentSignerTransport{cfg: cfg}, nil
}

func (t *grpcRemoteAgentSignerTransport) Description() string {
	return fmt.Sprintf("remote signer transport over gRPC to %s", t.cfg.Address)
}

func (t *grpcRemoteAgentSignerTransport) Endpoint() string {
	return t.cfg.Address
}

func (t *grpcRemoteAgentSignerTransport) OpenSession(sessionID string,
	auth []byte, ttl time.Duration, reason string) error {

	respErr := t.invoke(func(
		ctx context.Context, client remotepb.RemoteSignerServiceClient) error {

		_, err := client.OpenSession(ctx, &remotepb.OpenSessionRequest{
			SessionId:  sessionID,
			Auth:       auth,
			TtlSeconds: int64(ttl / time.Second),
			Reason:     reason,
		})
		return err
	})
	if respErr != nil {
		return translateRemoteSignerTransportError("open remote signer session",
			respErr)
	}

	return nil
}

func (t *grpcRemoteAgentSignerTransport) CloseSession(sessionID,
	reason string) error {

	respErr := t.invoke(func(
		ctx context.Context, client remotepb.RemoteSignerServiceClient) error {

		_, err := client.CloseSession(ctx, &remotepb.CloseSessionRequest{
			SessionId: sessionID,
			Reason:    reason,
		})
		return err
	})
	if respErr == nil || status.Code(respErr) == codes.NotFound {
		return nil
	}

	return translateRemoteSignerTransportError("close remote signer session",
		respErr)
}

func (t *grpcRemoteAgentSignerTransport) FinalizePsbt(sessionID string,
	account uint32, packet *psbt.Packet) error {

	rawPSBT, _, err := serializePsbt(packet)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to serialize PSBT for remote signer: %v", err)
	}

	var signedPSBT []byte
	respErr := t.invoke(func(
		ctx context.Context, client remotepb.RemoteSignerServiceClient) error {

		resp, err := client.FinalizePsbt(ctx, &remotepb.FinalizePsbtRequest{
			SessionId:     sessionID,
			AccountNumber: account,
			UnsignedPsbt:  rawPSBT,
		})
		if err != nil {
			return err
		}
		signedPSBT = resp.GetSignedPsbt()
		return nil
	})
	if respErr != nil {
		return translateRemoteSignerTransportError("finalize PSBT with remote signer",
			respErr)
	}
	if len(signedPSBT) == 0 {
		return status.Error(codes.Internal,
			"remote signer returned empty signed_psbt")
	}

	signedPacket, err := parsePsbtBytes(signedPSBT)
	if err != nil {
		return status.Errorf(codes.Internal,
			"remote signer returned invalid signed PSBT: %v", err)
	}
	*packet = *signedPacket

	return nil
}

func (t *grpcRemoteAgentSignerTransport) invoke(
	call func(context.Context, remotepb.RemoteSignerServiceClient) error) error {

	ctx, cancel := context.WithTimeout(context.Background(), t.cfg.DialTimeout)
	defer cancel()

	ctx = t.decorateContext(ctx)

	conn, err := t.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := remotepb.NewRemoteSignerServiceClient(conn)
	return call(ctx, client)
}

func (t *grpcRemoteAgentSignerTransport) dial(
	ctx context.Context) (*grpc.ClientConn, error) {

	creds, err := t.transportCredentials()
	if err != nil {
		return nil, err
	}

	conn, err := grpc.DialContext(
		ctx,
		t.cfg.Address,
		grpc.WithTransportCredentials(creds),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable,
			"unable to dial remote signer %s: %v", t.cfg.Address, err)
	}

	return conn, nil
}

func (t *grpcRemoteAgentSignerTransport) transportCredentials() (
	credentials.TransportCredentials, error) {

	if t.cfg.DisableTLS {
		return insecure.NewCredentials(), nil
	}

	if t.cfg.TLSCertPath != "" {
		creds, err := credentials.NewClientTLSFromFile(
			t.cfg.TLSCertPath, t.cfg.TLSServerName,
		)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument,
				"failed to load remote signer TLS certificate: %v", err)
		}
		return creds, nil
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: t.cfg.TLSServerName,
	}
	return credentials.NewTLS(tlsCfg), nil
}

func (t *grpcRemoteAgentSignerTransport) decorateContext(
	ctx context.Context) context.Context {

	if strings.TrimSpace(t.cfg.AuthToken) == "" {
		return ctx
	}

	return metadata.AppendToOutgoingContext(
		ctx, "authorization", bearerTokenValue(t.cfg.AuthToken),
	)
}

func bearerTokenValue(token string) string {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return ""
	}

	if strings.HasPrefix(strings.ToLower(trimmed), "bearer ") {
		return trimmed
	}

	return "Bearer " + trimmed
}

func remoteSignerAddressIsLoopback(address string) bool {
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

func translateRemoteSignerTransportError(action string, err error) error {
	if err == nil {
		return nil
	}

	st, ok := status.FromError(err)
	if ok {
		return status.Error(st.Code(),
			fmt.Sprintf("%s failed: %s", action, st.Message()))
	}

	return status.Errorf(codes.Unavailable, "%s failed: %v", action, err)
}
