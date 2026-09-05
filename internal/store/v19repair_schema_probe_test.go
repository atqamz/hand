package store

import (
	"strings"
	"testing"
)

func TestCanonicalV19RepairSchemaProbe(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.sql.Query(`SELECT name, sql FROM sqlite_schema
		WHERE name LIKE 'repair%'
		ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var out strings.Builder
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		out.WriteString(name)
		out.WriteString("\n")
		out.WriteString(ddl)
		out.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("canonical v19 Repair schema probe:\n%s", out.String())
}
