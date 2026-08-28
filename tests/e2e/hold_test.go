//go:build e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/state"
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
	Satisfied    bool   `json:"satisfied"`
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

// Drives holds end to end through the built binary: set on a live task and on an id with no task row at
// all, rendered by every hand status surface (including an unknown blocked_on, atqamz/hand#417),
// surviving teardown, refusing the spawn that would reuse the id, cleared without a trace.
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

	// migrate-schema is never spawned in this test, so hand status must flag it inconsistent rather than
	// report it satisfied or repeat the stale "waiting on" text - atqamz/hand#417's unknown-blocker case.
	single := runHand(t, home, "status", "fix-login")
	if single.code != 0 || !strings.Contains(single.stdout, `held: "inconsistent: blocked hold waits on unknown task \"migrate-schema\""`) {
		t.Fatalf("status fix-login = %q (exit %d), want the held field flagging its unknown blocker", single.stdout, single.code)
	}

	var one singleStatus
	decodeJSON(t, runHand(t, home, "status", "fix-login", "--json"), &one)
	if one.Held == nil || one.Held.Kind != state.HoldKindBlocked || one.Held.BlockedOn != "migrate-schema" ||
		one.Held.Reason != "needs the new column before this can proceed" || one.Held.SetAt == "" {
		t.Fatalf("single-task JSON held = %+v", one.Held)
	}
	if one.Held.Inconsistent == "" || one.Held.Satisfied {
		t.Fatalf("single-task JSON held = %+v, want an unknown blocker flagged inconsistent, never satisfied", one.Held)
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
		`  fix-login,blocked,"inconsistent: blocked hold waits on unknown task \"migrate-schema\"",`,
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

	// The motivating case: the hold set on this id has to outlive the work it was
	// raised against, which teardown leaves terminal rather than removing.
	runGitIn(t, worktree, "commit", "--allow-empty", "-q", "-m", "wip")
	if got := runHand(t, home, "merge", "fix-login", "--local"); got.code != 0 {
		t.Fatalf("merge --local: exit %d, stderr %q", got.code, got.stderr)
	}
	if got := runHand(t, home, "teardown", "fix-login"); got.code != 0 {
		t.Fatalf("teardown: exit %d, stderr %q", got.code, got.stderr)
	}
	if exists, err := state.Exists(home, "fix-login"); err != nil || !exists {
		t.Fatalf("state.Exists after teardown = %v, %v, want the task row preserved", exists, err)
	}

	survived := runHand(t, home, "status")
	if survived.code != 0 || !strings.Contains(survived.stdout, `  fix-login,blocked,"inconsistent: blocked hold waits on unknown task \"migrate-schema\""`) {
		t.Fatalf("status after teardown = %q, want the torn-down task's hold still listed", survived.stdout)
	}

	// A terminal id is never reused by spawn, even while its old hold remains open.
	writeBrief(t, home, "fix-login")
	respawn := runHand(t, home, "spawn", "fix-login", "demo")
	assertInvocation(t, respawn, 3, "hand reopen fix-login")
	if strings.Contains(respawn.stderr, "open hold") {
		t.Fatalf("spawn stderr = %q, want terminal-task refusal before hold check", respawn.stderr)
	}

	cleared := runHand(t, home, "hold", "clear", "fix-login")
	if cleared.code != 0 || !strings.Contains(cleared.stdout, "result: released\n") {
		t.Fatalf("hold clear = %q (exit %d), stderr %q", cleared.stdout, cleared.code, cleared.stderr)
	}
	if !strings.Contains(cleared.stdout, "hand status fix-login") {
		t.Fatalf("hold clear = %q, want a help line naming the task it released", cleared.stdout)
	}
	assertInvocation(t, runHand(t, home, "hold", "clear", "fix-login"), 3, `hold "fix-login" not found`)

	if got := runHand(t, home, "hold", "clear", "unqueued-work"); got.code != 0 {
		t.Fatalf("hold clear unqueued-work: exit %d, stderr %q", got.code, got.stderr)
	}
	if got := runHand(t, home, "status"); got.code != 0 || !strings.Contains(got.stdout, "held: 0\n") {
		t.Fatalf("status after clearing every hold = %q, want the held count back to zero", got.stdout)
	}
}

// atqamz/hand#417: once a blocked hold's blocker lands, hand status and hand orient must say so - the
// gap the issue reports as a blocked hold being write-only. migrate-schema is merged and torn down to
// terminal (cleanly released, so it leaves ListReconciliationHistories) while needs-migration stays open.
func TestHoldReportsSatisfiedOnceItsBlockerGoesTerminal(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "local-only")
	writeBrief(t, home, "migrate-schema")
	writeBrief(t, home, "needs-migration")

	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	dir := binDir(t)
	writeFakeHerdrStatic(t, dir, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})

	blockerWorktree := filepath.Join(home, "wt-migrate-schema")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "migrate-schema-branch", blockerWorktree)
	heldWorktree := filepath.Join(home, "wt-needs-migration")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "needs-migration-branch", heldWorktree)
	// Both slots declared up front: needs-migration is spawned into the workspace migrate-schema's spawn
	// created (via the spare tab writeFakeHerdrStatic keeps ready) before migrate-schema tears down, so its
	// tab close never has to close the whole workspace out from under the still-live task.
	faketool.Treehouse{Slots: []string{blockerWorktree, heldWorktree}}.Install(t, dir)

	if got := runHand(t, home, "spawn", "migrate-schema", "demo"); got.code != 0 {
		t.Fatalf("spawn migrate-schema: exit %d, stderr %q", got.code, got.stderr)
	}
	if got := runHand(t, home, "spawn", "needs-migration", "demo"); got.code != 0 {
		t.Fatalf("spawn needs-migration: exit %d, stderr %q", got.code, got.stderr)
	}
	runGitIn(t, blockerWorktree, "commit", "--allow-empty", "-q", "-m", "wip")
	if got := runHand(t, home, "merge", "migrate-schema", "--local"); got.code != 0 {
		t.Fatalf("merge --local: exit %d, stderr %q", got.code, got.stderr)
	}
	if got := runHand(t, home, "teardown", "migrate-schema"); got.code != 0 {
		t.Fatalf("teardown migrate-schema: exit %d, stderr %q", got.code, got.stderr)
	}

	if got := runHand(t, home, "hold", "set", "needs-migration",
		"--kind", "blocked", "--reason", "needs the schema migration merged first",
		"--blocked-on", "migrate-schema"); got.code != 0 {
		t.Fatalf("hold set: exit %d, stderr %q", got.code, got.stderr)
	}

	status := runHand(t, home, "status")
	if status.code != 0 || !strings.Contains(status.stdout, `  needs-migration,blocked,"satisfied: migrate-schema is terminal; this hold can be cleared",`) {
		t.Fatalf("status = %q, want the hold reported satisfied, naming its terminal blocker", status.stdout)
	}

	oriented := runHand(t, home, "orient")
	if oriented.code != 0 {
		t.Fatalf("orient: exit %d, stderr %q", oriented.code, oriented.stderr)
	}
	if !strings.Contains(oriented.stdout, "next_action_kind: hold-satisfied\n") ||
		!strings.Contains(oriented.stdout, "next_action_task: needs-migration\n") {
		t.Fatalf("orient = %q, want the satisfied hold to lead next_action", oriented.stdout)
	}
	if !strings.Contains(oriented.stdout, "hold-satisfied,migrate-schema is terminal; this hold can be cleared,hold\n") {
		t.Fatalf("orient = %q, want the actionable item naming its blocker", oriented.stdout)
	}

	// Reporting a satisfied hold did not clear it - only hand hold clear does.
	if _, found, err := state.ReadHold(home, "needs-migration"); err != nil || !found {
		t.Fatalf("ReadHold = %v, %v, want the satisfied hold to survive being reported", found, err)
	}
	if got := runHand(t, home, "hold", "clear", "needs-migration"); got.code != 0 {
		t.Fatalf("hold clear: exit %d, stderr %q", got.code, got.stderr)
	}
}
