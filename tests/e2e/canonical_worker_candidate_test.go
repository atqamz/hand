//go:build e2e

package e2e

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalWorkerCandidateUsesExactPlanRoutingWithoutMutation(t *testing.T) {
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
	ok("task", "create", "task-1", "--project-id", projectID, "--goal", "route exact work")
	ok("project", "policy", "policy-1", "--project-id", projectID,
		"--worker-profile-ref", "worker", "--qualification-policy-ref", "", "--integration-policy-ref", "",
		"--production-policy-ref", "", "--publication-policy-ref", "")
	ok("plan", "create", "plan-1", "--task-id", "task-1", "--workspace-binding-id", workspaceID,
		"--policy-revision-id", "policy-1", "--intent", "execute", "--judgment", "bounded",
		"--basis", "registered repository", "--brief", "route this Plan")
	policy := func(profile string) []byte {
		return []byte(fmt.Sprintf(`{"schema":"hand.worker-policy.v1","profiles":[{"name":"worker","harness":"codex"},{"name":"alternate","harness":"claude"}],"routes":[{"intent":"explore","judgment":"mechanical","profile":"worker"},{"intent":"explore","judgment":"bounded","profile":"worker"},{"intent":"explore","judgment":"substantial","profile":"worker"},{"intent":"execute","judgment":"mechanical","profile":"worker"},{"intent":"execute","judgment":"bounded","profile":"%s"},{"intent":"execute","judgment":"substantial","profile":"worker"}]}`, profile))
	}
	path := filepath.Join(fleet, "config", "worker-policy.json")
	if err := os.WriteFile(path, policy("worker"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, fleet)
	first := ok("config", "worker-policy", "candidate", "--plan-id", "plan-1")
	for _, want := range []string{"plan_id: plan-1", "plan_policy_revision_id: policy-1", "intent: execute", "judgment: bounded", "profile: worker", "harness: codex", "qualification: unverified", "attempt_created: false", fmt.Sprintf(`policy_witness: "sha256:%x"`, sha256.Sum256(policy("worker")))} {
		if !strings.Contains(first.stdout, want) {
			t.Fatalf("Plan candidate = %+v, missing %q", first, want)
		}
	}
	assertTreeUnchanged(t, fleet, before)
	if err := os.WriteFile(path, policy("alternate"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := ok("config", "worker-policy", "candidate", "--plan-id", "plan-1")
	if !strings.Contains(second.stdout, "profile: alternate") || !strings.Contains(second.stdout, "harness: claude") ||
		!strings.Contains(second.stdout, fmt.Sprintf(`policy_witness: "sha256:%x"`, sha256.Sum256(policy("alternate")))) {
		t.Fatalf("edited policy did not change only future preview: %+v", second)
	}
	if got := runHand(t, fleet, "config", "worker-policy", "candidate", "--plan-id", "plan-1", "execute", "substantial"); got.code == 0 {
		t.Fatalf("caller-supplied axes overrode exact Plan: %+v", got)
	}
}
