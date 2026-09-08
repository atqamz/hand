package store

import "testing"

func TestCanonicalV19DecisionLockedSchemaDiagnostic(t *testing.T) {
	fixture, _, _ := canonicalV19ExecutorBindingFixture(t)
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var sql string
	if err := db.sql.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='decision'`).Scan(&sql); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("locked Decision schema:\n%s", sql)
}
