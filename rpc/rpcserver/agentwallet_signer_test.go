package rpcserver

import (
	"testing"
	"time"
)

func TestPublishOnlyAgentSignerBackendInfo(t *testing.T) {
	backend := newPublishOnlyAgentSignerBackend()
	info := backend.Info()

	if info.GetBackendId() != "publish_only" {
		t.Fatalf("unexpected backend id: %s", info.GetBackendId())
	}
	if info.GetLocalSigningAvailable() {
		t.Fatalf("publish-only backend must not report local signing")
	}
	if !info.GetExternalSignedPsbtSupported() {
		t.Fatalf("publish-only backend must support external signed PSBT")
	}
	if info.GetMaxActiveSessions() != 0 {
		t.Fatalf("unexpected max active sessions: %d",
			info.GetMaxActiveSessions())
	}
}

func TestPublishOnlyAgentSignerBackendRejectsLocalSigning(t *testing.T) {
	backend := newPublishOnlyAgentSignerBackend()

	if err := backend.OpenSession("sess_1", []byte("secret"),
		time.Minute, "test", nil); err == nil {
		t.Fatalf("expected OpenSession to fail")
	}
	if err := backend.ValidateSession("sess_1"); err == nil {
		t.Fatalf("expected ValidateSession to fail")
	}
	if err := backend.FinalizePsbt("sess_1", 0, nil); err == nil {
		t.Fatalf("expected FinalizePsbt to fail")
	}
}

func TestRemoteAgentSignerBackendInfo(t *testing.T) {
	backend := newRemoteAgentSignerBackend(nil)
	info := backend.Info()

	if info.GetBackendId() != "remote" {
		t.Fatalf("unexpected backend id: %s", info.GetBackendId())
	}
	if info.GetLocalSigningAvailable() {
		t.Fatalf("remote backend must not report local signing")
	}
	if !info.GetExternalSignedPsbtSupported() {
		t.Fatalf("remote backend must support external signed PSBT")
	}
	if info.GetMaxActiveSessions() == 0 {
		t.Fatalf("remote backend must report session capacity")
	}
}

func TestRemoteAgentSignerBackendSessionLifecycle(t *testing.T) {
	backend := newRemoteAgentSignerBackend(nil)

	if err := backend.OpenSession("sess_1", nil, time.Minute, "test", nil); err != nil {
		t.Fatalf("OpenSession error: %v", err)
	}
	if err := backend.ValidateSession("sess_1"); err != nil {
		t.Fatalf("ValidateSession error: %v", err)
	}
	if err := backend.FinalizePsbt("sess_1", 0, nil); err == nil {
		t.Fatalf("expected FinalizePsbt to fail without remote transport")
	}
	if err := backend.CloseSession("sess_1", "test"); err != nil {
		t.Fatalf("CloseSession error: %v", err)
	}
	if err := backend.ValidateSession("sess_1"); err == nil {
		t.Fatalf("expected session to be closed")
	}
}
