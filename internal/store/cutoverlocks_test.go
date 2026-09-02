package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/filelock"
)

func TestAcquireLegacyV18CutoverLocksHoldsClosedWorldFleetLocks(t *testing.T) {
	home, worktreePath := createLegacyV18CutoverLockTestSource(t)
	beforeDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}

	unknownRelease, err := Lock(home, "historical:unknown-rendezvous", true)
	if err != nil {
		t.Fatal(err)
	}
	unknownRelease()

	gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	locks, err := acquireLegacyV18CutoverLocks(context.Background(), home, gate)
	if err != nil {
		_ = gate.Close()
		t.Fatal(err)
	}

	logicalKeys := []string{
		"config:routing",
		"completions",
		SchemaLock,
		"task:task-one",
		"send:task-one",
		"project:alpha",
		"worktree:" + worktreePath,
		"historical:unknown-rendezvous",
	}
	for _, key := range logicalKeys {
		if release, err := Lock(home, key, true); !errors.Is(err, filelock.ErrBusy) {
			if err == nil {
				release()
			}
			_ = locks.Close()
			_ = gate.Close()
			t.Fatalf("Lock(%q) while cutover lock closure held = %v, want filelock.ErrBusy", key, err)
		}
	}

	assertLegacyV18CutoverFixedLockBusy(t, filepath.Join(Dir(home), "watch.pid.lock"), 0o644)
	assertLegacyV18CutoverFixedLockBusy(t, filepath.Join(home, "data", "projects.md.lock"), 0o600)

	if release, err := Lock(home, MigrationLock, true); !errors.Is(err, filelock.ErrBusy) {
		if err == nil {
			release()
		}
		_ = locks.Close()
		_ = gate.Close()
		t.Fatalf("MigrationLock while lock closure held = %v, want filelock.ErrBusy", err)
	}

	if err := locks.Close(); err != nil {
		_ = gate.Close()
		t.Fatal(err)
	}
	for _, key := range logicalKeys {
		release, err := Lock(home, key, true)
		if err != nil {
			_ = gate.Close()
			t.Fatalf("Lock(%q) after lock closure Close = %v", key, err)
		}
		release()
	}
	assertLegacyV18CutoverFixedLockAvailable(t, filepath.Join(Dir(home), "watch.pid.lock"), 0o644)
	assertLegacyV18CutoverFixedLockAvailable(t, filepath.Join(home, "data", "projects.md.lock"), 0o600)

	if release, err := Lock(home, MigrationLock, true); !errors.Is(err, filelock.ErrBusy) {
		if err == nil {
			release()
		}
		_ = gate.Close()
		t.Fatalf("MigrationLock after child lock closure Close = %v, want parent 5A2 gate still to hold it", err)
	}
	if err := gate.Close(); err != nil {
		t.Fatal(err)
	}
	releaseMigration, err := Lock(home, MigrationLock, true)
	if err != nil {
		t.Fatalf("MigrationLock after parent gate Close = %v", err)
	}
	releaseMigration()

	afterDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if afterDigest != beforeDigest {
		t.Fatalf("5A3 lock closure changed source DB bytes: before=%s after=%s", beforeDigest, afterDigest)
	}
}

func TestAcquireLegacyV18CutoverLocksRejectsBusyDerivedProjectLock(t *testing.T) {
	home, _ := createLegacyV18CutoverLockTestSource(t)
	holder, err := Lock(home, "project:alpha", true)
	if err != nil {
		t.Fatal(err)
	}
	defer holder()

	gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Close() }()

	locks, err := acquireLegacyV18CutoverLocks(context.Background(), home, gate)
	if locks != nil {
		_ = locks.Close()
		t.Fatal("lock closure succeeded while a derived project lock was live")
	}
	if !errors.Is(err, errLegacyV18CutoverLocksUnsafe) {
		t.Fatalf("lock closure error = %v, want errLegacyV18CutoverLocksUnsafe", err)
	}
}

func TestAcquireLegacyV18CutoverLocksRejectsBusyWatcherLock(t *testing.T) {
	home, _ := createLegacyV18CutoverLockTestSource(t)
	watchPath := filepath.Join(Dir(home), "watch.pid.lock")
	holder := holdLegacyV18CutoverFixedLock(t, watchPath, 0o644)
	defer holder()

	gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Close() }()

	locks, err := acquireLegacyV18CutoverLocks(context.Background(), home, gate)
	if locks != nil {
		_ = locks.Close()
		t.Fatal("lock closure succeeded while watcher ownership lock was live")
	}
	if !errors.Is(err, errLegacyV18CutoverLocksUnsafe) {
		t.Fatalf("lock closure error = %v, want errLegacyV18CutoverLocksUnsafe", err)
	}
}

func TestAcquireLegacyV18CutoverLocksRejectsBusyProjectRegistryLock(t *testing.T) {
	home, _ := createLegacyV18CutoverLockTestSource(t)
	registryPath := filepath.Join(home, "data", "projects.md.lock")
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	holder := holdLegacyV18CutoverFixedLock(t, registryPath, 0o600)
	defer holder()

	gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Close() }()

	locks, err := acquireLegacyV18CutoverLocks(context.Background(), home, gate)
	if locks != nil {
		_ = locks.Close()
		t.Fatal("lock closure succeeded while project registry lock was live")
	}
	if !errors.Is(err, errLegacyV18CutoverLocksUnsafe) {
		t.Fatalf("lock closure error = %v, want errLegacyV18CutoverLocksUnsafe", err)
	}
}

func TestAcquireLegacyV18CutoverLocksRequiresLiveExclusiveGate(t *testing.T) {
	home, _ := createLegacyV18CutoverLockTestSource(t)
	locks, err := acquireLegacyV18CutoverLocks(context.Background(), home, nil)
	if locks != nil {
		_ = locks.Close()
		t.Fatal("lock closure succeeded without a 5A2 EXCLUSIVE gate")
	}
	if !errors.Is(err, errLegacyV18CutoverLocksUnsafe) {
		t.Fatalf("lock closure error = %v, want errLegacyV18CutoverLocksUnsafe", err)
	}
}

func createLegacyV18CutoverLockTestSource(t *testing.T) (string, string) {
	t.Helper()
	home := createLegacyV18CutoverTestSource(t)
	worktreePath := filepath.Join(home, "treehouses", "task-one")
	db := openLegacyV18CutoverTestDB(t, home, true)
	if _, err := db.Exec(`INSERT INTO project (id, name, url, mode, position) VALUES ('project-id-alpha', 'alpha', 'https://example.invalid/alpha.git', 'direct-pr', 1)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO task (id, project, project_id, lifecycle) VALUES ('task-one', 'alpha', 'project-id-alpha', 'terminal')`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO attempt (task_id, ordinal, lifecycle, worktree) VALUES ('task-one', 1, 'completed', ?)`, worktreePath); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	return home, worktreePath
}

func holdLegacyV18CutoverFixedLock(t *testing.T, path string, perm os.FileMode) func() {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, perm)
	if err != nil {
		t.Fatal(err)
	}
	if err := filelock.Lock(file, false); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	return func() {
		_ = filelock.Unlock(file)
		_ = file.Close()
	}
}

func assertLegacyV18CutoverFixedLockBusy(t *testing.T, path string, perm os.FileMode) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, perm)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := filelock.Lock(file, false); !errors.Is(err, filelock.ErrBusy) {
		if err == nil {
			_ = filelock.Unlock(file)
		}
		t.Fatalf("fixed lock %s while cutover lock closure held = %v, want filelock.ErrBusy", path, err)
	}
}

func assertLegacyV18CutoverFixedLockAvailable(t *testing.T, path string, perm os.FileMode) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, perm)
	if err != nil {
		t.Fatal(err)
	}
	if err := filelock.Lock(file, false); err != nil {
		_ = file.Close()
		t.Fatalf("fixed lock %s after cutover lock closure Close = %v", path, err)
	}
	_ = filelock.Unlock(file)
	_ = file.Close()
}
