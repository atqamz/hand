package store

import (
	"strings"
	"testing"
)

func TestCanonicalV19AnswerOriginLockedSchemaDiagnostic(t *testing.T) {
	fixture, _, _ := canonicalV19ExecutorBindingFixture(t)
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var out []string
	for _, table := range []string{"worker_input_answer_origin", "decision_answer"} {
		var sql string
		if err := db.sql.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&sql); err != nil {
			t.Fatal(err)
		}
		out = append(out, table+":\n"+sql)
		rows, err := db.sql.Query(`SELECT name,sql FROM sqlite_schema WHERE type='trigger' AND tbl_name=? ORDER BY name`, table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name, triggerSQL string
			if err := rows.Scan(&name, &triggerSQL); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			out = append(out, name+":\n"+triggerSQL)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		_ = rows.Close()
	}
	t.Fatalf("locked Answer-origin schema:\n%s", strings.Join(out, "\n\n"))
}
