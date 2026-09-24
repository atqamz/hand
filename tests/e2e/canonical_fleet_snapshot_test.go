//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
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
