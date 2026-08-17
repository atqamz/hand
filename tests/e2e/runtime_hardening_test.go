//go:build e2e

package e2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/routing"
	"github.com/atqamz/hand/internal/state"
)

func TestRuntimeRouteMatrixPersistsEveryCanonicalCell(t *testing.T) {
	type routeCell struct {
		kind  string
		class string
	}
	cells := []routeCell{
		{kind: "scout", class: "mechanical"},
		{kind: "scout", class: "standard"},
		{kind: "scout", class: "deep"},
		{kind: "ship", class: "mechanical"},
		{kind: "ship", class: "standard"},
		{kind: "ship", class: "deep"},
	}

	home := newHome(t)
	registerProject(t, home, "demo", "local-only")
	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	plannedAgainst := strings.TrimSpace(runGitIn(t, clonePath, "rev-parse", "HEAD"))
	worktrees := make([]string, 0, len(cells))
	for i := range cells {
		worktree := filepath.Join(home, fmt.Sprintf("wt-task-%d", i))
		runGitIn(t, clonePath, "worktree", "add", "-q", "-b", fmt.Sprintf("task-%d-branch", i), worktree)
		worktrees = append(worktrees, worktree)
	}

	dir := binDir(t)
	writeFakeBin(t, dir, "claude", "exit 0\n")
	faketool.Treehouse{Slots: worktrees}.Install(t, dir)
	workspaces := make([]faketool.HerdrWorkspace, 0, len(cells))
	for i := range cells {
		workspaces = append(workspaces, faketool.HerdrWorkspace{
			ID: fmt.Sprintf("workspace-%d", i), Label: "demo",
			Tabs: []faketool.HerdrTab{{ID: fmt.Sprintf("tab-%d", i), Label: "1", Pane: fmt.Sprintf("pane-%d", i)}},
		})
	}
	tabCreates := make([]faketool.HerdrTab, 0, len(cells)-1)
	for i := 1; i < len(cells); i++ {
		tabCreates = append(tabCreates, faketool.HerdrTab{ID: fmt.Sprintf("tab-extra-%d", i), Pane: fmt.Sprintf("pane-extra-%d", i)})
	}
	faketool.Herdr{Creates: workspaces, TabCreates: tabCreates, PaneAgent: "claude", PaneStatus: "working"}.Install(t, dir)

	for i, cell := range cells {
		profile := fmt.Sprintf("operator-%s-%d", cell.class, i)
		if got := runHand(t, home, "config", "profile", "set", profile, "--harness", "claude", "--model", "model-"+cell.class, "--effort", "effort-"+cell.class); got.code != 0 {
			t.Fatalf("profile %s: exit %d, stderr %q", profile, got.code, got.stderr)
		}
		if got := runHand(t, home, "config", "route", "set", cell.kind, cell.class, profile); got.code != 0 {
			t.Fatalf("route %s.%s: exit %d, stderr %q", cell.kind, cell.class, got.code, got.stderr)
		}
	}

	for i, cell := range cells {
		id := fmt.Sprintf("task-%d", i)
		writeBriefWith(t, home, id, executionBrief(cell.class, plannedAgainst))
		args := []string{"spawn", id, "demo", "--skip-gate-check"}
		if cell.kind == "scout" {
			args = append(args, "--scout")
		}
		got := runHand(t, home, args...)
		if got.code != 0 {
			t.Fatalf("spawn %s.%s: exit %d, stderr %q", cell.kind, cell.class, got.code, got.stderr)
		}
		task, attempt := readTaskAttempt(t, home, id)
		profile := fmt.Sprintf("operator-%s-%d", cell.class, i)
		if task.Kind != cell.kind || attempt.ExecutionClass != cell.class || attempt.RequestedProfile != profile || attempt.Harness != "claude" || attempt.Model != "model-"+cell.class || attempt.Effort != "effort-"+cell.class || attempt.PlannedAgainst != plannedAgainst || attempt.RoutingSource != string(routing.RoutingSourceRoute) {
			t.Fatalf("%s.%s task=%+v attempt=%+v, want immutable route snapshot for %s", cell.kind, cell.class, task, attempt, profile)
		}
	}
}

func TestReopenUsesCurrentRouteWithoutMutatingHistoricalAttempt(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "local-only")
	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	plannedAgainst := strings.TrimSpace(runGitIn(t, clonePath, "rev-parse", "HEAD"))
	worktree := filepath.Join(home, "wt-task-1")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-1-branch", worktree)

	dir := binDir(t)
	writeFakeBin(t, dir, "claude", "exit 0\n")
	faketool.Treehouse{Slots: []string{worktree}}.Install(t, dir)
	faketool.Herdr{
		Creates: []faketool.HerdrWorkspace{
			{ID: "workspace-1", Label: "demo", Tabs: []faketool.HerdrTab{{ID: "tab-1", Label: "1", Pane: "pane-1"}}},
			{ID: "workspace-2", Label: "demo", Tabs: []faketool.HerdrTab{{ID: "tab-2", Label: "1", Pane: "pane-2"}}},
		},
		PaneAgent: "claude", PaneStatus: "working",
	}.Install(t, dir)

	setExecutionProfile(t, home, "route-a", "claude", "model-a", "effort-a")
	setHardeningRoute(t, home, "ship", "standard", "route-a")
	writeBriefWith(t, home, "task-1", executionBrief("standard", plannedAgainst))
	if got := runHand(t, home, "spawn", "task-1", "demo", "--skip-gate-check"); got.code != 0 {
		t.Fatalf("spawn: exit %d, stderr %q", got.code, got.stderr)
	}
	_, first := readTaskAttempt(t, home, "task-1")
	if first.RequestedProfile != "route-a" || first.Model != "model-a" || first.Effort != "effort-a" {
		t.Fatalf("first Attempt = %+v, want route-a snapshot", first)
	}

	runGitIn(t, worktree, "commit", "--allow-empty", "-q", "-m", "finish first attempt")
	if got := runHand(t, home, "merge", "task-1", "--local"); got.code != 0 {
		t.Fatalf("merge --local: exit %d, stderr %q", got.code, got.stderr)
	}
	if got := runHand(t, home, "teardown", "task-1"); got.code != 0 {
		t.Fatalf("teardown: exit %d, stderr %q", got.code, got.stderr)
	}

	setExecutionProfile(t, home, "route-b", "claude", "model-b", "effort-b")
	setHardeningRoute(t, home, "ship", "standard", "route-b")
	if got := runHand(t, home, "reopen", "task-1", "--skip-gate-check"); got.code != 0 {
		t.Fatalf("reopen: exit %d, stderr %q", got.code, got.stderr)
	}

	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Attempts) != 2 || history.ActiveAttempt == nil {
		t.Fatalf("history = %+v, want two Attempts and one active replacement", history)
	}
	old, current := history.Attempts[0], *history.ActiveAttempt
	if old.ID == current.ID || old.Lifecycle != state.AttemptCompleted {
		t.Fatalf("old=%+v current=%+v, want distinct terminal historical Attempt", old, current)
	}
	if old.RequestedProfile != "route-a" || old.Model != "model-a" || old.Effort != "effort-a" || old.ExecutionClass != "standard" {
		t.Fatalf("historical Attempt changed after route edit: %+v", old)
	}
	if current.RequestedProfile != "route-b" || current.Model != "model-b" || current.Effort != "effort-b" || current.ExecutionClass != "standard" {
		t.Fatalf("reopened Attempt = %+v, want current route-b snapshot", current)
	}
}

func TestSelectedProfileFailureDoesNotFallBackToAnotherProfile(t *testing.T) {
	home, _, treehouseLog, herdrLog := setupClassifiedRoutingRefusal(t)
	setExecutionProfile(t, home, "valid-alternative", "claude", "good-model", "high")
	setExecutionProfile(t, home, "selected-incompatible", "grok", "", "")
	setHardeningRoute(t, home, "ship", "standard", "selected-incompatible")
	writeBriefWith(t, home, "task-1", executionBrief("standard", strings.Repeat("a", 40)))

	got := runHand(t, home, "spawn", "task-1", "demo", "--skip-gate-check")
	assertInvocation(t, got, 3, `harness "grok" is not installed on PATH`)
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state.Exists = %v, %v, want no Task or Attempt", exists, err)
	}
	if log := readOptionalLog(t, treehouseLog); log != "" {
		t.Fatalf("treehouse log = %q, want no acquisition after selected Profile failure", log)
	}
	if log := readOptionalLog(t, herdrLog); log != "" {
		t.Fatalf("herdr log = %q, want no resource creation after selected Profile failure", log)
	}
}

func setHardeningRoute(t *testing.T, home, kind, class, profile string) {
	t.Helper()
	got := runHand(t, home, "config", "route", "set", kind, class, profile)
	if got.code != 0 {
		t.Fatalf("route %s.%s = profile %s: exit %d, stderr %q", kind, class, profile, got.code, got.stderr)
	}
}
