package store

import (
	"strings"
	"testing"
)

func TestProbeCanonicalV19InterruptSchema(t *testing.T) {
	fixture, _, _ := canonicalV19SessionBindingFixture(t)
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.sql.Query(`SELECT type,name,tbl_name,sql FROM sqlite_schema
		WHERE sql IS NOT NULL AND (
			name LIKE '%interrupt%' OR name LIKE '%executor_binding%' OR
			tbl_name LIKE '%interrupt%' OR tbl_name LIKE '%executor_binding%' OR
			sql LIKE '%interrupt_operation%' OR sql LIKE '%executor_binding_termination%'
		)
		ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var out strings.Builder
	for rows.Next() {
		var typ, name, table, sql string
		if err := rows.Scan(&typ, &name, &table, &sql); err != nil {
			t.Fatal(err)
		}
		out.WriteString("\n--- ")
		out.WriteString(typ)
		out.WriteString(" ")
		out.WriteString(name)
		out.WriteString(" [")
		out.WriteString(table)
		out.WriteString("] ---\n")
		out.WriteString(sql)
		out.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("locked v19 Interrupt schema:%s", out.String())
}
