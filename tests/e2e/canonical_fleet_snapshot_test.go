//go:build e2e

package e2e

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

func TestCanonicalFleetSnapshotCoreCLI(t *testing.T) {
	parent := t.TempDir()
	fleet := filepath.Join(parent, "fleet")
	if got := runHand(t, parent, "init", "--canonical", fleet); got.code != 0 {
		t.Fatal(got)
	}
	initGitRepo(t, filepath.Join(fleet, "projects", "sample"))
	ok := func(args ...string) invocation {
		t.Helper()
		got := runHand(t, fleet, args...)
		if got.code != 0 {
			t.Fatalf("%v: %+v", args, got)
		}
		return got
	}
	registered := ok("project", "register", "sample")
	projectID := canonicalOutputField(t, registered, "project_id")
	workspaceID := canonicalOutputField(t, registered, "workspace_binding_id")
	ok("task", "create", "task-1", "--project-id", projectID, "--goal", "inspect canonical source")
	ok("task", "create", "task-2", "--project-id", projectID, "--goal", "another task")
	ok("project", "policy", "policy-1", "--project-id", projectID,
		"--worker-profile-ref", "", "--qualification-policy-ref", "", "--integration-policy-ref", "",
		"--production-policy-ref", "", "--publication-policy-ref", "")
	ok("plan", "create", "plan-1", "--task-id", "task-1", "--workspace-binding-id", workspaceID,
		"--policy-revision-id", "policy-1", "--intent", "explore", "--judgment", "bounded",
		"--basis", "registered repository", "--brief", "inspect exact state")
	before := snapshotTree(t, fleet)
	shown := ok("fleet", "snapshot")
	if canonicalOutputField(t, shown, "snapshot_schema") != "hand.fleet.core.v1" ||
		canonicalOutputField(t, shown, "completeness") != "partial" ||
		canonicalOutputField(t, shown, "attention") != "unknown" {
		t.Fatalf("snapshot claimed complete authority: %+v", shown)
	}
	if !strings.Contains(shown.stdout, projectID) || !strings.Contains(shown.stdout, "plan-1") ||
		strings.Index(shown.stdout, "task-1") >= strings.Index(shown.stdout, "task-2") {
		t.Fatalf("snapshot lost exact ordered lineage: %+v", shown)
	}
	if !strings.Contains(shown.stdout, "unacknowledged_inputs[0]") || !strings.Contains(shown.stdout, "unresolved_operations[0]") {
		t.Fatalf("snapshot omitted independent input and effect families: %+v", shown)
	}
	if !strings.Contains(shown.stdout, "report_state") {
		t.Fatalf("snapshot omitted typed WorkerReport Attention state: %+v", shown)
	}
	assertTreeUnchanged(t, fleet, before)
}

func TestCanonicalFleetSnapshotShowsLatestWorkerReportAndExactAcknowledgement(t *testing.T) {
	parent := t.TempDir()
	fleet := filepath.Join(parent, "fleet")
	if got := runHand(t, parent, "init", "--canonical", fleet); got.code != 0 {
		t.Fatal(got)
	}
	initGitRepo(t, filepath.Join(fleet, "projects", "sample"))
	ok := func(args ...string) invocation {
		t.Helper()
		got := runHand(t, fleet, args...)
		if got.code != 0 {
			t.Fatalf("%v: %+v", args, got)
		}
		return got
	}
	registered := ok("project", "register", "sample")
	projectID := canonicalOutputField(t, registered, "project_id")
	workspaceID := canonicalOutputField(t, registered, "workspace_binding_id")
	ok("task", "create", "task-1", "--project-id", projectID, "--goal", "inspect report")
	ok("project", "policy", "policy-1", "--project-id", projectID,
		"--worker-profile-ref", "", "--qualification-policy-ref", "", "--integration-policy-ref", "",
		"--production-policy-ref", "", "--publication-policy-ref", "")
	ok("plan", "create", "plan-1", "--task-id", "task-1", "--workspace-binding-id", workspaceID,
		"--policy-revision-id", "policy-1", "--intent", "explore", "--judgment", "bounded",
		"--basis", "registered repository", "--brief", "inspect exact state")
	if _, err := store.CreateCanonicalV19Attempt(context.Background(), fleet, store.CanonicalV19AttemptCreateInput{
		ID: "attempt-1", PlanID: "plan-1", WorkerHarnessRef: "worker-harness/codex",
		WorkerHarnessVersion: "1.0.0", WorkerProfileRef: "profile/default", ModelRef: "model/example",
		EffortRef: "medium", SessionAdapterRef: "builtin/session", CreatedAt: "2026-09-24T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	execFleetFixtureSQL(t, fleet, `INSERT INTO worker_report(
		id,attempt_id,source_prefix_digest,source_end_offset,report_state,note,created_at
	) VALUES('report-1','attempt-1','prefix-1',17,'blocked','wait for evidence','2026-09-24T00:01:00Z')`)
	before := snapshotTree(t, fleet)
	shown := ok("fleet", "snapshot")
	if !strings.Contains(shown.stdout, "latest_worker_reports[1]{attempt_id,report_id,source_prefix_digest,source_end_offset,report_state,acknowledged}:") ||
		!strings.Contains(shown.stdout, "attempt-1,report-1,prefix-1,17,blocked,false") ||
		!strings.Contains(shown.stdout, "current-latest-worker-report-unacknowledged") ||
		strings.Contains(shown.stdout, "wait for evidence") {
		t.Fatalf("snapshot lost bounded unacknowledged report metadata: %+v", shown)
	}
	assertTreeUnchanged(t, fleet, before)
	if _, err := store.CreateCanonicalV19WorkerReportAcknowledgement(context.Background(), fleet,
		store.CanonicalV19WorkerReportAcknowledgementCreateInput{
			WorkerReportID: "report-1", ActorKind: "supervisor", AcknowledgedAt: "2026-09-24T00:02:00Z",
			EvidenceDigest: "read-report-1",
		}); err != nil {
		t.Fatal(err)
	}
	before = snapshotTree(t, fleet)
	shown = ok("fleet", "snapshot")
	if !strings.Contains(shown.stdout, "attempt-1,report-1,prefix-1,17,blocked,true") ||
		strings.Contains(shown.stdout, "current-latest-worker-report-unacknowledged") {
		t.Fatalf("snapshot lost exact report acknowledgement: %+v", shown)
	}
	assertTreeUnchanged(t, fleet, before)
}

func TestCanonicalFleetSnapshotCoreRefusesMissingAndLegacyStores(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		fleet := t.TempDir()
		if legacy {
			createCutoverLegacyFixture(t, fleet)
		}
		seedPrivateRuntime(t, fleet)
		before := snapshotTree(t, fleet)
		if got := runHand(t, fleet, "fleet", "snapshot"); got.code == 0 {
			t.Fatalf("accepted missing/legacy Fleet: %+v", got)
		}
		assertTreeUnchanged(t, fleet, before)
	}
}
