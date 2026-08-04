package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/atqamz/secondhand/internal/store"
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
		t.Fatalf("got %q, want tasks array with task-1 and agent_state working", out.String())
	}
	if !strings.Contains(out.String(), `"holds": []`) {
		t.Fatalf("got %q, want an empty holds array", out.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Fatalf("got %q, want a JSON object wrapping tasks and holds", out.String())
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

func TestStatusFleetShowsHeldBlock(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	if err := state.SetHold(home, state.Hold{ID: "fix-login", Kind: state.HoldKindOperator,
		Reason: "needs a call", SetAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "held:") {
		t.Fatalf("got %q, want a held block", got)
	}
	if !strings.Contains(got, "fix-login") || !strings.Contains(got, "operator") || !strings.Contains(got, "needs a call") {
		t.Fatalf("got %q, want the hold's id, kind, and reason", got)
	}
}

// A hold outliving its task is the case the standalone hold table exists for;
// this pins that hand status still surfaces it once the task row is gone.
func TestStatusFleetShowsHeldBlockWithNoTaskRowBehindIt(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	if err := state.SetHold(home, state.Hold{ID: "torn-down-task", Kind: state.HoldKindOperator,
		Reason: "question never answered", SetAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "torn-down-task") {
		t.Fatalf("got %q, want the hold shown despite no task row", out.String())
	}
}

func TestStatusFleetOmitsHeldBlockWithoutHolds(t *testing.T) {
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
	if strings.Contains(out.String(), "held:") {
		t.Fatalf("got %q, want no held block when nothing is held", out.String())
	}
}

func TestStatusFleetFlagsInconsistentHold(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetHold(store.Hold{ID: "weird", Kind: "not-a-real-kind", Reason: "who knows"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `inconsistent: unrecognized kind "not-a-real-kind"`) {
		t.Fatalf("got %q, want the inconsistent hold flagged rather than silently rendered", out.String())
	}
}

// hand watch writes limit holds, so hand status has to render one as the ordinary fact
// it is. Left out of holdInconsistency it would come out flagged inconsistent, turning
// every routine usage limit into a report that something outside hand corrupted the
// database.
func TestStatusFleetRendersAMachineSetLimitHold(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	if err := state.SetHold(home, state.Hold{
		ID: "fix-login", Kind: state.HoldKindLimit,
		Reason: "harness stopped on a usage limit; 1 attempt made, next try 2026-08-04T15:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "inconsistent") {
		t.Fatalf("got %q, want a limit hold rendered as a real kind", out.String())
	}
	if !strings.Contains(out.String(), "limit") || !strings.Contains(out.String(), "next try 2026-08-04T15:01:00Z") {
		t.Fatalf("got %q, want the limit hold's kind and reason in the held block", out.String())
	}
}

func TestStatusFleetJSONFlagsInconsistentHold(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetHold(store.Hold{ID: "weird", Kind: state.HoldKindBlocked, Reason: "who knows"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"inconsistent": "blocked hold has no blocked_on"`) {
		t.Fatalf("got %q, want the inconsistency named in JSON", out.String())
	}
}

// A store fault reading holds must fail the whole command rather than degrade
// to an empty holds list, which would read as "nothing is waiting" - exactly
// the false all-clear this feature exists to avoid.
func TestStatusFleetPropagatesAnUnreadableHoldStore(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := os.Remove(store.Path(home)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.Path(home), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatal("got nil error, want the unreadable hold store to fail the command")
	}
}

func TestStatusSingleTaskShowsHeldLine(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetHold(home, state.Hold{ID: "task-1", Kind: state.HoldKindBlocked,
		Reason: "waiting on migration", BlockedOn: "task-2", SetAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Held:        waiting on task-2: waiting on migration") {
		t.Fatalf("got %q, want a Held line naming what it waits on", out.String())
	}
}

func TestStatusSingleTaskOmitsHeldLineWithoutAHold(t *testing.T) {
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
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Held:") {
		t.Fatalf("got %q, want no Held line without a hold", out.String())
	}
}

// Delivered work is terminal but not merged, so both views have to name that
// state: an operator who cannot see it would go looking for a merge that is
// never coming.
func TestStatusShowsDeliveredWorkWithoutClaimingAMerge(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z",
		PR:          "https://github.com/kunchenguid/no-mistakes/pull/597",
		DeliveredAt: "2026-08-03T00:00:00Z", DeliveredReason: "offered upstream, maintainer decides"}); err != nil {
		t.Fatal(err)
	}

	single := newStatusCmd()
	var out bytes.Buffer
	single.SetOut(&out)
	single.SetArgs([]string{"task-1"})
	if err := single.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Delivered:   offered upstream, maintainer decides (2026-08-03T00:00:00Z)") {
		t.Fatalf("got %q, want a Delivered line carrying the reason", out.String())
	}
	if strings.Contains(out.String(), "(merged)") {
		t.Fatalf("got %q, want no merge claim on delivered work", out.String())
	}

	fleet := newStatusCmd()
	var fleetOut bytes.Buffer
	fleet.SetOut(&fleetOut)
	if err := fleet.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fleetOut.String(), "(delivered)") {
		t.Fatalf("got %q, want a delivered marker in the fleet view", fleetOut.String())
	}

	asJSON := newStatusCmd()
	var jsonOut bytes.Buffer
	asJSON.SetOut(&jsonOut)
	asJSON.SetArgs([]string{"task-1", "--json"})
	if err := asJSON.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut.String(), `"delivered_reason": "offered upstream, maintainer decides"`) {
		t.Fatalf("got %q, want the delivered fields in JSON", jsonOut.String())
	}
	if !strings.Contains(jsonOut.String(), `"merged": false`) {
		t.Fatalf("got %q, want merged still false", jsonOut.String())
	}
}

func TestStatusSingleTaskJSONIncludesHeld(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetHold(home, state.Hold{ID: "task-1", Kind: state.HoldKindOperator,
		Reason: "needs a call", SetAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"held"`) || !strings.Contains(out.String(), `"needs a call"`) {
		t.Fatalf("got %q, want the held hold in JSON", out.String())
	}
}

// The same false-all-clear risk as the fleet view, one task at a time.
func TestStatusSingleTaskPropagatesAnUnreadableHoldStore(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(store.Path(home)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.Path(home), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("got nil error, want the unreadable hold store to fail the command")
	}
}

func TestStatusFleetEmptyIsAPositiveStatement(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "no tasks (0)" {
		t.Fatalf("got %q, want an explicit no-tasks count and nothing else", out.String())
	}
}

func TestStatusFleetEmptyJSONCarriesACount(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"task_count": 0`) {
		t.Fatalf("got %q, want an explicit task_count of 0", out.String())
	}
}

// An empty fleet must never read as "nothing to see" when a hold is still open on a torn-down
// task's id - the exact false-all-clear atqamz/secondhand#63 guards against.
func TestStatusFleetEmptyStillShowsHeldBlock(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := state.SetHold(home, state.Hold{ID: "gone-task", Kind: state.HoldKindOperator,
		Reason: "needs a call", SetAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no tasks (0)") || !strings.Contains(out.String(), "held:") ||
		!strings.Contains(out.String(), "needs a call") {
		t.Fatalf("got %q, want both the no-tasks count and the held block", out.String())
	}
}

// registerNoMistakesProject registers a no-mistakes-mode project and creates its clone directory, so
// gateRunIssue's os.Stat(clonePath) check and its no-mistakes invocation both have something to find.
func registerNoMistakesProject(t *testing.T, home, name string) {
	t.Helper()
	if err := project.Add(home, project.Project{Name: name, URL: "https://example.com/" + name + ".git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", name), 0o755); err != nil {
		t.Fatal(err)
	}
}

// writeDoneReport writes a single done report line, so LastReportedState reads the task as reported done.
func writeDoneReport(t *testing.T, home, id, note string) {
	t.Helper()
	if err := os.WriteFile(state.ReportPath(home, id), []byte("done: "+note+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const gateRunTestPR = "https://github.com/atqamz/secondhand/pull/120"

func TestStatusFleetFlagsShippedPRWithNoGateRun(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	registerNoMistakesProject(t, home, "gated")
	t.Setenv("PATH", fakeNoMistakesPath(t, "  completed    other-branch   758d72bf  2026-08-03 04:29  https://github.com/atqamz/secondhand/pull/999\n"))

	if err := state.Write(home, state.Task{ID: "task-1", Project: "gated", Kind: state.KindShip,
		PR: gateRunTestPR, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR "+gateRunTestPR+" checks green")

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "(gate: no run found)") {
		t.Fatalf("got %q, want a gate marker naming the shipped PR never ran through the gate", out.String())
	}
}

func TestStatusFleetJSONFlagsShippedPRWithNoGateRun(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	registerNoMistakesProject(t, home, "gated")
	t.Setenv("PATH", fakeNoMistakesPath(t, "  completed    other-branch   758d72bf  2026-08-03 04:29  https://github.com/atqamz/secondhand/pull/999\n"))

	if err := state.Write(home, state.Task{ID: "task-1", Project: "gated", Kind: state.KindShip,
		PR: gateRunTestPR, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR "+gateRunTestPR+" checks green")

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"gate_run_issue": "no run found"`) {
		t.Fatalf("got %q, want gate_run_issue naming no run found", out.String())
	}
}

func TestStatusFleetNoGateMarkerWhenRunFound(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	registerNoMistakesProject(t, home, "gated")
	t.Setenv("PATH", fakeNoMistakesPath(t, "  completed    97-gate-visibility   758d72bf  2026-08-03 04:29  "+gateRunTestPR+"\n"))

	if err := state.Write(home, state.Task{ID: "task-1", Project: "gated", Kind: state.KindShip,
		PR: gateRunTestPR, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR "+gateRunTestPR+" checks green")

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "(gate:") {
		t.Fatalf("got %q, want no gate marker once a completed run recorded this PR", out.String())
	}
}

func TestStatusFleetGateRunUnreachableWhenNoMistakesBinaryMissing(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	registerNoMistakesProject(t, home, "gated")
	t.Setenv("PATH", t.TempDir())

	if err := state.Write(home, state.Task{ID: "task-1", Project: "gated", Kind: state.KindShip,
		PR: gateRunTestPR, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR "+gateRunTestPR+" checks green")

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "(gate: unreachable)") {
		t.Fatalf("got %q, want the gate check named unreachable rather than the stronger no-run-found claim", out.String())
	}
}

// TestStatusFleetSkipsGateCheckWhenItDoesNotApply covers a scout task with a PR-like field unset and
// no report at all: the check has nothing to say about a task that never shipped, so it must stay
// silent rather than misreport it as an ungated ship.
func TestStatusFleetSkipsGateCheckWhenItDoesNotApply(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	registerNoMistakesProject(t, home, "gated")
	t.Setenv("PATH", t.TempDir())

	if err := state.Write(home, state.Task{ID: "task-1", Project: "gated", Kind: state.KindScout,
		CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "(gate:") {
		t.Fatalf("got %q, want no gate marker for a scout task with no shipped PR", out.String())
	}
}

func TestStatusSingleTaskFlagsShippedPRWithNoGateRun(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	registerNoMistakesProject(t, home, "gated")
	t.Setenv("PATH", fakeNoMistakesPath(t, "  completed    other-branch   758d72bf  2026-08-03 04:29  https://github.com/atqamz/secondhand/pull/999\n"))

	if err := state.Write(home, state.Task{ID: "task-1", Project: "gated", Kind: state.KindShip,
		PR: gateRunTestPR, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR "+gateRunTestPR+" checks green")

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Gate run:    no run found") {
		t.Fatalf("got %q, want a Gate run line naming the shipped PR never ran through the gate", out.String())
	}
}

func TestStatusSingleTaskJSONFlagsShippedPRWithNoGateRun(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	registerNoMistakesProject(t, home, "gated")
	t.Setenv("PATH", fakeNoMistakesPath(t, "  completed    other-branch   758d72bf  2026-08-03 04:29  https://github.com/atqamz/secondhand/pull/999\n"))

	if err := state.Write(home, state.Task{ID: "task-1", Project: "gated", Kind: state.KindShip,
		PR: gateRunTestPR, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR "+gateRunTestPR+" checks green")

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"gate_run_issue": "no run found"`) {
		t.Fatalf("got %q, want gate_run_issue naming no run found", out.String())
	}
}

func TestStatusSingleTaskNoGateLineWhenRunFound(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	registerNoMistakesProject(t, home, "gated")
	t.Setenv("PATH", fakeNoMistakesPath(t, "  completed    97-gate-visibility   758d72bf  2026-08-03 04:29  "+gateRunTestPR+"\n"))

	if err := state.Write(home, state.Task{ID: "task-1", Project: "gated", Kind: state.KindShip,
		PR: gateRunTestPR, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR "+gateRunTestPR+" checks green")

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Gate run:") {
		t.Fatalf("got %q, want no Gate run line once a completed run recorded this PR", out.String())
	}
}

// countingNoMistakesPath is fakeNoMistakesPath plus an append to countFile per invocation, so a test
// can assert how many no-mistakes processes one render actually spawned.
func countingNoMistakesPath(t *testing.T, stdout, countFile string) string {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho x >> " + countFile + "\ncat <<'EOF'\n" + stdout + "\nEOF\n"
	if err := os.WriteFile(filepath.Join(bin, "no-mistakes"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin + string(os.PathListSeparator) + os.Getenv("PATH")
}

// TestStatusFleetAsksNoMistakesOncePerProject pins the per-clone caching: without it every done ship
// task on one project spawns its own `no-mistakes runs` and re-parses identical output, on the
// command CLAUDE.md makes the first step of every session.
func TestStatusFleetAsksNoMistakesOncePerProject(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	registerNoMistakesProject(t, home, "gated")
	countFile := filepath.Join(t.TempDir(), "calls")
	t.Setenv("PATH", countingNoMistakesPath(t, "  completed    other-branch   758d72bf  2026-08-03 04:29  https://github.com/atqamz/secondhand/pull/999\n", countFile))

	for _, id := range []string{"task-1", "task-2", "task-3"} {
		if err := state.Write(home, state.Task{ID: id, Project: "gated", Kind: state.KindShip,
			PR: gateRunTestPR, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		writeDoneReport(t, home, id, "PR "+gateRunTestPR+" checks green")
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "(gate: no run found)") != 3 {
		t.Fatalf("got %q, want all three ungated tasks marked", out.String())
	}
	calls, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Fields(string(calls))); got != 1 {
		t.Fatalf("no-mistakes ran %d times for one project, want 1", got)
	}
}

// writeBrokenRegistry writes a data/projects.md line the registry parser rejects, so project.List
// and project.Find both fail on this home.
func writeBrokenRegistry(t *testing.T, home string) {
	t.Helper()
	if err := os.WriteFile(project.RegistryPath(home), []byte("- broken line with no url or mode\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An unreadable registry silently drops every (gate: ...) marker fleet-wide, which renders an
// ungated PR as clean - so the overview still prints, but says on stderr that it did.
func TestStatusFleetNamesAnUnreadableRegistryOnStderr(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeBrokenRegistry(t, home)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "gated", Kind: state.KindShip,
		PR: gateRunTestPR, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR "+gateRunTestPR+" checks green")

	cmd := newStatusCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want a registry fault to degrade the gate check rather than fail the overview", err)
	}
	if !strings.Contains(out.String(), "task-1") {
		t.Fatalf("got %q, want the task table to still print", out.String())
	}
	if !strings.Contains(errOut.String(), "warning:") || !strings.Contains(errOut.String(), "registry") {
		t.Fatalf("got stderr %q, want a warning naming the unreadable project registry", errOut.String())
	}
}

// TestStatusSingleTaskReadsRegistryOnlyWhenTheGateCheckApplies covers a scout task on a home whose
// registry does not parse: the gate-run check has nothing to say about it, so the detail view must
// not fail over a lookup it never needed.
func TestStatusSingleTaskReadsRegistryOnlyWhenTheGateCheckApplies(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeBrokenRegistry(t, home)
	if err := state.Write(home, state.Task{ID: "scout-1", Project: "gated", Kind: state.KindScout,
		CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"scout-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want the detail view to print without reading the registry", err)
	}
	if !strings.Contains(out.String(), "Task:        scout-1") {
		t.Fatalf("got %q, want the detail view rendered", out.String())
	}
}

// The counterpart: when the check does apply, the same unreadable registry fails the command rather
// than silently degrading to no marker - this id's own project is the one fact the check is about.
func TestStatusSingleTaskPropagatesAnUnreadableRegistryWhenTheGateCheckApplies(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeBrokenRegistry(t, home)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "gated", Kind: state.KindShip,
		PR: gateRunTestPR, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR "+gateRunTestPR+" checks green")

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("got nil error, want the unreadable registry to fail the check it is needed for")
	}
}

// TestStatusGateRunUnreachableWhenGateNotInitialized is the uninitialized-gate half of the
// unreachable bucket: no-mistakes still holds that repo's completed runs, so reading its refusal as
// an empty run list would report a genuinely gated PR as never gated.
func TestStatusGateRunUnreachableWhenGateNotInitialized(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	registerNoMistakesProject(t, home, "gated")
	t.Setenv("PATH", fakeNoMistakesPathExit(t, "repo not initialized (run 'no-mistakes init' first)", 1))

	if err := state.Write(home, state.Task{ID: "task-1", Project: "gated", Kind: state.KindShip,
		PR: gateRunTestPR, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR "+gateRunTestPR+" checks green")

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "(gate: unreachable)") {
		t.Fatalf("got %q, want an uninitialized gate read as unreachable, never as no run found", out.String())
	}
}

// The whole point of atqamz/secondhand#70: a worker that finished while nobody was
// attached has to be visible in the next hand status, not only in the stream of the
// watcher that was not running.
func TestStatusFleetFlagsATerminalReportNoWatcherConsumed(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR up")

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "idle (reported: done, unacknowledged)") {
		t.Fatalf("got %q, want the done report flagged unacknowledged", out.String())
	}
}

func TestStatusFleetDoesNotFlagATerminalReportAWatcherConsumed(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	report := "done: PR up\n"
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z",
		ReportOffset: int64(len(report))}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "idle (reported: done)") {
		t.Fatalf("got %q, want the reported state still shown", out.String())
	}
	if strings.Contains(out.String(), "unacknowledged") {
		t.Fatalf("got %q, want no flag on a report a watcher already announced", out.String())
	}
}

func TestStatusFleetJSONFlagsATerminalReportNoWatcherConsumed(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR up")

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"unacknowledged": true`) {
		t.Fatalf("got %q, want unacknowledged true", out.String())
	}
}

func TestStatusSingleTaskFlagsATerminalReportNoWatcherConsumed(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR up")

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "done: PR up (unacknowledged)") {
		t.Fatalf("got %q, want the detail view to flag the unread completion too", out.String())
	}
}

// Free text appended after a real report is expected traffic that must never
// erase it, and enough of it used to push the terminal line out of the detail
// view's 5-line history window - the one view deriving the flag from that
// window instead of the whole file, so it alone called the completion
// acknowledged.
func TestStatusSingleTaskFlagsATerminalReportBeyondTheHistoryWindow(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	report := "done: PR up\nstill tidying\nand more\nand more\nand more\nand more\n"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "done: PR up (unacknowledged)") {
		t.Fatalf("got %q, want the unread completion flagged however much free text follows it", out.String())
	}
}

// The clause has to qualify the state it describes: the flag comes from the
// last line that classified, so the line it decorates must be that one and not
// whatever the worker appended after it.
func TestStatusSingleTaskFlagsTheClassifiedLineNotTrailingFreeText(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR up\nstill tidying\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Reported:    done: PR up (unacknowledged)") {
		t.Fatalf("got %q, want the clause on the terminal report it describes", out.String())
	}
	if !strings.Contains(out.String(), "  still tidying") {
		t.Fatalf("got %q, want the worker's trailing free text still in the history block", out.String())
	}
}

// Without the flag the Reported line stays the worker's literal last line, free
// text included.
func TestStatusSingleTaskShowsTrailingFreeTextWhenAcknowledged(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	report := "done: PR up\nstill tidying\n"
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z",
		ReportOffset: int64(len(report))}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Reported:    still tidying") {
		t.Fatalf("got %q, want the literal last line when no clause applies", out.String())
	}
}

func TestStatusSingleTaskJSONOmitsUnacknowledgedWhenAcknowledged(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	report := "done: PR up\n"
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z",
		ReportOffset: int64(len(report))}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "unacknowledged") {
		t.Fatalf("got %q, want the field absent for a consumer that predates it", out.String())
	}
}

// A worker whose append lands between the row's own read and the flag's read is
// the one way the two can disagree, and it cannot be staged through the command
// itself - so the guard is exercised where it lives. A --json row saying
// "unacknowledged" next to a "working" it reported in the same breath is
// contradictory on the interface the supervisor reads on every check.
func TestUnacknowledgedAnswersForTheStateTheRowPrints(t *testing.T) {
	home := t.TempDir()
	mkFleetDirs(t, home)
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR up\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := state.Task{ID: "task-1"}

	cases := []struct {
		name     string
		reported state.ReportLine
		ok       bool
		want     bool
	}{
		{name: "row prints the terminal state", reported: state.ReportLine{State: state.ReportDone}, ok: true, want: true},
		{name: "row prints work that supersedes it", reported: state.ReportLine{State: state.ReportWorking}, ok: true},
		{name: "row prints nothing classified", reported: state.ReportLine{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := unacknowledged(home, task, c.reported, c.ok, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

// The watcher leaves an unterminated line for its next tick, so a done written
// without a trailing newline has been announced to nobody. Flagging it is the
// whole of atqamz/secondhand#70; skipping it would let the same silent completion
// back in through the newline.
func TestStatusFleetFlagsATerminalReportWithNoTrailingNewline(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Herdr: state.Herdr{PaneID: "wA:pB"}, CreatedAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR up"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "idle (reported: done, unacknowledged)") {
		t.Fatalf("got %q, want an unterminated done flagged like any other unread completion", out.String())
	}
}
