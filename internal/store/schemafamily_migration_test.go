package store

import (
	"database/sql"
	"testing"
)

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
	assertLegacyV072LayoutMatchesFresh(t, home)

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
	assertLegacyV072LayoutMatchesFresh(t, home)

	info, err := InspectSchema(home)
	if err != nil {
		t.Fatalf("InspectSchema migrated pre-versioning = %+v, error = %v", info, err)
	}
	if info.Family != SchemaFamilyLegacyV18 {
		t.Fatalf("schema family = %q, want %q", info.Family, SchemaFamilyLegacyV18)
	}
}

func assertLegacyV072LayoutMatchesFresh(t *testing.T, migratedHome string) {
	t.Helper()

	freshHome := t.TempDir()
	fresh, err := Open(freshHome)
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}

	freshDB, err := openReadOnlySQLForLayout(freshHome)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = freshDB.Close() }()
	migratedDB, err := openReadOnlySQLForLayout(migratedHome)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migratedDB.Close() }()

	freshLines, err := legacyV18LayoutLines(freshDB)
	if err != nil {
		t.Fatal(err)
	}
	migratedLines, err := legacyV18LayoutLines(migratedDB)
	if err != nil {
		t.Fatal(err)
	}

	freshOnly, migratedOnly := layoutLineDifference(freshLines, migratedLines)
	if len(freshOnly) != 0 || len(migratedOnly) != 0 {
		t.Fatalf("normalized legacy layout differs\nfresh-only: %#v\nmigrated-only: %#v", freshOnly, migratedOnly)
	}
}

func openReadOnlySQLForLayout(home string) (*sql.DB, error) {
	db, err := openReadOnly(home)
	if err != nil {
		return nil, err
	}
	return db.sql, nil
}

func layoutLineDifference(left, right []string) ([]string, []string) {
	leftSet := make(map[string]bool, len(left))
	rightSet := make(map[string]bool, len(right))
	for _, line := range left {
		leftSet[line] = true
	}
	for _, line := range right {
		rightSet[line] = true
	}

	var leftOnly, rightOnly []string
	for _, line := range left {
		if !rightSet[line] {
			leftOnly = append(leftOnly, line)
		}
	}
	for _, line := range right {
		if !leftSet[line] {
			rightOnly = append(rightOnly, line)
		}
	}
	return leftOnly, rightOnly
}
