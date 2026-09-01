package store

import (
	"errors"
	"strings"
	"testing"
)

func TestInspectSchemaRejectsLegacyCheckConstraintDrift(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(schema,
		"singleton INTEGER PRIMARY KEY CHECK (singleton = 1)",
		"singleton INTEGER PRIMARY KEY",
		1,
	)
	if drifted == schema {
		_ = sqlDB.Close()
		t.Fatal("test fixture did not remove fleet_identity CHECK constraint")
	}
	if _, err := sqlDB.Exec(drifted); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(sendSchema); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`PRAGMA user_version = 21`); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := InspectSchema(home)
	if !errors.Is(err, ErrUnsupportedLegacyV18Schema) {
		t.Fatalf("InspectSchema = %+v, error = %v, want ErrUnsupportedLegacyV18Schema", info, err)
	}
	if info.Family != SchemaFamilyLegacyV18 {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyLegacyV18)
	}
}

func TestInspectSchemaRejectsLegacyAutoincrementDrift(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(schema,
		"id                     INTEGER PRIMARY KEY AUTOINCREMENT",
		"id                     INTEGER PRIMARY KEY",
		1,
	)
	if drifted == schema {
		_ = sqlDB.Close()
		t.Fatal("test fixture did not remove attempt AUTOINCREMENT")
	}
	if _, err := sqlDB.Exec(drifted); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(sendSchema); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`PRAGMA user_version = 21`); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := InspectSchema(home)
	if !errors.Is(err, ErrUnsupportedLegacyV18Schema) {
		t.Fatalf("InspectSchema = %+v, error = %v, want ErrUnsupportedLegacyV18Schema", info, err)
	}
	if info.Family != SchemaFamilyLegacyV18 {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyLegacyV18)
	}
}

func TestInspectSchemaRejectsCommentSpoofedLegacyCheck(t *testing.T) {
	drifted := strings.Replace(schema,
		"singleton INTEGER PRIMARY KEY CHECK (singleton = 1)",
		"singleton INTEGER PRIMARY KEY /* CHECK (singleton = 1) */",
		1,
	)
	if drifted == schema {
		t.Fatal("test fixture did not replace fleet_identity CHECK with a comment")
	}
	assertUnsupportedLegacyDDL(t, drifted)
}

func TestInspectSchemaRejectsCaseChangedLegacyCheckLiteral(t *testing.T) {
	drifted := strings.Replace(schema,
		"CHECK (lifecycle IN ('open', 'terminal'))",
		"CHECK (lifecycle IN ('OPEN', 'terminal'))",
		1,
	)
	if drifted == schema {
		t.Fatal("test fixture did not change task lifecycle CHECK literal")
	}
	assertUnsupportedLegacyDDL(t, drifted)
}

func assertUnsupportedLegacyDDL(t *testing.T, ddl string) {
	t.Helper()
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(ddl); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(sendSchema); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`PRAGMA user_version = 21`); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := InspectSchema(home)
	if !errors.Is(err, ErrUnsupportedLegacyV18Schema) {
		t.Fatalf("InspectSchema = %+v, error = %v, want ErrUnsupportedLegacyV18Schema", info, err)
	}
	if info.Family != SchemaFamilyLegacyV18 {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyLegacyV18)
	}
}
