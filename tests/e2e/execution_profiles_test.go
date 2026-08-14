//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutionProfilesFreezeExistingAttemptAndRouteFutureTask(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "local-only")
	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	worktreeOne := filepath.Join(home, "wt-task-1")
	worktreeTwo := filepath.Join(home, "wt-task-2")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-1-branch", worktreeOne)
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-2-branch", worktreeTwo)

	dir := binDir(t)
	writeFakeBin(t, dir, "claude", "exit 0\n")
	writeFakeBin(t, dir, "codex", "exit 0\n")
	writeFakeTreehouse(t, dir, worktreeOne, worktreeTwo)
	writeFakeHerdrStaticLogged(t, dir, "", herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})

	for _, args := range [][]string{
		{"config", "profile", "set", "initial", "--harness", "claude", "--model", "initial-model", "--effort", "high"},
		{"config", "profile", "set", "future", "--harness", "codex", "--model", "future-model", "--effort", "medium"},
		{"config", "route", "set", "ship", "standard", "initial"},
	} {
		if got := runHand(t, home, args...); got.code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, got.code, got.stderr)
		}
	}

	writeBriefWith(t, home, "task-1", executionBrief("standard", "planned-revision"))
	spawned := runHand(t, home, "spawn", "task-1", "demo", "--skip-gate-check")
	if spawned.code != 0 {
		t.Fatalf("spawn task-1: exit %d, stderr %q", spawned.code, spawned.stderr)
	}
	_, first := readTaskAttempt(t, home, "task-1")
	if first.RequestedProfile != "initial" || first.Harness != "claude" || first.Model != "initial-model" || first.Effort != "high" || first.ExecutionClass != "standard" {
		t.Fatalf("task-1 attempt = %+v, want initial route snapshot", first)
	}

	if got := runHand(t, home, "status", "task-1"); got.code != 0 || !strings.Contains(got.stdout, "profile: initial") || !strings.Contains(got.stdout, "model: initial-model") {
		t.Fatalf("task-1 status = %+v, want initial snapshot", got)
	}

	for _, args := range [][]string{
		{"config", "profile", "set", "initial", "--harness", "codex", "--model", "edited-model", "--effort", "low"},
		{"config", "route", "set", "ship", "standard", "future"},
	} {
		if got := runHand(t, home, args...); got.code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, got.code, got.stderr)
		}
	}
	unchanged := runHand(t, home, "status", "task-1", "--json")
	if unchanged.code != 0 {
		t.Fatalf("status task-1 after config change: exit %d, stderr %q", unchanged.code, unchanged.stderr)
	}
	for _, want := range []string{`"execution_class": "standard"`, `"profile": "initial"`, `"harness": "claude"`, `"model": "initial-model"`, `"effort": "high"`} {
		if !strings.Contains(unchanged.stdout, want) {
			t.Fatalf("task-1 status after config change = %q, want %q", unchanged.stdout, want)
		}
	}

	writeBriefWith(t, home, "task-2", executionBrief("standard", "planned-revision"))
	spawned = runHand(t, home, "spawn", "task-2", "demo", "--skip-gate-check")
	if spawned.code != 0 {
		t.Fatalf("spawn task-2: exit %d, stderr %q", spawned.code, spawned.stderr)
	}
	_, second := readTaskAttempt(t, home, "task-2")
	if second.RequestedProfile != "future" || second.Harness != "codex" || second.Model != "future-model" || second.Effort != "medium" || second.ExecutionClass != "standard" {
		t.Fatalf("task-2 attempt = %+v, want future route snapshot", second)
	}

	if _, err := os.Stat(filepath.Join(home, "config", "profiles", "initial", "model")); err != nil {
		t.Fatalf("profile configuration was not persisted through the CLI: %v", err)
	}
}
