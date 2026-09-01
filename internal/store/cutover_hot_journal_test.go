package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestCutoverReadGuardPreventsHotJournalRecovery(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	var journalMode string
	if err := db.sql.QueryRow(`PRAGMA journal_mode = DELETE`).Scan(&journalMode); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if journalMode != "delete" {
		_ = db.Close()
		t.Fatalf("journal_mode = %q, want delete", journalMode)
	}
	if _, err := db.sql.Exec(`INSERT INTO meta (key, value) VALUES ('hot-journal-probe', 'clean')`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	path := Path(home)
	baseline, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	baselineDigest := sha256.Sum256(baseline)
	escaped := (&url.URL{Path: path}).EscapedPath()
	ctx := context.Background()

	// Hold a genuine read-only SHARED transaction before a legacy writer can
	// race the rw-capable cutover gate. A rollback-journal writer may reserve and
	// create its journal, but it cannot modify the main DB while this reader lives.
	readDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=ro&_pragma=busy_timeout(0)&_pragma=foreign_keys(1)&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	readDB.SetMaxOpenConns(1)
	defer func() { _ = readDB.Close() }()
	readGuard, err := readDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readGuard.Close() }()
	if _, err := readGuard.ExecContext(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = readGuard.ExecContext(ctx, `ROLLBACK`) }()
	var clean string
	if err := readGuard.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'hot-journal-probe'`).Scan(&clean); err != nil {
		t.Fatal(err)
	}
	if clean != "clean" {
		t.Fatalf("read guard saw %q, want clean", clean)
	}

	readyPath := path + ".hot-writer-ready"
	cmd := exec.Command(os.Args[0], "-test.run=^TestCutoverHotJournalWriterHelper$")
	cmd.Env = append(os.Environ(),
		"HAND_HOT_JOURNAL_HELPER=1",
		"HAND_HOT_JOURNAL_DB="+path,
		"HAND_HOT_JOURNAL_READY="+readyPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		default:
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("hot-journal writer exited before reserving: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			<-done
			t.Fatal("hot-journal writer did not reserve in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	journalPath := path + "-journal"
	deadline = time.Now().Add(5 * time.Second)
	for {
		if info, err := os.Stat(journalPath); err == nil && info.Size() > 512 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("hot-journal writer exited before creating journal: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			<-done
			t.Fatal("hot-journal writer did not create a rollback journal in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-done

	mainBeforeGate, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(mainBeforeGate); got != baselineDigest {
		t.Fatalf("concurrent writer changed main DB while read guard held: baseline=%x got=%x", baselineDigest, got)
	}
	journalBeforeGate, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	journalDigest := sha256.Sum256(journalBeforeGate)

	// Opening the rw-capable gate in the presence of the now-hot journal would
	// normally trigger rollback recovery. The still-held SHARED guard must make
	// recovery/gate acquisition fail busy instead, without touching either file.
	gateDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=rw&_pragma=busy_timeout(0)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	gateDB.SetMaxOpenConns(1)
	defer func() { _ = gateDB.Close() }()
	gate, err := gateDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Close() }()
	if _, err := gate.ExecContext(ctx, `BEGIN IMMEDIATE`); !isSQLiteBusy(err) {
		if err == nil {
			_, _ = gate.ExecContext(ctx, `ROLLBACK`)
		}
		t.Fatalf("BEGIN IMMEDIATE with hot journal + read guard = %v, want SQLITE_BUSY without recovery", err)
	}

	mainAfterGate, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(mainAfterGate); got != baselineDigest {
		t.Fatalf("failed writer gate changed main DB: baseline=%x got=%x", baselineDigest, got)
	}
	journalAfterGate, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("failed writer gate removed hot journal: %v", err)
	}
	if got := sha256.Sum256(journalAfterGate); got != journalDigest {
		t.Fatalf("failed writer gate changed hot journal: before=%x after=%x", journalDigest, got)
	}

	// Prove the fixture really is a hot journal: once the SHARED guard is gone,
	// the same rw BEGIN IMMEDIATE is allowed to recover it.
	if _, err := readGuard.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if err := readGuard.Close(); err != nil {
		t.Fatal(err)
	}
	if err := readDB.Close(); err != nil {
		t.Fatal(err)
	}

	recoveryDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=rw&_pragma=busy_timeout(0)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	recoveryDB.SetMaxOpenConns(1)
	defer func() { _ = recoveryDB.Close() }()
	recovery, err := recoveryDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = recovery.Close() }()
	if _, err := recovery.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("recover hot journal after releasing read guard: %v", err)
	}
	if _, err := recovery.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("rollback journal still exists after unguarded recovery: %v", err)
	}
}

func TestCutoverHotJournalWriterHelper(t *testing.T) {
	if os.Getenv("HAND_HOT_JOURNAL_HELPER") != "1" {
		return
	}
	path := os.Getenv("HAND_HOT_JOURNAL_DB")
	readyPath := os.Getenv("HAND_HOT_JOURNAL_READY")
	if path == "" || readyPath == "" {
		t.Fatal("hot-journal helper missing path environment")
	}
	escaped := (&url.URL{Path: path}).EscapedPath()
	db, err := sql.Open("sqlite", "file:"+escaped+"?mode=rw&_pragma=busy_timeout(30000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readyPath, []byte("reserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE meta SET value = 'dirty' WHERE key = 'hot-journal-probe'`); err != nil {
		t.Fatal(err)
	}
	// Parent kills this process. Exiting normally would run SQLite rollback and
	// destroy the hot-journal fixture we need to exercise.
	time.Sleep(time.Hour)
}
