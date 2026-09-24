package toolchain

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
	"strings"
	"time"

	"github.com/atqamz/hand/internal/filelock"
)

const LeaseSchema = "hand.runtime.lease.v2"

var (
	ErrLeaseHeld            = errors.New("runtime generation lease is held by another process")
	ErrLeaseMetadataUnknown = errors.New("runtime generation lease metadata is unknown")
)

type LeaseRequest struct {
	Generation string
	LeaseID    string
	// LockScope bounds permanent lock rendezvous independently of the unique
	// holder record. Empty preserves the one-holder/one-lock default.
	LockScope string
	FleetID   string
	Consumer  string
	Evidence  string
}

type leaseRecord struct {
	Schema       string    `json:"schema"`
	Generation   string    `json:"generation"`
	LeaseID      string    `json:"lease_id"`
	LockScope    string    `json:"lock_scope,omitempty"`
	LockIdentity string    `json:"lock_identity"`
	FleetID      string    `json:"fleet_id"`
	Consumer     string    `json:"consumer"`
	Evidence     string    `json:"evidence"`
	CreatedAt    time.Time `json:"created_at"`
}

type Lease struct {
	record       leaseRecord
	storeRoot    string
	rootHandle   *os.Root
	recordPath   string
	recordInfo   os.FileInfo
	lockPath     string
	lock         *os.File
	childStarted bool
	closed       bool
}

func (s *Store) GenerationID(goos, goarch string) (string, error) {
	if err := s.Lock.Validate(); err != nil {
		return "", fmt.Errorf("validate runtime generation contract: %w", err)
	}
	target, err := s.Lock.Target(goos, goarch)
	if err != nil {
		return "", err
	}
	bundle, err := generationBundleName(s.Lock.RuntimeID, currentTargetName(goos, goarch), target)
	if err != nil {
		return "", err
	}
	return filepath.Base(bundle), nil
}

// Generation resolves and verifies one exact deterministic generation without
// consulting the mutable selection pointer.
func (s *Store) Generation(generation, goos, goarch string) (Runtime, error) {
	expected, err := s.GenerationID(goos, goarch)
	if err != nil {
		return Runtime{}, err
	}
	if generation != expected {
		return Runtime{}, fmt.Errorf("runtime generation %q does not match exact generation %q", generation, expected)
	}
	target, err := s.Lock.Target(goos, goarch)
	if err != nil {
		return Runtime{}, err
	}
	runtime, _, err := s.generation(filepath.Join("bundles", generation), currentTargetName(goos, goarch), target)
	if err != nil {
		return Runtime{}, fmt.Errorf("validate runtime generation %s: %w", generation, err)
	}
	return runtime, nil
}

func (s *Store) AcquireLease(request LeaseRequest) (*Lease, error) {
	if err := request.validate(); err != nil {
		return nil, err
	}
	generation, err := s.GenerationID("", "")
	if err != nil {
		return nil, err
	}
	if request.Generation != generation {
		return nil, fmt.Errorf("runtime generation lease names %q, want exact generation %q", request.Generation, generation)
	}
	target, err := s.Lock.Target("", "")
	if err != nil {
		return nil, err
	}
	rootHandle, err := openDirectRuntimeRoot(s.Root)
	if err != nil {
		return nil, fmt.Errorf("open runtime generation reference store: %w", err)
	}
	if _, _, err := s.generationAt(rootHandle, filepath.Join("bundles", generation), currentTargetName("", ""), target); err != nil {
		_ = rootHandle.Close()
		return nil, fmt.Errorf("validate runtime generation %s: %w", generation, err)
	}
	return s.acquireLeaseAt(rootHandle, request, filepath.Join(s.Root, "runtime", "references", generation))
}

// AcquireHandLease retains one exact managed Hand executable generation.
func (s *Store) AcquireHandLease(request LeaseRequest) (*Lease, error) {
	if err := request.validate(); err != nil {
		return nil, err
	}
	rootHandle, err := openDirectRuntimeRoot(s.Root)
	if err != nil {
		return nil, fmt.Errorf("open managed Hand generation reference store: %w", err)
	}
	if _, err := s.handGenerationAt(rootHandle, request.Generation); err != nil {
		_ = rootHandle.Close()
		return nil, err
	}
	digest := strings.TrimPrefix(request.Generation, "sha256:")
	return s.acquireLeaseAt(rootHandle, request, filepath.Join(s.Root, "runtime", "hand-references", digest))
}

// RuntimeLeaseHeld proves that the exact runtime generation lease record and
// its kernel lock are both live. A busy reusable LockScope returns
// ErrLeaseMetadataUnknown because it cannot identify the exact holder.
func (s *Store) RuntimeLeaseHeld(request LeaseRequest) (bool, error) {
	if err := request.validate(); err != nil {
		return false, err
	}
	generation, err := s.GenerationID("", "")
	if err != nil {
		return false, err
	}
	if request.Generation != generation {
		return false, fmt.Errorf("runtime generation lease names %q, want exact generation %q", request.Generation, generation)
	}
	target, err := s.Lock.Target("", "")
	if err != nil {
		return false, err
	}
	rootHandle, err := openDirectRuntimeRoot(s.Root)
	if err != nil {
		return false, err
	}
	defer func() { _ = rootHandle.Close() }()
	if _, _, err := s.generationAt(rootHandle, filepath.Join("bundles", generation), currentTargetName("", ""), target); err != nil {
		return false, fmt.Errorf("validate runtime generation %s: %w", generation, err)
	}
	return s.leaseHeldAt(rootHandle, request, filepath.Join(s.Root, "runtime", "references", generation))
}

// HandLeaseHeld proves that the exact managed Hand generation lease record
// and its kernel lock are both live. A busy reusable LockScope returns
// ErrLeaseMetadataUnknown because it cannot identify the exact holder.
func (s *Store) HandLeaseHeld(request LeaseRequest) (bool, error) {
	if err := request.validate(); err != nil {
		return false, err
	}
	rootHandle, err := openDirectRuntimeRoot(s.Root)
	if err != nil {
		return false, err
	}
	defer func() { _ = rootHandle.Close() }()
	if _, err := s.handGenerationAt(rootHandle, request.Generation); err != nil {
		return false, err
	}
	digest := strings.TrimPrefix(request.Generation, "sha256:")
	return s.leaseHeldAt(rootHandle, request, filepath.Join(s.Root, "runtime", "hand-references", digest))
}

func (s *Store) acquireLeaseAt(rootHandle *os.Root, request LeaseRequest, referenceRoot string) (*Lease, error) {
	if err := ensureRuntimeDirectoryAt(rootHandle, s.Root, referenceRoot, 0o700); err != nil {
		_ = rootHandle.Close()
		return nil, fmt.Errorf("create runtime generation reference store: %w", err)
	}
	retainRoot := false
	defer func() {
		if !retainRoot {
			_ = rootHandle.Close()
		}
	}()
	recordPath, lockPath := leasePaths(request, referenceRoot)
	lock, _, err := openRuntimeFile(rootHandle, s.Root, lockPath, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		if recordErr := s.requireNoLeaseRecordForUnboundScope(rootHandle, referenceRoot, request); recordErr != nil {
			return nil, recordErr
		}
		lock, _, err = openRuntimeFile(rootHandle, s.Root, lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	}
	if err != nil {
		return nil, fmt.Errorf("open runtime generation lease lock: %w", err)
	}
	if err := filelock.Lock(lock, false); err != nil {
		_ = lock.Close()
		if errors.Is(err, filelock.ErrBusy) {
			return nil, fmt.Errorf("%w: generation=%s lease=%s fleet=%s", ErrLeaseHeld, request.Generation, request.LeaseID, request.FleetID)
		}
		return nil, fmt.Errorf("lock runtime generation lease: %w", err)
	}
	lockIdentity, err := leaseLockIdentity(lock)
	if err != nil {
		_ = filelock.Unlock(lock)
		_ = lock.Close()
		return nil, fmt.Errorf("identify runtime generation lease lock: %w", err)
	}
	if err := s.requireLeaseScopeIdentity(rootHandle, referenceRoot, request, lockPath, lockIdentity); err != nil {
		_ = filelock.Unlock(lock)
		_ = lock.Close()
		return nil, err
	}

	record := leaseRecord{
		Schema: LeaseSchema, Generation: request.Generation, LeaseID: request.LeaseID, LockScope: request.LockScope, LockIdentity: lockIdentity,
		FleetID: request.FleetID, Consumer: request.Consumer, Evidence: request.Evidence,
		CreatedAt: time.Now().UTC(),
	}
	existing, recordInfo, err := readLeaseRecord(rootHandle, s.Root, recordPath)
	switch {
	case err == nil:
		if err := existing.validate(); err != nil || !existing.sameIdentity(record) {
			_ = filelock.Unlock(lock)
			_ = lock.Close()
			return nil, fmt.Errorf("%w: generation=%s lease=%s record=%s", ErrLeaseMetadataUnknown, request.Generation, request.LeaseID, recordPath)
		}
		record = existing
	case errors.Is(err, os.ErrNotExist):
		data, encodeErr := json.MarshalIndent(record, "", "  ")
		if encodeErr != nil {
			_ = filelock.Unlock(lock)
			_ = lock.Close()
			return nil, encodeErr
		}
		if writeErr := atomicWriteRuntimeFile(rootHandle, s.Root, recordPath, ".runtime-reference-", append(data, '\n'), 0o600); writeErr != nil {
			_ = filelock.Unlock(lock)
			_ = lock.Close()
			return nil, fmt.Errorf("publish runtime generation lease: %w", writeErr)
		}
		existing, recordInfo, err = readLeaseRecord(rootHandle, s.Root, recordPath)
		if err != nil || !existing.sameIdentity(record) {
			_ = filelock.Unlock(lock)
			_ = lock.Close()
			return nil, fmt.Errorf("%w: generation=%s lease=%s record=%s", ErrLeaseMetadataUnknown, request.Generation, request.LeaseID, recordPath)
		}
	default:
		_ = filelock.Unlock(lock)
		_ = lock.Close()
		return nil, fmt.Errorf("%w: generation=%s lease=%s record=%s: %v", ErrLeaseMetadataUnknown, request.Generation, request.LeaseID, recordPath, err)
	}
	if err := requireLeaseLockPathIdentity(rootHandle, s.Root, lockPath, lock); err != nil {
		_ = filelock.Unlock(lock)
		_ = lock.Close()
		return nil, fmt.Errorf("%w: generation=%s lease=%s lock=%s: %v", ErrLeaseMetadataUnknown, request.Generation, request.LeaseID, lockPath, err)
	}
	retainRoot = true
	return &Lease{record: record, storeRoot: s.Root, rootHandle: rootHandle, recordPath: recordPath, recordInfo: recordInfo, lockPath: lockPath, lock: lock}, nil
}

func requireLeaseLockPathIdentity(rootHandle *os.Root, root, lockPath string, lock *os.File) error {
	currentLock, currentInfo, err := openRuntimeFile(rootHandle, root, lockPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = currentLock.Close() }()
	lockedInfo, err := lock.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(currentInfo, lockedInfo) {
		return errors.New("lease lock file identity changed")
	}
	return nil
}

func (s *Store) requireLeaseScopeIdentity(rootHandle *os.Root, referenceRoot string, request LeaseRequest, lockPath, identity string) error {
	markerIdentity, err := readLeaseScopeIdentity(rootHandle, s.Root, lockPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := s.requireNoLeaseRecordForUnboundScope(rootHandle, referenceRoot, request); err != nil {
			return err
		}
		marker, _, createErr := openRuntimeFile(rootHandle, s.Root, lockPath+".identity", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr == nil {
			_, writeErr := marker.Write([]byte(identity + "\n"))
			syncErr := marker.Sync()
			closeErr := marker.Close()
			if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
				return fmt.Errorf("%w: publish lease scope identity: %v", ErrLeaseMetadataUnknown, err)
			}
		} else if !errors.Is(createErr, os.ErrExist) {
			return fmt.Errorf("%w: create lease scope identity: %v", ErrLeaseMetadataUnknown, createErr)
		}
		markerIdentity, err = readLeaseScopeIdentity(rootHandle, s.Root, lockPath)
	}
	if err == nil && markerIdentity != identity {
		err = errors.New("stored scope identity does not match locked file")
	}
	if err != nil {
		return fmt.Errorf("%w: generation=%s lease=%s lock scope identity changed: %v", ErrLeaseMetadataUnknown,
			request.Generation, request.LeaseID, err)
	}
	return nil
}

func readLeaseScopeIdentity(rootHandle *os.Root, root, lockPath string) (string, error) {
	marker, _, err := openRuntimeFile(rootHandle, root, lockPath+".identity", os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = marker.Close() }()
	data, err := io.ReadAll(io.LimitReader(marker, 129))
	if err != nil {
		return "", err
	}
	identity := strings.TrimSuffix(string(data), "\n")
	if len(data) > 128 || identity == "" || string(data) != identity+"\n" {
		return "", errors.New("lease scope identity is malformed")
	}
	return identity, nil
}

func (s *Store) requireNoLeaseRecordForUnboundScope(rootHandle *os.Root, referenceRoot string, request LeaseRequest) error {
	unknown := func() error {
		return fmt.Errorf("%w: generation=%s lease=%s lock scope has durable metadata without verified lock identity", ErrLeaseMetadataUnknown, request.Generation, request.LeaseID)
	}
	lockScope := request.LockScope
	if lockScope == "" {
		lockScope = request.LeaseID
	}
	relative, err := runtimeRelativePath(s.Root, referenceRoot)
	if err != nil {
		return err
	}
	directory, owned, err := openDirectRuntimeSubroot(rootHandle, relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if owned {
		defer func() { _ = directory.Close() }()
	}
	file, err := directory.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	entries, err := file.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		record, _, err := readLeaseRecord(rootHandle, s.Root, filepath.Join(referenceRoot, entry.Name()))
		if err != nil {
			return unknown()
		}
		if record.Schema == "hand.runtime.lease.v1" {
			if record.CreatedAt.IsZero() || record.LockIdentity != "" || (LeaseRequest{
				Generation: record.Generation, LeaseID: record.LeaseID, LockScope: record.LockScope,
				FleetID: record.FleetID, Consumer: record.Consumer, Evidence: record.Evidence,
			}).validate() != nil {
				return unknown()
			}
		} else if record.validate() != nil {
			return unknown()
		}
		recordScope := record.LockScope
		if recordScope == "" {
			recordScope = record.LeaseID
		}
		if recordScope == lockScope {
			return unknown()
		}
	}
	return nil
}

func (s *Store) leaseHeldAt(rootHandle *os.Root, request LeaseRequest, referenceRoot string) (bool, error) {
	recordPath, lockPath := leasePaths(request, referenceRoot)
	record, _, err := readLeaseRecord(rootHandle, s.Root, recordPath)
	if errors.Is(err, os.ErrNotExist) {
		var expectedIdentity string
		markerIdentity, markerErr := readLeaseScopeIdentity(rootHandle, s.Root, lockPath)
		if errors.Is(markerErr, os.ErrNotExist) {
			if err := s.requireNoLeaseRecordForUnboundScope(rootHandle, referenceRoot, request); err != nil {
				return false, err
			}
		} else if markerErr != nil {
			return false, fmt.Errorf("%w: generation=%s lease=%s lock scope identity: %v", ErrLeaseMetadataUnknown,
				request.Generation, request.LeaseID, markerErr)
		} else {
			expectedIdentity = markerIdentity
		}
		exists, held, lockErr := probeLeaseLock(rootHandle, s.Root, lockPath, expectedIdentity)
		if lockErr != nil || held || (!exists && expectedIdentity != "") {
			if lockErr == nil {
				if !exists {
					lockErr = os.ErrNotExist
				} else {
					lockErr = errors.New("live kernel lock has no durable lease record")
				}
			}
			return false, fmt.Errorf("%w: generation=%s lease=%s lock=%s: %v", ErrLeaseMetadataUnknown, request.Generation, request.LeaseID, lockPath, lockErr)
		}
		return false, nil
	}
	if err != nil || record.validate() != nil || !record.sameIdentity(leaseRecord{
		Schema: LeaseSchema, Generation: request.Generation, LeaseID: request.LeaseID, LockScope: request.LockScope, LockIdentity: record.LockIdentity,
		FleetID: request.FleetID, Consumer: request.Consumer, Evidence: request.Evidence,
	}) {
		return false, fmt.Errorf("%w: generation=%s lease=%s record=%s", ErrLeaseMetadataUnknown, request.Generation, request.LeaseID, recordPath)
	}
	markerIdentity, markerErr := readLeaseScopeIdentity(rootHandle, s.Root, lockPath)
	if markerErr == nil && markerIdentity != record.LockIdentity {
		markerErr = errors.New("stored scope identity does not match lease record")
	}
	if markerErr != nil {
		return false, fmt.Errorf("%w: generation=%s lease=%s lock scope identity is unknown: %v", ErrLeaseMetadataUnknown,
			request.Generation, request.LeaseID, markerErr)
	}
	exists, held, err := probeLeaseLock(rootHandle, s.Root, lockPath, record.LockIdentity)
	if err != nil || !exists {
		if err == nil {
			err = os.ErrNotExist
		}
		return false, fmt.Errorf("%w: generation=%s lease=%s lock=%s: %v", ErrLeaseMetadataUnknown, request.Generation, request.LeaseID, lockPath, err)
	}
	if held && request.LockScope != "" {
		return false, fmt.Errorf("%w: generation=%s lease=%s lock_scope=%s: busy reusable lock does not identify its holder",
			ErrLeaseMetadataUnknown, request.Generation, request.LeaseID, request.LockScope)
	}
	return held, nil
}

func probeLeaseLock(rootHandle *os.Root, root, lockPath, expectedIdentity string) (exists, held bool, err error) {
	lock, _, err := openRuntimeFile(rootHandle, root, lockPath, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if expectedIdentity != "" {
		identity, identityErr := leaseLockIdentity(lock)
		if identityErr != nil {
			_ = lock.Close()
			return true, false, identityErr
		}
		if identity != expectedIdentity {
			_ = lock.Close()
			return true, false, errors.New("lease lock file identity changed")
		}
	}
	if err := filelock.Lock(lock, false); err != nil {
		closeErr := lock.Close()
		if errors.Is(err, filelock.ErrBusy) && closeErr == nil {
			return true, true, nil
		}
		return true, false, errors.Join(err, closeErr)
	}
	if err := errors.Join(filelock.Unlock(lock), lock.Close()); err != nil {
		return true, false, err
	}
	return true, false, nil
}

func leasePaths(request LeaseRequest, referenceRoot string) (string, string) {
	lockScope := request.LockScope
	if lockScope == "" {
		lockScope = request.LeaseID
	}
	recordIdentity := request.LeaseID
	if request.LockScope != "" {
		recordIdentity = request.LockScope + "\x00" + request.LeaseID
	}
	recordKey := sha256.Sum256([]byte(recordIdentity))
	lockKey := sha256.Sum256([]byte(lockScope))
	return filepath.Join(referenceRoot, hex.EncodeToString(recordKey[:])+".json"), filepath.Join(referenceRoot, hex.EncodeToString(lockKey[:])+".lock")
}

func (request LeaseRequest) validate() error {
	for name, value := range map[string]string{
		"generation":        request.Generation,
		"lease identity":    request.LeaseID,
		"consumer":          request.Consumer,
		"creation evidence": request.Evidence,
	} {
		if err := validateLeaseText(name, value, 512); err != nil {
			return err
		}
	}
	if request.LockScope != "" {
		if err := validateLeaseText("lock scope", request.LockScope, 512); err != nil {
			return err
		}
	}
	if len(request.FleetID) != 34 || !strings.HasPrefix(request.FleetID, "f_") {
		return fmt.Errorf("runtime generation lease has invalid Fleet identity %q", request.FleetID)
	}
	for _, char := range request.FleetID[2:] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return fmt.Errorf("runtime generation lease has invalid Fleet identity %q", request.FleetID)
		}
	}
	return nil
}

func validateLeaseText(name, value string, limit int) error {
	if value == "" || len(value) > limit || strings.IndexFunc(value, func(char rune) bool { return char < 0x20 || char == 0x7f }) >= 0 {
		return fmt.Errorf("runtime generation lease has invalid %s", name)
	}
	return nil
}

func (record leaseRecord) validate() error {
	if record.Schema != LeaseSchema || record.CreatedAt.IsZero() || record.LockIdentity == "" {
		return ErrLeaseMetadataUnknown
	}
	return (LeaseRequest{
		Generation: record.Generation, LeaseID: record.LeaseID, LockScope: record.LockScope, FleetID: record.FleetID,
		Consumer: record.Consumer, Evidence: record.Evidence,
	}).validate()
}

func (record leaseRecord) sameIdentity(other leaseRecord) bool {
	return record.Schema == other.Schema && record.Generation == other.Generation && record.LeaseID == other.LeaseID && record.LockScope == other.LockScope && record.LockIdentity == other.LockIdentity &&
		record.FleetID == other.FleetID && record.Consumer == other.Consumer && record.Evidence == other.Evidence
}

func readLeaseRecord(rootHandle *os.Root, root, path string) (leaseRecord, os.FileInfo, error) {
	file, info, err := openRuntimeFile(rootHandle, root, path, os.O_RDONLY, 0)
	if err != nil {
		return leaseRecord{}, nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		return leaseRecord{}, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record leaseRecord
	if err := decoder.Decode(&record); err != nil {
		return leaseRecord{}, nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return leaseRecord{}, nil, errors.New("runtime generation lease record has trailing data")
	}
	return record, info, nil
}

func (lease *Lease) RecordPath() string {
	if lease == nil {
		return ""
	}
	return lease.recordPath
}

func (lease *Lease) LockPath() string {
	if lease == nil {
		return ""
	}
	return lease.lockPath
}

// StartChild starts a managed consumer with the lease's locked file object
// inherited, so abrupt guardian death cannot release ownership while the
// consumer is still alive.
func (lease *Lease) StartChild(cmd *exec.Cmd) error {
	if lease == nil || lease.closed || lease.lock == nil {
		return errors.New("runtime generation lease is not live")
	}
	if cmd == nil {
		return errors.New("runtime generation lease child command is nil")
	}
	if err := startChildWithLease(cmd, lease.lock); err != nil {
		return err
	}
	lease.childStarted = true
	return nil
}

func (lease *Lease) Close() error {
	if lease == nil || lease.closed {
		return nil
	}
	lease.closed = true
	defer func() {
		if !lease.childStarted {
			_ = filelock.Unlock(lease.lock)
		}
		_ = lease.lock.Close()
		_ = lease.rootHandle.Close()
	}()
	existing, info, err := readLeaseRecord(lease.rootHandle, lease.storeRoot, lease.recordPath)
	if err != nil || !existing.sameIdentity(lease.record) || !os.SameFile(lease.recordInfo, info) {
		return fmt.Errorf("%w: refusing to retire generation=%s lease=%s record=%s", ErrLeaseMetadataUnknown, lease.record.Generation, lease.record.LeaseID, lease.recordPath)
	}
	if err := requireLeaseLockPathIdentity(lease.rootHandle, lease.storeRoot, lease.lockPath, lease.lock); err != nil {
		return fmt.Errorf("%w: refusing to retire generation=%s lease=%s lock=%s: %v", ErrLeaseMetadataUnknown,
			lease.record.Generation, lease.record.LeaseID, lease.lockPath, err)
	}
	markerIdentity, err := readLeaseScopeIdentity(lease.rootHandle, lease.storeRoot, lease.lockPath)
	if err == nil && markerIdentity != lease.record.LockIdentity {
		err = errors.New("stored scope identity does not match lease record")
	}
	if err != nil {
		return fmt.Errorf("%w: refusing to retire generation=%s lease=%s lock scope identity is unknown: %v", ErrLeaseMetadataUnknown,
			lease.record.Generation, lease.record.LeaseID, err)
	}
	return nil
}
