package store

import (
	"fmt"
	"strings"
	"testing"
)

func TestAAAWorkerWakeLockedSchemaDiagnostic(t *testing.T) {
	fixture, _, _ := canonicalV19ExecutorBindingFixture(t)
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var out []string
	for _, objectType := range []string{"table", "trigger", "index"} {
		rows, err := db.sql.Query(`SELECT name,sql FROM sqlite_schema
			WHERE type=? AND (name='worker_wake_operation' OR tbl_name='worker_wake_operation')
			  AND sql IS NOT NULL ORDER BY name`, objectType)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name, objectSQL string
			if err := rows.Scan(&name, &objectSQL); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			out = append(out, objectType+" "+name+": "+strings.Join(strings.Fields(objectSQL), " "))
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		_ = rows.Close()
	}
	panic(fmt.Sprintf("locked WorkerWake schema:\n%s", strings.Join(out, "\n")))
}
