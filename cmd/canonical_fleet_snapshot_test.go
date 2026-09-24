package cmd

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

func TestFleetSnapshotAttentionShowsExactWorktreeScope(t *testing.T) {
	home := filepath.Join(t.TempDir(), "fleet")
	fleetID, err := store.InitializeCanonicalV19(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_HOME", home)
	t.Setenv("SECONDHAND_HOME", filepath.Join(t.TempDir(), "registry"))
	db, err := sql.Open("sqlite", "file:"+store.Path(home)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO project(id,fleet_id,ordinal,display_name,created_at)
		VALUES('project-1',?,1,'demo','2026-09-24T00:00:00Z');
		INSERT INTO external_operation(id,kind,adapter_ref,operation_key,request_digest,project_id,
			primary_scope_kind,primary_scope_key,created_at,state_changed_at)
		VALUES('remove-1','worktree-remove','git','remove-key','digest','project-1',
			'worktree','binding-1','2026-09-24T00:01:00Z','2026-09-24T00:01:00Z')`, fleetID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	root := newRootCmd(devBuild("test"))
	var out strings.Builder
	root.SetOut(&out)
	root.SetArgs([]string{"fleet", "snapshot"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "scope_kind,scope_key") ||
		!strings.Contains(out.String(), "10,external-operation-unresolved,remove-1,worktree-remove,prepared,worktree,binding-1") {
		t.Fatalf("snapshot Attention lost exact worktree scope: %q", out.String())
	}
	after, err := os.ReadFile(store.Path(home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("snapshot mutated canonical database: %v", err)
	}
}
