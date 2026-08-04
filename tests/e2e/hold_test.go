//go:build e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/state"
)

type fleetStatus struct {
	Tasks []struct {
		ID string `json:"id"`
	} `json:"tasks"`
	Holds []holdView `json:"holds"`
}

type singleStatus struct {
	ID   string    `json:"id"`
	Held *holdView `json:"held"`
}

type holdView struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Reason       string `json:"reason"`
	BlockedOn    string `json:"blocked_on"`
	SetAt        string `json:"set_at"`
	Inconsistent string `json:"inconsistent"`
}

func decodeJSON(t *testing.T, got invocation, into any) {
	t.Helper()
	if got.code != 0 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	if err := json.Unmarshal([]byte(got.stdout), into); err != nil {
		t.Fatalf("decode %q: %v", got.stdout, err)
	}
}

// TestHoldLifecycle drives holds end to end through the built binary: set on a
// live task and on an id with no task row at all, rendered by every hand status
// surface, surviving the teardown of the task it was set on (the case a
// task-scoped hold could not cover), refusing the spawn that would reuse the
// id, and leaving nothing behind once cleared.
func TestHoldLifecycle(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "local-only")
	writeBrief(t, home, "fix-login")

	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	worktree := filepath.Join(home, "wt-fix-login")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "fix-login-branch", worktree)

	dir := binDir(t)
	writeFakeTreehouse(t, dir, worktree)
	writeFakeHerdrStatic(t, dir, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})

	if got := runHand(t, home, "spawn", "fix-login", "demo"); got.code != 0 {
		t.Fatalf("spawn: exit %d, stderr %q", got.code, got.stderr)
	}

	// A fleet with nothing held says so rather than leaving the block out.
	if got := runHand(t, home, "status"); got.code != 0 || !strings.Contains(got.stdout, "held: 0\n") {
		t.Fatalf("status with no holds = %q (exit %d), want an explicit zero held count", got.stdout, got.code)
	}

	set := runHand(t, home, "hold", "set", "fix-login",
		"--kind", "blocked", "--reason", "needs the new column before this can proceed",
		"--blocked-on", "migrate-schema")
	if set.code != 0 || !strings.Contains(set.stdout, "result: held\nkind: blocked\n") {
		t.Fatalf("hold set = %q (exit %d), stderr %q", set.stdout, set.code, set.stderr)
	}

	single := runHand(t, home, "status", "fix-login")
	if single.code != 0 || !strings.Contains(single.stdout, `held: "waiting on migrate-schema: needs the new column before this can proceed"`) {
		t.Fatalf("status fix-login = %q (exit %d), want the held field naming what it waits on", single.stdout, single.code)
	}

	var one singleStatus
	decodeJSON(t, runHand(t, home, "status", "fix-login", "--json"), &one)
	if one.Held == nil || one.Held.Kind != state.HoldKindBlocked || one.Held.BlockedOn != "migrate-schema" ||
		one.Held.Reason != "needs the new column before this can proceed" || one.Held.SetAt == "" {
		t.Fatalf("single-task JSON held = %+v", one.Held)
	}

	// An id with no task row behind it: never dispatched, so nothing but the
	// hold table could carry it.
	if got := runHand(t, home, "hold", "set", "unqueued-work",
		"--kind", "operator", "--reason", "two ways to do this, needs a call"); got.code != 0 {
		t.Fatalf("hold set unqueued-work: exit %d, stderr %q", got.code, got.stderr)
	}

	fleet := runHand(t, home, "status")
	if fleet.code != 0 {
		t.Fatalf("status: exit %d, stderr %q", fleet.code, fleet.stderr)
	}
	for _, want := range []string{
		"held: 2\n",
		"holds[2]{id,kind,detail,age}:\n",
		`  fix-login,blocked,"waiting on migrate-schema: needs the new column before this can proceed",`,
		`  unqueued-work,operator,"two ways to do this, needs a call",`,
	} {
		if !strings.Contains(fleet.stdout, want) {
			t.Fatalf("status = %q, want it to contain %q", fleet.stdout, want)
		}
	}

	var all fleetStatus
	decodeJSON(t, runHand(t, home, "status", "--json"), &all)
	if len(all.Tasks) != 1 || all.Tasks[0].ID != "fix-login" {
		t.Fatalf("fleet JSON tasks = %+v, want the one spawned task", all.Tasks)
	}
	if len(all.Holds) != 2 || all.Holds[0].ID != "fix-login" || all.Holds[1].ID != "unqueued-work" {
		t.Fatalf("fleet JSON holds = %+v, want both holds", all.Holds)
	}

	// The motivating case: teardown deletes the task row, and the hold set on
	// that id has to outlive it.
	runGitIn(t, worktree, "commit", "--allow-empty", "-q", "-m", "wip")
	if got := runHand(t, home, "merge", "fix-login", "--local"); got.code != 0 {
		t.Fatalf("merge --local: exit %d, stderr %q", got.code, got.stderr)
	}
	if got := runHand(t, home, "teardown", "fix-login"); got.code != 0 {
		t.Fatalf("teardown: exit %d, stderr %q", got.code, got.stderr)
	}
	if exists, err := state.Exists(home, "fix-login"); err != nil || exists {
		t.Fatalf("state.Exists after teardown = %v, %v, want the task row gone", exists, err)
	}

	survived := runHand(t, home, "status")
	if survived.code != 0 || !strings.Contains(survived.stdout, `  fix-login,blocked,"waiting on migrate-schema`) {
		t.Fatalf("status after teardown = %q, want the torn-down task's hold still listed", survived.stdout)
	}

	// Reusing the id would reattach that still-open question to unrelated work.
	writeBrief(t, home, "fix-login")
	respawn := runHand(t, home, "spawn", "fix-login", "demo")
	assertInvocation(t, respawn, 3, `id "fix-login" has an open hold`)
	if !strings.Contains(respawn.stderr, "hand hold clear fix-login") {
		t.Fatalf("spawn stderr = %q, want it to name the remedy", respawn.stderr)
	}

	cleared := runHand(t, home, "hold", "clear", "fix-login")
	if cleared.code != 0 || !strings.Contains(cleared.stdout, "result: released\n") {
		t.Fatalf("hold clear = %q (exit %d), stderr %q", cleared.stdout, cleared.code, cleared.stderr)
	}
	assertInvocation(t, runHand(t, home, "hold", "clear", "fix-login"), 3, `hold "fix-login" not found`)

	if got := runHand(t, home, "hold", "clear", "unqueued-work"); got.code != 0 {
		t.Fatalf("hold clear unqueued-work: exit %d, stderr %q", got.code, got.stderr)
	}
	if got := runHand(t, home, "status"); got.code != 0 || !strings.Contains(got.stdout, "held: 0\n") {
		t.Fatalf("status after clearing every hold = %q, want the held count back to zero", got.stdout)
	}
}
