//go:build e2e

package e2e

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

func TestCanonicalAttemptCreateAndRetryFreezeCurrentWorkerRouteWithoutLaunch(t *testing.T) {
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
	refused := func(want string, args ...string) {
		t.Helper()
		if got := runHand(t, fleet, args...); got.code == 0 || !strings.Contains(got.stdout+got.stderr, want) {
			t.Fatalf("%v = %+v, want refusal naming %q", args, got, want)
		}
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
	if got := runHandEnv(t, fleet, []string{"HAND_ROLE=worker"}, "attempt", "create", "attempt-1", "--plan-id", "plan-1"); got.code != 3 {
		t.Fatalf("worker Attempt creation: %+v", got)
	}
	if got := runHand(t, fleet, "attempt", "retry", "attempt-1", "--plan-id", "plan-1", "--predecessor", ""); got.code != 2 {
		t.Fatalf("retry without exact predecessor: %+v", got)
	}
	assertTreeUnchanged(t, fleet, before)
	created := ok("attempt", "create", "attempt-1", "--plan-id", "plan-1", "--effort-override", "high")
	for _, want := range []string{"attempt_id: attempt-1", "ordinal: 1", "profile: worker", "harness: codex", "effort: high",
		"effort-override,high", "worker_launched: false", fmt.Sprintf(`policy_witness: "sha256:%x"`, sha256.Sum256(policy("worker")))} {
		if !strings.Contains(created.stdout, want) {
			t.Fatalf("attempt create = %+v, missing %q", created, want)
		}
	}
	shown := ok("fleet", "snapshot")
	if !strings.Contains(shown.stdout, "attempt-1,plan-1,1,codex,worker,\"\",high,herdr") ||
		!strings.Contains(shown.stdout, "current_open_worktree_bindings[0]") || !strings.Contains(shown.stdout, "current_open_session_bindings[0]") {
		t.Fatalf("snapshot lost Attempt provenance or started resources: %+v", shown)
	}
	refused("already has active Attempt", "attempt", "create", "attempt-dup", "--plan-id", "plan-1")
	refused("already has active Attempt", "attempt", "retry", "attempt-2", "--plan-id", "plan-1", "--predecessor", "attempt-1")
	if err := store.TerminalizeCanonicalV19Attempt(context.Background(), fleet, store.CanonicalV19AttemptTerminalizeInput{
		AttemptID: "attempt-1", Lifecycle: "failed", TerminalAt: "2026-09-25T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	refused("not expected predecessor", "attempt", "create", "attempt-2", "--plan-id", "plan-1")
	if err := os.WriteFile(path, policy("alternate"), 0o600); err != nil {
		t.Fatal(err)
	}
	retried := ok("attempt", "retry", "attempt-2", "--plan-id", "plan-1", "--predecessor", "attempt-1")
	if !strings.Contains(retried.stdout, "ordinal: 2") || !strings.Contains(retried.stdout, "profile: alternate") ||
		!strings.Contains(retried.stdout, "harness: claude") {
		t.Fatalf("retry did not resolve current Worker policy: %+v", retried)
	}
	if err := store.TerminalizeCanonicalV19Attempt(context.Background(), fleet, store.CanonicalV19AttemptTerminalizeInput{
		AttemptID: "attempt-2", Lifecycle: "failed", TerminalAt: "2026-09-25T00:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	refused("not expected predecessor", "attempt", "retry", "attempt-3", "--plan-id", "plan-1", "--predecessor", "attempt-1")
}
