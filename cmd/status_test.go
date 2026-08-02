package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/atqamz/secondhand/internal/state"
)

// writeFakeHerdrPaneStatus fakes "pane get" as a query command per
// internal/herdr/client.go's call() doc comment: a non-null result object on
// success. It always succeeds; the "herdr unreachable" degrade path is
// exercised for real (no fake, empty PATH) by
// TestStatusFleetDegradesToUnknownWhenHerdrUnreachable below.
func writeFakeHerdrPaneStatus(t *testing.T, status string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '{\"id\":\"cli:1\",\"result\":{\"pane\":{\"pane_id\":\"wA:pB\",\"agent_status\":\"" + status + "\"}}}'\n"
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestStatusFleetListsAllTasks(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "working")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "task-1") || !strings.Contains(out.String(), "working") {
		t.Fatalf("got %q, want task-1 and working", out.String())
	}
}

func TestStatusFleetColumnsDoNotMergeAtFieldWidth(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "working")

	id := "task-aaaaaaaaaaa"
	if err := state.Write(home, state.Task{ID: id, Project: "myproject12", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), id+"myproject12") {
		t.Fatalf("got %q, ID and project columns merged", out.String())
	}
	if !strings.Contains(out.String(), id+" ") {
		t.Fatalf("got %q, want %q followed by a column separator", out.String(), id)
	}
}

func TestStatusFleetDegradesToUnknownWhenHerdrUnreachable(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	t.Setenv("PATH", t.TempDir())

	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "unknown") {
		t.Fatalf("got %q, want unknown", out.String())
	}
}

func TestStatusFleetJSON(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "working")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"id": "task-1"`) || !strings.Contains(out.String(), `"agent_state": "working"`) {
		t.Fatalf("got %q, want JSON array with task-1 and agent_state working", out.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "[") {
		t.Fatalf("got %q, want JSON array", out.String())
	}
}

func TestStatusSingleTaskDetail(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip, Harness: "claude",
		Herdr: state.Herdr{Session: "default", TabID: "wA:tB", PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Task:        task-1") || !strings.Contains(out.String(), "State:       idle") {
		t.Fatalf("got %q", out.String())
	}
}

func TestStatusMergeStateCombinationsRenderDistinguishably(t *testing.T) {
	cases := []struct {
		name             string
		merged           bool
		prMergedObserved bool
		want             string
		wantNot          []string
	}{
		{"neither", false, false, "PR:          https://github.com/a/b/pull/1\n", []string{"merged"}},
		{"handMerged", true, false, "PR:          https://github.com/a/b/pull/1 (merged)\n", []string{"external"}},
		{"observedOnly", false, true, "PR:          https://github.com/a/b/pull/1 (merged, external)\n", nil},
		{"both", true, true, "PR:          https://github.com/a/b/pull/1 (merged)\n", []string{"external"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Chdir(home)
			mkFleetDirs(t, home)
			writeFakeHerdrPaneStatus(t, "idle")

			if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
				Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z",
				PR: "https://github.com/a/b/pull/1", MergeExecuted: c.merged, MergeAnnounced: c.prMergedObserved}); err != nil {
				t.Fatal(err)
			}

			cmd := newStatusCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"task-1"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			if !strings.Contains(got, c.want) {
				t.Fatalf("got %q, want to contain %q", got, c.want)
			}
			for _, absent := range c.wantNot {
				if strings.Contains(got, absent) {
					t.Fatalf("got %q, want no occurrence of %q", got, absent)
				}
			}
		})
	}
}

// TestStatusSingleTaskDetectsGateOpenedPR covers hand status's half of
// atqamz/secondhand#69: a task whose PR a no-mistakes gate opened directly
// (bypassing hand pr) still shows the PR, once status looks it up by branch.
func TestStatusSingleTaskDetectsGateOpenedPR(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	writeFakeHerdrPaneStatus(t, "idle")
	writeFakeGHPRListAndView(t, ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "OPEN"})

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "https://github.com/owner/repo/pull/9") {
		t.Fatalf("got %q, want the detected PR shown", out.String())
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PR != "https://github.com/owner/repo/pull/9" {
		t.Fatalf("task.PR = %q, want status to have recorded the detected PR", got.PR)
	}
}

// A detected PR reaches the dashboard's PR column too, not just task state:
// data/dashboard.md is what the supervising agent reads for fleet state, so a
// state write the dashboard never learned about leaves the two disagreeing.
func TestStatusDetectionFillsTheDashboardPRColumn(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	writeFakeHerdrPaneStatus(t, "idle")
	writeFakeGHPRListAndView(t, ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "OPEN"})

	dashPath := filepath.Join(home, "data", "dashboard.md")
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dashPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://github.com/owner/repo/pull/9") {
		t.Fatalf("dashboard = %q, want the detected PR in the task's row", string(data))
	}
}

// No active row for the task means only the dashboard column is left stale, and
// detection is a side effect of a read command: it stays quiet rather than
// failing hand status over it, unlike hand pr, which exists to record the PR.
func TestStatusDetectionSucceedsWithNoDashboardRow(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	writeFakeHerdrPaneStatus(t, "idle")
	writeFakeGHPRListAndView(t, ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "OPEN"})

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want status to succeed with the PR recorded and no row to reconcile", err)
	}
	if !strings.Contains(out.String(), "https://github.com/owner/repo/pull/9") {
		t.Fatalf("got %q, want the detected PR shown", out.String())
	}
}

// A scout task's deliverable is data/<id>/report.md, never a PR, so status skips
// the branch lookup for it exactly as checkLandedWork does - the gh fake here
// would answer with a PR, and recording it would pin one onto a task whose
// completion detail never uses it.
func TestStatusSkipsPRDetectionForScoutTasks(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	writeFakeHerdrPaneStatus(t, "idle")
	writeFakeGHPRListAndView(t, ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "OPEN"})

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindScout, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PR != "" {
		t.Fatalf("task.PR = %q, want a scout task left without a PR", got.PR)
	}
}

func TestStatusFleetOverviewRendersMergeMarker(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z",
		PR: "https://github.com/a/b/pull/1", MergeAnnounced: true}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "(merged, external)") {
		t.Fatalf("got %q, want the fleet overview to carry the merge marker", out.String())
	}
}

func TestStatusFleetOverviewTaskWithNoPRIsUnaffected(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "merged") {
		t.Fatalf("got %q, want no merge marker without a PR", out.String())
	}
}

func TestStatusSingleTaskJSON(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "blocked")

	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"agent_state": "blocked"`) {
		t.Fatalf("got %q, want agent_state blocked", out.String())
	}
}

func TestStatusSingleTaskMissing(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newStatusCmd()
	cmd.SetArgs([]string{"missing-task"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got err %v, want not found", err)
	}
}

func TestStatusFleetFlagsIdleWithoutTerminalReportAsUnreported(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "idle (unreported)") {
		t.Fatalf("got %q, want idle flagged unreported", out.String())
	}
}

func TestStatusFleetFlagsIdleWithTerminalReportInsteadOfUnreported(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("needs-decision: waiting on review\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "idle (reported: needs-decision)") {
		t.Fatalf("got %q, want idle flagged with the reported state", out.String())
	}
	if strings.Contains(out.String(), "unreported") {
		t.Fatalf("got %q, want no unreported flag once a terminal report explains the idle", out.String())
	}
}

// A worker that appends free text after a real report has still reported, so the
// suffix comes from the last line that classified - the same answer hand watch
// reaches about the same quiet pane. The Reported field still shows the raw last
// line, free text included.
func TestStatusFleetKeepsTheReportedFlagAfterATrailingMalformedLine(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "task-1"),
		[]byte("needs-decision: waiting on review\nstill here, no answer yet\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "idle (reported: needs-decision)") {
		t.Fatalf("got %q, want the last classified report kept behind the free text", out.String())
	}
}

func TestStatusFleetDoesNotFlagWorkingTasks(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "working")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "unreported") || strings.Contains(out.String(), "reported:") {
		t.Fatalf("got %q, want no report suffix on a working task", out.String())
	}
}

// A worker that appends `paused:` and leaves its harness running used to render
// as a bare `working`: the column showed the pane and hid the only party that
// said why. One of the two live symptoms atqamz/secondhand#89 names.
func TestStatusFleetCarriesAPausedReportThroughABusyPane(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "working")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("paused: waiting on the nightly build\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "working (reported: paused)") {
		t.Fatalf("got %q, want the paused report carried through the busy pane", out.String())
	}
}

// The second live symptom in atqamz/secondhand#89: an hours-old figure sitting
// next to a status file touched minutes earlier, which reads as a stalled
// worker that is in fact reporting.
func TestStatusFleetDwellTimeFollowsTheReportFileNotTheTaskAge(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "working")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr:     state.Herdr{PaneID: "wA:pB"},
		CreatedAt: time.Now().Add(-5 * time.Hour).UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	report := state.ReportPath(home, "task-1")
	if err := os.WriteFile(report, []byte("working: still going\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touched := time.Now().Add(-3 * time.Minute)
	if err := os.Chtimes(report, touched, touched); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	row := ""
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "task-1") {
			row = line
		}
	}
	if !strings.Contains(row, "5h ago") {
		t.Fatalf("got %q, want the age column still measuring the task", row)
	}
	if !strings.Contains(row, "3m ago") {
		t.Fatalf("got %q, want the last report column measuring the report file", row)
	}
}

// The same distinction in the detail view, where a stale figure is worse: it is
// the view an operator opens once the fleet table has already worried them.
func TestStatusSingleTaskDwellTimeFollowsTheReportFileNotTheTaskAge(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "working")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr:     state.Herdr{PaneID: "wA:pB"},
		CreatedAt: time.Now().Add(-5 * time.Hour).UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	report := state.ReportPath(home, "task-1")
	if err := os.WriteFile(report, []byte("working: still going\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touched := time.Now().Add(-3 * time.Minute)
	if err := os.Chtimes(report, touched, touched); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "Created:     5h ago") {
		t.Fatalf("got %q, want Created still measuring the task", got)
	}
	if !strings.Contains(got, "Last report: 3m ago") {
		t.Fatalf("got %q, want Last report measuring the report file", got)
	}
}

// A task whose worker has never written a report has no dwell time to show, and
// the column must say so rather than fall back to the task's age - the fallback
// is what made an untouched report look freshly written.
func TestStatusReportsNoDwellTimeWithoutAReportFile(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "working")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr:     state.Herdr{PaneID: "wA:pB"},
		CreatedAt: time.Now().Add(-5 * time.Hour).UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Last report: (none)") {
		t.Fatalf("got %q, want no dwell time invented for a task that never reported", out.String())
	}
}

func TestStatusSingleTaskShowsReportedStateAndHistory(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	reportBody := "working: started\nneeds-decision: waiting on review\n"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(reportBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "Reported:    needs-decision: waiting on review") {
		t.Fatalf("got %q, want the last reported state on its own line", got)
	}
	if !strings.Contains(got, "Report history (reported by worker, not verified current truth):") {
		t.Fatalf("got %q, want the history block labeled as reported, not verified truth", got)
	}
	if !strings.Contains(got, "working: started") {
		t.Fatalf("got %q, want the earlier report line in the history", got)
	}
}

// TestStatusSingleTaskDegradesOnAnUnreadableReport holds the detail view to the
// same graceful degradation the fleet view already has: a report file that
// exists but can't be read names the fault and still prints the rest, rather
// than failing the whole command and showing nothing at all. A directory in the
// report file's place is a real EISDIR, not a mocked error.
func TestStatusSingleTaskDegradesOnAnUnreadableReport(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state.ReportPath(home, "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want the read fault degraded rather than failing the command", err)
	}
	got := out.String()
	if !strings.Contains(got, "Reported:    report "+reportUnreadable) {
		t.Fatalf("got %q, want the unreadable report named on the Reported line", got)
	}
	if !strings.Contains(got, "Task:        task-1") || !strings.Contains(got, "State:       idle") {
		t.Fatalf("got %q, want the rest of the detail view still printed", got)
	}
}

// atqamz/secondhand#65: a worker's report prose has run 2.7-4.3 KB for a
// single task, and hand status rendered it in full - the point of this test.
func TestTruncateReportLineKeepsVocabularyPrefixIntactUnderAnAdversarialBudget(t *testing.T) {
	line := state.ParseReportLine("done: " + strings.Repeat("x", 500))
	got := truncateReportLine(line, 3) // smaller than the "done: " prefix itself
	if !strings.HasPrefix(got, "done: ") {
		t.Fatalf("got %q, want the done: prefix preserved even when the budget can't fit it", got)
	}
}

func TestTruncateReportLineMarksTruncationVisibly(t *testing.T) {
	line := state.ParseReportLine("working: " + strings.Repeat("x", 500))
	got := truncateReportLine(line, 50)
	if strings.Contains(got, strings.Repeat("x", 500)) {
		t.Fatalf("got %q, want the note cut short", got)
	}
	if !strings.Contains(got, "[+") {
		t.Fatalf("got %q, want a visible marker naming the cut, not a silently shortened line", got)
	}
}

func TestTruncateReportLineLeavesShortLinesUntouched(t *testing.T) {
	line := state.ParseReportLine("done: short")
	if got := truncateReportLine(line, reportSummaryBudget); got != "done: short" {
		t.Fatalf("got %q, want a report already under budget left exactly as reported", got)
	}
}

func TestTruncateReportLineDoesNotSplitAMultibyteRune(t *testing.T) {
	line := state.ParseReportLine("done: " + strings.Repeat("héllo", 100))
	got := truncateReportLine(line, 50)
	if !utf8.ValidString(got) {
		t.Fatalf("got %q, want valid utf8 even when the cut lands inside a multi-byte character", got)
	}
}

// atqamz/secondhand#65's core complaint: hand status <id> printed the latest
// report twice, once under Reported: and again as the last entry of Report
// history. This asserts the fix at the command level, not just the helper.
func TestStatusSingleTaskTruncatesALongReportedLineAndPointsAtTheFile(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	longNote := strings.Repeat("x", 500)
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: "+longNote+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "Reported:    done: ") {
		t.Fatalf("got %q, want the done: prefix intact on the Reported line", got)
	}
	if strings.Contains(got, longNote) {
		t.Fatalf("got %q, want the long note truncated rather than printed in full", got)
	}
	if !strings.Contains(got, "[+") {
		t.Fatalf("got %q, want a visible truncation marker", got)
	}
	if !strings.Contains(got, "Report file: "+state.ReportPath(home, "task-1")) {
		t.Fatalf("got %q, want the absolute path to the full report shown", got)
	}
}

// The longest label in the block is "Report file:", so every other label pads
// to its width; a new label that outgrows the column has to widen the whole
// block, not start its own.
func TestStatusSingleTaskAlignsEveryLabeledValueInOneColumn(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Harness: "claude", Model: "sonnet", Worktree: filepath.Join(home, "wt"),
		Herdr:     state.Herdr{Session: "default", TabID: "wA:tB", PaneID: "wA:pB"},
		CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: landed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	labels := 0
	for _, line := range strings.Split(out.String(), "\n") {
		if line == "" {
			break
		}
		label, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("got %q, want every line of the detail block labeled", line)
		}
		labels++
		if col := len(label) + 1 + (len(value) - len(strings.TrimLeft(value, " "))); col != len("Report file: ") {
			t.Fatalf("got %q starting its value at column %d, want every value at column %d", line, col+1, len("Report file: ")+1)
		}
	}
	if labels != 13 {
		t.Fatalf("got %d labeled lines, want all 13 checked for alignment", labels)
	}
}

func TestStatusSingleTaskHistoryDoesNotRepeatTheReportedEntry(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	reportBody := "working: started\nneeds-decision: waiting on review\n"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(reportBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Count(got, "needs-decision: waiting on review") != 1 {
		t.Fatalf("got %q, want the latest report shown exactly once, not repeated in history", got)
	}
	if !strings.Contains(got, "working: started") {
		t.Fatalf("got %q, want the earlier report kept in history", got)
	}
}

// --full is the literal opt-out: it must reproduce the pre-#65 shape exactly,
// duplicate entry and all, with no new report-file pointer line.
func TestStatusSingleTaskFullFlagRestoresThePreviousBehaviorExactly(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	longNote := strings.Repeat("x", 500)
	reportBody := "working: started\ndone: " + longNote + "\n"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(reportBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "--full"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Count(got, "done: "+longNote) != 2 {
		t.Fatalf("got %q, want --full to show the full untruncated entry twice, matching prior behavior", got)
	}
	if strings.Contains(got, "Report file:") {
		t.Fatalf("got %q, want --full to skip the new report-file pointer line", got)
	}
}

func TestStatusSingleTaskJSONKeepsTheFullReportUntruncated(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}
	longNote := strings.Repeat("x", 500)
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: "+longNote+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), longNote) {
		t.Fatalf("got %q, want the JSON report note left fully untruncated - a machine consumer wants the whole field", out.String())
	}
}

func TestStatusSingleTaskJSONIncludesReportedAndHistory(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("blocked: waiting on secrets\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"state": "blocked"`) || !strings.Contains(out.String(), `"note": "waiting on secrets"`) {
		t.Fatalf("got %q, want reported state and note in JSON", out.String())
	}
	if !strings.Contains(out.String(), `"report_history"`) {
		t.Fatalf("got %q, want report_history in JSON", out.String())
	}
}
