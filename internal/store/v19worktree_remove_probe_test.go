package store

import (
	"context"
	"fmt"
	"testing"
)

func TestProbeCanonicalV19WorktreeRemoveSchema(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home,
		canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-create", "binding-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19WorktreeBinding(context.Background(), fixture.Home,
		canonicalV19WorktreeBindingEvidence(request, "physical-identity-1")); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, table := range []string{"worktree_binding_release", "session_binding_release"} {
		rows, err := db.sql.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			t.Fatal(err)
		}
		var cols []string
		for rows.Next() {
			var cid, notNull, pk int
			var name, typ string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			cols = append(cols, fmt.Sprintf("%s:%s:notnull=%d:pk=%d", name, typ, notNull, pk))
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s columns: %v", table, cols)
	}
	t.FailNow()
}
