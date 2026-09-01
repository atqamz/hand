package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestCutoverPendingExclusiveHandoffSurvivesHotJournal(t *testing.T) {
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

	// Establish a genuine read-only SHARED snapshot. A rollback-journal writer
	// may reserve while this lives, but cannot commit main-database changes.
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
		t.Fatalf("read snapshot saw %q, want clean", clean)
	}

	// Race a supported old writer after the snapshot. It obtains RESERVED and
	// creates rollback-journal state, then crashes before it can get EXCLUSIVE.
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

	mainBeforeGate, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mainDigest := sha256.Sum256(mainBeforeGate)
	journalBeforeGate, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	journalDigest := sha256.Sum256(journalBeforeGate)

	// Ask SQLite for EXCLUSIVE while the known SHARED snapshot remains held.
	// A successful waiting writer must first reach PENDING, which blocks new
	// SHARED readers before the original reader is deliberately released.
	gateDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=rw&_pragma=busy_timeout(30000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	gateDB.SetMaxOpenConns(1)
	gate, err := gateDB.Conn(ctx)
	if err != nil {
		_ = gateDB.Close()
		t.Fatal(err)
	}
	exclusive := make(chan error, 1)
	go func() {
		_, err := gate.ExecContext(ctx, `BEGIN EXCLUSIVE`)
		exclusive <- err
	}()

	probeDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=ro&_pragma=busy_timeout(0)&_pragma=foreign_keys(1)&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	probeDB.SetMaxOpenConns(1)
	defer func() { _ = probeDB.Close() }()
	deadline = time.Now().Add(5 * time.Second)
	for {
		var value string
		err := probeDB.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'hot-journal-probe'`).Scan(&value)
		if isSQLiteBusy(err) {
			break
		}
		if err != nil {
			t.Fatalf("probe for pending cutover lock: %v", err)
		}
		if value != "clean" {
			t.Fatalf("probe reader saw %q before pending lock, want clean", value)
		}
		select {
		case err := <-exclusive:
			t.Fatalf("BEGIN EXCLUSIVE returned before original SHARED reader was released: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("BEGIN EXCLUSIVE never established a PENDING lock that blocked new readers")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Positive PENDING evidence is the handoff point: future old DB readers and
	// writers are excluded while the original SHARED snapshot still pins bytes.
	mainPending, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(mainPending); got != mainDigest {
		t.Fatalf("pending handoff changed main DB before archive: before=%x after=%x", mainDigest, got)
	}
	journalPending, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("pending handoff removed rollback journal before archive: %v", err)
	}
	if got := sha256.Sum256(journalPending); got != journalDigest {
		t.Fatalf("pending handoff changed rollback journal before archive: before=%x after=%x", journalDigest, got)
	}

	archivePath := path + ".original-candidate"
	if err := os.WriteFile(archivePath, mainPending, 0o600); err != nil {
		t.Fatal(err)
	}
	archived, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(archived); got != mainDigest {
		t.Fatalf("archive candidate digest = %x, want %x", got, mainDigest)
	}

	// Archive is durable and PENDING bars newcomers, so release only the known
	// reader and require the already-waiting gate to become EXCLUSIVE.
	if _, err := readGuard.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if err := readGuard.Close(); err != nil {
		t.Fatal(err)
	}
	if err := readDB.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exclusive:
		if err != nil {
			t.Fatalf("BEGIN EXCLUSIVE after releasing known reader: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("BEGIN EXCLUSIVE did not complete after releasing known reader")
	}
	defer func() { _, _ = gate.ExecContext(ctx, `ROLLBACK`) }()
	if _, err := gate.ExecContext(ctx, `PRAGMA query_only = 1`); err != nil {
		t.Fatalf("enable query_only after exclusive gate: %v", err)
	}

	// EXCLUSIVE must block a fresh legacy read, not just writes. This closes the
	// read-then-external-effect path used by commands such as project add/create.
	freshReadDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=ro&_pragma=busy_timeout(0)&_pragma=foreign_keys(1)&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	freshReadDB.SetMaxOpenConns(1)
	defer func() { _ = freshReadDB.Close() }()
	var fresh string
	if err := freshReadDB.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'hot-journal-probe'`).Scan(&fresh); !isSQLiteBusy(err) {
		t.Fatalf("fresh reader under EXCLUSIVE = value %q, err %v; want SQLITE_BUSY", fresh, err)
	}

	var recovered string
	if err := gate.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'hot-journal-probe'`).Scan(&recovered); err != nil {
		t.Fatalf("query source under exclusive gate: %v", err)
	}
	if recovered != "clean" {
		t.Fatalf("exclusive gate observed %q, want committed value clean", recovered)
	}
	mainBeforeFreeze, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(mainBeforeFreeze); got != mainDigest {
		t.Fatalf("source bytes differ from archive before freeze: archive=%x active=%x", mainDigest, got)
	}

	// Cross the deliberate one-way mutation boundary only after exact archive and
	// final digest proof. The companion test proves the complete 21-guard set.
	if _, err := gate.ExecContext(ctx, `PRAGMA query_only = 0`); err != nil {
		t.Fatalf("disable query_only at freeze boundary: %v", err)
	}
	certificate := fmt.Sprintf("v1:%x", mainDigest)
	if _, err := gate.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('v19-cutover-freeze', ?)`, certificate); err != nil {
		t.Fatalf("write freeze certificate after exclusive handoff: %v", err)
	}
	if _, err := gate.ExecContext(ctx, `PRAGMA user_version = 22`); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatalf("commit freeze boundary after exclusive handoff: %v", err)
	}
	if err := gate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gateDB.Close(); err != nil {
		t.Fatal(err)
	}

	archivedAfterFreeze, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(archivedAfterFreeze); got != mainDigest {
		t.Fatalf("immutable original archive changed after freeze: before=%x after=%x", mainDigest, got)
	}
	verifyDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=ro&_pragma=foreign_keys(1)&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = verifyDB.Close() }()
	var gotCertificate string
	if err := verifyDB.QueryRow(`SELECT value FROM meta WHERE key = 'v19-cutover-freeze'`).Scan(&gotCertificate); err != nil {
		t.Fatal(err)
	}
	if gotCertificate != certificate {
		t.Fatalf("freeze certificate = %q, want %q", gotCertificate, certificate)
	}
	var version int
	if err := verifyDB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 22 {
		t.Fatalf("frozen bridge user_version = %d, want 22", version)
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
