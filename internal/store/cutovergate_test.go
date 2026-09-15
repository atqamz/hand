package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/filelock"
)

func TestOpenLegacyV18CutoverPinnedSourceRejectsHardLinkAlias(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	if err := os.Link(Path(home), filepath.Join(home, "source-alias.db")); err != nil {
		t.Fatal(err)
	}
	source, err := openLegacyV18CutoverPinnedSource(Path(home))
	if source != nil {
		_ = source.Close()
		t.Fatal("pinned source accepted a hard-link alias")
	}
	if err == nil {
		t.Fatal("pinned source accepted a hard-link alias without error")
	}
}

func TestLegacyV18CutoverPinnedSourceRejectsIdentityAndSizeDrift(t *testing.T) {
	t.Run("alias added while pinned", func(t *testing.T) {
		home := createLegacyV18CutoverTestSource(t)
		source := openLegacyV18CutoverTestPinnedSource(t, Path(home))
		alias := filepath.Join(home, "source-alias.db")
		if err := os.Link(Path(home), alias); err != nil {
			t.Fatal(err)
		}
		if _, err := source.sha256(); err == nil {
			t.Fatal("pinned source accepted an alias added after open")
		}
	})

	t.Run("path replacement", func(t *testing.T) {
		home := createLegacyV18CutoverTestSource(t)
		path := Path(home)
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := openLegacyV18CutoverTestPinnedSource(t, path)
		replacement := filepath.Join(home, "replacement.db")
		if err := os.WriteFile(replacement, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Skipf("platform refused replacement while source was pinned: %v", err)
		}
		if _, err := source.sha256(); err == nil {
			t.Fatal("pinned source silently retargeted to replacement")
		}
	})

	t.Run("size drift", func(t *testing.T) {
		home := createLegacyV18CutoverTestSource(t)
		path := Path(home)
		source := openLegacyV18CutoverTestPinnedSource(t, path)
		if err := os.Truncate(path, source.size-1); err != nil {
			t.Fatal(err)
		}
		if _, err := source.sha256(); err == nil {
			t.Fatal("pinned source accepted size drift")
		}
	})
}

func TestOpenLegacyV18CutoverPinnedSourceRejectsIndirectAndNonRegularPaths(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		home := createLegacyV18CutoverTestSource(t)
		symlink := filepath.Join(home, "source-symlink.db")
		if err := os.Symlink(Path(home), symlink); err != nil {
			t.Skipf("platform cannot create test symlink: %v", err)
		}
		if source, err := openLegacyV18CutoverPinnedSource(symlink); err == nil {
			_ = source.Close()
			t.Fatal("pinned source accepted a symlink")
		}
	})

	t.Run("directory", func(t *testing.T) {
		home := createLegacyV18CutoverTestSource(t)
		if source, err := openLegacyV18CutoverPinnedSource(home); err == nil {
			_ = source.Close()
			t.Fatal("pinned source accepted a directory")
		}
	})
}

func TestLegacyV18CutoverPinnedSourceCloseIsIdempotent(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	source, err := openLegacyV18CutoverPinnedSource(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := source.sha256(); err == nil {
		t.Fatal("closed pinned source remained readable")
	}
}

func TestAcquireLegacyV18CutoverGateHoldsExactExclusiveSource(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	beforeDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}

	gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if gate.info.Family != SchemaFamilyLegacyV18 {
		_ = gate.Close()
		t.Fatalf("gate family = %q, want %q", gate.info.Family, SchemaFamilyLegacyV18)
	}
	if gate.info.LayoutFingerprint != legacyV072LayoutFingerprint {
		_ = gate.Close()
		t.Fatalf("gate layout fingerprint = %s, want %s", gate.info.LayoutFingerprint, legacyV072LayoutFingerprint)
	}
	if gate.sourceSHA256 != beforeDigest {
		_ = gate.Close()
		t.Fatalf("gate source digest = %s, want %s", gate.sourceSHA256, beforeDigest)
	}
	if gate.source == nil {
		_ = gate.Close()
		t.Fatal("gate did not retain pinned source ownership")
	}
	if gate.readDB == nil || gate.probeDB == nil {
		_ = gate.Close()
		t.Fatalf("gate did not retain auxiliary SQLite handles: readDB=%p probeDB=%p", gate.readDB, gate.probeDB)
	}
	retainedSource := gate.source
	retainedReadDB := gate.readDB
	retainedProbeDB := gate.probeDB
	retainedReadConn := gate.readConn
	retainedProbeConn := gate.probeConn

	var queryOnly int
	if err := gate.conn.QueryRowContext(context.Background(), `PRAGMA query_only`).Scan(&queryOnly); err != nil {
		_ = gate.Close()
		t.Fatal(err)
	}
	if queryOnly != 1 {
		_ = gate.Close()
		t.Fatalf("gate query_only = %d, want 1", queryOnly)
	}
	if _, err := gate.conn.ExecContext(context.Background(), `CREATE TABLE forbidden_cutover_write (id INTEGER)`); err == nil {
		_ = gate.Close()
		t.Fatal("query-only cutover gate allowed a source mutation")
	}

	migrationRelease, lockErr := Lock(home, MigrationLock, true)
	if !errors.Is(lockErr, filelock.ErrBusy) {
		if lockErr == nil {
			migrationRelease()
		}
		_ = gate.Close()
		t.Fatalf("MigrationLock while cutover gate is held = %v, want filelock.ErrBusy", lockErr)
	}

	reader, err := openLegacyV18CutoverSQLite(Path(home), "ro", 0, true)
	if err != nil {
		_ = gate.Close()
		t.Fatal(err)
	}
	var objects int
	readErr := reader.QueryRow(`SELECT COUNT(*) FROM sqlite_schema`).Scan(&objects)
	_ = reader.Close()
	if !isSQLiteBusy(readErr) {
		_ = gate.Close()
		t.Fatalf("fresh reader under cutover EXCLUSIVE = %v, want SQLITE_BUSY", readErr)
	}

	writer, err := openLegacyV18CutoverSQLite(Path(home), "rw", 0, false)
	if err != nil {
		_ = gate.Close()
		t.Fatal(err)
	}
	_, writeErr := writer.Exec(`BEGIN IMMEDIATE`)
	_ = writer.Close()
	if !isSQLiteBusy(writeErr) {
		_ = gate.Close()
		t.Fatalf("fresh writer under cutover EXCLUSIVE = %v, want SQLITE_BUSY", writeErr)
	}

	if err := gate.Close(); err != nil {
		t.Fatal(err)
	}
	if retainedSource.file != nil {
		t.Fatal("gate Close left pinned source open after SQL teardown")
	}
	if err := retainedReadDB.Ping(); err == nil {
		t.Fatal("gate Close left retained read database open")
	}
	if err := retainedProbeDB.Ping(); err == nil {
		t.Fatal("gate Close left retained probe database open")
	}
	if err := retainedReadConn.PingContext(context.Background()); err == nil {
		t.Fatal("gate Close left retained read connection open")
	}
	if err := retainedProbeConn.PingContext(context.Background()); err == nil {
		t.Fatal("gate Close left retained probe connection open")
	}
	if err := gate.Close(); err != nil {
		t.Fatalf("repeated gate Close: %v", err)
	}
	migrationRelease, err = Lock(home, MigrationLock, true)
	if err != nil {
		t.Fatalf("MigrationLock after cutover gate close = %v, want success", err)
	}
	migrationRelease()

	reader, err = openLegacyV18CutoverSQLite(Path(home), "ro", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.QueryRow(`SELECT COUNT(*) FROM sqlite_schema`).Scan(&objects); err != nil {
		_ = reader.Close()
		t.Fatalf("fresh reader after cutover gate close: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	afterDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if afterDigest != beforeDigest {
		t.Fatalf("cutover gate changed source bytes: before=%s after=%s", beforeDigest, afterDigest)
	}
}

func TestAcquireLegacyV18CutoverGateRequiresMigrationLock(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	release, err := Lock(home, MigrationLock, true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	_, err = acquireLegacyV18CutoverGateWithTimeout(context.Background(), home, time.Second)
	if !errors.Is(err, filelock.ErrBusy) {
		t.Fatalf("acquireLegacyV18CutoverGateWithTimeout error = %v, want filelock.ErrBusy", err)
	}
}

func TestAcquireLegacyV18CutoverGateFailureReleasesResources(t *testing.T) {
	t.Run("canceled context", func(t *testing.T) {
		home := createLegacyV18CutoverTestSource(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if gate, err := acquireLegacyV18CutoverGateWithTimeout(ctx, home, time.Second); err == nil {
			_ = gate.Close()
			t.Fatal("cutover gate accepted a canceled context")
		}
		release, err := Lock(home, MigrationLock, true)
		if err != nil {
			t.Fatalf("MigrationLock after canceled acquisition: %v", err)
		}
		release()
	})

	t.Run("archive candidate failure", func(t *testing.T) {
		home := createLegacyV18CutoverTestSource(t)
		setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
		gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
		if err != nil {
			t.Fatal(err)
		}
		candidatePath := gate.archiveCandidate.Path
		if err := gate.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(candidatePath); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(candidatePath, 0o700); err != nil {
			t.Fatal(err)
		}
		if gate, err := acquireLegacyV18CutoverGateWithTimeout(context.Background(), home, 2*time.Second); err == nil {
			_ = gate.Close()
			t.Fatal("cutover gate accepted a non-regular archive candidate")
		}
		release, err := Lock(home, MigrationLock, true)
		if err != nil {
			t.Fatalf("MigrationLock after candidate failure: %v", err)
		}
		release()
	})
}

func TestAcquireLegacyV18CutoverGateDoesNotTreatReaderBarrierAsOwnership(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	beforeDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}

	foreignDB, err := openLegacyV18CutoverSQLite(Path(home), "ro", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = foreignDB.Close() }()
	foreignTx, err := foreignDB.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = foreignTx.Rollback() }()
	var objects int
	if err := foreignTx.QueryRow(`SELECT COUNT(*) FROM sqlite_schema`).Scan(&objects); err != nil {
		t.Fatal(err)
	}

	_, err = acquireLegacyV18CutoverGateWithTimeout(context.Background(), home, 2*time.Second)
	if err == nil {
		t.Fatal("acquireLegacyV18CutoverGateWithTimeout succeeded while foreign SHARED reader remained live")
	}
	if !strings.Contains(err.Error(), "EXCLUSIVE gate") {
		t.Fatalf("gate error = %v, want failure after reader-barrier observation while OUR EXCLUSIVE remained blocked", err)
	}
	release, lockErr := Lock(home, MigrationLock, true)
	if lockErr != nil {
		t.Fatalf("MigrationLock after failed EXCLUSIVE acquisition: %v", lockErr)
	}
	release()
	afterDigest, digestErr := legacyV18CutoverFileSHA256(Path(home))
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	if afterDigest != beforeDigest {
		t.Fatalf("failed cutover gate changed source bytes: before=%s after=%s", beforeDigest, afterDigest)
	}
}
