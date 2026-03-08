package rpcserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/btcsuite/btcwallet/internal/zero"
	pb "github.com/btcsuite/btcwallet/rpc/agentwalletrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultCapabilityTTLSeconds    int64 = 3600
	defaultSignerSessionTTLSeconds int64 = 300

	capabilityPermissionAll               = "*"
	capabilityPermissionSignerSessionOpen = "signer_session.open"
	capabilityPermissionRenewalSign       = "renewal.sign"
	capabilityPermissionRenewalPublish    = "renewal.publish"
	capabilityPermissionRenewalSubmit     = "renewal.submit"

	signerSessionCloseReasonExpired         = "expired"
	signerSessionCloseReasonRevoked         = "capability_revoked"
	signerSessionCloseReasonServiceRestart  = "service_restart"
	signerSessionCloseReasonClientRequested = "closed_by_request"
)

type agentSignerSessionControl struct {
	lock  chan time.Time
	timer *time.Timer
}

func (s *agentWalletServer) IssueCapability(_ context.Context,
	req *pb.IssueCapabilityRequest) (*pb.IssueCapabilityResponse, error) {

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}
	if req.Principal == "" {
		return nil, status.Error(codes.InvalidArgument,
			"principal must not be empty")
	}

	permissions := normalizePermissions(req.Permissions)
	if len(permissions) == 0 {
		return nil, status.Error(codes.InvalidArgument,
			"permissions must not be empty")
	}

	ttlSeconds := req.TtlSeconds
	if ttlSeconds <= 0 {
		ttlSeconds = defaultCapabilityTTLSeconds
	}

	now := time.Now().Unix()

	s.mu.Lock()
	capabilityID := s.newCapabilityIDLocked()
	record := &agentCapabilityRecord{
		CapabilityID:  capabilityID,
		WalletID:      normalizeWalletID(req.GetWalletId()),
		Principal:     req.Principal,
		Permissions:   permissions,
		CreatedAtUnix: now,
		ExpiresAtUnix: now + ttlSeconds,
		IssuedBy:      req.GetMeta().GetPrincipal(),
		UpdatedAtUnix: now,
	}
	s.mu.Unlock()

	if err := s.persistence.putCapability(record); err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to persist capability: %v", err)
	}

	s.mu.Lock()
	s.capabilities[capabilityID] = cloneCapabilityRecord(record)
	s.mu.Unlock()

	return &pb.IssueCapabilityResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
		},
		Capability: toPBCapability(record),
	}, nil
}

func (s *agentWalletServer) RevokeCapability(_ context.Context,
	req *pb.RevokeCapabilityRequest) (*pb.RevokeCapabilityResponse, error) {

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}
	if req.CapabilityId == "" {
		return nil, status.Error(codes.InvalidArgument,
			"capability_id must not be empty")
	}

	s.mu.Lock()
	record, ok := s.capabilities[req.CapabilityId]
	s.mu.Unlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound,
			"capability %s not found", req.CapabilityId)
	}

	updated := cloneCapabilityRecord(record)
	warnings := make([]string, 0, 1)
	if updated.Revoked {
		warnings = append(warnings,
			"capability already revoked; returning recorded state")
	} else {
		now := time.Now().Unix()
		updated.Revoked = true
		updated.RevokedAtUnix = now
		updated.RevocationReason = req.Reason
		if updated.RevocationReason == "" {
			updated.RevocationReason = signerSessionCloseReasonRevoked
		}
		updated.UpdatedAtUnix = now

		if err := s.persistence.putCapability(updated); err != nil {
			return nil, status.Errorf(codes.Internal,
				"failed to persist revoked capability: %v", err)
		}

		s.mu.Lock()
		s.capabilities[updated.CapabilityID] = cloneCapabilityRecord(updated)
		s.mu.Unlock()

		closeWarnings := s.closeSignerSessionsForCapability(
			updated.CapabilityID, signerSessionCloseReasonRevoked,
		)
		warnings = append(warnings, closeWarnings...)
	}

	return &pb.RevokeCapabilityResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
			Warnings:  warnings,
		},
		Capability: toPBCapability(updated),
	}, nil
}

func (s *agentWalletServer) OpenSignerSession(_ context.Context,
	req *pb.OpenSignerSessionRequest) (*pb.OpenSignerSessionResponse, error) {

	defer zero.Bytes(req.Passphrase)

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}
	if req.CapabilityId == "" {
		return nil, status.Error(codes.InvalidArgument,
			"capability_id must not be empty")
	}

	walletID := normalizeWalletID(req.GetWalletId())
	capability, err := s.requireCapabilityByID(
		walletID, req.CapabilityId, req.GetMeta(),
		capabilityPermissionSignerSessionOpen,
		capabilityPermissionRenewalSign,
		capabilityPermissionRenewalSubmit,
	)
	if err != nil {
		return nil, err
	}

	ttlSeconds := req.TtlSeconds
	if ttlSeconds <= 0 {
		ttlSeconds = defaultSignerSessionTTLSeconds
	}

	now := time.Now().Unix()

	s.mu.Lock()
	sessionID := s.newSignerSessionIDLocked()
	record := &agentSignerSessionRecord{
		SignerSessionID:    sessionID,
		WalletID:           capability.WalletID,
		CapabilityID:       capability.CapabilityID,
		Principal:          capability.Principal,
		Permissions:        append([]string(nil), capability.Permissions...),
		CreatedAtUnix:      now,
		ExpiresAtUnix:      now + ttlSeconds,
		Reason:             req.Reason,
		OpenRequestID:      requestID(req.GetMeta()),
		OpenIdempotencyKey: req.GetMeta().GetIdempotencyKey(),
		UpdatedAtUnix:      now,
	}
	s.mu.Unlock()

	if err := s.signerBackend.OpenSession(
		sessionID, req.Passphrase, time.Duration(ttlSeconds)*time.Second,
		func() {
			_, _ = s.closeSignerSessionInternal(
				sessionID, signerSessionCloseReasonExpired,
				time.Now().Unix(), false,
			)
		},
	); err != nil {
		if status.Code(err) != codes.Unknown {
			return nil, err
		}
		return nil, translateError(err)
	}

	if err := s.persistence.putSignerSession(record); err != nil {
		_ = s.signerBackend.CloseSession(sessionID)
		return nil, status.Errorf(codes.Internal,
			"failed to persist signer session: %v", err)
	}

	s.mu.Lock()
	s.signerSessions[sessionID] = cloneSignerSessionRecord(record)
	s.mu.Unlock()

	return &pb.OpenSignerSessionResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
		},
		Session: toPBSignerSession(record),
	}, nil
}

func (s *agentWalletServer) GetSignerSession(_ context.Context,
	req *pb.GetSignerSessionRequest) (*pb.GetSignerSessionResponse, error) {

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}
	if req.SignerSessionId == "" {
		return nil, status.Error(codes.InvalidArgument,
			"signer_session_id must not be empty")
	}

	session, err := s.loadSignerSession(req.SignerSessionId)
	if err != nil {
		return nil, err
	}
	if err := authorizeSignerSessionAccess(session, req.GetMeta()); err != nil {
		return nil, err
	}

	return &pb.GetSignerSessionResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
		},
		Session: toPBSignerSession(session),
	}, nil
}

func (s *agentWalletServer) CloseSignerSession(_ context.Context,
	req *pb.CloseSignerSessionRequest) (*pb.CloseSignerSessionResponse, error) {

	if err := s.ensurePersistenceLoaded(); err != nil {
		return nil, err
	}
	if req.SignerSessionId == "" {
		return nil, status.Error(codes.InvalidArgument,
			"signer_session_id must not be empty")
	}

	session, err := s.loadSignerSession(req.SignerSessionId)
	if err != nil {
		return nil, err
	}
	if err := authorizeSignerSessionAccess(session, req.GetMeta()); err != nil {
		return nil, err
	}

	reason := req.Reason
	if reason == "" {
		reason = signerSessionCloseReasonClientRequested
	}

	warnings := make([]string, 0, 1)
	if session.Closed {
		warnings = append(warnings,
			"signer session already closed; returning recorded state")
	} else {
		session, err = s.closeSignerSessionInternal(
			req.SignerSessionId, reason, time.Now().Unix(), true,
		)
		if err != nil {
			return nil, err
		}
	}

	return &pb.CloseSignerSessionResponse{
		Meta: &pb.ResponseMeta{
			RequestId: requestID(req.GetMeta()),
			Warnings:  warnings,
		},
		Session: toPBSignerSession(session),
	}, nil
}

func (s *agentWalletServer) requireSignerSessionForOperation(op *pb.Operation,
	meta *pb.RequestMeta, signerSessionID string,
	requiredPermissions ...string) (*agentSignerSessionRecord,
	*agentCapabilityRecord, error) {

	capability, err := s.requireCapabilityForOperation(
		op, meta, requiredPermissions...,
	)
	if err != nil {
		return nil, nil, err
	}

	session, err := s.requireSignerSessionByID(
		capability, signerSessionID, meta, requiredPermissions...,
	)
	if err != nil {
		return nil, nil, err
	}

	return session, capability, nil
}

func (s *agentWalletServer) requirePublishAuthorization(op *pb.Operation,
	meta *pb.RequestMeta, signerSessionID string,
	requiredPermissions ...string) (*agentCapabilityRecord, error) {

	capability, err := s.requireCapabilityForOperation(
		op, meta, requiredPermissions...,
	)
	if err != nil {
		return nil, err
	}

	if signerSessionID == "" {
		return capability, nil
	}

	if _, err := s.requireSignerSessionByID(
		capability, signerSessionID, meta, requiredPermissions...,
	); err != nil {
		return nil, err
	}

	return capability, nil
}

func (s *agentWalletServer) requireCapabilityForOperation(op *pb.Operation,
	meta *pb.RequestMeta, requiredPermissions ...string) (
	*agentCapabilityRecord, error) {

	if op == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"operation is required for capability checks")
	}

	capability, err := s.requireCapabilityByID(
		normalizeWalletID(op.GetWalletId()), meta.GetCapabilityId(), meta,
		requiredPermissions...,
	)
	if err != nil {
		return nil, err
	}

	if op.GetCreatorPrincipal() != "" &&
		capability.Principal != "" &&
		op.GetCreatorPrincipal() != capability.Principal {

		return nil, status.Errorf(codes.PermissionDenied,
			"capability principal %s may not execute operation owned by %s",
			capability.Principal, op.GetCreatorPrincipal())
	}
	if op.GetCreatorCapabilityId() != "" &&
		op.GetCreatorCapabilityId() != capability.CapabilityID {

		return nil, status.Errorf(codes.PermissionDenied,
			"capability %s may not execute operation created by %s",
			capability.CapabilityID, op.GetCreatorCapabilityId())
	}

	return capability, nil
}

func (s *agentWalletServer) requireCapabilityByID(walletID, capabilityID string,
	meta *pb.RequestMeta, requiredPermissions ...string) (
	*agentCapabilityRecord, error) {

	if capabilityID == "" {
		return nil, status.Error(codes.PermissionDenied,
			"capability_id must be provided")
	}

	s.mu.Lock()
	record, ok := s.capabilities[capabilityID]
	s.mu.Unlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound,
			"capability %s not found", capabilityID)
	}

	capability := cloneCapabilityRecord(record)
	now := time.Now().Unix()
	if capability.Revoked {
		return nil, status.Errorf(codes.PermissionDenied,
			"capability %s has been revoked", capabilityID)
	}
	if capability.ExpiresAtUnix > 0 && now >= capability.ExpiresAtUnix {
		return nil, status.Errorf(codes.PermissionDenied,
			"capability %s has expired", capabilityID)
	}
	if walletID != "" && capability.WalletID != walletID {
		return nil, status.Errorf(codes.PermissionDenied,
			"capability %s is not valid for wallet %s",
			capabilityID, walletID)
	}
	if meta != nil && meta.GetPrincipal() != "" &&
		capability.Principal != "" &&
		meta.GetPrincipal() != capability.Principal {

		return nil, status.Errorf(codes.PermissionDenied,
			"principal %s may not use capability %s",
			meta.GetPrincipal(), capabilityID)
	}
	if !capabilityAllowsAny(capability.Permissions, requiredPermissions...) {
		return nil, status.Errorf(codes.PermissionDenied,
			"capability %s does not allow %s",
			capabilityID, strings.Join(requiredPermissions, ","))
	}

	return capability, nil
}

func (s *agentWalletServer) requireSignerSessionByID(
	capability *agentCapabilityRecord, signerSessionID string,
	meta *pb.RequestMeta, requiredPermissions ...string) (
	*agentSignerSessionRecord, error) {

	if signerSessionID == "" {
		return nil, status.Error(codes.FailedPrecondition,
			"signer_session_id must be provided")
	}
	if capability == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"capability is required for signer session checks")
	}

	session, err := s.loadSignerSession(signerSessionID)
	if err != nil {
		return nil, err
	}
	if session.CapabilityID != capability.CapabilityID {
		return nil, status.Errorf(codes.PermissionDenied,
			"signer session %s is not bound to capability %s",
			signerSessionID, capability.CapabilityID)
	}
	if session.WalletID != capability.WalletID {
		return nil, status.Errorf(codes.PermissionDenied,
			"signer session %s is not valid for wallet %s",
			signerSessionID, capability.WalletID)
	}
	if session.Principal != "" &&
		capability.Principal != "" &&
		session.Principal != capability.Principal {

		return nil, status.Errorf(codes.PermissionDenied,
			"signer session %s principal mismatch", signerSessionID)
	}
	if meta != nil && meta.GetPrincipal() != "" &&
		session.Principal != "" &&
		meta.GetPrincipal() != session.Principal {

		return nil, status.Errorf(codes.PermissionDenied,
			"principal %s may not use signer session %s",
			meta.GetPrincipal(), signerSessionID)
	}
	if session.Closed {
		return nil, status.Errorf(codes.FailedPrecondition,
			"signer session %s is already closed", signerSessionID)
	}
	now := time.Now().Unix()
	if session.ExpiresAtUnix > 0 && now >= session.ExpiresAtUnix {
		_, _ = s.closeSignerSessionInternal(
			signerSessionID, signerSessionCloseReasonExpired, now, true,
		)
		return nil, status.Errorf(codes.FailedPrecondition,
			"signer session %s has expired", signerSessionID)
	}
	if !capabilityAllowsAny(session.Permissions, requiredPermissions...) {
		return nil, status.Errorf(codes.PermissionDenied,
			"signer session %s does not allow %s",
			signerSessionID, strings.Join(requiredPermissions, ","))
	}
	if err := s.signerBackend.ValidateSession(signerSessionID); err != nil {
		return nil, err
	}

	return session, nil
}

func (s *agentWalletServer) loadSignerSession(
	signerSessionID string) (*agentSignerSessionRecord, error) {

	s.mu.Lock()
	record, ok := s.signerSessions[signerSessionID]
	s.mu.Unlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound,
			"signer session %s not found", signerSessionID)
	}

	return cloneSignerSessionRecord(record), nil
}

func (s *agentWalletServer) reconcileRecoveredSignerSessions() error {
	s.mu.Lock()
	sessionIDs := make([]string, 0, len(s.signerSessions))
	for sessionID, record := range s.signerSessions {
		if record == nil || record.Closed {
			continue
		}
		sessionIDs = append(sessionIDs, sessionID)
	}
	s.mu.Unlock()

	for _, sessionID := range sessionIDs {
		if _, err := s.closeSignerSessionInternal(
			sessionID, signerSessionCloseReasonServiceRestart,
			time.Now().Unix(), false,
		); err != nil {
			return fmt.Errorf("reconcile signer session %s: %w",
				sessionID, err)
		}
	}

	return nil
}

func (s *agentWalletServer) closeSignerSessionsForCapability(
	capabilityID, reason string) []string {

	s.mu.Lock()
	sessionIDs := make([]string, 0, len(s.signerSessions))
	for sessionID, record := range s.signerSessions {
		if record == nil || record.Closed {
			continue
		}
		if record.CapabilityID == capabilityID {
			sessionIDs = append(sessionIDs, sessionID)
		}
	}
	s.mu.Unlock()

	warnings := make([]string, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if _, err := s.closeSignerSessionInternal(
			sessionID, reason, time.Now().Unix(), true,
		); err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"capability revoked but signer session %s close failed: %v",
				sessionID, err,
			))
		}
	}

	return warnings
}

func (s *agentWalletServer) closeSignerSessionInternal(sessionID, reason string,
	closedAtUnix int64, signalLock bool) (*agentSignerSessionRecord, error) {

	s.mu.Lock()
	record, ok := s.signerSessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound,
			"signer session %s not found", sessionID)
	}

	if record.Closed {
		return cloneSignerSessionRecord(record), nil
	}

	if reason == "" {
		reason = signerSessionCloseReasonClientRequested
	}

	updated := cloneSignerSessionRecord(record)
	updated.Closed = true
	updated.ClosedAtUnix = closedAtUnix
	updated.CloseReason = reason
	updated.UpdatedAtUnix = closedAtUnix

	if signalLock {
		if err := s.signerBackend.CloseSession(sessionID); err != nil {
			return cloneSignerSessionRecord(updated), err
		}
	}

	persistErr := s.persistence.putSignerSession(updated)

	s.mu.Lock()
	s.signerSessions[sessionID] = cloneSignerSessionRecord(updated)
	s.mu.Unlock()

	if persistErr != nil {
		return cloneSignerSessionRecord(updated), status.Errorf(
			codes.Internal, "failed to persist signer session close: %v",
			persistErr,
		)
	}

	return cloneSignerSessionRecord(updated), nil
}

func (s *agentWalletServer) newCapabilityIDLocked() string {
	s.nextCapabilityID++
	return fmt.Sprintf("cap_%d_%d", time.Now().UnixNano(), s.nextCapabilityID)
}

func (s *agentWalletServer) newSignerSessionIDLocked() string {
	s.nextSignerSessionID++
	return fmt.Sprintf("sess_%d_%d", time.Now().UnixNano(),
		s.nextSignerSessionID)
}

func capabilityAllowsAny(permissions []string, required ...string) bool {
	if len(required) == 0 {
		return true
	}

	for _, have := range permissions {
		if have == "" {
			continue
		}
		if have == capabilityPermissionAll {
			return true
		}
		for _, need := range required {
			if need == "" {
				continue
			}
			if have == need {
				return true
			}
			if strings.HasSuffix(have, ".*") &&
				strings.HasPrefix(need, strings.TrimSuffix(have, "*")) {

				return true
			}
		}
	}

	return false
}

func normalizePermissions(permissions []string) []string {
	if len(permissions) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(permissions))
	seen := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		permission = strings.TrimSpace(permission)
		if permission == "" {
			continue
		}
		if _, ok := seen[permission]; ok {
			continue
		}
		seen[permission] = struct{}{}
		normalized = append(normalized, permission)
	}

	return normalized
}

func signalWalletLock(lock chan time.Time) {
	if lock == nil {
		return
	}

	select {
	case lock <- time.Now():
	default:
	}
}

func authorizeSignerSessionAccess(session *agentSignerSessionRecord,
	meta *pb.RequestMeta) error {

	if session == nil {
		return status.Error(codes.NotFound, "signer session not found")
	}
	if meta == nil {
		return nil
	}
	if meta.GetCapabilityId() != "" &&
		meta.GetCapabilityId() != session.CapabilityID {

		return status.Errorf(codes.PermissionDenied,
			"capability %s may not access signer session %s",
			meta.GetCapabilityId(), session.SignerSessionID)
	}
	if meta.GetPrincipal() != "" &&
		session.Principal != "" &&
		meta.GetPrincipal() != session.Principal {

		return status.Errorf(codes.PermissionDenied,
			"principal %s may not access signer session %s",
			meta.GetPrincipal(), session.SignerSessionID)
	}

	return nil
}

func toPBCapability(record *agentCapabilityRecord) *pb.Capability {
	if record == nil {
		return nil
	}

	return &pb.Capability{
		CapabilityId:     record.CapabilityID,
		WalletId:         record.WalletID,
		Principal:        record.Principal,
		Permissions:      append([]string(nil), record.Permissions...),
		CreatedAtUnix:    record.CreatedAtUnix,
		ExpiresAtUnix:    record.ExpiresAtUnix,
		Revoked:          record.Revoked,
		RevokedAtUnix:    record.RevokedAtUnix,
		RevocationReason: record.RevocationReason,
		IssuedBy:         record.IssuedBy,
	}
}

func toPBSignerSession(record *agentSignerSessionRecord) *pb.SignerSession {
	if record == nil {
		return nil
	}

	return &pb.SignerSession{
		SignerSessionId:    record.SignerSessionID,
		WalletId:           record.WalletID,
		CapabilityId:       record.CapabilityID,
		Principal:          record.Principal,
		Permissions:        append([]string(nil), record.Permissions...),
		CreatedAtUnix:      record.CreatedAtUnix,
		ExpiresAtUnix:      record.ExpiresAtUnix,
		Closed:             record.Closed,
		ClosedAtUnix:       record.ClosedAtUnix,
		CloseReason:        record.CloseReason,
		Reason:             record.Reason,
		OpenRequestId:      record.OpenRequestID,
		OpenIdempotencyKey: record.OpenIdempotencyKey,
	}
}
