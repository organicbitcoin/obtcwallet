package rpcserver

import (
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	pb "github.com/btcsuite/btcwallet/rpc/agentwalletrpc"
	"github.com/btcsuite/btcwallet/wallet"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AgentSignerBackend abstracts the runtime signer used by the agent wallet
// service. The initial implementation wraps the local wallet unlock path, but
// the interface is shaped so remote signers or HSM-backed signers can replace
// it without changing the agent execution API.
type AgentSignerBackend interface {
	Info() *pb.SignerBackendInfo
	OpenSession(sessionID string, passphrase []byte, ttl time.Duration,
		onExpire func()) error
	CloseSession(sessionID string) error
	ValidateSession(sessionID string) error
	FinalizePsbt(sessionID string, account uint32, packet *psbt.Packet) error
}

type localAgentSignerBackend struct {
	wallet *wallet.Wallet

	mu              sync.Mutex
	activeSessionID string
	controls        map[string]*agentSignerSessionControl
}

func newLocalAgentSignerBackend(
	wallet *wallet.Wallet) AgentSignerBackend {

	return &localAgentSignerBackend{
		wallet:   wallet,
		controls: make(map[string]*agentSignerSessionControl),
	}
}

func (b *localAgentSignerBackend) Info() *pb.SignerBackendInfo {
	return &pb.SignerBackendInfo{
		BackendId:                   "local",
		Mode:                        "local",
		LocalSigningAvailable:       true,
		ExternalSignedPsbtSupported: true,
		MaxActiveSessions:           1,
		Description: "local wallet signer backed by wallet unlock; " +
			"supports one active signer session",
	}
}

func (b *localAgentSignerBackend) OpenSession(sessionID string,
	passphrase []byte, ttl time.Duration, onExpire func()) error {

	if b.wallet.Manager.WatchOnly() {
		return status.Error(codes.FailedPrecondition,
			"wallet is watch-only; local signer sessions are unavailable")
	}
	if len(passphrase) == 0 {
		return status.Error(codes.InvalidArgument,
			"passphrase must not be empty")
	}

	b.mu.Lock()
	if b.activeSessionID != "" {
		b.mu.Unlock()
		return status.Errorf(codes.FailedPrecondition,
			"local signer backend already has an active session %s",
			b.activeSessionID)
	}
	b.mu.Unlock()

	lockChan := make(chan time.Time, 1)
	if err := b.wallet.Unlock(passphrase, lockChan); err != nil {
		return err
	}

	control := &agentSignerSessionControl{
		lock: lockChan,
	}
	control.timer = time.AfterFunc(ttl, func() {
		_ = b.CloseSession(sessionID)
		if onExpire != nil {
			onExpire()
		}
	})

	b.mu.Lock()
	b.controls[sessionID] = control
	b.activeSessionID = sessionID
	b.mu.Unlock()

	return nil
}

func (b *localAgentSignerBackend) CloseSession(sessionID string) error {
	b.mu.Lock()
	control, ok := b.controls[sessionID]
	isActive := b.activeSessionID == sessionID
	if ok {
		delete(b.controls, sessionID)
	}
	if isActive {
		b.activeSessionID = ""
	}
	b.mu.Unlock()

	if !ok {
		return nil
	}

	if control.timer != nil {
		control.timer.Stop()
	}
	signalWalletLock(control.lock)
	return nil
}

func (b *localAgentSignerBackend) ValidateSession(sessionID string) error {
	b.mu.Lock()
	_, ok := b.controls[sessionID]
	activeSessionID := b.activeSessionID
	b.mu.Unlock()

	if !ok || activeSessionID != sessionID {
		return status.Errorf(codes.FailedPrecondition,
			"local signer session %s is not active", sessionID)
	}

	return nil
}

func (b *localAgentSignerBackend) FinalizePsbt(sessionID string,
	account uint32, packet *psbt.Packet) error {

	if err := b.ValidateSession(sessionID); err != nil {
		return err
	}

	return b.wallet.FinalizePsbt(nil, account, packet)
}

type RemoteAgentPsbtSigner interface {
	FinalizePsbt(sessionID string, account uint32, packet *psbt.Packet) error
}

type remoteAgentSignerBackend struct {
	signer RemoteAgentPsbtSigner

	mu       sync.Mutex
	controls map[string]*agentSignerSessionControl
}

func newRemoteAgentSignerBackend(
	signer RemoteAgentPsbtSigner) AgentSignerBackend {

	return &remoteAgentSignerBackend{
		signer:   signer,
		controls: make(map[string]*agentSignerSessionControl),
	}
}

func (b *remoteAgentSignerBackend) Info() *pb.SignerBackendInfo {
	description := "remote signer backend; opens agent-managed signer " +
		"sessions and can delegate PSBT signing to a remote signer transport"
	if b.signer == nil {
		description = "remote signer backend skeleton; remote signing " +
			"transport is not configured, so signed PSBT must be provided externally"
	}

	return &pb.SignerBackendInfo{
		BackendId:                   "remote",
		Mode:                        "remote",
		LocalSigningAvailable:       false,
		ExternalSignedPsbtSupported: true,
		MaxActiveSessions:           1024,
		Description:                 description,
	}
}

func (b *remoteAgentSignerBackend) OpenSession(sessionID string,
	_ []byte, ttl time.Duration, onExpire func()) error {

	b.mu.Lock()
	if _, ok := b.controls[sessionID]; ok {
		b.mu.Unlock()
		return status.Errorf(codes.AlreadyExists,
			"remote signer session %s already exists", sessionID)
	}

	control := &agentSignerSessionControl{}
	control.timer = time.AfterFunc(ttl, func() {
		_ = b.CloseSession(sessionID)
		if onExpire != nil {
			onExpire()
		}
	})
	b.controls[sessionID] = control
	b.mu.Unlock()

	return nil
}

func (b *remoteAgentSignerBackend) CloseSession(sessionID string) error {
	b.mu.Lock()
	control, ok := b.controls[sessionID]
	if ok {
		delete(b.controls, sessionID)
	}
	b.mu.Unlock()

	if !ok {
		return nil
	}
	if control.timer != nil {
		control.timer.Stop()
	}

	return nil
}

func (b *remoteAgentSignerBackend) ValidateSession(sessionID string) error {
	b.mu.Lock()
	_, ok := b.controls[sessionID]
	b.mu.Unlock()

	if !ok {
		return status.Errorf(codes.FailedPrecondition,
			"remote signer session %s is not active", sessionID)
	}

	return nil
}

func (b *remoteAgentSignerBackend) FinalizePsbt(sessionID string,
	account uint32, packet *psbt.Packet) error {

	if err := b.ValidateSession(sessionID); err != nil {
		return err
	}
	if b.signer == nil {
		return status.Error(codes.Unimplemented,
			"remote signer transport is not configured; sign externally and use PublishTransaction")
	}

	return b.signer.FinalizePsbt(sessionID, account, packet)
}

type publishOnlyAgentSignerBackend struct{}

func newPublishOnlyAgentSignerBackend() AgentSignerBackend {
	return publishOnlyAgentSignerBackend{}
}

func (publishOnlyAgentSignerBackend) Info() *pb.SignerBackendInfo {
	return &pb.SignerBackendInfo{
		BackendId:                   "publish_only",
		Mode:                        "publish_only",
		LocalSigningAvailable:       false,
		ExternalSignedPsbtSupported: true,
		MaxActiveSessions:           0,
		Description: "watch-only or external-signer mode; local signer " +
			"sessions are disabled and signed PSBT must be provided externally",
	}
}

func (publishOnlyAgentSignerBackend) OpenSession(string, []byte,
	time.Duration, func()) error {

	return status.Error(codes.FailedPrecondition,
		"signer backend does not support local signer sessions")
}

func (publishOnlyAgentSignerBackend) CloseSession(string) error {
	return nil
}

func (publishOnlyAgentSignerBackend) ValidateSession(string) error {
	return status.Error(codes.FailedPrecondition,
		"signer backend does not manage local signer sessions")
}

func (publishOnlyAgentSignerBackend) FinalizePsbt(string, uint32,
	*psbt.Packet) error {

	return status.Error(codes.FailedPrecondition,
		"signer backend is publish-only; use an external signer and PublishTransaction")
}
