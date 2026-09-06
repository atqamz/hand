package store

import (
	"context"
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

	rows, err := db.sql.Query(`SELECT name,sql FROM sqlite_master
		WHERE type='trigger' AND sql LIKE '%worktree_remove_operation%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name, sqlText string
		if err := rows.Scan(&name, &sqlText); err != nil {
			t.Fatal(err)
		}
		t.Logf("trigger %s: %s", name, sqlText)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.FailNow()
}
