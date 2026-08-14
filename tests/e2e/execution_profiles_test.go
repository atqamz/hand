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

func TestExecutionProfilesFreezeExistingAttemptAndRouteFutureTask(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "local-only")
	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	plannedAgainst := strings.TrimSpace(runGitIn(t, clonePath, "rev-parse", "HEAD"))
	worktreeOne := filepath.Join(home, "wt-task-1")
	worktreeTwo := filepath.Join(home, "wt-task-2")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-1-branch", worktreeOne)
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-2-branch", worktreeTwo)

	dir := binDir(t)
	writeFakeBin(t, dir, "claude", "exit 0\n")
	writeFakeBin(t, dir, "codex", "exit 0\n")
	faketool.Treehouse{Slots: []string{worktreeOne, worktreeTwo}}.Install(t, dir)
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

	writeBriefWith(t, home, "task-1", executionBrief("standard", plannedAgainst))
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

	writeBriefWith(t, home, "task-2", executionBrief("standard", plannedAgainst))
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

func TestClassifiedRoutingRefusalsStopBeforeStateAndProvisioning(t *testing.T) {
	for _, test := range []struct {
		name      string
		class     string
		configure func(t *testing.T, home, bin string)
		args      []string
		want      string
	}{
		{
			name:  "missing route",
			class: "standard",
			want:  "route ship.standard is not configured",
		},
		{
			name:  "dangling profile",
			class: "standard",
			configure: func(t *testing.T, home, _ string) {
				t.Helper()
				setExecutionProfile(t, home, "missing", "claude", "", "")
				setExecutionRoute(t, home, "standard", "missing")
				if err := os.RemoveAll(filepath.Join(home, "config", "profiles", "missing")); err != nil {
					t.Fatal(err)
				}
			},
			want: "profile \"missing\" is not configured",
		},
		{
			name:  "missing PATH",
			class: "standard",
			configure: func(t *testing.T, home, _ string) {
				t.Helper()
				setExecutionProfile(t, home, "daily", "claude", "", "")
				setExecutionRoute(t, home, "standard", "daily")
			},
			want: "harness \"claude\" is not installed on PATH",
		},
		{
			name:  "capability mismatch",
			class: "standard",
			configure: func(t *testing.T, home, bin string) {
				t.Helper()
				writeFakeBin(t, bin, "grok", "exit 0\n")
				setExecutionProfile(t, home, "daily", "grok", "", "")
				setExecutionRoute(t, home, "standard", "daily")
			},
			args: []string{"--model", "opaque"},
			want: "harness \"grok\" takes no model",
		},
		{
			name:  "mechanical prompt incompatibility",
			class: "mechanical",
			configure: func(t *testing.T, home, bin string) {
				t.Helper()
				writeFakeBin(t, bin, "grok", "exit 0\n")
				setExecutionProfile(t, home, "daily", "grok", "", "")
				setExecutionRoute(t, home, "mechanical", "daily")
			},
			want: "cannot carry the required mechanical worker guidance",
		},
		{
			name:  "stale mechanical plan",
			class: "mechanical",
			configure: func(t *testing.T, home, bin string) {
				t.Helper()
				writeFakeBin(t, bin, "claude", "exit 0\n")
				setExecutionProfile(t, home, "daily", "claude", "", "")
				setExecutionRoute(t, home, "mechanical", "daily")
			},
			want: "mechanical plan is stale",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, _, treehouseLog, herdrLog := setupClassifiedRoutingRefusal(t)
			bin := binDir(t)
			if test.configure != nil {
				test.configure(t, home, bin)
			}
			writeBriefWith(t, home, "task-1", executionBrief(test.class, strings.Repeat("a", 40)))

			args := append([]string{"spawn", "task-1", "demo", "--skip-gate-check"}, test.args...)
			got := runHand(t, home, args...)
			assertInvocation(t, got, 3, test.want)
			if exists, err := state.Exists(home, "task-1"); err != nil || exists {
				t.Fatalf("state.Exists = %v, %v, want no Task or Attempt", exists, err)
			}
			for _, log := range []struct {
				name string
				path string
			}{
				{name: "treehouse", path: treehouseLog},
				{name: "herdr", path: herdrLog},
			} {
				if got := readOptionalLog(t, log.path); got != "" {
					t.Fatalf("%s log = %q, want no worktree or pane operation", log.name, got)
				}
			}
		})
	}
}

func setupClassifiedRoutingRefusal(t *testing.T) (home, bin, treehouseLog, herdrLog string) {
	t.Helper()
	home = newHome(t)
	registerProject(t, home, "demo", "local-only")
	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	worktree := filepath.Join(home, "wt-task-1")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-1-branch", worktree)

	bin = binDir(t)
	treehouseLog = filepath.Join(t.TempDir(), "treehouse.log")
	herdrLog = filepath.Join(t.TempDir(), "herdr.log")
	faketool.Treehouse{Slots: []string{worktree}, Log: treehouseLog}.Install(t, bin)
	writeFakeHerdrStaticLogged(t, bin, herdrLog, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})
	return home, bin, treehouseLog, herdrLog
}

func setExecutionProfile(t *testing.T, home, name, harness, model, effort string) {
	t.Helper()
	args := []string{"config", "profile", "set", name, "--harness", harness}
	if model != "" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	if got := runHand(t, home, args...); got.code != 0 {
		t.Fatalf("%v: exit %d, stderr %q", args, got.code, got.stderr)
	}
}

func setExecutionRoute(t *testing.T, home, class, profile string) {
	t.Helper()
	args := []string{"config", "route", "set", "ship", class, profile}
	if got := runHand(t, home, args...); got.code != 0 {
		t.Fatalf("%v: exit %d, stderr %q", args, got.code, got.stderr)
	}
}
