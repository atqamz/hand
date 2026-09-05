package store

import (
	"strings"
	"testing"
)

func TestCanonicalV19WorktreeSchemaProbe(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.sql.Query(`SELECT type,name,sql FROM sqlite_schema
		WHERE name LIKE '%worktree%'
		   OR name LIKE 'external_operation%'
		   OR name LIKE '%operation_scope%'
		ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var out strings.Builder
	for rows.Next() {
		var typ, name, ddl string
		if err := rows.Scan(&typ, &name, &ddl); err != nil {
			t.Fatal(err)
		}
		out.WriteString(typ)
		out.WriteString(" ")
		out.WriteString(name)
		out.WriteString("\n")
		out.WriteString(ddl)
		out.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("canonical v19 Worktree/external-operation schema probe:\n%s", out.String())
}
