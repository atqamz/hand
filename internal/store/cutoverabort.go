package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var errLegacyV18CutoverAbortUnsafe = errors.New("v19 cutover abort is not mechanically safe")

const (
	legacyV18CutoverAbortRecordPrefix    = "aborted-"
	legacyV18CutoverAbortedDisposition   = "aborted"
	legacyV18CutoverNotFrozenDisposition = "not-frozen"
)

type legacyV18CutoverAbort struct {
	Disposition string
	MigrationID string
	Record      string
}

// Reverses a freeze before publication by restoring the original archive's exact bytes;
// the retained bridge and manifest become an inert record of the abort.
func abortLegacyV18CutoverFreeze(homeDir string) (legacyV18CutoverAbort, error) {
	release, err := Lock(homeDir, MigrationLock, true)
	if err != nil {
		return legacyV18CutoverAbort{}, fmt.Errorf("abort v19 cutover: acquire MigrationLock: %w", err)
	}
	defer release()

	activePath := Path(homeDir)
	if err := requireLegacyV18CutoverDirectRegularFile(activePath, "active database"); err != nil {
		return legacyV18CutoverAbort{}, fmt.Errorf("%w: %v", errLegacyV18CutoverAbortUnsafe, err)
	}
	bridge, bridgeErr := discoverLegacyV18CutoverFrozenBridge(homeDir)
	if bridgeErr != nil {
		state, err := inspectLegacyV18CutoverRecovery(homeDir)
		if err != nil || state.Disposition != legacyV18CutoverRecoveryLegacySource {
			return legacyV18CutoverAbort{}, fmt.Errorf("%w: active database is neither an exact frozen bridge (%v) nor an exact legacy source", errLegacyV18CutoverAbortUnsafe, bridgeErr)
		}
		if err := syncLegacyV18CutoverDirectoryParent(activePath); err != nil {
			return legacyV18CutoverAbort{}, fmt.Errorf("abort v19 cutover: flush state directory: %w", err)
		}
		return legacyV18CutoverAbort{Disposition: legacyV18CutoverNotFrozenDisposition}, nil
	}

	archivePath := legacyV18CutoverOriginalArchivePath(homeDir, bridge.MigrationID)
	if err := requireLegacyV18CutoverDirectRegularFile(archivePath, "original archive"); err != nil {
		return legacyV18CutoverAbort{}, fmt.Errorf("%w: %v", errLegacyV18CutoverAbortUnsafe, err)
	}
	if digest, err := legacyV18CutoverFileSHA256(archivePath); err != nil || digest != bridge.SourceSHA256 {
		return legacyV18CutoverAbort{}, fmt.Errorf("%w: original archive digest=%s (%v), want certificate source %s", errLegacyV18CutoverAbortUnsafe, digest, err, bridge.SourceSHA256)
	}
	if err := requireLegacyV18CutoverNoSQLiteSidecars(activePath, "active frozen bridge"); err != nil {
		return legacyV18CutoverAbort{}, fmt.Errorf("%w: %v", errLegacyV18CutoverAbortUnsafe, err)
	}

	record, err := ensureLegacyV18CutoverAbortRecord(homeDir, bridge.MigrationID)
	if err != nil {
		return legacyV18CutoverAbort{}, fmt.Errorf("abort v19 cutover: record abort: %w", err)
	}
	if err := moveLegacyV18CutoverManifestIntoAbortRecord(homeDir, bridge.MigrationID, record); err != nil {
		return legacyV18CutoverAbort{}, fmt.Errorf("abort v19 cutover: %w", err)
	}
	if err := discardLegacyV18CutoverInvalidCanonicalTemp(legacyV18CutoverCanonicalTargetPath(homeDir, bridge.MigrationID)); err != nil {
		return legacyV18CutoverAbort{}, fmt.Errorf("abort v19 cutover: %w", err)
	}
	if err := removeLegacyV18CutoverSameFile(legacyV18CutoverRetiredBridgePath(homeDir, bridge.MigrationID), activePath); err != nil {
		return legacyV18CutoverAbort{}, fmt.Errorf("abort v19 cutover: remove retired bridge link: %w", err)
	}

	candidate := filepath.Join(Dir(homeDir), ".v19-cutover-"+bridge.MigrationID+"-restore.db.candidate")
	if err := copyLegacyV18CutoverFileDurable(archivePath, candidate, bridge.SourceSHA256); err != nil {
		return legacyV18CutoverAbort{}, fmt.Errorf("abort v19 cutover: write restore candidate: %w", err)
	}
	if err := replaceLegacyV18CutoverDurable(candidate, activePath); err != nil {
		return legacyV18CutoverAbort{}, fmt.Errorf("abort v19 cutover: restore original archive: %w", err)
	}
	if err := validateLegacyV18CutoverAbortedSource(activePath, bridge.SourceSHA256); err != nil {
		return legacyV18CutoverAbort{}, err
	}
	return legacyV18CutoverAbort{Disposition: legacyV18CutoverAbortedDisposition, MigrationID: bridge.MigrationID, Record: record}, nil
}

// The record of the active bridge, if an abort already passed its commit point for it.
func findLegacyV18CutoverAbortRecord(homeDir, migrationID string) (string, bool, error) {
	active, err := os.Lstat(Path(homeDir))
	if err != nil {
		return "", false, err
	}
	dir := legacyV18CutoverOriginalArchiveDir(homeDir, migrationID)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), legacyV18CutoverAbortRecordPrefix) {
			continue
		}
		record := filepath.Join(dir, entry.Name())
		linked, err := os.Lstat(filepath.Join(record, "frozen-bridge.db"))
		if err == nil && os.SameFile(linked, active) {
			return record, true, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", false, err
		}
	}
	return "", false, nil
}

func ensureLegacyV18CutoverAbortRecord(homeDir, migrationID string) (string, error) {
	if record, found, err := findLegacyV18CutoverAbortRecord(homeDir, migrationID); err != nil || found {
		return record, err
	}
	dir := legacyV18CutoverOriginalArchiveDir(homeDir, migrationID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		record := filepath.Join(dir, entry.Name())
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), legacyV18CutoverAbortRecordPrefix) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(record, "frozen-bridge.db")); os.IsNotExist(err) {
			if err := os.Remove(record); err != nil {
				return "", fmt.Errorf("remove abort record %s without a bridge link: %w", record, err)
			}
		}
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	record := filepath.Join(dir, legacyV18CutoverAbortRecordPrefix+hex.EncodeToString(nonce[:]))
	if err := os.Mkdir(record, 0o700); err != nil {
		return "", err
	}
	if err := syncLegacyV18CutoverDirectoryParent(record); err != nil {
		return "", err
	}
	return record, linkLegacyV18CutoverNoReplaceDurable(Path(homeDir), filepath.Join(record, "frozen-bridge.db"))
}

func moveLegacyV18CutoverManifestIntoAbortRecord(homeDir, migrationID, record string) error {
	manifest := legacyV18CutoverManifestPath(homeDir, migrationID)
	target := filepath.Join(record, legacyV18CutoverManifestFileName)
	if _, err := os.Lstat(manifest); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect frozen manifest: %w", err)
	}
	if err := moveLegacyV18CutoverNoReplaceDurable(manifest, target); err != nil {
		return fmt.Errorf("move frozen manifest into abort record: %w", err)
	}
	return nil
}

func removeLegacyV18CutoverSameFile(path, reference string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	referenceInfo, err := os.Lstat(reference)
	if err != nil {
		return err
	}
	if !os.SameFile(info, referenceInfo) {
		return fmt.Errorf("%s is not a link to %s", path, reference)
	}
	return os.Remove(path)
}

func copyLegacyV18CutoverFileDurable(source, target, wantSHA256 string) error {
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	if closeErr := output.Close(); copyErr == nil && syncErr == nil {
		syncErr = closeErr
	}
	if err := errors.Join(copyErr, syncErr); err != nil {
		return err
	}
	digest, err := legacyV18CutoverFileSHA256(target)
	if err != nil {
		return err
	}
	if digest != wantSHA256 {
		return fmt.Errorf("restore candidate digest=%s, want %s", digest, wantSHA256)
	}
	return nil
}

func validateLegacyV18CutoverAbortedSource(activePath, sourceSHA256 string) error {
	sqlDB, err := openLegacyV18CutoverSQLite(activePath, "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		return fmt.Errorf("abort v19 cutover: reopen restored source: %w", err)
	}
	_, validationErr := validateLegacyV18CutoverSource(sqlDB)
	if closeErr := sqlDB.Close(); validationErr == nil {
		validationErr = closeErr
	}
	if validationErr != nil {
		return fmt.Errorf("%w: restored source is not exact v0.7.2: %v", errLegacyV18CutoverAbortUnsafe, validationErr)
	}
	digest, err := legacyV18CutoverFileSHA256(activePath)
	if err != nil || digest != sourceSHA256 {
		return fmt.Errorf("%w: restored source digest=%s (%v), want %s", errLegacyV18CutoverAbortUnsafe, digest, err, sourceSHA256)
	}
	return nil
}
