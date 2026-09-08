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
	}
	t.Fatalf("locked Answer-origin schema:\n%s", strings.Join(out, "\n\n"))
}
