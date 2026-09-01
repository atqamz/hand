package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
)

func TestCutoverWriterGateCanReserveWithoutMutatingLegacyDB(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	beforeDigest := sha256.Sum256(before)

	path := Path(home)
	escaped := (&url.URL{Path: path}).EscapedPath()
	gateDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=rw&_pragma=busy_timeout(0)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	gateDB.SetMaxOpenConns(1)
	defer func() { _ = gateDB.Close() }()

	ctx := context.Background()
	gate, err := gateDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Close() }()

	if _, err := gate.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("BEGIN IMMEDIATE = %v; cannot reserve legacy writer slot", err)
	}
	defer func() { _, _ = gate.ExecContext(ctx, `ROLLBACK`) }()
	if _, err := gate.ExecContext(ctx, `PRAGMA query_only = 1`); err != nil {
		t.Fatalf("enable query_only after reserving writer slot: %v", err)
	}

	var queryOnly int
	if err := gate.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil {
		t.Fatal(err)
	}
	if queryOnly != 1 {
		t.Fatalf("PRAGMA query_only = %d, want 1", queryOnly)
	}
	if _, err := gate.ExecContext(ctx, `CREATE TABLE forbidden_cutover_write (id INTEGER)`); err == nil {
		t.Fatal("query_only connection allowed a source mutation")
	}

	writerDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=rw&_pragma=busy_timeout(0)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	writerDB.SetMaxOpenConns(1)
	defer func() { _ = writerDB.Close() }()
	writer, err := writerDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if _, err := writer.ExecContext(ctx, `BEGIN IMMEDIATE`); !isSQLiteBusy(err) {
		if err == nil {
			_, _ = writer.ExecContext(ctx, `ROLLBACK`)
		}
		t.Fatalf("second writer BEGIN IMMEDIATE = %v, want SQLITE_BUSY while cutover gate is held", err)
	}

	if _, err := gate.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if afterDigest := sha256.Sum256(after); afterDigest != beforeDigest {
		t.Fatalf("legacy DB bytes changed while acquiring/releasing writer gate: before=%x after=%x", beforeDigest, afterDigest)
	}
}

func TestFrozenLegacyBridgeBlocksPreparedStaleWriter(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	path := Path(home)
	escaped := (&url.URL{Path: path}).EscapedPath()
	staleDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=rw&_pragma=busy_timeout(0)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	staleDB.SetMaxOpenConns(1)
	defer func() { _ = staleDB.Close() }()
	staleWrite, err := staleDB.Prepare(`INSERT INTO meta (key, value) VALUES ('stale-writer', 'committed')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = staleWrite.Close() }()

	gateDB, err := sql.Open("sqlite", "file:"+escaped+"?mode=rw&_pragma=busy_timeout(0)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	gateDB.SetMaxOpenConns(1)
	defer func() { _ = gateDB.Close() }()
	ctx := context.Background()
	gate, err := gateDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Close() }()
	if _, err := gate.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = gate.ExecContext(ctx, `ROLLBACK`) }()

	// Source identity is frozen only after the writer reservation exists. The
	// byte-exact original archive must therefore be read and digested here, not
	// during an optimistic preflight another process could race.
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeDigest := sha256.Sum256(before)
	archivePath := path + ".original"
	if err := os.WriteFile(archivePath, before, 0o600); err != nil {
		t.Fatal(err)
	}
	archived, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if archivedDigest := sha256.Sum256(archived); archivedDigest != beforeDigest {
		t.Fatalf("original archive digest = %x, want %x", archivedDigest, beforeDigest)
	}

	tables := []string{"task", "attempt", "fleet_identity", "meta", "hold", "project", "send_attempt"}
	operations := []string{"INSERT", "UPDATE", "DELETE"}
	for _, table := range tables {
		for _, operation := range operations {
			name := fmt.Sprintf("v19_freeze_%s_%s", table, operation)
			statement := fmt.Sprintf(`CREATE TRIGGER %q BEFORE %s ON %q BEGIN SELECT RAISE(ABORT, 'legacy source frozen for v19 cutover'); END`, name, operation, table)
			if _, err := gate.ExecContext(ctx, statement); err != nil {
				t.Fatalf("create %s: %v", name, err)
			}
		}
	}
	if _, err := gate.ExecContext(ctx, `PRAGMA user_version = 22`); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatal(err)
	}

	if _, err := staleWrite.Exec(); err == nil {
		t.Fatal("prepared pre-freeze legacy write succeeded after frozen bridge commit")
	}
	var staleRows int
	if err := staleDB.QueryRow(`SELECT COUNT(*) FROM meta WHERE key = 'stale-writer'`).Scan(&staleRows); err != nil {
		t.Fatal(err)
	}
	if staleRows != 0 {
		t.Fatalf("stale writer rows = %d, want 0", staleRows)
	}

	if _, err := Open(home); !errors.Is(err, ErrSchemaNewer) {
		t.Fatalf("Open frozen bridge = %v, want ErrSchemaNewer for a v0.7.2-style binary", err)
	}
}
