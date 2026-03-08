package rpcserver

import (
	"encoding/json"
	"fmt"

	pb "github.com/btcsuite/btcwallet/rpc/agentwalletrpc"
	"github.com/btcsuite/btcwallet/walletdb"
	"google.golang.org/protobuf/proto"
)

var (
	agentWalletNamespaceKey            = []byte("agentwallet")
	agentWalletOperationsBucketKey     = []byte("operations")
	agentWalletArtifactsBucketKey      = []byte("artifacts")
	agentWalletReservationsBucketKey   = []byte("reservations")
	agentWalletCapabilitiesBucketKey   = []byte("capabilities")
	agentWalletSignerSessionsBucketKey = []byte("signer_sessions")
)

type agentReservationRecord struct {
	ReservationID  string   `json:"reservation_id"`
	WalletID       string   `json:"wallet_id,omitempty"`
	AccountNumber  uint32   `json:"account_number,omitempty"`
	Outpoints      []string `json:"outpoints,omitempty"`
	ExpiresAtUnix  int64    `json:"expires_at_unix,omitempty"`
	CreatedAtUnix  int64    `json:"created_at_unix,omitempty"`
	UpdatedAtUnix  int64    `json:"updated_at_unix,omitempty"`
	Released       bool     `json:"released,omitempty"`
	ReleasedAtUnix int64    `json:"released_at_unix,omitempty"`
}

type agentOperationArtifacts struct {
	OperationID       string `json:"operation_id"`
	UnsignedPsbt      []byte `json:"unsigned_psbt,omitempty"`
	SignedPsbt        []byte `json:"signed_psbt,omitempty"`
	SignedTransaction []byte `json:"signed_transaction,omitempty"`
}

type agentCapabilityRecord struct {
	CapabilityID     string   `json:"capability_id"`
	WalletID         string   `json:"wallet_id,omitempty"`
	Principal        string   `json:"principal,omitempty"`
	Permissions      []string `json:"permissions,omitempty"`
	CreatedAtUnix    int64    `json:"created_at_unix,omitempty"`
	ExpiresAtUnix    int64    `json:"expires_at_unix,omitempty"`
	Revoked          bool     `json:"revoked,omitempty"`
	RevokedAtUnix    int64    `json:"revoked_at_unix,omitempty"`
	RevocationReason string   `json:"revocation_reason,omitempty"`
	IssuedBy         string   `json:"issued_by,omitempty"`
	UpdatedAtUnix    int64    `json:"updated_at_unix,omitempty"`
}

type agentSignerSessionRecord struct {
	SignerSessionID    string   `json:"signer_session_id"`
	WalletID           string   `json:"wallet_id,omitempty"`
	CapabilityID       string   `json:"capability_id,omitempty"`
	Principal          string   `json:"principal,omitempty"`
	Permissions        []string `json:"permissions,omitempty"`
	CreatedAtUnix      int64    `json:"created_at_unix,omitempty"`
	ExpiresAtUnix      int64    `json:"expires_at_unix,omitempty"`
	Closed             bool     `json:"closed,omitempty"`
	ClosedAtUnix       int64    `json:"closed_at_unix,omitempty"`
	CloseReason        string   `json:"close_reason,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	OpenRequestID      string   `json:"open_request_id,omitempty"`
	OpenIdempotencyKey string   `json:"open_idempotency_key,omitempty"`
	UpdatedAtUnix      int64    `json:"updated_at_unix,omitempty"`
}

type agentWalletPersistentStore struct {
	db walletdb.DB
}

func newAgentWalletPersistentStore(db walletdb.DB) *agentWalletPersistentStore {
	return &agentWalletPersistentStore{db: db}
}

func (s *agentWalletPersistentStore) load() (map[string]*pb.Operation,
	map[string]*agentReservationRecord,
	map[string]*agentOperationArtifacts,
	map[string]*agentCapabilityRecord,
	map[string]*agentSignerSessionRecord, error) {

	operations := make(map[string]*pb.Operation)
	reservations := make(map[string]*agentReservationRecord)
	artifacts := make(map[string]*agentOperationArtifacts)
	capabilities := make(map[string]*agentCapabilityRecord)
	signerSessions := make(map[string]*agentSignerSessionRecord)

	err := walletdb.Update(s.db, func(tx walletdb.ReadWriteTx) error {
		root, err := ensureAgentWalletRootBucket(tx)
		if err != nil {
			return err
		}

		operationsBucket, err := root.CreateBucketIfNotExists(
			agentWalletOperationsBucketKey,
		)
		if err != nil {
			return err
		}
		artifactsBucket, err := root.CreateBucketIfNotExists(
			agentWalletArtifactsBucketKey,
		)
		if err != nil {
			return err
		}
		reservationsBucket, err := root.CreateBucketIfNotExists(
			agentWalletReservationsBucketKey,
		)
		if err != nil {
			return err
		}
		capabilitiesBucket, err := root.CreateBucketIfNotExists(
			agentWalletCapabilitiesBucketKey,
		)
		if err != nil {
			return err
		}
		signerSessionsBucket, err := root.CreateBucketIfNotExists(
			agentWalletSignerSessionsBucketKey,
		)
		if err != nil {
			return err
		}

		err = operationsBucket.ForEach(func(k, v []byte) error {
			if v == nil {
				return nil
			}

			var op pb.Operation
			if err := proto.Unmarshal(v, &op); err != nil {
				return fmt.Errorf("decode operation %q: %w", k, err)
			}

			operations[string(k)] = &op
			return nil
		})
		if err != nil {
			return err
		}

		err = artifactsBucket.ForEach(func(k, v []byte) error {
			if v == nil {
				return nil
			}

			var record agentOperationArtifacts
			if err := json.Unmarshal(v, &record); err != nil {
				return fmt.Errorf("decode artifacts %q: %w", k, err)
			}
			if record.OperationID == "" {
				record.OperationID = string(k)
			}
			if record.OperationID != string(k) {
				return fmt.Errorf("artifact key mismatch for %q", k)
			}

			artifacts[record.OperationID] = cloneOperationArtifacts(&record)
			return nil
		})
		if err != nil {
			return err
		}

		err = reservationsBucket.ForEach(func(k, v []byte) error {
			if v == nil {
				return nil
			}

			var record agentReservationRecord
			if err := json.Unmarshal(v, &record); err != nil {
				return fmt.Errorf("decode reservation %q: %w", k, err)
			}
			if record.ReservationID == "" {
				record.ReservationID = string(k)
			}
			if record.ReservationID != string(k) {
				return fmt.Errorf("reservation key mismatch for %q", k)
			}

			reservations[record.ReservationID] = cloneReservationRecord(&record)
			return nil
		})
		if err != nil {
			return err
		}

		err = capabilitiesBucket.ForEach(func(k, v []byte) error {
			if v == nil {
				return nil
			}

			var record agentCapabilityRecord
			if err := json.Unmarshal(v, &record); err != nil {
				return fmt.Errorf("decode capability %q: %w", k, err)
			}
			if record.CapabilityID == "" {
				record.CapabilityID = string(k)
			}
			if record.CapabilityID != string(k) {
				return fmt.Errorf("capability key mismatch for %q", k)
			}

			capabilities[record.CapabilityID] = cloneCapabilityRecord(&record)
			return nil
		})
		if err != nil {
			return err
		}

		return signerSessionsBucket.ForEach(func(k, v []byte) error {
			if v == nil {
				return nil
			}

			var record agentSignerSessionRecord
			if err := json.Unmarshal(v, &record); err != nil {
				return fmt.Errorf("decode signer session %q: %w", k, err)
			}
			if record.SignerSessionID == "" {
				record.SignerSessionID = string(k)
			}
			if record.SignerSessionID != string(k) {
				return fmt.Errorf("signer session key mismatch for %q", k)
			}

			signerSessions[record.SignerSessionID] =
				cloneSignerSessionRecord(&record)
			return nil
		})
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	return operations, reservations, artifacts, capabilities, signerSessions, nil
}

func (s *agentWalletPersistentStore) putOperation(op *pb.Operation) error {
	if op == nil || op.OperationId == "" {
		return fmt.Errorf("operation_id must not be empty")
	}

	raw, err := proto.Marshal(op)
	if err != nil {
		return err
	}

	return walletdb.Update(s.db, func(tx walletdb.ReadWriteTx) error {
		root, err := ensureAgentWalletRootBucket(tx)
		if err != nil {
			return err
		}

		operationsBucket, err := root.CreateBucketIfNotExists(
			agentWalletOperationsBucketKey,
		)
		if err != nil {
			return err
		}

		return operationsBucket.Put([]byte(op.OperationId), raw)
	})
}

func (s *agentWalletPersistentStore) putOperationArtifacts(
	artifacts *agentOperationArtifacts) error {

	if artifacts == nil || artifacts.OperationID == "" {
		return fmt.Errorf("operation_id must not be empty")
	}

	raw, err := json.Marshal(artifacts)
	if err != nil {
		return err
	}

	return walletdb.Update(s.db, func(tx walletdb.ReadWriteTx) error {
		root, err := ensureAgentWalletRootBucket(tx)
		if err != nil {
			return err
		}

		artifactsBucket, err := root.CreateBucketIfNotExists(
			agentWalletArtifactsBucketKey,
		)
		if err != nil {
			return err
		}

		return artifactsBucket.Put([]byte(artifacts.OperationID), raw)
	})
}

func (s *agentWalletPersistentStore) putOperationBundle(op *pb.Operation,
	artifacts *agentOperationArtifacts) error {

	if op == nil || op.OperationId == "" {
		return fmt.Errorf("operation_id must not be empty")
	}

	opRaw, err := proto.Marshal(op)
	if err != nil {
		return err
	}

	var artifactsRaw []byte
	if artifacts != nil {
		if artifacts.OperationID == "" {
			artifacts.OperationID = op.OperationId
		}
		if artifacts.OperationID != op.OperationId {
			return fmt.Errorf("artifact operation_id mismatch")
		}

		artifactsRaw, err = json.Marshal(artifacts)
		if err != nil {
			return err
		}
	}

	return walletdb.Update(s.db, func(tx walletdb.ReadWriteTx) error {
		root, err := ensureAgentWalletRootBucket(tx)
		if err != nil {
			return err
		}

		operationsBucket, err := root.CreateBucketIfNotExists(
			agentWalletOperationsBucketKey,
		)
		if err != nil {
			return err
		}
		if err := operationsBucket.Put([]byte(op.OperationId), opRaw); err != nil {
			return err
		}

		if artifacts == nil {
			return nil
		}

		artifactsBucket, err := root.CreateBucketIfNotExists(
			agentWalletArtifactsBucketKey,
		)
		if err != nil {
			return err
		}

		return artifactsBucket.Put([]byte(op.OperationId), artifactsRaw)
	})
}

func (s *agentWalletPersistentStore) putReservation(
	record *agentReservationRecord) error {

	if record == nil || record.ReservationID == "" {
		return fmt.Errorf("reservation_id must not be empty")
	}

	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}

	return walletdb.Update(s.db, func(tx walletdb.ReadWriteTx) error {
		root, err := ensureAgentWalletRootBucket(tx)
		if err != nil {
			return err
		}

		reservationsBucket, err := root.CreateBucketIfNotExists(
			agentWalletReservationsBucketKey,
		)
		if err != nil {
			return err
		}

		return reservationsBucket.Put([]byte(record.ReservationID), raw)
	})
}

func (s *agentWalletPersistentStore) putCapability(
	record *agentCapabilityRecord) error {

	if record == nil || record.CapabilityID == "" {
		return fmt.Errorf("capability_id must not be empty")
	}

	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}

	return walletdb.Update(s.db, func(tx walletdb.ReadWriteTx) error {
		root, err := ensureAgentWalletRootBucket(tx)
		if err != nil {
			return err
		}

		capabilitiesBucket, err := root.CreateBucketIfNotExists(
			agentWalletCapabilitiesBucketKey,
		)
		if err != nil {
			return err
		}

		return capabilitiesBucket.Put([]byte(record.CapabilityID), raw)
	})
}

func (s *agentWalletPersistentStore) putSignerSession(
	record *agentSignerSessionRecord) error {

	if record == nil || record.SignerSessionID == "" {
		return fmt.Errorf("signer_session_id must not be empty")
	}

	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}

	return walletdb.Update(s.db, func(tx walletdb.ReadWriteTx) error {
		root, err := ensureAgentWalletRootBucket(tx)
		if err != nil {
			return err
		}

		signerSessionsBucket, err := root.CreateBucketIfNotExists(
			agentWalletSignerSessionsBucketKey,
		)
		if err != nil {
			return err
		}

		return signerSessionsBucket.Put([]byte(record.SignerSessionID), raw)
	})
}

func ensureAgentWalletRootBucket(
	tx walletdb.ReadWriteTx) (walletdb.ReadWriteBucket, error) {

	root := tx.ReadWriteBucket(agentWalletNamespaceKey)
	if root != nil {
		return root, nil
	}

	return tx.CreateTopLevelBucket(agentWalletNamespaceKey)
}

func cloneReservationRecord(
	record *agentReservationRecord) *agentReservationRecord {

	if record == nil {
		return nil
	}

	cp := *record
	cp.Outpoints = append([]string(nil), record.Outpoints...)
	return &cp
}

func cloneOperationArtifacts(
	record *agentOperationArtifacts) *agentOperationArtifacts {

	if record == nil {
		return nil
	}

	cp := *record
	cp.UnsignedPsbt = append([]byte(nil), record.UnsignedPsbt...)
	cp.SignedPsbt = append([]byte(nil), record.SignedPsbt...)
	cp.SignedTransaction = append([]byte(nil), record.SignedTransaction...)
	return &cp
}

func cloneCapabilityRecord(
	record *agentCapabilityRecord) *agentCapabilityRecord {

	if record == nil {
		return nil
	}

	cp := *record
	cp.Permissions = append([]string(nil), record.Permissions...)
	return &cp
}

func cloneSignerSessionRecord(
	record *agentSignerSessionRecord) *agentSignerSessionRecord {

	if record == nil {
		return nil
	}

	cp := *record
	cp.Permissions = append([]string(nil), record.Permissions...)
	return &cp
}
