package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/filelock"
)

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

	var queryOnly int
	if err := gate.conn.QueryRowContext(context.Background(), `PRAGMA query_only`).Scan(&queryOnly); err != nil {
		_ = gate.Close()
		t.Fatal(err)
	}
	if queryOnly != 1 {
		_ = gate.Close()
		t.Fatalf("gate query_only = %d, want 1", queryOnly)
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
	afterDigest, digestErr := legacyV18CutoverFileSHA256(Path(home))
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	if afterDigest != beforeDigest {
		t.Fatalf("failed cutover gate changed source bytes: before=%s after=%s", beforeDigest, afterDigest)
	}
}
