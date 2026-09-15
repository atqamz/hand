package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/filelock"
)

const PayloadReferenceSchema = "hand.integration.reference.v1"

var (
	ErrPayloadReferenceHeld    = errors.New("integration payload reference is held by another process")
	ErrPayloadReferenceUnknown = errors.New("integration payload reference metadata is unknown")
)

type PayloadReferenceRequest struct {
	ReferenceID string
	// LockScope bounds permanent lock rendezvous independently of the unique
	// holder record. Empty preserves the one-holder/one-lock default.
	LockScope string
	FleetID   string
	Consumer  string
	Evidence  string
}

type payloadReferenceRecord struct {
	Schema      string    `json:"schema"`
	Capability  string    `json:"capability"`
	Payload     string    `json:"payload"`
	ReferenceID string    `json:"reference_id"`
	LockScope   string    `json:"lock_scope,omitempty"`
	FleetID     string    `json:"fleet_id,omitempty"`
	Consumer    string    `json:"consumer"`
	Evidence    string    `json:"evidence"`
	CreatedAt   time.Time `json:"created_at"`
}

type PayloadReference struct {
	record      payloadReferenceRecord
	storeRoot   string
	rootHandle  *os.Root
	recordPath  string
	lockPath    string
	lock        *os.File
	executable  *os.File
	executePath string
	closed      bool
}

func (s *Store) AcquireReference(id, path string, request PayloadReferenceRequest) (*PayloadReference, error) {
	if _, ok := find(id); !ok {
		return nil, fmt.Errorf("unsupported optional capability %q", id)
	}
	if err := request.validate(); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve optional capability %q payload: %w", id, err)
	}
	payload, err := s.verifyPayloadPath(id, path)
	if err != nil {
		return nil, err
	}
	referenceRoot := filepath.Join(s.Root, "integrations", filepath.FromSlash(id), "references", payload)
	if err := ensureIntegrationDirectory(s.Root, referenceRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create integration payload reference store: %w", err)
	}
	rootHandle, err := openDirectIntegrationRoot(s.Root)
	if err != nil {
		return nil, fmt.Errorf("open integration payload reference store: %w", err)
	}
	executable, err := openVerifiedPayload(rootHandle, s.Root, id, path, payload)
	if err != nil {
		_ = rootHandle.Close()
		return nil, err
	}
	relativePath, err := integrationRelativePath(s.Root, path)
	if err != nil {
		_ = executable.Close()
		_ = rootHandle.Close()
		return nil, err
	}
	retainRoot := false
	defer func() {
		if !retainRoot {
			_ = executable.Close()
			_ = rootHandle.Close()
		}
	}()
	lockScope := request.LockScope
	if lockScope == "" {
		lockScope = request.ReferenceID
	}
	recordIdentity := request.ReferenceID
	if request.LockScope != "" {
		recordIdentity = request.LockScope + "\x00" + request.ReferenceID
	}
	recordKey := sha256.Sum256([]byte(recordIdentity))
	lockKey := sha256.Sum256([]byte(lockScope))
	recordPath := filepath.Join(referenceRoot, hex.EncodeToString(recordKey[:])+".json")
	lockPath := filepath.Join(referenceRoot, hex.EncodeToString(lockKey[:])+".lock")
	lock, _, err := openIntegrationFile(rootHandle, s.Root, lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open integration payload reference lock: %w", err)
	}
	if err := filelock.Lock(lock, false); err != nil {
		_ = lock.Close()
		if errors.Is(err, filelock.ErrBusy) {
			return nil, fmt.Errorf("%w: capability=%s payload=%s reference=%s fleet=%s", ErrPayloadReferenceHeld, id, payload, request.ReferenceID, request.FleetID)
		}
		return nil, fmt.Errorf("lock integration payload reference: %w", err)
	}

	record := payloadReferenceRecord{
		Schema: PayloadReferenceSchema, Capability: id, Payload: payload,
		ReferenceID: request.ReferenceID, LockScope: request.LockScope, FleetID: request.FleetID,
		Consumer: request.Consumer, Evidence: request.Evidence, CreatedAt: time.Now().UTC(),
	}
	existing, err := readPayloadReferenceRecord(rootHandle, s.Root, recordPath)
	switch {
	case err == nil:
		if err := existing.validate(); err != nil || !existing.sameIdentity(record) {
			_ = filelock.Unlock(lock)
			_ = lock.Close()
			return nil, fmt.Errorf("%w: capability=%s payload=%s reference=%s record=%s", ErrPayloadReferenceUnknown, id, payload, request.ReferenceID, recordPath)
		}
		record = existing
	case errors.Is(err, os.ErrNotExist):
		data, encodeErr := json.MarshalIndent(record, "", "  ")
		if encodeErr != nil {
			_ = filelock.Unlock(lock)
			_ = lock.Close()
			return nil, encodeErr
		}
		if writeErr := atomicWriteIntegrationFile(rootHandle, s.Root, recordPath, ".integration-reference-", append(data, '\n'), 0o600); writeErr != nil {
			_ = filelock.Unlock(lock)
			_ = lock.Close()
			return nil, fmt.Errorf("publish integration payload reference: %w", writeErr)
		}
	default:
		_ = filelock.Unlock(lock)
		_ = lock.Close()
		return nil, fmt.Errorf("%w: capability=%s payload=%s reference=%s record=%s: %v", ErrPayloadReferenceUnknown, id, payload, request.ReferenceID, recordPath, err)
	}
	retainRoot = true
	return &PayloadReference{
		record: record, storeRoot: s.Root, rootHandle: rootHandle,
		recordPath: recordPath, lockPath: lockPath, lock: lock,
		executable: executable, executePath: filepath.Join(rootHandle.Name(), relativePath),
	}, nil
}

func (s *Store) verifyPayloadPath(id, path string) (string, error) {
	capability, _ := find(id)
	root, err := filepath.Abs(filepath.Join(s.Root, "integrations", filepath.FromSlash(id)))
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve optional capability %q payload: %w", id, err)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("optional capability %q payload reference escapes its private store", id)
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 3 || parts[0] != "payloads" || parts[2] != capability.Executable || len(parts[1]) != sha256.Size*2 {
		return "", fmt.Errorf("optional capability %q reference has no exact payload identity", id)
	}
	_, err = hex.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("optional capability %q reference has invalid payload identity", id)
	}
	rootHandle, err := openDirectIntegrationRoot(s.Root)
	if err != nil {
		return "", fmt.Errorf("open optional capability %q referenced payload store: %w", id, err)
	}
	defer func() { _ = rootHandle.Close() }()
	input, err := openVerifiedPayload(rootHandle, s.Root, id, path, parts[1])
	if err != nil {
		return "", err
	}
	defer func() { _ = input.Close() }()
	return parts[1], nil
}

func openVerifiedPayload(rootHandle *os.Root, root, id, path, payload string) (*os.File, error) {
	input, info, err := openIntegrationFile(rootHandle, root, path, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("inspect optional capability %q referenced payload: %w", id, err)
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		_ = input.Close()
		return nil, fmt.Errorf("optional capability %q referenced payload is not an executable regular file", id)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		_ = input.Close()
		return nil, fmt.Errorf("hash optional capability %q referenced payload: %w", id, err)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != payload {
		_ = input.Close()
		return nil, fmt.Errorf("optional capability %q referenced payload digest mismatch", id)
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		_ = input.Close()
		return nil, fmt.Errorf("rewind optional capability %q referenced payload: %w", id, err)
	}
	return input, nil
}

func (request PayloadReferenceRequest) validate() error {
	for name, value := range map[string]string{
		"reference identity": request.ReferenceID,
		"consumer":           request.Consumer,
		"creation evidence":  request.Evidence,
	} {
		if value == "" || len(value) > 512 || strings.IndexFunc(value, func(char rune) bool { return char < 0x20 || char == 0x7f }) >= 0 {
			return fmt.Errorf("integration payload reference has invalid %s", name)
		}
	}
	if request.LockScope != "" {
		if len(request.LockScope) > 512 || strings.IndexFunc(request.LockScope, func(char rune) bool { return char < 0x20 || char == 0x7f }) >= 0 {
			return errors.New("integration payload reference has invalid lock scope")
		}
	}
	if request.FleetID == "" {
		return nil
	}
	if len(request.FleetID) != 34 || !strings.HasPrefix(request.FleetID, "f_") {
		return fmt.Errorf("integration payload reference has invalid Fleet identity %q", request.FleetID)
	}
	for _, char := range request.FleetID[2:] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return fmt.Errorf("integration payload reference has invalid Fleet identity %q", request.FleetID)
		}
	}
	return nil
}

func (record payloadReferenceRecord) validate() error {
	if record.Schema != PayloadReferenceSchema || record.Capability == "" || len(record.Payload) != sha256.Size*2 || record.CreatedAt.IsZero() {
		return ErrPayloadReferenceUnknown
	}
	if _, err := hex.DecodeString(record.Payload); err != nil {
		return ErrPayloadReferenceUnknown
	}
	return (PayloadReferenceRequest{
		ReferenceID: record.ReferenceID, LockScope: record.LockScope, FleetID: record.FleetID,
		Consumer: record.Consumer, Evidence: record.Evidence,
	}).validate()
}

func (record payloadReferenceRecord) sameIdentity(other payloadReferenceRecord) bool {
	return record.Schema == other.Schema && record.Capability == other.Capability && record.Payload == other.Payload &&
		record.ReferenceID == other.ReferenceID && record.LockScope == other.LockScope && record.FleetID == other.FleetID && record.Consumer == other.Consumer && record.Evidence == other.Evidence
}

func readPayloadReferenceRecord(rootHandle *os.Root, root, path string) (payloadReferenceRecord, error) {
	data, err := readIntegrationFile(rootHandle, root, path)
	if err != nil {
		return payloadReferenceRecord{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record payloadReferenceRecord
	if err := decoder.Decode(&record); err != nil {
		return payloadReferenceRecord{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return payloadReferenceRecord{}, errors.New("integration payload reference record has trailing data")
	}
	return record, nil
}

func (reference *PayloadReference) RecordPath() string {
	if reference == nil {
		return ""
	}
	return reference.recordPath
}

func (reference *PayloadReference) LockPath() string {
	if reference == nil {
		return ""
	}
	return reference.lockPath
}

// StartChild starts a managed consumer while preserving this reference if its
// parent dies before the consumer exits.
func (reference *PayloadReference) StartChild(cmd *exec.Cmd) error {
	if reference == nil || reference.closed || reference.lock == nil || reference.executable == nil {
		return errors.New("integration payload reference is not live")
	}
	if cmd == nil {
		return errors.New("integration payload reference child command is nil")
	}
	return startChildWithPayloadReference(cmd, reference.lock, reference.executable, reference.executePath)
}

func (reference *PayloadReference) Close() error {
	if reference == nil || reference.closed {
		return nil
	}
	reference.closed = true
	defer func() {
		_ = filelock.Unlock(reference.lock)
		_ = reference.lock.Close()
		_ = reference.executable.Close()
		_ = reference.rootHandle.Close()
	}()
	existing, err := readPayloadReferenceRecord(reference.rootHandle, reference.storeRoot, reference.recordPath)
	if err != nil || !existing.sameIdentity(reference.record) {
		return fmt.Errorf("%w: refusing to retire capability=%s payload=%s reference=%s record=%s", ErrPayloadReferenceUnknown, reference.record.Capability, reference.record.Payload, reference.record.ReferenceID, reference.recordPath)
	}
	if err := removeIntegrationFile(reference.rootHandle, reference.storeRoot, reference.recordPath); err != nil {
		return fmt.Errorf("retire integration payload reference: %w", err)
	}
	return nil
}
