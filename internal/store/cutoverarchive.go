package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	legacyV18CutoverMigrationIdentityVersion = "v1"
	legacyV18CutoverMigrationIdentityDomain  = "hand:v19-cutover:migration:v1"
)

type legacyV18CutoverArchiveCandidate struct {
	MigrationID string
	Path        string
	SHA256      string
}

// legacyV18CutoverMigrationIdentity is the versioned deterministic identity for one
// exact legacy source. The digest input is the lowercase hex SHA-256 of the original
// pre-freeze database bytes, not any later frozen-bridge digest.
func legacyV18CutoverMigrationIdentity(fleetID, sourceSHA256 string) (string, error) {
	if err := validateFleetID(fleetID); err != nil {
		return "", fmt.Errorf("derive legacy v18 cutover migration identity: %w", err)
	}
	if err := validateLegacyV18CutoverSHA256(sourceSHA256); err != nil {
		return "", fmt.Errorf("derive legacy v18 cutover migration identity: %w", err)
	}
	payload := legacyV18CutoverMigrationIdentityDomain + "\x00" + fleetID + "\x00" + sourceSHA256
	sum := sha256.Sum256([]byte(payload))
	return legacyV18CutoverMigrationIdentityVersion + "-" + hex.EncodeToString(sum[:]), nil
}

func validateLegacyV18CutoverSHA256(value string) error {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return fmt.Errorf("source SHA-256 must be exactly 64 lowercase hex characters")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("source SHA-256 is not hexadecimal: %w", err)
	}
	return nil
}

func legacyV18CutoverFleetID(q sqliteQueryer) (string, error) {
	var singleton int
	var fleetID string
	if err := q.QueryRow(`SELECT singleton, fleet_id FROM fleet_identity`).Scan(&singleton, &fleetID); err != nil {
		return "", fmt.Errorf("read legacy v18 Fleet identity for cutover archive: %w", err)
	}
	if singleton != 1 {
		return "", fmt.Errorf("read legacy v18 Fleet identity for cutover archive: singleton=%d, want 1", singleton)
	}
	if err := validateFleetID(fleetID); err != nil {
		return "", fmt.Errorf("read legacy v18 Fleet identity for cutover archive: %w", err)
	}
	return fleetID, nil
}

func legacyV18CutoverArchiveCandidatePath(homeDir, migrationID string) string {
	return filepath.Join(homeDir, "state", ".v19-cutover-"+migrationID+"-original.db.candidate")
}

func prepareLegacyV18CutoverArchiveCandidate(homeDir, fleetID, sourceSHA256 string) (legacyV18CutoverArchiveCandidate, error) {
	migrationID, err := legacyV18CutoverMigrationIdentity(fleetID, sourceSHA256)
	if err != nil {
		return legacyV18CutoverArchiveCandidate{}, err
	}
	candidate := legacyV18CutoverArchiveCandidate{
		MigrationID: migrationID,
		Path:        legacyV18CutoverArchiveCandidatePath(homeDir, migrationID),
		SHA256:      sourceSHA256,
	}
	if err := writeLegacyV18CutoverArchiveCandidate(Path(homeDir), candidate.Path, sourceSHA256); err != nil {
		return legacyV18CutoverArchiveCandidate{}, err
	}
	return candidate, nil
}

func writeLegacyV18CutoverArchiveCandidate(sourcePath, candidatePath, expectedSHA256 string) error {
	if err := validateLegacyV18CutoverSHA256(expectedSHA256); err != nil {
		return err
	}
	if info, err := os.Lstat(candidatePath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("legacy v18 cutover archive candidate %s is not a direct regular file", candidatePath)
		}
		digest, err := legacyV18CutoverFileSHA256(candidatePath)
		if err != nil {
			return fmt.Errorf("hash existing legacy v18 cutover archive candidate: %w", err)
		}
		if digest == expectedSHA256 {
			return nil
		}
		if err := os.Remove(candidatePath); err != nil {
			return fmt.Errorf("discard mismatched legacy v18 cutover archive candidate: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect legacy v18 cutover archive candidate: %w", err)
	}

	input, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open legacy v18 cutover source for archive candidate: %w", err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(candidatePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create legacy v18 cutover archive candidate: %w", err)
	}
	keep := false
	closed := false
	defer func() {
		if !closed {
			_ = output.Close()
		}
		if !keep {
			_ = os.Remove(candidatePath)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("copy legacy v18 cutover archive candidate: %w", err)
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("flush legacy v18 cutover archive candidate: %w", err)
	}
	if err := output.Close(); err != nil {
		closed = true
		return fmt.Errorf("close legacy v18 cutover archive candidate: %w", err)
	}
	closed = true

	digest, err := legacyV18CutoverFileSHA256(candidatePath)
	if err != nil {
		return fmt.Errorf("reopen and hash legacy v18 cutover archive candidate: %w", err)
	}
	if digest != expectedSHA256 {
		return fmt.Errorf("legacy v18 cutover archive candidate digest = %s, want %s", digest, expectedSHA256)
	}
	keep = true
	return nil
}
