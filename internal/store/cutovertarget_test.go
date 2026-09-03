package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareCanonicalV19CutoverTargetBuildsFreshExactLockedSchema(t *testing.T) {
	home := t.TempDir()
	migrationID := "v1-" + strings.Repeat("a", 64)

	target, err := prepareCanonicalV19CutoverTarget(home, migrationID)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(home, "state", ".v19-cutover-"+migrationID+"-canonical.db.tmp")
	if target.MigrationID != migrationID || target.Path != wantPath {
		t.Fatalf("target = %#v, want migration=%q path=%q", target, migrationID, wantPath)
	}
	if filepath.Dir(target.Path) != filepath.Dir(Path(home)) {
		t.Fatalf("target directory = %q, want active database sibling directory %q", filepath.Dir(target.Path), filepath.Dir(Path(home)))
	}
	if err := requireLegacyV18CutoverDirectRegularFile(target.Path, "canonical target"); err != nil {
		t.Fatal(err)
	}

	sqlDB, err := open(target.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := validateCanonicalV19Schema(sqlDB); err != nil {
		t.Fatal(err)
	}
	identity, err := inspectCanonicalV19Identity(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Fingerprint != canonicalV19SchemaFingerprint || identity.Tables != canonicalV19TableCount || identity.Indexes != canonicalV19IndexCount || identity.Triggers != canonicalV19TriggerCount {
		t.Fatalf("canonical identity = %#v", identity)
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Lstat(target.Path + suffix); !os.IsNotExist(err) {
			t.Fatalf("unexpected SQLite sidecar %q: %v", target.Path+suffix, err)
		}
	}
}

func TestPrepareCanonicalV19CutoverTargetRefusesExistingDeterministicBytes(t *testing.T) {
	home := t.TempDir()
	migrationID := "v1-" + strings.Repeat("b", 64)
	path := legacyV18CutoverCanonicalTargetPath(home, migrationID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := []byte("stale-or-unknown-target")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := prepareCanonicalV19CutoverTarget(home, migrationID); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing target error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("existing deterministic target was modified: got %q, want %q", after, before)
	}
}

func TestPrepareCanonicalV19CutoverTargetRejectsInvalidMigrationIdentity(t *testing.T) {
	for _, migrationID := range []string{"", "../escape", "v2-" + strings.Repeat("a", 64), "v1-" + strings.Repeat("A", 64)} {
		if _, err := prepareCanonicalV19CutoverTarget(t.TempDir(), migrationID); err == nil {
			t.Fatalf("migration identity %q was accepted", migrationID)
		}
	}
}

func TestPrepareCanonicalV19CutoverTargetRefusesSymlinkAtDeterministicPath(t *testing.T) {
	if testing.Short() {
		t.Skip("symlink behavior is a filesystem safety test")
	}
	home := t.TempDir()
	migrationID := "v1-" + strings.Repeat("c", 64)
	path := legacyV18CutoverCanonicalTargetPath(home, migrationID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, "other.db")
	if err := os.WriteFile(other, []byte("do-not-touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := prepareCanonicalV19CutoverTarget(home, migrationID); err == nil || !strings.Contains(err.Error(), "not a direct regular file") {
		t.Fatalf("symlink target error = %v", err)
	}
	got, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "do-not-touch" {
		t.Fatalf("symlink referent was modified: %q", got)
	}
}
