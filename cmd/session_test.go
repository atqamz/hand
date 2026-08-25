package cmd

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/orientation"
	"github.com/atqamz/hand/internal/selfupdate"
	"github.com/atqamz/hand/internal/shellquote"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/store"
	"github.com/atqamz/hand/internal/supervision"
	"github.com/atqamz/hand/internal/watcher"
)

func setupSessionHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	mkFleetDirs(t, home)
	if err := initMarker(home); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	t.Setenv("HAND_HARNESS", harness.Codex)
	t.Setenv(supervision.CodexThreadEnv, "")
	t.Setenv(harness.RoleEnv, "")
	writeSessionContext(t, home, "## Hard constraints\nKeep the fleet observable.\n", "# Backlog\n\n## Queue\n")
	return home
}

func writeSessionContext(t *testing.T, home, operator, backlog string) {
	t.Helper()
	for name, contents := range map[string]string{
		"operator.md": operator,
		"backlog.md":  backlog,
	} {
		if err := os.WriteFile(filepath.Join(home, "data", name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func executeSessionStart(t *testing.T, in io.Reader) (string, error) {
	t.Helper()
	out, _, err := executeRootForTest(t, devBuild("test"), in, "session", "start")
	return out, err
}

func executeRootForTest(t *testing.T, info selfupdate.BuildInfo, in io.Reader, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd(info)
	root.SetArgs(args)
	var out bytes.Buffer
	var errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	if in != nil {
		root.SetIn(in)
	}
	_, err := root.ExecuteC()
	return out.String(), errOut.String(), err
}

func runSessionStartForTest(t *testing.T) string {
	t.Helper()
	out, err := executeSessionStart(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSessionStartEmitsCompleteBoundedDigest(t *testing.T) {
	home := setupSessionHome(t)
	writeSessionContext(t, home,
		"## Hard constraints\nKeep every line.\nIncluding: punctuation.\n",
		"# Backlog\n\n## Queue\n- queued-task\n  private implementation body\n")

	out := runSessionStartForTest(t)
	for _, want := range []string{
		"session_bootstrap: complete\n",
		"tool: hand\n",
		"version: test\n",
		"exec:",
		"home: " + axi.Value(home) + "\n",
		"supervisor_harness: codex\n",
		"supervisor_harness_source: override\n",
		"runtime_identity_status: unidentified\n",
		// Session start never installs; an un-integrated home reports the
		// absence and degrades honestly instead of repairing silently.
		"bootstrap_integration_status: absent\n",
		"wake_delivery_capability: degraded\n",
		"wake_delivery_attachment_status:",
		"watcher_observer_liveness:",
		"next_command: hand orient\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("out = %q, want %q", out, want)
		}
	}
	// Bootstrap is not the ordinary current-work read: no orientation dump, no
	// project/backlog/operator context, no instruction list. Every reasoning
	// turn observes through hand orient instead.
	for _, banned := range []string{
		"orientation_schema:",
		"orientation_actionable",
		"projects[0]{name,mode,url,upstream}:",
		"backlog[",
		"operator: ",
		"instructions[",
		"tasks[0]{id,state,reported,age,flags}:",
		"private implementation body",
	} {
		if strings.Contains(out, banned) {
			t.Fatalf("out = %q, want bootstrap output without %q", out, banned)
		}
	}
}

func TestSessionTargetsIncludesEveryRunningTaskBeyondOrientationBound(t *testing.T) {
	views := make([]taskView, 65)
	for i := range views {
		views[i] = taskView{
			task:    state.Task{ID: fmt.Sprintf("task-%02d", i), Lifecycle: state.TaskOpen},
			attempt: &state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "pane"}},
		}
	}

	targets := sessionTargets("fleet-1", views)
	if len(targets) != len(views) {
		t.Fatalf("targets = %d, want %d", len(targets), len(views))
	}
	for i, target := range targets {
		want := orientation.TaskTarget("fleet-1", taskTargetFacts(views[i]))
		if target.Target != want || target.TaskID != views[i].task.ID {
			t.Fatalf("target %d = %#v, want %#v", i, target, watcher.TargetBinding{TaskID: views[i].task.ID, Target: want})
		}
	}
}

func TestSessionStartRefusesWorkerRoleBeforeReadingContext(t *testing.T) {
	home := setupSessionHome(t)
	if err := os.Remove(filepath.Join(home, "data", "operator.md")); err != nil {
		t.Fatal(err)
	}
	t.Setenv(harness.RoleEnv, harness.WorkerRole)

	_, err := executeSessionStart(t, nil)
	assertExitCode(t, err, 3)
	if want := "supervisor session bootstrap is unavailable when HAND_ROLE=worker"; !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want %q", err, want)
	}
}

func TestSessionStartRefusesOutsideFleetHome(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HAND_HOME", "")
	t.Setenv("HAND_HARNESS", harness.Codex)
	t.Setenv(harness.RoleEnv, "")

	_, err := executeSessionStart(t, nil)
	assertExitCode(t, err, 3)
	if !strings.Contains(err.Error(), "run `hand init`") {
		t.Fatalf("err = %q, want the supported hand init remedy", err)
	}
}

// data/operator.md and data/backlog.md are the supervisor's own reading list,
// not bootstrap inputs: one-time runtime qualification must complete without
// them and leave their absence to the generated instructions to surface.
func TestSessionStartCompletesWithoutOperatorOrBacklogContext(t *testing.T) {
	home := setupSessionHome(t)
	for _, name := range []string{"operator.md", "backlog.md"} {
		if err := os.Remove(filepath.Join(home, "data", name)); err != nil {
			t.Fatal(err)
		}
	}

	out, err := executeSessionStart(t, nil)
	if err != nil {
		t.Fatalf("session start = %v, want a completed bootstrap", err)
	}
	if !strings.Contains(out, "session_bootstrap: complete\n") || !strings.Contains(out, "next_command: hand orient\n") {
		t.Fatalf("out = %q, want the bootstrap contract", out)
	}
}

func TestSessionStartResolvesFleetIdentityBeforeQualifyingTheRuntime(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, "fleet path's `printf injected`;printf injected")
	mkFleetDirs(t, home)
	writeSessionContext(t, home, "operator", "# Backlog\n")
	if err := os.Remove(filepath.Join(home, "state", "hand.db")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_HOME", home)
	t.Setenv("HAND_HARNESS", harness.Codex)
	t.Setenv(harness.RoleEnv, "")
	t.Chdir(parent)

	_, err := executeSessionStart(t, nil)
	assertExitCode(t, err, 3)
	for _, want := range []string{"fleet identity missing", "run `hand init " + shellquote.Quote(home)} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want it to name %q", err, want)
		}
	}
}

func TestSessionStartPreservesFleetReaderErrorOwnership(t *testing.T) {
	home := setupSessionHome(t)
	if err := os.Remove(store.Path(home)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.Path(home), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := executeSessionStart(t, nil)
	if err == nil {
		t.Fatal("got nil, want the project/state store error")
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		t.Fatalf("err = %v, want the owning fleet reader's general error, not ExitError", err)
	}
}

type readFunc func([]byte) (int, error)

func (f readFunc) Read(p []byte) (int, error) { return f(p) }

func TestSessionStartNeverReadsStdin(t *testing.T) {
	setupSessionHome(t)
	read := false
	in := readFunc(func([]byte) (int, error) {
		read = true
		return 0, errors.New("stdin must not be read")
	})

	if _, err := executeSessionStart(t, in); err != nil {
		t.Fatal(err)
	}
	if read {
		t.Fatal("session start read stdin")
	}
}

func TestSessionOverviewsDoNotMigrateLegacyConfig(t *testing.T) {
	home := setupSessionHome(t)
	configDir := filepath.Join(home, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"harness": harness.Codex, "model": "legacy-model"} {
		if err := os.WriteFile(filepath.Join(configDir, name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	assertUnmigrated := func() {
		t.Helper()
		if _, err := os.Stat(filepath.Join(configDir, "model")); err != nil {
			t.Fatalf("legacy model was moved: %v", err)
		}
		if _, err := os.Stat(filepath.Join(configDir, "model.codex")); !os.IsNotExist(err) {
			t.Fatalf("keyed model stat error = %v, want not-exist", err)
		}
	}

	if _, err := executeSessionStart(t, nil); err != nil {
		t.Fatal(err)
	}
	assertUnmigrated()
	runBareRoot(t)
	assertUnmigrated()
}

func TestSessionOverviewsDoNotMutateFleetState(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{"session start", []string{"session", "start"}},
		{"bare hand", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			if err := initLayout(home); err != nil {
				t.Fatal(err)
			}
			seedDB, err := store.Open(home)
			if err != nil {
				t.Fatal(err)
			}
			if err := seedDB.Close(); err != nil {
				t.Fatal(err)
			}
			writeSessionContext(t, home, "operator", "# Backlog\n")
			t.Setenv("HAND_HOME", home)
			t.Setenv("HAND_HARNESS", harness.Codex)
			t.Setenv(harness.RoleEnv, "")

			db, err := store.Open(home)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AddProject(store.Project{Name: "sqlite-project", URL: "local", Mode: "local-only"}); err != nil {
				t.Fatal(err)
			}
			if err := db.CreateTask(store.Task{ID: "sqlite-task", Project: "sqlite-project", Kind: store.KindShip}); err != nil {
				t.Fatal(err)
			}
			if err := db.SetHold(store.Hold{ID: "sqlite-hold", Kind: store.HoldKindOperator, Reason: "waiting"}); err != nil {
				t.Fatal(err)
			}
			migrated, err := db.Migrated("projects.md")
			if err != nil {
				t.Fatal(err)
			}
			if migrated {
				t.Fatal("fresh initialized home already has the project migration marker")
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			before := snapshotFleetTree(t, home)
			out, _, err := executeRootForTest(t, devBuild("test"), nil, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			// Bootstrap qualifies the runtime without rendering current work;
			// the bare overview is the surface that reports SQLite-backed state.
			if len(test.args) == 0 {
				for _, want := range []string{"sqlite-project", "sqlite-task", "sqlite-hold"} {
					if !strings.Contains(out, want) {
						t.Fatalf("out = %q, want current SQLite-backed %q", out, want)
					}
				}
			} else if !strings.Contains(out, "session_bootstrap: complete\n") {
				t.Fatalf("out = %q, want a completed bootstrap", out)
			}
			after := snapshotFleetTree(t, home)
			if !slices.Equal(after, before) {
				t.Fatalf("fleet tree changed:\nbefore: %v\nafter:  %v", before, after)
			}
		})
	}
}

func TestSessionStartDoesNotAcknowledgeExistingReport(t *testing.T) {
	home := setupSessionHome(t)
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(store.Task{
		ID: "reported-task", Project: "demo", Kind: store.KindShip,
		AcknowledgedAt: "2026-08-22T00:00:00Z", AcknowledgedOffset: 12, AcknowledgedDigest: "ack-digest",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := executeSessionStart(t, nil); err != nil {
		t.Fatal(err)
	}
	after, err := state.ReadHistoryReadOnly(home, "reported-task")
	if err != nil {
		t.Fatal(err)
	}
	if after.Task.AcknowledgedAt != "2026-08-22T00:00:00Z" || after.Task.AcknowledgedOffset != 12 || after.Task.AcknowledgedDigest != "ack-digest" {
		t.Fatalf("acknowledgement changed after session start: %#v", after.Task)
	}
}

func TestOldFleetRequiresExplicitRecoveryBeforeReadOnlyOverview(t *testing.T) {
	home := t.TempDir()
	if err := initLayout(home); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	downgradeSessionStore(t, home)
	writeSessionContext(t, home, "operator", "# Backlog\n")
	if err := os.WriteFile(filepath.Join(home, "data", "projects.md"),
		[]byte("# Projects\n\n- legacy-project: local mode=local-only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyTask, err := json.Marshal(store.Task{ID: "legacy-task", Project: "legacy-project", Kind: store.KindShip})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state", "legacy-task.json"), legacyTask, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_HOME", home)
	t.Setenv("HAND_HARNESS", harness.Codex)
	t.Setenv(harness.RoleEnv, "")

	beforeRecovery := snapshotFleetTree(t, home)
	remedy := "hand init '" + home + "'"
	for _, args := range [][]string{{"session", "start"}, nil} {
		_, _, err = executeRootForTest(t, devBuild("test"), nil, args...)
		if err == nil {
			t.Fatalf("overview %q opened an older schema read-only", args)
		}
		if !strings.Contains(err.Error(), remedy) || strings.Contains(err.Error(), "hand update") {
			t.Errorf("err = %q, want only the exact recovery %q", err, remedy)
		}
		if afterRefusal := snapshotFleetTree(t, home); !slices.Equal(afterRefusal, beforeRecovery) {
			t.Fatalf("read-only refusal changed the fleet:\nbefore: %v\nafter:  %v", beforeRecovery, afterRefusal)
		}
	}

	if _, _, err := executeRootForTest(t, devBuild("test"), nil, "init", home); err != nil {
		t.Fatalf("run advertised recovery %q: %v", remedy, err)
	}
	afterRecovery := snapshotFleetTree(t, home)
	for i, args := range [][]string{{"session", "start"}, nil} {
		out, _, err := executeRootForTest(t, devBuild("test"), nil, args...)
		if err != nil {
			t.Fatalf("overview %d: %v", i+1, err)
		}
		// Bootstrap qualifies the runtime without rendering current work; the
		// bare overview is the surface that reports migrated tasks.
		if len(args) == 0 {
			for _, want := range []string{"legacy-project", "legacy-task"} {
				if !strings.Contains(out, want) {
					t.Fatalf("overview %d = %q, want migrated %q", i+1, out, want)
				}
			}
			continue
		}
		if !strings.Contains(out, "session_bootstrap: complete\n") {
			t.Fatalf("overview %d = %q, want a completed bootstrap", i+1, out)
		}
		if afterOverview := snapshotFleetTree(t, home); !slices.Equal(afterOverview, afterRecovery) {
			t.Fatalf("overview %d mutated the recovered fleet:\nbefore: %v\nafter:  %v", i+1, afterRecovery, afterOverview)
		}
	}
}

func downgradeSessionStore(t *testing.T, home string) {
	t.Helper()
	db, err := sql.Open("sqlite", store.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("PRAGMA user_version = 0"); err != nil {
		t.Fatal(err)
	}
}

func snapshotFleetTree(t *testing.T, root string) []string {
	t.Helper()
	var snapshot []string
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := rel + " " + info.Mode().String()
		switch {
		case info.Mode().IsRegular():
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry += fmt.Sprintf(" %x", sha256.Sum256(contents))
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entry += " -> " + target
		}
		snapshot = append(snapshot, entry)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func writeFreshVersionCheck(t *testing.T, home string) {
	t.Helper()
	contents := `{"checked_at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","latest":"v9.0.0"}`
	if err := os.WriteFile(filepath.Join(home, "state", ".version-check"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSessionOverviewsSkipReleasedVersionCheck(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{"session start", []string{"session", "start"}},
		{"bare hand", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := setupSessionHome(t)
			writeFreshVersionCheck(t, home)

			_, errOut, err := executeRootForTest(t, stableBuild("v0.1.0"), nil, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(errOut, "A new version of hand is available") {
				t.Fatalf("stderr = %q, want the read-only overview to skip the version-check path", errOut)
			}
		})
	}
}

func TestSessionStartWorkerRefusalPrecedesReleasedVersionCheck(t *testing.T) {
	home := setupSessionHome(t)
	writeFreshVersionCheck(t, home)
	t.Setenv(harness.RoleEnv, harness.WorkerRole)

	_, errOut, err := executeRootForTest(t, stableBuild("v0.1.0"), nil, "session", "start")
	assertExitCode(t, err, 3)
	if errOut != "" {
		t.Fatalf("stderr before worker refusal = %q, want no version-check output", errOut)
	}
}

func TestNormalCommandKeepsReleasedVersionNotice(t *testing.T) {
	home := setupSessionHome(t)
	writeFreshVersionCheck(t, home)

	_, errOut, err := executeRootForTest(t, stableBuild("v0.1.0"), nil, "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "A new version of hand is available: v0.1.0 -> v9.0.0") {
		t.Fatalf("stderr = %q, want normal commands to retain the released-version notice", errOut)
	}
}

func TestReadBacklogSummaryBoundsIdentityLinesAndCountsTheWholeQueue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backlog.md")
	contents := "# Backlog\n## Queue\n- first\n  hidden detail\n* second\n- third\n### hidden subsection\n## Notes\n- note\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readBacklogSummary(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"## Queue", "- first", "* second"}
	if len(got.Items) != 4 {
		t.Fatalf("items = %#v, want three identity lines plus one recovery line", got.Items)
	}
	for i := range want {
		if got.Items[i] != want[i] {
			t.Fatalf("items = %#v, want prefix %#v", got.Items, want)
		}
	}
	if last := got.Items[len(got.Items)-1]; !strings.Contains(last, "truncated") || !strings.Contains(last, "data/backlog.md") {
		t.Fatalf("last item = %q, want truncation and recovery path", last)
	}
	if got.Queued != 3 {
		t.Fatalf("queued = %d, want all three queued items counted beyond the output bound", got.Queued)
	}
}

// classifyNextAction precedence coverage lives in cmd/nextaction_test.go; this
// is the orient integration boundary: fleet-state-derived fields, the bounded
// orientation view, and the progress receipt reach one rendered document.
func TestOrientRendersNextActionFromFleetState(t *testing.T) {
	home := setupSessionHome(t)
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddProject(store.Project{Name: "demo", URL: "local", Mode: "local-only"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(store.Task{ID: "needs-repair-task", Project: "demo", Kind: store.KindShip, RepairCode: "E_ORPHAN"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	writeSessionContext(t, home, "operator", "# Backlog\n\n## Queue\n- queued-task\n")

	root := newRootCmd(devBuild("test"))
	root.SetArgs([]string{"orient"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(new(bytes.Buffer))
	if _, err := root.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"orientation_schema: hand.supervisor.v1\n",
		"fleet_id: f_",
		"orientation_actionable[",
		"next_action_kind: needs-repair\n",
		"next_action_task: needs-repair-task\n",
		"next_action_command: hand status needs-repair-task\n",
		"next_action_reason: Run `hand status needs-repair-task` and resolve its repair ambiguity\n",
		"wake_delivery_last_accepted: none\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("out = %q, want %q", got, want)
		}
	}

	// The reasoning re-entry is recorded only as disposable mechanism progress:
	// state/runtime/supervision-wake.json exists and carries oriented stamps,
	// while canonical task truth stays untouched.
	raw, err := os.ReadFile(filepath.Join(home, "state", "runtime", "supervision-wake.json"))
	if err != nil {
		t.Fatalf("read wake ledger: %v", err)
	}
	if !strings.Contains(string(raw), `"oriented":`) || !strings.Contains(string(raw), "hand.supervision.wake.v1") {
		t.Fatalf("ledger = %q, want oriented stamps under the wake schema", raw)
	}
	after, err := state.ReadHistoryReadOnly(home, "needs-repair-task")
	if err != nil {
		t.Fatal(err)
	}
	if after.Task.RepairCode != "E_ORPHAN" {
		t.Fatalf("task mutated by orient: %#v", after.Task)
	}
}

func TestOrientRefusesWorkerRole(t *testing.T) {
	setupSessionHome(t)
	t.Setenv(harness.RoleEnv, harness.WorkerRole)

	root := newRootCmd(devBuild("test"))
	root.SetArgs([]string{"orient"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(new(bytes.Buffer))
	_, err := root.ExecuteC()
	assertExitCode(t, err, 3)
	if want := "hand orient is unavailable when HAND_ROLE=worker"; !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want %q", err, want)
	}
}
