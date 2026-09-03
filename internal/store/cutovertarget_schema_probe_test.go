package store

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestProbeCanonicalV19CutoverRelations(t *testing.T) {
	db, err := open(filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := createCanonicalV19Schema(db); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Query(`SELECT name, sql FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var found []string
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "fleet") || strings.Contains(lower, "project") || strings.Contains(lower, "workspace") || strings.Contains(lower, "policy") || strings.Contains(lower, "migration") || strings.Contains(lower, "import") || strings.Contains(lower, "archive") || strings.Contains(lower, "cutover") {
			found = append(found, "TABLE "+name+" "+strings.Join(strings.Fields(ddl), " "))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	t.Fatalf("locked v19 cutover relations: %s", strings.Join(found, " || "))
}
