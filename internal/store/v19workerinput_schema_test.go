package store

import (
	"strings"
	"testing"
)

func TestCanonicalV19WorkerInputLockedSchemaDiagnostic(t *testing.T) {
	fixture, _, _ := canonicalV19ExecutorBindingFixture(t)
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var tableSQL string
	if err := db.sql.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='worker_input'`).Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	rows, err := db.sql.Query(`SELECT name,sql FROM sqlite_schema
		WHERE type='trigger' AND tbl_name='worker_input' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var triggers []string
	for rows.Next() {
		var name, sql string
		if err := rows.Scan(&name, &sql); err != nil {
			t.Fatal(err)
		}
		triggers = append(triggers, name+": "+sql)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("locked worker_input schema:\n%s\ntriggers:\n%s", tableSQL, strings.Join(triggers, "\n"))
}
