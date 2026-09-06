package store

import (
	"strings"
	"testing"
)

func TestCanonicalV19Step10SchemaProbe(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.sql.Query(`
		SELECT type, name, COALESCE(sql, '')
		FROM sqlite_master
		WHERE lower(name) LIKE '%scope%'
		   OR lower(name) LIKE 'external_operation%'
		ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var out strings.Builder
	for rows.Next() {
		var typ, name, sql string
		if err := rows.Scan(&typ, &name, &sql); err != nil {
			t.Fatal(err)
		}
		out.WriteString("\n--- ")
		out.WriteString(typ)
		out.WriteString(" ")
		out.WriteString(name)
		out.WriteString(" ---\n")
		out.WriteString(sql)
		out.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("locked v19 operation scope schema:%s", out.String())
}
