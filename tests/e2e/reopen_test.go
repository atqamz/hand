//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
)

type attemptView struct {
	Ordinal   int    `json:"ordinal"`
	Lifecycle string `json:"lifecycle"`
	Harness   string `json:"harness"`
	Worktree  string `json:"worktree"`
}

type detailStatus struct {
	ID               string        `json:"id"`
	Kind             string        `json:"kind"`
	TaskLifecycle    string        `json:"task_lifecycle"`
	AttemptOrdinal   int           `json:"attempt_ordinal"`
	AttemptLifecycle string        `json:"attempt_lifecycle"`
	Harness          string        `json:"harness"`
	CreatedAt        string        `json:"created_at"`
	Attempts         []attemptView `json:"attempts"`
}

type openFleet struct {
	TaskCount int `json:"task_count"`
	Tasks     []struct {
		ID             string `json:"id"`
		AttemptOrdinal int    `json:"attempt_ordinal"`
	} `json:"tasks"`
}

// Drives the whole terminal-task round trip through the built binary: spawn, teardown, a refused second
// spawn that names `hand reopen` as the way back, then the reopen itself. The task row and its durable
// identity survive; the execution attempt does not, and the finished one stays on record beside the new.
func TestReopenIsTheOnlyWayBackFromATerminalTask(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "local-only")
	writeBrief(t, home, "task-1")

	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	worktree := filepath.Join(home, "wt-task-1")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-1-branch", worktree)

	dir := binDir(t)
	writeFakeTreehouse(t, dir, worktree)
	// Two workspaces, because teardown closes the first attempt's: the reopened attempt is dispatched into
	// herdr as freshly as the spawn was, not reattached to the pane the torn-down attempt left behind.
	faketool.Herdr{
		Creates: []faketool.HerdrWorkspace{
			{ID: "ws-1", Label: "demo", Tabs: []faketool.HerdrTab{{ID: "tab-1", Label: "1", Pane: "pane-1"}}},
			{ID: "ws-2", Label: "demo", Tabs: []faketool.HerdrTab{{ID: "tab-2", Label: "1", Pane: "pane-2"}}},
		},
		PaneAgent: "claude",
	}.Install(t, dir)

	if got := runHand(t, home, "spawn", "task-1", "demo"); got.code != 0 {
		t.Fatalf("spawn: exit %d, stderr %q", got.code, got.stderr)
	}
	spawnedTask, spawnedAttempt := readTaskAttempt(t, home, "task-1")
	if spawnedAttempt.Ordinal != 1 || spawnedAttempt.Herdr.PaneID != "pane-1" {
		t.Fatalf("spawned attempt = %+v, want the first attempt in pane-1", spawnedAttempt)
	}
	running := runHand(t, home, "status", "task-1")
	if running.code != 0 || !strings.Contains(running.stdout, "Attempt 1: running (claude") {
		t.Fatalf("status after spawn = %q (exit %d), want attempt 1 running", running.stdout, running.code)
	}

	runGitIn(t, worktree, "commit", "--allow-empty", "-q", "-m", "wip")
	if got := runHand(t, home, "merge", "task-1", "--local"); got.code != 0 {
		t.Fatalf("merge --local: exit %d, stderr %q", got.code, got.stderr)
	}
	if got := runHand(t, home, "teardown", "task-1"); got.code != 0 {
		t.Fatalf("teardown: exit %d, stderr %q", got.code, got.stderr)
	}

	var torndown detailStatus
	decodeJSON(t, runHand(t, home, "status", "task-1", "--json"), &torndown)
	if torndown.TaskLifecycle != "terminal" || torndown.AttemptLifecycle != "completed" {
		t.Fatalf("torn-down detail = %+v, want a terminal task whose attempt is completed", torndown)
	}
	var afterTeardown openFleet
	decodeJSON(t, runHand(t, home, "status", "--json"), &afterTeardown)
	if afterTeardown.TaskCount != 0 || len(afterTeardown.Tasks) != 0 {
		t.Fatalf("fleet after teardown = %+v, want the terminal task out of the open fleet", afterTeardown)
	}

	// The one path back: spawn refuses the id outright and names the command that reopens it, so nothing
	// but hand reopen can put a terminal task back to work.
	assertInvocation(t, runHand(t, home, "spawn", "task-1", "demo"), 3, "use hand reopen task-1")

	reopened := runHand(t, home, "reopen", "task-1")
	if reopened.code != 0 {
		t.Fatalf("reopen: exit %d, stderr %q", reopened.code, reopened.stderr)
	}
	for _, want := range []string{"result: reopened\n", "attempt: new\n", "project: demo\n", "kind: ship\n"} {
		if !strings.Contains(reopened.stdout, want) {
			t.Fatalf("reopen stdout = %q, want it to contain %q", reopened.stdout, want)
		}
	}

	var detail detailStatus
	decodeJSON(t, runHand(t, home, "status", "task-1", "--json"), &detail)
	if detail.TaskLifecycle != "open" || detail.AttemptOrdinal != 2 || detail.AttemptLifecycle != "running" {
		t.Fatalf("reopened detail = %+v, want an open task on a running second attempt", detail)
	}
	if detail.Kind != string(spawnedTask.Kind) || detail.CreatedAt != spawnedTask.CreatedAt {
		t.Fatalf("reopened detail = %+v, want the task's durable identity (kind %q, created %q) preserved",
			detail, spawnedTask.Kind, spawnedTask.CreatedAt)
	}
	if len(detail.Attempts) != 2 ||
		detail.Attempts[0].Ordinal != 1 || detail.Attempts[0].Lifecycle != "completed" ||
		detail.Attempts[1].Ordinal != 2 || detail.Attempts[1].Lifecycle != "running" {
		t.Fatalf("attempt history = %+v, want the finished first attempt kept beside the running second", detail.Attempts)
	}

	text := runHand(t, home, "status", "task-1")
	for _, want := range []string{"Attempt 1: completed (claude", "Attempt 2: running (claude"} {
		if !strings.Contains(text.stdout, want) {
			t.Fatalf("status after reopen = %q, want it to contain %q", text.stdout, want)
		}
	}

	var afterReopen openFleet
	decodeJSON(t, runHand(t, home, "status", "--json"), &afterReopen)
	if afterReopen.TaskCount != 1 || len(afterReopen.Tasks) != 1 ||
		afterReopen.Tasks[0].ID != "task-1" || afterReopen.Tasks[0].AttemptOrdinal != 2 {
		t.Fatalf("fleet after reopen = %+v, want the task back in the open fleet on its second attempt", afterReopen)
	}

	reopenedTask, reopenedAttempt := readTaskAttempt(t, home, "task-1")
	if reopenedAttempt.ID == spawnedAttempt.ID || reopenedAttempt.Herdr.PaneID != "pane-2" {
		t.Fatalf("reopened attempt = %+v, want a new attempt row in pane-2, not the torn-down %+v", reopenedAttempt, spawnedAttempt)
	}
	// Task-owned delivery facts are not an execution detail: the merge recorded before teardown is still
	// on the row the reopened attempt hangs off.
	if !reopenedTask.MergeExecuted {
		t.Fatalf("reopened task = %+v, want the merge recorded before teardown preserved", reopenedTask)
	}
}
