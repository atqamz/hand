package store

import (
	"fmt"
	"os"
	"path/filepath"
)

type canonicalV19CutoverTarget struct {
	MigrationID string
	Path        string
}

func legacyV18CutoverCanonicalTargetPath(homeDir, migrationID string) string {
	return filepath.Join(homeDir, "state", ".v19-cutover-"+migrationID+"-canonical.db.tmp")
}

// Creates one fresh, non-authoritative canonical v19 sibling for later import.
// Existing bytes at the deterministic path are never reused or overwritten.
func prepareCanonicalV19CutoverTarget(homeDir, migrationID string) (canonicalV19CutoverTarget, error) {
	if err := validateLegacyV18CutoverMigrationID(migrationID); err != nil {
		return canonicalV19CutoverTarget{}, fmt.Errorf("prepare canonical v19 cutover target: %w", err)
	}
	target := canonicalV19CutoverTarget{
		MigrationID: migrationID,
		Path:        legacyV18CutoverCanonicalTargetPath(homeDir, migrationID),
	}
	if info, err := os.Lstat(target.Path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return canonicalV19CutoverTarget{}, fmt.Errorf("prepare canonical v19 cutover target: existing target %s is not a direct regular file", target.Path)
		}
		return canonicalV19CutoverTarget{}, fmt.Errorf("prepare canonical v19 cutover target: deterministic target %s already exists", target.Path)
	} else if !os.IsNotExist(err) {
		return canonicalV19CutoverTarget{}, fmt.Errorf("prepare canonical v19 cutover target: inspect deterministic target: %w", err)
	}

	sqlDB, err := open(target.Path)
	if err != nil {
		return canonicalV19CutoverTarget{}, fmt.Errorf("prepare canonical v19 cutover target: %w", err)
	}
	keep := false
	closed := false
	defer func() {
		if !closed {
			_ = sqlDB.Close()
		}
		if !keep {
			_ = os.Remove(target.Path)
			_ = os.Remove(target.Path + "-journal")
			_ = os.Remove(target.Path + "-wal")
			_ = os.Remove(target.Path + "-shm")
		}
	}()
	if err := createCanonicalV19Schema(sqlDB); err != nil {
		return canonicalV19CutoverTarget{}, fmt.Errorf("prepare canonical v19 cutover target: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		closed = true
		return canonicalV19CutoverTarget{}, fmt.Errorf("prepare canonical v19 cutover target: close created target: %w", err)
	}
	closed = true
	if err := requireLegacyV18CutoverDirectRegularFile(target.Path, "canonical target"); err != nil {
		return canonicalV19CutoverTarget{}, err
	}
	if err := syncLegacyV18CutoverFile(target.Path); err != nil {
		return canonicalV19CutoverTarget{}, fmt.Errorf("prepare canonical v19 cutover target: flush target: %w", err)
	}
	if err := syncLegacyV18CutoverDirectoryParent(target.Path); err != nil {
		return canonicalV19CutoverTarget{}, fmt.Errorf("prepare canonical v19 cutover target: flush target directory: %w", err)
	}

	reopened, err := open(target.Path)
	if err != nil {
		return canonicalV19CutoverTarget{}, fmt.Errorf("prepare canonical v19 cutover target: reopen target: %w", err)
	}
	if err := validateCanonicalV19Schema(reopened); err != nil {
		_ = reopened.Close()
		return canonicalV19CutoverTarget{}, fmt.Errorf("prepare canonical v19 cutover target: revalidate reopened target: %w", err)
	}
	if err := reopened.Close(); err != nil {
		return canonicalV19CutoverTarget{}, fmt.Errorf("prepare canonical v19 cutover target: close revalidated target: %w", err)
	}
	keep = true
	return target, nil
}
