package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// InitializeCanonicalV19 creates a fresh Fleet only in a newly claimed home.
// An existing exact canonical Fleet is read without mutation; every other
// existing target is preserved, including interrupted bootstrap evidence.
func InitializeCanonicalV19(ctx context.Context, homeDir string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if info, err := os.Lstat(homeDir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("canonical init target must be a direct directory")
		}
		state, err := os.Lstat(filepath.Join(homeDir, "state"))
		if err != nil || !state.IsDir() || state.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("canonical init requires an exact canonical Fleet at an existing path; preserving %s", homeDir)
		}
		if err := requireLegacyV18CutoverDirectRegularFile(Path(homeDir), "active database"); err != nil {
			return "", err
		}
		id, canonical, err := canonicalFleetIDReadOnly(homeDir)
		if err == nil && canonical {
			return id, nil
		}
		return "", fmt.Errorf("canonical init requires a new path or an exact canonical Fleet; preserving existing target %s", homeDir)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// Mkdir claims the home before any database is created. Losing creators
	// cannot adopt partial state, mint a second identity, or remove the winner.
	if err := os.Mkdir(homeDir, 0o755); err != nil {
		return "", fmt.Errorf("claim canonical Fleet home: %w", err)
	}
	stateDir := filepath.Join(homeDir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		return "", err
	}
	for _, name := range []string{"projects", "config", "data"} {
		if err := os.Mkdir(filepath.Join(homeDir, name), 0o755); err != nil {
			return "", err
		}
	}
	id, err := newFleetID()
	if err != nil {
		return "", err
	}
	target := filepath.Join(stateDir, ".canonical-init.db")
	sqlDB, err := open(target)
	if err != nil {
		return "", err
	}
	defer func() { _ = sqlDB.Close() }()
	if err := createCanonicalV19Schema(sqlDB); err != nil {
		return "", err
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO fleet(singleton,fleet_id,created_at) VALUES(1,?,?)`, id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return "", fmt.Errorf("initialize canonical Fleet identity: %w", err)
	}
	if _, err := validateCanonicalV19CutoverActiveFleet(sqlDB); err != nil {
		return "", err
	}
	if err := sqlDB.Close(); err != nil {
		return "", err
	}
	if err := syncLegacyV18CutoverFile(target); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := moveLegacyV18CutoverNoReplaceDurable(target, Path(homeDir)); err != nil {
		return "", fmt.Errorf("publish fresh canonical Fleet without replacement: %w", err)
	}
	if err := syncLegacyV18CutoverDirectoryParent(stateDir); err != nil {
		return "", err
	}
	if err := syncLegacyV18CutoverDirectoryParent(homeDir); err != nil {
		return "", err
	}
	return FleetIDReadOnly(homeDir)
}
