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

func TestFleetSnapshotShowsOpenAttemptBackoffAndRepairAsDigestsNotProse(t *testing.T) {
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
		INSERT INTO workspace_binding(id,project_id,ordinal,repository_locator,repository_identity_digest,
			common_git_dir,physical_identity_digest,revision,established_at,superseded_at)
		VALUES('workspace-1','project-1',1,'projects/demo','repo-digest','projects/demo/.git',
			'physical-digest','revision-1','2026-09-24T00:00:00Z','');
		INSERT INTO policy_revision(id,project_id,ordinal,policy_digest,created_at,superseded_at)
		VALUES('policy-1','project-1',1,'policy-digest-1','2026-09-24T00:00:00Z','')`, fleetID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCanonicalV19Task(context.Background(), home, store.CanonicalV19TaskCreateInput{
		ID: "task-1", ProjectID: "project-1", Goal: "task-1", GoalDigest: "digest-task-1",
		CreatedAt: "2026-09-24T00:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	planAndAttempt, err := sql.Open("sqlite", "file:"+store.Path(home)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planAndAttempt.Exec(`INSERT INTO plan(id,task_id,ordinal,lineage_kind,intent,judgment,basis,brief,
			brief_digest,workspace_binding_id,policy_revision_id,lifecycle,created_at,terminal_at)
		VALUES('plan-root','task-1',1,'root','execute','bounded','operator goal','ship the fix',
			'brief-digest','workspace-1','policy-1','active','2026-09-24T00:02:00Z','');
		INSERT INTO attempt(id,plan_id,ordinal,worker_harness_ref,session_adapter_ref,lifecycle,created_at,terminal_at)
		VALUES('attempt-1','plan-root',1,'worker-harness/codex','builtin/session','active','2026-09-24T00:03:00Z','')`); err != nil {
		t.Fatal(err)
	}
	if err := planAndAttempt.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCanonicalV19AttemptBackoff(context.Background(), home, store.CanonicalV19AttemptBackoffCreateInput{
		ID: "backoff-1", AttemptID: "attempt-1", Reason: "rate-limit", NotBefore: "2026-09-25T00:00:00Z",
		EvidenceDigest: "digest-backoff", CreatedAt: "2026-09-24T00:05:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCanonicalV19Repair(context.Background(), home, store.CanonicalV19RepairCreateInput{
		ID: "repair-1", RepairCode: "worktree-drift", Reason: "free-text diagnosis that must stay hidden from CLI rows",
		EvidenceDigest: "digest-repair", CreatedAt: "2026-09-24T00:06:00Z",
		Target: store.CanonicalV19RepairTarget{TaskID: "task-1"},
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
	output := out.String()
	if !strings.Contains(output, "current_open_attempt_backoffs[1]") ||
		!strings.Contains(output, `backoff-1,project-1,task-1,plan-root,attempt-1,rate-limit,"2026-09-25T00:00:00Z",digest-backoff,"2026-09-24T00:05:00Z"`) {
		t.Fatalf("snapshot did not project AttemptBackoff rows: %q", output)
	}
	if !strings.Contains(output, "open_repairs[1]") ||
		!strings.Contains(output, `repair-1,project-1,task-1,task,task-1,worktree-drift,digest-repair,"2026-09-24T00:06:00Z"`) ||
		strings.Contains(output, "free-text diagnosis") {
		t.Fatalf("snapshot did not project Repair as digest rows: %q", output)
	}
	after, err := os.ReadFile(store.Path(home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("snapshot mutated canonical database: %v", err)
	}
}
