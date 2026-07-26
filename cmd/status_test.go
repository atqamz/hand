package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if !strings.Contains(out.String(), "Task:       task-1") || !strings.Contains(out.String(), "State:      idle") {
		t.Fatalf("got %q", out.String())
	}
}

func TestStatusSingleTaskJSON(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
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

	cmd := newStatusCmd()
	cmd.SetArgs([]string{"missing-task"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got err %v, want not found", err)
	}
}

func TestStatusFleetFlagsIdleWithoutTerminalReportAsUnreported(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
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

func TestStatusFleetDoesNotFlagWorkingTasks(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
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

func TestStatusSingleTaskShowsReportedStateAndHistory(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
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
	if !strings.Contains(got, "Reported:   needs-decision: waiting on review") {
		t.Fatalf("got %q, want the last reported state on its own line", got)
	}
	if !strings.Contains(got, "Report history (reported by worker, not verified current truth):") {
		t.Fatalf("got %q, want the history block labeled as reported, not verified truth", got)
	}
	if !strings.Contains(got, "working: started") {
		t.Fatalf("got %q, want the earlier report line in the history", got)
	}
}

func TestStatusSingleTaskJSONIncludesReportedAndHistory(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
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
