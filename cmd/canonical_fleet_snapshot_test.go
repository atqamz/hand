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

func TestFleetSnapshotShowsCurrentTaskHoldWithoutInventingAttention(t *testing.T) {
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
		VALUES('project-1',?,1,'demo','2026-09-24T00:00:00Z')`, fleetID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"task-1", "task-2"} {
		if _, err := store.CreateCanonicalV19Task(context.Background(), home, store.CanonicalV19TaskCreateInput{
			ID: id, ProjectID: "project-1", Goal: id, GoalDigest: "digest-" + id,
			CreatedAt: "2026-09-24T00:01:00Z",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CreateCanonicalV19Decision(context.Background(), home, store.CanonicalV19DecisionCreateInput{
		ID: "decision-1", TaskID: "task-1", ScopeKind: "task", Question: "Who can unblock this task?",
		CreatedAt: "2026-09-24T00:02:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCanonicalV19TaskHold(context.Background(), home, store.CanonicalV19TaskHoldCreateInput{
		ID: "hold-1", TaskID: "task-1", Kind: "blocked", Reason: "waiting for task-2",
		EvidenceDigest: "digest-hold", CreatedAt: "2026-09-24T00:03:00Z",
		BlockedOnTaskID: "task-2", DecisionID: "decision-1", RecheckNotBefore: "2026-09-25T00:00:00Z",
	}); err != nil {
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
	if !strings.Contains(out.String(), "current_open_task_holds[1]") ||
		!strings.Contains(out.String(), "hold-1,task-1,1,blocked,digest-hold,") ||
		strings.Contains(out.String(), "waiting for task-2") ||
		!strings.Contains(out.String(), "task-2,decision-1,") ||
		!strings.Contains(out.String(), "partial_attention_items[0]") {
		t.Fatalf("snapshot did not separate TaskHold from Attention: %q", out.String())
	}
	after, err := os.ReadFile(store.Path(home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("snapshot mutated canonical database: %v", err)
	}
}
