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

func TestCutoverArchiveBeforeGateSurvivesHotJournalRecovery(t *testing.T) {
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
	escaped := (&url.URL{Path: path}).EscapedPath()
	ctx := context.Background()

	// First establish a real read-only SHARED snapshot. While it is held, no
	// concurrent rollback-journal writer can commit changes to the main DB file.
	readDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=ro&_pragma=busy_timeout(0)&_pragma=foreign_keys(1)&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	readDB.SetMaxOpenConns(1)
	readGuard, err := readDB.Conn(ctx)
	if err != nil {
		_ = readDB.Close()
		t.Fatal(err)
	}
	if _, err := readGuard.ExecContext(ctx, `BEGIN`); err != nil {
		_ = readGuard.Close()
		_ = readDB.Close()
		t.Fatal(err)
	}
	var clean string
	if err := readGuard.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'hot-journal-probe'`).Scan(&clean); err != nil {
		_ = readGuard.Close()
		_ = readDB.Close()
		t.Fatal(err)
	}
	if clean != "clean" {
		_ = readGuard.Close()
		_ = readDB.Close()
		t.Fatalf("read guard saw %q, want clean", clean)
	}

	// The byte-exact archive candidate is made durable before any rw-capable
	// SQLite gate is opened. The later writer gate must revalidate this digest.
	archived, err := os.ReadFile(path)
	if err != nil {
		_ = readGuard.Close()
		_ = readDB.Close()
		t.Fatal(err)
	}
	archiveDigest := sha256.Sum256(archived)
	archivePath := path + ".original-candidate"
	if err := os.WriteFile(archivePath, archived, 0o600); err != nil {
		_ = readGuard.Close()
		_ = readDB.Close()
		t.Fatal(err)
	}
	archivedAgain, err := os.ReadFile(archivePath)
	if err != nil {
		_ = readGuard.Close()
		_ = readDB.Close()
		t.Fatal(err)
	}
	if got := sha256.Sum256(archivedAgain); got != archiveDigest {
		_ = readGuard.Close()
		_ = readDB.Close()
		t.Fatalf("archive candidate digest = %x, want %x", got, archiveDigest)
	}

	// Race a supported old writer after the archive snapshot. It can obtain a
	// RESERVED lock and create rollback journal state, but the SHARED reader keeps
	// the committed main DB bytes equal to the archive until the reader is released.
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
		_ = readGuard.Close()
		_ = readDB.Close()
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

	mainBeforeRelease, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(mainBeforeRelease); got != archiveDigest {
		t.Fatalf("concurrent writer changed main DB while read snapshot held: archive=%x got=%x", archiveDigest, got)
	}
	if _, err := os.ReadFile(journalPath); err != nil {
		t.Fatalf("expected crashed writer rollback journal: %v", err)
	}

	// The archive is already durable, so it is safe to release the reader and let
	// BEGIN IMMEDIATE perform normal crash recovery if needed. A committed writer
	// in this gap would change hand.db and be caught by the exact digest check below.
	if _, err := readGuard.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if err := readGuard.Close(); err != nil {
		t.Fatal(err)
	}
	if err := readDB.Close(); err != nil {
		t.Fatal(err)
	}

	gateDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=rw&_pragma=busy_timeout(0)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	gateDB.SetMaxOpenConns(1)
	gate, err := gateDB.Conn(ctx)
	if err != nil {
		_ = gateDB.Close()
		t.Fatal(err)
	}
	if _, err := gate.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		_ = gate.Close()
		_ = gateDB.Close()
		t.Fatalf("acquire writer gate after durable archive candidate: %v", err)
	}

	mainAfterGate, err := os.ReadFile(path)
	if err != nil {
		_ = gate.Close()
		_ = gateDB.Close()
		t.Fatal(err)
	}
	if got := sha256.Sum256(mainAfterGate); got != archiveDigest {
		_ = gate.Close()
		_ = gateDB.Close()
		t.Fatalf("writer gate/recovery changed committed source bytes: archive=%x active=%x", archiveDigest, got)
	}
	var recovered string
	if err := gate.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'hot-journal-probe'`).Scan(&recovered); err != nil {
		_ = gate.Close()
		_ = gateDB.Close()
		t.Fatal(err)
	}
	if recovered != "clean" {
		_ = gate.Close()
		_ = gateDB.Close()
		t.Fatalf("writer gate observed recovered value %q, want clean", recovered)
	}
	archivedAfterGate, err := os.ReadFile(archivePath)
	if err != nil {
		_ = gate.Close()
		_ = gateDB.Close()
		t.Fatal(err)
	}
	if got := sha256.Sum256(archivedAfterGate); got != archiveDigest {
		_ = gate.Close()
		_ = gateDB.Close()
		t.Fatalf("archive candidate changed during gate recovery: before=%x after=%x", archiveDigest, got)
	}

	// SQLite may leave a non-hot rollback-journal file behind. File existence is
	// not the safety property. After releasing the writer gate, a new read-only
	// connection must observe the archived committed state without mutating the DB.
	if _, err := gate.ExecContext(ctx, `ROLLBACK`); err != nil {
		_ = gate.Close()
		_ = gateDB.Close()
		t.Fatal(err)
	}
	if err := gate.Close(); err != nil {
		_ = gateDB.Close()
		t.Fatal(err)
	}
	if err := gateDB.Close(); err != nil {
		t.Fatal(err)
	}

	verifyDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=ro&_pragma=busy_timeout(0)&_pragma=foreign_keys(1)&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	verifyDB.SetMaxOpenConns(1)
	defer func() { _ = verifyDB.Close() }()
	var verified string
	if err := verifyDB.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'hot-journal-probe'`).Scan(&verified); err != nil {
		t.Fatalf("read-only verification after writer-gate recovery: %v", err)
	}
	if verified != "clean" {
		t.Fatalf("read-only verification saw %q, want clean", verified)
	}
	mainAfterVerify, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(mainAfterVerify); got != archiveDigest {
		t.Fatalf("post-recovery read-only verification changed source bytes: archive=%x active=%x", archiveDigest, got)
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
