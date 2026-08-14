//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/state"
)

func TestMechanicalPlanDispatchesAgainstTheRegisteredBase(t *testing.T) {
	home, clonePath, worktree, base, treehouseLog, herdrLog := setupExecutionDispatch(t, "mechanical")
	writeBriefWith(t, home, "task-1", mechanicalBrief(base))

	got := runHand(t, home, "spawn", "task-1", "demo")
	if got.code != 0 {
		t.Fatalf("spawn: exit %d, stderr %q", got.code, got.stderr)
	}

	task, attempt := readTaskAttempt(t, home, "task-1")
	if task.Project != "demo" || attempt.Worktree != worktree {
		t.Fatalf("spawned task = %+v, attempt = %+v, want project demo and worktree %s", task, attempt, worktree)
	}
	if log := readOptionalLog(t, treehouseLog); !strings.Contains(log, "treehouse get") {
		t.Fatalf("treehouse log = %q, want acquisition", log)
	}
	if log := readOptionalLog(t, herdrLog); !strings.Contains(log, "herdr pane run") {
		t.Fatalf("herdr log = %q, want launch", log)
	}
	if current := strings.TrimSpace(runGitIn(t, clonePath, "rev-parse", "refs/heads/main^{commit}")); current != base {
		t.Fatalf("current base = %q, want planned base %q", current, base)
	}
}

func TestMechanicalPlanRefusesBeforeWorktreeAcquisitionWhenBaseIsStale(t *testing.T) {
	home, clonePath, _, planned, treehouseLog, herdrLog := setupExecutionDispatch(t, "mechanical")
	writeBriefWith(t, home, "task-1", mechanicalBrief(planned))
	current := advanceDefaultBranch(t, clonePath)

	got := runHand(t, home, "spawn", "task-1", "demo")
	assertInvocation(t, got, 3, "mechanical plan is stale")
	message := errorMessage(t, got.stderr)
	for _, want := range []string{planned, current, "re-check and rewrite"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error = %q, want %q", message, want)
		}
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state.Exists = %v, %v, want no Attempt or Task", exists, err)
	}
	if log := readOptionalLog(t, treehouseLog); log != "" {
		t.Fatalf("treehouse log = %q, want no acquisition", log)
	}
	if log := readOptionalLog(t, herdrLog); log != "" {
		t.Fatalf("herdr log = %q, want no launch", log)
	}
}

func TestStandardAndDeepPlansDoNotUseMechanicalStaleRefusal(t *testing.T) {
	for _, class := range []string{"standard", "deep"} {
		t.Run(class, func(t *testing.T) {
			home, clonePath, worktree, planned, treehouseLog, herdrLog := setupExecutionDispatch(t, class)
			writeBriefWith(t, home, "task-1", executionBrief(class, planned))
			advanceDefaultBranch(t, clonePath)

			got := runHand(t, home, "spawn", "task-1", "demo")
			if got.code != 0 {
				t.Fatalf("spawn: exit %d, stderr %q", got.code, got.stderr)
			}
			_, attempt := readTaskAttempt(t, home, "task-1")
			if attempt.Worktree != worktree {
				t.Fatalf("attempt.Worktree = %q, want %q", attempt.Worktree, worktree)
			}
			if log := readOptionalLog(t, treehouseLog); !strings.Contains(log, "treehouse get") {
				t.Fatalf("treehouse log = %q, want acquisition", log)
			}
			if log := readOptionalLog(t, herdrLog); !strings.Contains(log, "herdr pane run") {
				t.Fatalf("herdr log = %q, want launch", log)
			}
		})
	}
}

func TestLegacyBriefStillDispatchesWithoutExecutionPlanPreflight(t *testing.T) {
	home, _, worktree, _, treehouseLog, herdrLog := setupExecutionDispatch(t, "")
	writeBrief(t, home, "task-1")

	got := runHand(t, home, "spawn", "task-1", "demo")
	if got.code != 0 {
		t.Fatalf("spawn: exit %d, stderr %q", got.code, got.stderr)
	}
	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.Worktree != worktree {
		t.Fatalf("attempt.Worktree = %q, want %q", attempt.Worktree, worktree)
	}
	if log := readOptionalLog(t, treehouseLog); !strings.Contains(log, "treehouse get") {
		t.Fatalf("treehouse log = %q, want acquisition", log)
	}
	if log := readOptionalLog(t, herdrLog); !strings.Contains(log, "herdr pane run") {
		t.Fatalf("herdr log = %q, want launch", log)
	}
}

func setupExecutionDispatch(t *testing.T, class string) (home, clonePath, worktree, base, treehouseLog, herdrLog string) {
	t.Helper()
	home = newHome(t)
	registerProject(t, home, "demo", "local-only")
	clonePath = filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	base = strings.TrimSpace(runGitIn(t, clonePath, "rev-parse", "refs/heads/main^{commit}"))
	worktree = filepath.Join(home, "wt-task-1")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-1-branch", worktree)

	dir := binDir(t)
	treehouseLog = filepath.Join(t.TempDir(), "treehouse.log")
	herdrLog = filepath.Join(t.TempDir(), "herdr.log")
	faketool.Treehouse{Slots: []string{worktree}, Log: treehouseLog}.Install(t, dir)
	writeFakeHerdrStaticLogged(t, dir, herdrLog, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})
	if class != "" {
		writeBriefWith(t, home, "task-1", executionBrief(class, base))
	}
	return home, clonePath, worktree, base, treehouseLog, herdrLog
}

func executionBrief(class, plannedAgainst string) string {
	return "---\nexecution_class: " + class + "\nplanned_against: " + plannedAgainst + "\n---\n\n# brief\n"
}

func mechanicalBrief(plannedAgainst string) string {
	return executionBrief("mechanical", plannedAgainst)
}

func advanceDefaultBranch(t *testing.T, clonePath string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(clonePath, "advanced.txt"), []byte("advanced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, clonePath, "add", "advanced.txt")
	runGitIn(t, clonePath, "commit", "-q", "-m", "advance default branch")
	return strings.TrimSpace(runGitIn(t, clonePath, "rev-parse", "refs/heads/main^{commit}"))
}

func readOptionalLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}
