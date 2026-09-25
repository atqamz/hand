package cmd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/routing"
	"github.com/atqamz/hand/internal/store"
)

func TestAttemptCreateRefusesWorkerPolicyEditedBetweenResolveAndCommit(t *testing.T) {
	ctx := context.Background()
	home := filepath.Join(t.TempDir(), "fleet")
	fleetID, err := store.InitializeCanonicalV19(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+store.Path(home)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO project(id,fleet_id,ordinal,display_name,created_at) VALUES('project-1',?,1,'demo','2026-09-25T00:00:00Z');
		INSERT INTO workspace_binding(id,project_id,ordinal,repository_locator,repository_identity_digest,common_git_dir,physical_identity_digest,revision,established_at)
		VALUES('workspace-1','project-1',1,'repo','repo-digest','repo/.git','physical-digest','revision','2026-09-25T00:00:00Z');
		INSERT INTO policy_revision(id,project_id,ordinal,policy_digest,created_at) VALUES('policy-1','project-1',1,'policy-digest','2026-09-25T00:00:00Z');
		INSERT INTO task(id,project_id,ordinal,goal,goal_digest,created_at) VALUES('task-1','project-1',1,'goal','goal-digest','2026-09-25T00:00:00Z');
		INSERT INTO plan(id,task_id,ordinal,lineage_kind,intent,judgment,basis,brief,brief_digest,workspace_binding_id,policy_revision_id,created_at)
		VALUES('plan-1','task-1',1,'root','execute','bounded','basis','brief','brief-digest','workspace-1','policy-1','2026-09-25T00:00:00Z')`, fleetID); err != nil {
		t.Fatal(err)
	}
	writePolicy := func(profile string) {
		t.Helper()
		policy := fmt.Sprintf(`{"schema":"hand.worker-policy.v1","profiles":[{"name":"worker","harness":"codex"},{"name":"alternate","harness":"claude"}],"routes":[{"intent":"explore","judgment":"mechanical","profile":"worker"},{"intent":"explore","judgment":"bounded","profile":"worker"},{"intent":"explore","judgment":"substantial","profile":"worker"},{"intent":"execute","judgment":"mechanical","profile":"worker"},{"intent":"execute","judgment":"bounded","profile":"%s"},{"intent":"execute","judgment":"substantial","profile":"worker"}]}`, profile)
		if err := os.WriteFile(filepath.Join(home, "config", "worker-policy.json"), []byte(policy), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writePolicy("worker")
	stale, _, err := resolveCanonicalAttempt(ctx, home, "attempt-1", "plan-1", "", routing.WorkerCandidateOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	writePolicy("alternate")
	if _, err := store.CreateCanonicalV19Attempt(ctx, home, stale); !errors.Is(err, routing.ErrWorkerPolicyStale) {
		t.Fatalf("R1 resolution committed under R2 = %v, want %v", err, routing.ErrWorkerPolicyStale)
	}
	var attempts int
	if err := db.QueryRow(`SELECT count(*) FROM attempt`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("Attempt rows after stale witness = %d, %v", attempts, err)
	}
	current, _, err := resolveCanonicalAttempt(ctx, home, "attempt-1", "plan-1", "", routing.WorkerCandidateOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if ordinal, err := store.CreateCanonicalV19Attempt(ctx, home, current); err != nil || ordinal != 1 {
		t.Fatalf("unchanged R2 resolution = %d, %v", ordinal, err)
	}
	var profile, harness, adapter string
	if err := db.QueryRow(`SELECT worker_profile_ref,worker_harness_ref,session_adapter_ref FROM attempt WHERE id='attempt-1'`).Scan(&profile, &harness, &adapter); err != nil {
		t.Fatal(err)
	}
	if profile != "alternate" || harness != "claude" || adapter != store.CanonicalV19HerdrSessionAdapterRef {
		t.Fatalf("Attempt provenance = %q/%q/%q, want R2 alternate/claude/%s", profile, harness, adapter, store.CanonicalV19HerdrSessionAdapterRef)
	}
}
