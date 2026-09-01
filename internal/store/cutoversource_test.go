package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"testing"
)

func TestValidateLegacyV18CutoverSourceAcceptsPinnedExactRollbackSource(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")

	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	tx, err := db.sql.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	info, err := validateLegacyV18CutoverSource(tx)
	if err != nil {
		t.Fatalf("validateLegacyV18CutoverSource = %+v, error = %v", info, err)
	}
	if info.Family != SchemaFamilyLegacyV18 {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyLegacyV18)
	}
	if info.LayoutFingerprint != legacyV072LayoutFingerprint {
		t.Fatalf("layout fingerprint = %s, want %s", info.LayoutFingerprint, legacyV072LayoutFingerprint)
	}
}

func TestValidateLegacyV18CutoverSourceRejectsMemoryJournal(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	sqlDB := openLegacyV18CutoverTestDB(t, home, true)
	defer func() { _ = sqlDB.Close() }()

	var journalMode string
	if err := sqlDB.QueryRow(`PRAGMA journal_mode = MEMORY`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "memory" {
		t.Fatalf("journal_mode = %q, want memory", journalMode)
	}

	ctx := context.Background()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only = 1`); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = validateLegacyV18CutoverSource(tx)
	if !errors.Is(err, errLegacyV18CutoverSourceUnsafe) {
		t.Fatalf("validateLegacyV18CutoverSource error = %v, want errLegacyV18CutoverSourceUnsafe", err)
	}
}

func TestValidateLegacyV18CutoverSourceRejectsQueryOnlyDisabled(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	sqlDB := openLegacyV18CutoverTestDB(t, home, true)
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = validateLegacyV18CutoverSource(tx)
	if !errors.Is(err, errLegacyV18CutoverSourceUnsafe) {
		t.Fatalf("validateLegacyV18CutoverSource error = %v, want errLegacyV18CutoverSourceUnsafe", err)
	}
}

func TestValidateLegacyV18CutoverSourceRejectsForeignKeysDisabled(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	sqlDB := openLegacyV18CutoverTestDB(t, home, false)
	defer func() { _ = sqlDB.Close() }()

	ctx := context.Background()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only = 1`); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = validateLegacyV18CutoverSource(tx)
	if !errors.Is(err, errLegacyV18CutoverSourceUnsafe) {
		t.Fatalf("validateLegacyV18CutoverSource error = %v, want errLegacyV18CutoverSourceUnsafe", err)
	}
}

func createLegacyV18CutoverTestSource(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return home
}

func setLegacyV18CutoverTestJournalMode(t *testing.T, home, mode string) {
	t.Helper()
	sqlDB := openLegacyV18CutoverTestDB(t, home, true)
	defer func() { _ = sqlDB.Close() }()
	var got string
	if err := sqlDB.QueryRow(`PRAGMA journal_mode = ` + mode).Scan(&got); err != nil {
		t.Fatal(err)
	}
}

func openLegacyV18CutoverTestDB(t *testing.T, home string, foreignKeys bool) *sql.DB {
	t.Helper()
	foreignKeysValue := "0"
	if foreignKeys {
		foreignKeysValue = "1"
	}
	uri := "file:" + (&url.URL{Path: Path(home)}).EscapedPath() +
		"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(" + foreignKeysValue + ")"
	sqlDB, err := sql.Open("sqlite", uri)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	return sqlDB
}
