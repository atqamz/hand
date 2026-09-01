package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
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
