package store

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestFleetIDReadOnlyCanonicalV19(t *testing.T) {
	const fleetID = "f_0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name    string
		change  string
		wantErr error
	}{
		{name: "exact"},
		{name: "schema drift", change: `CREATE TABLE unexpected(id TEXT)`, wantErr: ErrCanonicalV19SchemaMismatch},
		{name: "missing singleton", wantErr: ErrFleetIdentityMissing},
		{name: "invalid identity", change: `INSERT INTO fleet(singleton,fleet_id,created_at) VALUES(1,'invalid','2026-09-23T00:00:00Z')`, wantErr: ErrFleetIdentityInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			db, err := open(Path(home))
			if err != nil {
				t.Fatal(err)
			}
			if err := createCanonicalV19Schema(db); err != nil {
				t.Fatal(err)
			}
			if tc.name != "invalid identity" && tc.name != "missing singleton" {
				if _, err := db.Exec(`INSERT INTO fleet(singleton,fleet_id,created_at) VALUES(1,?,'2026-09-23T00:00:00Z')`, fleetID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.change != "" {
				if _, err := db.Exec(tc.change); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(Path(home))
			if err != nil {
				t.Fatal(err)
			}
			got, err := FleetIDReadOnly(home)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("FleetIDReadOnly = %q, %v; want error %v", got, err, tc.wantErr)
			}
			if tc.wantErr == nil && got != fleetID {
				t.Fatalf("FleetIDReadOnly = %q, want %q", got, fleetID)
			}
			after, err := os.ReadFile(Path(home))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("identity read changed state database")
			}
		})
	}
}

func TestLegacyOpenPreservesCanonicalV19(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	before, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	db, openErr := Open(home)
	if db != nil {
		_ = db.Close()
	}
	if !errors.Is(openErr, ErrCanonicalV19LegacyAccess) {
		t.Fatalf("legacy writer error = %v, want ErrCanonicalV19LegacyAccess", openErr)
	}
	after, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("legacy writer changed canonical database despite error: %v", openErr)
	}
	if err := ValidateInitTarget(home); !errors.Is(err, ErrCanonicalV19LegacyAccess) {
		t.Fatalf("init validation = %v", err)
	}
	readOnly, err := OpenReadOnly(home)
	if readOnly != nil {
		_ = readOnly.Close()
	}
	if !errors.Is(err, ErrCanonicalV19LegacyAccess) {
		t.Fatalf("legacy reader = %v", err)
	}
}

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

func TestReadOnlyMissingDatabaseReportsMissingFleetIdentity(t *testing.T) {
	home := t.TempDir()

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
