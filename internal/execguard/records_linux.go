//go:build linux

package execguard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/osfacts"
	"github.com/atqamz/hand/internal/store"
)

// Protocol versions the handoff and every guard record. It is also the secret-ref
// material of the HAND_WORKER_CREDENTIAL Launch environment row.
const Protocol = "hand-exec-guard:v1"

const (
	CredentialEnv      = "HAND_WORKER_CREDENTIAL"
	ExecutorBindingEnv = "HAND_WORKER_EXECUTOR_BINDING"
)

const (
	KindClaimed          = "claimed"
	KindPinned           = "pinned"
	KindRunning          = "running"
	KindRefused          = "refused"
	KindCeased           = "ceased"
	KindInterruptRequest = "interrupt-request"
)

const (
	CauseHarnessExit         = "harness-exit"
	CauseInterruptRequest    = "interrupt-request"
	CauseHangup              = "hangup"
	CauseExternalTermination = "external-termination"
)

const (
	ClassExact   = "exact"
	ClassSampled = "sampled"
)

const (
	handoffName   = "handoff"
	tombstoneName = "fenced"
	claimPrefix   = "claim."
)

var (
	ErrUnknownProtocol = errors.New("exec-guard protocol version is unknown")
	ErrRecordAbsent    = fmt.Errorf("exec-guard record is absent or unreadable: %w", fs.ErrNotExist)
)

// Handoff is handoff(L): the persisted Launch spec with its resolved values, written by
// Hand core before Tx B into L's Fleet-private directory and claimed by exactly one guard.
type Handoff struct {
	Protocol          string                       `json:"protocol"`
	LaunchOperationID string                       `json:"launch_operation_id"`
	FleetID           string                       `json:"fleet_id"`
	ExecutorBindingID string                       `json:"executor_binding_id"`
	RequestDigest     string                       `json:"request_digest"`
	LaunchSpecDigest  string                       `json:"launch_spec_digest"`
	Spec              store.CanonicalV19LaunchSpec `json:"spec"`
	Values            map[string]string            `json:"values"`
	WorktreePath      string                       `json:"worktree_path"`
	BootID            string                       `json:"boot_id"`
	PIDNamespace      uint64                       `json:"pid_namespace"`
	UID               uint32                       `json:"uid"`
}

type Object struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	SHA256 string `json:"sha256,omitempty"`
}

type Exit struct {
	Code   int `json:"code"`
	Signal int `json:"signal"`
}

// Record is one guard record, or Hand's interrupt request, in L's directory. Every
// record names L and the guard incarnation G; the remaining fields belong to its kind.
type Record struct {
	Protocol             string               `json:"protocol"`
	Kind                 string               `json:"kind"`
	LaunchOperationID    string               `json:"launch_operation_id"`
	Guard                osfacts.Incarnation  `json:"guard"`
	RequestDigest        string               `json:"request_digest,omitempty"`
	LaunchSpecDigest     string               `json:"launch_spec_digest,omitempty"`
	CredentialVerifier   string               `json:"credential_verifier,omitempty"`
	Executable           *Object              `json:"executable,omitempty"`
	ObjectClass          string               `json:"object_class,omitempty"`
	Interpreter          *Object              `json:"interpreter,omitempty"`
	Root                 *osfacts.Incarnation `json:"root,omitempty"`
	ProcessGroup         int                  `json:"process_group,omitempty"`
	Terminal             uint64               `json:"terminal,omitempty"`
	Reason               string               `json:"reason,omitempty"`
	Cause                string               `json:"cause,omitempty"`
	InterruptOperationID string               `json:"interrupt_operation_id,omitempty"`
	Exit                 *Exit                `json:"exit,omitempty"`
	Predicate            string               `json:"predicate,omitempty"`
}

// WriteHandoff creates handoff(L) in dir, whose base name is L. It never replaces an
// existing handoff, because a second handoff for one L could start a second execution.
func WriteHandoff(dir string, handoff Handoff) error {
	handoff.Protocol = Protocol
	data, err := json.Marshal(handoff)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, handoffName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Close())
}

// Fence renames handoff(L) to its tombstone, so no guard can claim it afterwards. A nil
// error means the fence won; fs.ErrNotExist means a guard claim or an earlier fence won.
func Fence(dir string) error {
	return os.Rename(filepath.Join(dir, handoffName), filepath.Join(dir, tombstoneName))
}

// RequestInterrupt asks exactly guard to terminate its tree for Interrupt operation id.
// Every other incarnation ignores the request, and a replay rewrites the same bytes.
func RequestInterrupt(dir string, guard osfacts.Incarnation, interruptOperationID string) error {
	return writeRecord(dir, Record{
		Kind:                 KindInterruptRequest,
		LaunchOperationID:    filepath.Base(dir),
		Guard:                guard,
		InterruptOperationID: interruptOperationID,
	})
}

// ReadRecord reads the record of kind in dir. A torn, unparsable, or misfiled record is
// ErrRecordAbsent; a record from a protocol version this build does not know is
// ErrUnknownProtocol, never absent.
func ReadRecord(dir, kind string) (Record, error) {
	data, err := os.ReadFile(filepath.Join(dir, kind))
	if err != nil {
		return Record{}, err
	}
	var version struct {
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(data, &version); err != nil || version.Protocol == "" {
		return Record{}, ErrRecordAbsent
	}
	if version.Protocol != Protocol {
		return Record{}, fmt.Errorf("%w: %q", ErrUnknownProtocol, version.Protocol)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil || record.Kind != kind {
		return Record{}, ErrRecordAbsent
	}
	return record, nil
}

// TerminalKind maps a ceased record to the ExecutorBinding terminal kind. Only a
// request that names the exact pending Interrupt is interrupted, so a forged request
// ends failed.
func TerminalKind(ceased Record, pendingInterruptOperationID string) string {
	switch {
	case ceased.Cause == CauseInterruptRequest && pendingInterruptOperationID != "" &&
		ceased.InterruptOperationID == pendingInterruptOperationID:
		return "interrupted"
	case ceased.Cause == CauseHangup:
		return "provider-gone"
	case ceased.Cause == CauseHarnessExit && ceased.Exit != nil && *ceased.Exit == Exit{}:
		return "completed"
	default:
		return "failed"
	}
}

func writeRecord(dir string, record Record) error {
	record.Protocol = Protocol
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, record.Kind), ".record-*", data, 0o600)
}
