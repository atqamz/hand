package supervision

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/filelock"
)

// AttachmentSchema versions the bridge-attachment record. It is mechanism
// evidence only: a fresh record proves a live wait child claimed this host's
// bridge for one runtime, never workflow authority of any kind.
const AttachmentSchema = "hand.supervision.attachment.v1"

const (
	attachmentRel        = "state/runtime/supervision-attachment.json"
	attachmentLockSuffix = ".lock"
)

// AttachmentRecord is one running wait's claim on its host's wake bridge.
// HeartbeatAt/ExpiresAt make staleness mechanical: a dead child stops
// beating, and an expired record can never read as attached.
type AttachmentRecord struct {
	Schema      string    `json:"schema"`
	Host        string    `json:"host"`
	Runtime     string    `json:"runtime,omitempty"`
	PID         int       `json:"pid"`
	FleetID     string    `json:"fleet_id"`
	StartedAt   time.Time `json:"started_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// Fresh reports whether the record still describes a live wait.
func (r AttachmentRecord) Fresh(now time.Time) bool {
	if r.Schema != AttachmentSchema || r.Host == "" {
		return false
	}
	return now.Before(r.ExpiresAt)
}

// ErrBridgeOwned is the result when another live runtime already holds this
// host's bridge. A secondary session must defer, not steal.
var ErrBridgeOwned = errors.New("wake bridge is owned by another live runtime")

func attachmentPath(home string) string { return filepath.Join(home, attachmentRel) }

func attachmentLockPath(home string) string {
	return filepath.Join(home, attachmentRel+attachmentLockSuffix)
}

// Runs change under the attachment's advisory file lock, so acquire, refresh,
// and clear decisions are atomic across processes, not just goroutines.
func mutateAttachment(home string, change func(existing *AttachmentRecord) (*AttachmentRecord, bool, error)) error {
	if err := os.MkdirAll(filepath.Dir(attachmentPath(home)), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(attachmentRel), err)
	}
	lock, err := os.OpenFile(attachmentLockPath(home), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", attachmentRel+attachmentLockSuffix, err)
	}
	defer func() { _ = lock.Close() }()
	if err := filelock.Lock(lock, true); err != nil {
		return fmt.Errorf("lock %s: %w", attachmentRel+attachmentLockSuffix, err)
	}
	defer func() { _ = filelock.Unlock(lock) }()

	existing, err := os.ReadFile(attachmentPath(home))
	var record *AttachmentRecord
	switch {
	case err == nil:
		record = &AttachmentRecord{}
		if json.Unmarshal(existing, record) != nil || record.Schema != AttachmentSchema {
			record = nil
		}
	case os.IsNotExist(err):
	default:
		return fmt.Errorf("read %s: %w", attachmentRel, err)
	}

	next, write, err := change(record)
	if err != nil || !write {
		return err
	}
	if next == nil {
		// Removal verdict: drop the record so nothing reads as attached.
		if rmErr := os.Remove(attachmentPath(home)); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("remove %s: %w", attachmentRel, rmErr)
		}
		return nil
	}
	next.Schema = AttachmentSchema
	encoded, encodeErr := json.MarshalIndent(next, "", "  ")
	if encodeErr != nil {
		return encodeErr
	}
	if writeErr := atomicfile.Write(attachmentPath(home), ".supervision-attachment-", append(encoded, '\n'), 0o644); writeErr != nil {
		return fmt.Errorf("write %s: %w", attachmentRel, writeErr)
	}
	return nil
}

// The exact bridge-owner identity combines harness AND runtime: the harness
// selects the delivery mechanism, never an ownership domain - a Fleet holds
// one live Supervisor bridge regardless of provider.
func ownerKey(rec AttachmentRecord) string {
	return rec.Host + "\x00" + rec.Runtime
}

// Claims THE Fleet bridge for rec's owner under the file lock, exclusive
// across every harness: false means a fresh record shows another live owner
// still holding it. Stale or absent records never block a takeover.
func AcquireAttachment(home string, rec AttachmentRecord) (bool, error) {
	acquired := false
	err := mutateAttachment(home, func(existing *AttachmentRecord) (*AttachmentRecord, bool, error) {
		if existing != nil && existing.Fresh(time.Now()) && ownerKey(*existing) != ownerKey(rec) {
			return nil, false, nil
		}
		acquired = true
		return &rec, true, nil
	})
	if err != nil {
		return false, err
	}
	return acquired, nil
}

// Re-stamps the heartbeat when the record still belongs to rec's exact owner,
// preserving the original StartedAt and advancing the lease explicitly.
// False means ownership moved elsewhere: stop claiming the bridge.
func RefreshAttachment(home string, rec AttachmentRecord, lease time.Duration) (bool, error) {
	ours := false
	err := mutateAttachment(home, func(existing *AttachmentRecord) (*AttachmentRecord, bool, error) {
		if existing == nil || ownerKey(*existing) != ownerKey(rec) {
			return nil, false, nil
		}
		now := time.Now()
		fresh := rec
		fresh.StartedAt = existing.StartedAt
		fresh.HeartbeatAt = now
		fresh.ExpiresAt = now.Add(lease)
		ours = true
		return &fresh, true, nil
	})
	if err != nil {
		return false, err
	}
	return ours, nil
}

// Removes the record only when it still belongs to this runtime, so a
// replaced child cannot revoke its successor's claim.
func ClearAttachment(home, host, runtime string) {
	_ = mutateAttachment(home, func(existing *AttachmentRecord) (*AttachmentRecord, bool, error) {
		if existing == nil || existing.Host != host || existing.Runtime != runtime {
			return nil, false, nil
		}
		return nil, true, nil
	})
}

// ReadAttachment returns the current record for diagnostics, or nil when
// none exists or the file is unreadable/corrupt. Diagnostics tolerate the
// race; ownership decisions never rely on this unlocked read.
func ReadAttachment(home string) *AttachmentRecord {
	data, err := os.ReadFile(attachmentPath(home))
	if err != nil {
		return nil
	}
	var record AttachmentRecord
	if json.Unmarshal(data, &record) != nil {
		return nil
	}
	return &record
}

// BridgeOwner reports the live runtime holding host's bridge, or empty when
// no fresh record exists for that host.
func BridgeOwner(home string, host string, now time.Time) string {
	data, err := os.ReadFile(attachmentPath(home))
	if err != nil {
		return ""
	}
	var record AttachmentRecord
	if json.Unmarshal(data, &record) != nil {
		return ""
	}
	if !record.Fresh(now) || record.Host != host {
		return ""
	}
	return record.Runtime
}
