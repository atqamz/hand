package store

import (
	"errors"
	"os"
	"testing"
)

func TestInspectSchemaMissing(t *testing.T) {
	info, err := InspectSchema(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if info.Family != SchemaFamilyMissing {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyMissing)
	}
}

func TestInspectSchemaRecognizesExactV072Legacy(t *testing.T) {
	if len(migrations) != legacyV072SchemaVersion {
		t.Fatalf("legacy migration ladder = %d, v0.7.2 source contract is frozen at %d", len(migrations), legacyV072SchemaVersion)
	}

	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := InspectSchema(home)
	if err != nil {
		t.Fatalf("InspectSchema = %+v, error = %v", info, err)
	}
	if info.Family != SchemaFamilyLegacyV18 {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyLegacyV18)
	}
	if info.UserVersion != legacyV072SchemaVersion {
		t.Fatalf("user_version = %d, want %d", info.UserVersion, legacyV072SchemaVersion)
	}
	if info.LayoutFingerprint != legacyV072LayoutFingerprint {
		t.Fatalf("layout fingerprint = %s, want %s", info.LayoutFingerprint, legacyV072LayoutFingerprint)
	}
	if info.Tables != legacyV072TableCount || info.Indexes != legacyV072IndexCount || info.Triggers != legacyV072TriggerCount {
		t.Fatalf("schema objects = %d tables / %d indexes / %d triggers, want %d / %d / %d",
			info.Tables, info.Indexes, info.Triggers,
			legacyV072TableCount, legacyV072IndexCount, legacyV072TriggerCount)
	}
}

func TestInspectSchemaRejectsLegacyVersion19DespiteCurrentLayout(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`PRAGMA user_version = 19`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
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

func TestInspectSchemaRejectsLegacyLayoutDrift(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`CREATE TABLE legacy_drift (id TEXT PRIMARY KEY)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
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

func TestInspectSchemaRecognizesCanonicalV19(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := createCanonicalV19Schema(sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := InspectSchema(home)
	if err != nil {
		t.Fatal(err)
	}
	if info.Family != SchemaFamilyCanonicalV19 {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyCanonicalV19)
	}
	if info.Fingerprint != canonicalV19SchemaFingerprint {
		t.Fatalf("schema fingerprint = %s, want %s", info.Fingerprint, canonicalV19SchemaFingerprint)
	}
	if info.LayoutFingerprint != canonicalV19SchemaFingerprint {
		t.Fatalf("layout fingerprint = %s, want canonical schema fingerprint %s", info.LayoutFingerprint, canonicalV19SchemaFingerprint)
	}
}

func TestInspectSchemaKeepsDriftedCanonicalV19FailClosed(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := createCanonicalV19Schema(sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE canonical_drift (id TEXT PRIMARY KEY) STRICT`); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := InspectSchema(home)
	if !errors.Is(err, ErrCanonicalV19SchemaMismatch) {
		t.Fatalf("InspectSchema = %+v, error = %v, want ErrCanonicalV19SchemaMismatch", info, err)
	}
	if info.Family != SchemaFamilyCanonicalV19 {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyCanonicalV19)
	}
}

func TestInspectSchemaRejectsUnknownDatabase(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE mystery (id TEXT PRIMARY KEY)`); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := InspectSchema(home)
	if !errors.Is(err, ErrUnknownSchema) {
		t.Fatalf("InspectSchema = %+v, error = %v, want ErrUnknownSchema", info, err)
	}
	if info.Family != SchemaFamilyUnknown {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyUnknown)
	}
}

func TestInspectSchemaExistingEmptySQLite(t *testing.T) {
	home := t.TempDir()
	path := Path(home)
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE empty_probe (id INTEGER); DROP TABLE empty_probe;`); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := InspectSchema(home)
	if err != nil {
		t.Fatal(err)
	}
	if info.Family != SchemaFamilyEmpty {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyEmpty)
	}
}
