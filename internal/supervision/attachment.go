package supervision

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/atomicfile"
)

// AttachmentSchema versions the bridge-attachment record. It is mechanism
// evidence only: a fresh record proves a live wait child claimed this host's
// bridge for one runtime, never workflow authority of any kind.
const AttachmentSchema = "hand.supervision.attachment.v1"

const attachmentRel = "state/runtime/supervision-attachment.json"

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

// ErrBridgeOwned is Wait's result when another live runtime already holds
// this host's bridge. A secondary session must defer, not steal.
var ErrBridgeOwned = fmt.Errorf("wake bridge is owned by another live runtime")

var attachmentMu sync.Mutex

func attachmentPath(home string) string { return filepath.Join(home, attachmentRel) }

// WriteAttachment atomically replaces the fleet home's bridge-attachment
// record; it runs under one process-wide mutex because each wait child owns
// exactly one record and children never share one.
func WriteAttachment(home string, record AttachmentRecord) error {
	attachmentMu.Lock()
	defer attachmentMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(attachmentPath(home)), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(attachmentRel), err)
	}
	record.Schema = AttachmentSchema
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(attachmentPath(home), ".supervision-attachment-", append(encoded, '\n'), 0o644)
}

// ReadAttachment returns the current record, or nil when none exists or the
// file is unreadable or corrupt - all three mean "nothing attached".
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

// ClearAttachment removes the record when it still belongs to this runtime,
// so a replaced child cannot revoke its successor's claim.
func ClearAttachment(home, host, runtime string) {
	attachmentMu.Lock()
	defer attachmentMu.Unlock()
	data, err := os.ReadFile(attachmentPath(home))
	if err != nil {
		return
	}
	var record AttachmentRecord
	if json.Unmarshal(data, &record) != nil {
		_ = os.Remove(attachmentPath(home))
		return
	}
	if record.Host == host && record.Runtime == runtime {
		_ = os.Remove(attachmentPath(home))
	}
}

// BridgeOwner reports the live runtime holding host's bridge, or empty when
// no fresh record exists for that host.
func BridgeOwner(home string, host string, now time.Time) string {
	record := ReadAttachment(home)
	if record == nil || !record.Fresh(now) || record.Host != host {
		return ""
	}
	return record.Runtime
}
