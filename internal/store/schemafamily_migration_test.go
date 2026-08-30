package store

import "testing"

func TestInspectSchemaRecognizesMigrated040AsExactV072Legacy(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(legacy040Schema); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`PRAGMA user_version = 7`); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := InspectSchema(home)
	if err != nil {
		t.Fatalf("InspectSchema migrated 0.4.0 = %+v, error = %v", info, err)
	}
	if info.Family != SchemaFamilyLegacyV18 {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyLegacyV18)
	}
}

func TestInspectSchemaRecognizesMigratedPreVersioningAsExactV072Legacy(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(preVersioningSchema); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := InspectSchema(home)
	if err != nil {
		t.Fatalf("InspectSchema migrated pre-versioning = %+v, error = %v", info, err)
	}
	if info.Family != SchemaFamilyLegacyV18 {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyLegacyV18)
	}
}
