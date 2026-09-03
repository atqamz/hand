package store

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type legacyV18CutoverOriginalArchive struct {
	MigrationID string
	Directory   string
	Path        string
	SHA256      string
}

func legacyV18CutoverOriginalArchiveDir(homeDir, migrationID string) string {
	return filepath.Join(homeDir, "state", "v19-cutover-"+migrationID)
}

func legacyV18CutoverOriginalArchivePath(homeDir, migrationID string) string {
	return filepath.Join(legacyV18CutoverOriginalArchiveDir(homeDir, migrationID), "hand.db")
}

func promoteLegacyV18CutoverArchiveCandidate(homeDir string, candidate legacyV18CutoverArchiveCandidate) (legacyV18CutoverOriginalArchive, error) {
	if err := validateLegacyV18CutoverMigrationID(candidate.MigrationID); err != nil {
		return legacyV18CutoverOriginalArchive{}, fmt.Errorf("promote legacy v18 cutover archive candidate: %w", err)
	}
	if err := validateLegacyV18CutoverSHA256(candidate.SHA256); err != nil {
		return legacyV18CutoverOriginalArchive{}, fmt.Errorf("promote legacy v18 cutover archive candidate: %w", err)
	}
	expectedCandidatePath := legacyV18CutoverArchiveCandidatePath(homeDir, candidate.MigrationID)
	if filepath.Clean(candidate.Path) != filepath.Clean(expectedCandidatePath) {
		return legacyV18CutoverOriginalArchive{}, fmt.Errorf("promote legacy v18 cutover archive candidate: path=%q, want deterministic %q", candidate.Path, expectedCandidatePath)
	}
	if err := requireLegacyV18CutoverDirectRegularFile(candidate.Path, "archive candidate"); err != nil {
		return legacyV18CutoverOriginalArchive{}, err
	}
	candidateDigest, err := legacyV18CutoverFileSHA256(candidate.Path)
	if err != nil {
		return legacyV18CutoverOriginalArchive{}, fmt.Errorf("promote legacy v18 cutover archive candidate: hash candidate: %w", err)
	}
	if candidateDigest != candidate.SHA256 {
		return legacyV18CutoverOriginalArchive{}, fmt.Errorf("promote legacy v18 cutover archive candidate: candidate digest=%s, want %s", candidateDigest, candidate.SHA256)
	}

	archive := legacyV18CutoverOriginalArchive{
		MigrationID: candidate.MigrationID,
		Directory:   legacyV18CutoverOriginalArchiveDir(homeDir, candidate.MigrationID),
		Path:        legacyV18CutoverOriginalArchivePath(homeDir, candidate.MigrationID),
		SHA256:      candidate.SHA256,
	}
	if err := ensureLegacyV18CutoverArchiveDirectory(archive.Directory); err != nil {
		return legacyV18CutoverOriginalArchive{}, err
	}
	if info, err := os.Lstat(archive.Path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return legacyV18CutoverOriginalArchive{}, fmt.Errorf("legacy v18 cutover original archive %s is not a direct regular file", archive.Path)
		}
		digest, err := legacyV18CutoverFileSHA256(archive.Path)
		if err != nil {
			return legacyV18CutoverOriginalArchive{}, fmt.Errorf("hash existing legacy v18 cutover original archive: %w", err)
		}
		if digest != candidate.SHA256 {
			return legacyV18CutoverOriginalArchive{}, fmt.Errorf("existing legacy v18 cutover original archive digest=%s, want %s", digest, candidate.SHA256)
		}
		if err := syncLegacyV18CutoverFile(archive.Path); err != nil {
			return legacyV18CutoverOriginalArchive{}, fmt.Errorf("flush existing legacy v18 cutover original archive: %w", err)
		}
		if err := syncLegacyV18CutoverDirectoryParent(archive.Path); err != nil {
			return legacyV18CutoverOriginalArchive{}, fmt.Errorf("flush existing legacy v18 cutover original archive directory: %w", err)
		}
		digest, err = legacyV18CutoverFileSHA256(archive.Path)
		if err != nil {
			return legacyV18CutoverOriginalArchive{}, fmt.Errorf("reopen and hash existing legacy v18 cutover original archive: %w", err)
		}
		if digest != candidate.SHA256 {
			return legacyV18CutoverOriginalArchive{}, fmt.Errorf("existing legacy v18 cutover original archive changed after flush: digest=%s, want %s", digest, candidate.SHA256)
		}
		return archive, nil
	} else if !os.IsNotExist(err) {
		return legacyV18CutoverOriginalArchive{}, fmt.Errorf("inspect legacy v18 cutover original archive: %w", err)
	}

	if err := syncLegacyV18CutoverFile(candidate.Path); err != nil {
		return legacyV18CutoverOriginalArchive{}, fmt.Errorf("flush legacy v18 cutover archive candidate before promotion: %w", err)
	}
	if err := publishLegacyV18CutoverOriginalArchive(candidate.Path, archive.Path); err != nil {
		return legacyV18CutoverOriginalArchive{}, fmt.Errorf("publish legacy v18 cutover original archive: %w", err)
	}
	if err := requireLegacyV18CutoverDirectRegularFile(archive.Path, "original archive"); err != nil {
		return legacyV18CutoverOriginalArchive{}, err
	}
	publishedDigest, err := legacyV18CutoverFileSHA256(archive.Path)
	if err != nil {
		return legacyV18CutoverOriginalArchive{}, fmt.Errorf("reopen and hash published legacy v18 cutover original archive: %w", err)
	}
	if publishedDigest != candidate.SHA256 {
		return legacyV18CutoverOriginalArchive{}, fmt.Errorf("published legacy v18 cutover original archive digest=%s, want %s", publishedDigest, candidate.SHA256)
	}
	return archive, nil
}

func validateLegacyV18CutoverMigrationID(value string) error {
	prefix := legacyV18CutoverMigrationIdentityVersion + "-"
	if !strings.HasPrefix(value, prefix) {
		return fmt.Errorf("migration identity must start with %q", prefix)
	}
	digest := strings.TrimPrefix(value, prefix)
	if len(digest) != 64 || digest != strings.ToLower(digest) {
		return fmt.Errorf("migration identity digest must be exactly 64 lowercase hex characters")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("migration identity digest is not hexadecimal: %w", err)
	}
	return nil
}

func ensureLegacyV18CutoverArchiveDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create legacy v18 cutover archive directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect legacy v18 cutover archive directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("legacy v18 cutover archive directory %s is not a direct directory", path)
	}
	return syncLegacyV18CutoverDirectoryParent(path)
}

func requireLegacyV18CutoverDirectRegularFile(path, role string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect legacy v18 cutover %s: %w", role, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("legacy v18 cutover %s %s is not a direct regular file", role, path)
	}
	return nil
}

func syncLegacyV18CutoverFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}
