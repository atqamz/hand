//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/state"
)

// writeBriefWith writes a brief whose body the test controls, so a declaration
// block can be put in front of real prose.
func writeBriefWith(t *testing.T, home, id, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "data", id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", id, "brief.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeFakeHerdrLaunchLog is writeFakeHerdrStatic plus a record of the launch
// command every "pane run" carried, which is the only place a resolved
// model/effort becomes observable to an operator.
func writeFakeHerdrLaunchLog(t *testing.T, dir, launchLog string, ids herdrIDs) {
	t.Helper()
	workspaceList := herdrOK(t, map[string]any{"workspaces": []any{}})
	workspaceCreate := herdrOK(t, map[string]any{"workspace": map[string]any{"workspace_id": ids.WorkspaceID, "label": ids.Label, "tab_count": 1}})
	tabCreate := herdrOK(t, map[string]any{
		"tab":       map[string]any{"tab_id": ids.TabID, "workspace_id": ids.WorkspaceID, "label": ids.Label},
		"root_pane": map[string]any{"pane_id": ids.PaneID, "tab_id": ids.TabID, "workspace_id": ids.WorkspaceID, "agent_status": "working"},
	})
	status := ids.PaneStatus
	if status == "" {
		status = "working"
	}
	paneGet := herdrOK(t, map[string]any{"pane": map[string]any{"pane_id": ids.PaneID, "tab_id": ids.TabID, "workspace_id": ids.WorkspaceID, "agent": "claude", "agent_status": status}})
	tabList := herdrOK(t, map[string]any{"tabs": []any{
		map[string]any{"tab_id": "tab-old", "workspace_id": "ws-old", "label": ids.Label},
		map[string]any{"tab_id": "tab-other", "workspace_id": "ws-old", "label": "other"},
	}})

	body := strings.Join([]string{
		`  "workspace list") echo ` + shellSingleQuote(workspaceList) + ` ;;`,
		`  "workspace create") echo ` + shellSingleQuote(workspaceCreate) + ` ;;`,
		`  "tab create") echo ` + shellSingleQuote(tabCreate) + ` ;;`,
		`  "tab list") echo ` + shellSingleQuote(tabList) + ` ;;`,
		`  "tab close") echo ` + shellSingleQuote(herdrOK(t, map[string]any{"type": "ok"})) + ` ;;`,
		`  "pane run") printf '%s\n' "$4" >> ` + shellSingleQuote(launchLog) + ` ;;`,
		`  "pane send-keys") ;;`,
		`  "pane read") printf 'Welcome to Claude Code\n> \n  ? for shortcuts\n' ;;`,
		`  "pane get") echo ` + shellSingleQuote(paneGet) + ` ;;`,
	}, "\n")
	writeFakeDispatch(t, dir, "herdr", "", "$1 $2", body)
}

func readLaunchLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read launch log: %v", err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func TestSpawnHonorsBriefDeclaredTier(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "local-only")
	writeConfig(t, home, "model", "claude-sonnet-5\n")

	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)

	launchLog := filepath.Join(t.TempDir(), "launch.log")
	dir := binDir(t)
	writeFakeHerdrLaunchLog(t, dir, launchLog, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})

	// A declaring brief: unknown keys are ignored by design, and the prose
	// under the fence carries ordinary colons the parser must not reach.
	declaring := "---\nmodel: claude-opus-5\neffort: high\npriority: urgent\n---\n\n# deep refactor\n\nNote: this one is hard.\n"
	// A plain brief: no fence, and a first prose line with a colon in it.
	plain := "# small fix\n\nGoal: rename one field.\n"

	cases := []struct {
		id         string
		brief      string
		args       []string
		wantModel  string
		wantEffort string
		wantFlags  []string
		denyFlags  []string
	}{
		{
			id:         "task-declared",
			brief:      declaring,
			wantModel:  "claude-opus-5",
			wantEffort: "high",
			wantFlags: []string{
				"--model 'claude-opus-5'",
				"--effort 'high'",
				"are dispatch metadata, not task content",
			},
			denyFlags: []string{"claude-sonnet-5"},
		},
		{
			id:         "task-flag-wins",
			brief:      declaring,
			args:       []string{"--model", "claude-fable-5"},
			wantModel:  "claude-fable-5",
			wantEffort: "high",
			wantFlags:  []string{"--model 'claude-fable-5'", "--effort 'high'"},
			denyFlags:  []string{"claude-opus-5"},
		},
		{
			id:        "task-plain",
			brief:     plain,
			wantModel: "claude-sonnet-5",
			wantFlags: []string{"--model 'claude-sonnet-5'"},
			denyFlags: []string{"--effort", "are dispatch metadata, not task content"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			writeBriefWith(t, home, tc.id, tc.brief)
			worktree := filepath.Join(home, "wt-"+tc.id)
			runGitIn(t, clonePath, "worktree", "add", "-q", "-b", tc.id+"-branch", worktree)
			writeFakeTreehouse(t, dir, worktree)

			args := append([]string{"spawn", tc.id, "demo"}, tc.args...)
			spawned := runHand(t, home, args...)
			if spawned.code != 0 {
				t.Fatalf("spawn: exit %d, stderr %q", spawned.code, spawned.stderr)
			}
			if strings.Contains(spawned.stderr, "warning:") {
				t.Fatalf("spawn under claude warned about effort: %q", spawned.stderr)
			}

			task, err := state.Read(home, tc.id)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("persisted tier: model=%q effort=%q", task.Model, task.Effort)
			if task.Model != tc.wantModel || task.Effort != tc.wantEffort {
				t.Fatalf("persisted tier = model %q effort %q, want model %q effort %q",
					task.Model, task.Effort, tc.wantModel, tc.wantEffort)
			}

			lines := readLaunchLog(t, launchLog)
			launch := lines[len(lines)-1]
			t.Logf("launch command: %s", launch)
			for _, want := range tc.wantFlags {
				if !strings.Contains(launch, want) {
					t.Fatalf("launch command %q missing %q", launch, want)
				}
			}
			for _, deny := range tc.denyFlags {
				if strings.Contains(launch, deny) {
					t.Fatalf("launch command %q unexpectedly contains %q", launch, deny)
				}
			}
		})
	}
}

func TestPromoteHonorsBriefDeclaredTier(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	writeConfig(t, home, "model", "claude-sonnet-5\n")
	writeBriefWith(t, home, "task-1", "---\nmodel: claude-opus-5\neffort: max\n---\n\n# promoted scout\n")
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("# report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{
		ID: "task-1", Project: "demo", Kind: state.KindScout,
		Worktree:  filepath.Join(home, "wt-scout-old"),
		Herdr:     state.Herdr{WorkspaceID: "ws-old", TabID: "tab-old", PaneID: "pane-old"},
		Model:     "claude-sonnet-5",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	launchLog := filepath.Join(t.TempDir(), "launch.log")
	dir := binDir(t)
	writeFakeTreehouse(t, dir, filepath.Join(home, "wt-ship-new"))
	writeFakeHerdrLaunchLog(t, dir, launchLog, herdrIDs{WorkspaceID: "ws-new", TabID: "tab-new", PaneID: "pane-new", Label: "demo", PaneStatus: "done"})

	promoted := runHand(t, home, "promote", "task-1")
	if promoted.code != 0 {
		t.Fatalf("promote: exit %d, stderr %q", promoted.code, promoted.stderr)
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("persisted tier: model=%q effort=%q", task.Model, task.Effort)
	if task.Model != "claude-opus-5" || task.Effort != "max" {
		t.Fatalf("persisted tier = model %q effort %q, want claude-opus-5/max from the brief", task.Model, task.Effort)
	}

	launch := readLaunchLog(t, launchLog)[0]
	t.Logf("launch command: %s", launch)
	if !strings.Contains(launch, "--model 'claude-opus-5' --effort 'max'") {
		t.Fatalf("launch command %q, want the brief's declared tier applied", launch)
	}
}

// TestSpawnWarnsOnEffortIncapableHarness covers the deliberate middle path for
// a declared effort no harness flag can carry: the spawn proceeds, the model
// still applies, and the dropped effort is said out loud on stderr.
func TestSpawnWarnsOnEffortIncapableHarness(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "local-only")
	writeConfig(t, home, "harness", "opencode\n")
	writeBriefWith(t, home, "task-1", "---\nmodel: grok-code\neffort: high\n---\n\n# opencode task\n")

	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	worktree := filepath.Join(home, "wt-task-1")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-1-branch", worktree)

	launchLog := filepath.Join(t.TempDir(), "launch.log")
	dir := binDir(t)
	writeFakeTreehouse(t, dir, worktree)
	writeFakeHerdrLaunchLog(t, dir, launchLog, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})

	spawned := runHand(t, home, "spawn", "task-1", "demo")
	if spawned.code != 0 {
		t.Fatalf("spawn: exit %d, stderr %q", spawned.code, spawned.stderr)
	}
	if !strings.Contains(spawned.stderr, `warning: harness "opencode" has no effort flag, ignoring effort "high"`) {
		t.Fatalf("stderr = %q, want the effort-incapable warning", spawned.stderr)
	}

	launch := readLaunchLog(t, launchLog)[0]
	t.Logf("launch command: %s", launch)
	if !strings.Contains(launch, "--model 'grok-code'") || strings.Contains(launch, "--effort") {
		t.Fatalf("launch command %q, want the declared model applied and no effort flag", launch)
	}
}
