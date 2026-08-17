package watcher

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

// The only record layout the current hand understands. Any other version is
// untrusted routing input: no takeover action may be attempted against it.
const ownerRecordVersion = 1

// OwnerRecord is the versioned, generation-bound ownership record that routes
// an explicit takeover request to the current watcher. It is routing metadata
// only: authority is the kernel-backed watch.pid.lock, never this file.
type OwnerRecord struct {
	Version    int    `json:"version"`
	Generation string `json:"generation"`
	PID        int    `json:"pid"`
}

// Fresh 128 random bits encoded as 32 lowercase hex characters, tying this
// watcher's takeover endpoint to this incumbent alone.
func newGeneration() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate ownership generation: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// Strictly decodes an untrusted advisory record. Anything malformed, partial,
// truncated, wrong-version, or structurally invalid is rejected: no takeover
// may fall back to reading watch.pid and signaling a process.
func parseOwnerRecord(data []byte) (OwnerRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var rec OwnerRecord
	if err := dec.Decode(&rec); err != nil {
		return OwnerRecord{}, fmt.Errorf("parse owner record: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return OwnerRecord{}, fmt.Errorf("parse owner record: unexpected trailing data")
	}
	if err := validateOwnerRecord(rec); err != nil {
		return OwnerRecord{}, err
	}
	return rec, nil
}

func validateOwnerRecord(rec OwnerRecord) error {
	if rec.Version != ownerRecordVersion {
		return fmt.Errorf("owner record: unsupported version %d", rec.Version)
	}
	if len(rec.Generation) != ownerGenerationLen {
		return fmt.Errorf("owner record: generation must be %d lowercase hex characters, got %q", ownerGenerationLen, rec.Generation)
	}
	for _, c := range rec.Generation {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return fmt.Errorf("owner record: generation %q is not lowercase hex", rec.Generation)
		}
	}
	if rec.PID <= 0 {
		return fmt.Errorf("owner record: pid must be a positive integer, got %d", rec.PID)
	}
	return nil
}

const ownerGenerationLen = 32

// OwnerRecordPath names the routing record that ties an explicit takeover to a
// specific generation, alongside the advisory PID file OwnerPath names.
func OwnerRecordPath(homeDir string) string {
	return filepath.Join(stateDir(homeDir), "watch.owner")
}

// Folds ordinary references to the same fleet home into one endpoint identity:
// clean, absolute, and symlink-resolved.
func canonicalHome(homeDir string) string {
	home := filepath.Clean(homeDir)
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	return home
}

// Atomically replaces watch.owner. This is the final step of acquisition, so a
// valid live record implies a ready takeover endpoint.
func publishOwnerRecord(homeDir string, rec OwnerRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal owner record: %w", err)
	}
	if err := atomicfileWrite(OwnerRecordPath(homeDir), data); err != nil {
		return fmt.Errorf("publish owner record: %w", err)
	}
	return nil
}
