package store

import (
	"errors"
	"strings"
	"testing"
)

func TestOpenGeneratesAndPreservesFleetIdentity(t *testing.T) {
	home := t.TempDir()

	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	first, err := db.FleetID()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	second, err := db.FleetID()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("FleetID changed across reopen: first=%q second=%q", first, second)
	}
	if len(first) != len("f_")+32 || first[:2] != "f_" {
		t.Fatalf("FleetID = %q, want f_ plus 32 hexadecimal characters", first)
	}
}

func TestCurrentSchemaWithoutFleetIdentityFailsClosed(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`DELETE FROM fleet_identity`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	readOnly, err := OpenReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readOnly.Close() }()
	if _, err := readOnly.FleetID(); !errors.Is(err, ErrFleetIdentityMissing) {
		t.Fatalf("FleetID error = %v, want ErrFleetIdentityMissing", err)
	}

	if _, err := Open(home); !errors.Is(err, ErrFleetIdentityMissing) {
		t.Fatalf("Open error = %v, want ErrFleetIdentityMissing", err)
	}
}

func TestReadOnlyLegacySchemaReportsMissingFleetIdentity(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`DROP TABLE fleet_identity`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := FleetIDReadOnly(home); !errors.Is(err, ErrFleetIdentityMissing) {
		t.Fatalf("FleetIDReadOnly error = %v, want ErrFleetIdentityMissing", err)
	}
}

func TestMigrationGeneratesFleetIdentityOnce(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	legacySchema := strings.Replace(schema, `CREATE TABLE IF NOT EXISTS fleet_identity (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	fleet_id TEXT NOT NULL UNIQUE
);
`, "", 1)
	if _, err := sqlDB.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`PRAGMA user_version = 17`); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	first, err := db.FleetID()
	if err != nil {
		t.Fatal(err)
	}
	if version, err := db.schemaVersion(); err != nil || version != len(migrations) {
		t.Fatalf("schemaVersion = %d, %v, want %d", version, err, len(migrations))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	second, err := db.FleetID()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("migrated FleetID changed across reopen: first=%q second=%q", first, second)
	}
}

func TestMalformedFleetIdentityFailsClosed(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`UPDATE fleet_identity SET fleet_id = 'copied-from-somewhere' WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(home); !errors.Is(err, ErrFleetIdentityInvalid) {
		t.Fatalf("Open error = %v, want ErrFleetIdentityInvalid", err)
	}
}
