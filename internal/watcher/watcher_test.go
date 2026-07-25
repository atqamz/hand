package watcher

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/state"
)

const dashboardSkeleton = `# Dashboard

## Active Tasks
| id | project | kind | state | age | pr |
|---|---|---|---|---|---|

## Pending Decisions

## Recent Events

## Recent Completions

## Projects
`

func writeFakeHerdr(t *testing.T, statusFile string) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
"workspace list")
	printf '{"id":"cli:1","result":{"workspaces":[]}}'
	;;
"pane get")
	status=$(cat "$STATUS_FILE")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"p1","agent_status":"%s"}}}' "$status"
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STATUS_FILE", statusFile)
}

func writeFakeGh(t *testing.T, prState string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '{\"state\":\"" + prState + "\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func setStatus(t *testing.T, statusFile, status string) {
	t.Helper()
	if err := os.WriteFile(statusFile, []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupWatcherHome(t *testing.T, taskOpts state.Task) (home string) {
	t.Helper()
	home = t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "dashboard.md"), []byte(dashboardSkeleton), 0o644); err != nil {
		t.Fatal(err)
	}
	taskOpts.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := state.Write(home, taskOpts); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestTickClassifiesDoneAndUpdatesDashboardAndLog(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	dashPath := filepath.Join(home, "data", "dashboard.md")
	if err := dashboard.Update(dashPath, dashboard.UpdateOpts{AddActiveTask: &dashboard.ActiveTask{
		ID: "task-1", Project: "nsr", Kind: "ship", State: "working", Age: "just now",
	}}); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if buf.Len() != 0 {
		t.Fatalf("first tick printed output for newly seen task: %q", buf.String())
	}

	setStatus(t, statusFile, "done")
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "done task-1") {
		t.Fatalf("output = %q, want done task-1", buf.String())
	}

	d := readDashboard(t, dashPath)
	if len(d.ActiveTasks) != 1 || d.ActiveTasks[0].State != KindDone {
		t.Fatalf("ActiveTasks = %+v", d.ActiveTasks)
	}
	if len(d.RecentEvents) != 1 || !strings.Contains(d.RecentEvents[0], "done task-1") {
		t.Fatalf("RecentEvents = %+v", d.RecentEvents)
	}

	logData, err := os.ReadFile(filepath.Join(home, "state", "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "done task-1") {
		t.Fatalf("events.log = %q, want done task-1", string(logData))
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if buf.Len() != 0 {
		t.Fatalf("repeated done state fired again: %q", buf.String())
	}
}

func TestTickClassifiesBlockedAndSetsPendingDecision(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	dashPath := filepath.Join(home, "data", "dashboard.md")

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)

	setStatus(t, statusFile, "blocked")
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "blocked task-1:") {
		t.Fatalf("output = %q, want blocked task-1", buf.String())
	}

	d := readDashboard(t, dashPath)
	if len(d.PendingDecisions) != 1 || !strings.HasPrefix(d.PendingDecisions[0], "task-1:") {
		t.Fatalf("PendingDecisions = %+v", d.PendingDecisions)
	}
}

func TestTickClassifiesPRMerged(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "MERGED")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, PR: "https://example.com/pr/1", Herdr: state.Herdr{PaneID: "p1"}})

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "pr-merged task-1") {
		t.Fatalf("output = %q, want pr-merged task-1", buf.String())
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if buf.Len() != 0 {
		t.Fatalf("pr-merged fired again: %q", buf.String())
	}
}

func TestTickForgetsTornDownTasks(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if len(states) != 1 {
		t.Fatalf("states = %+v, want task-1 tracked", states)
	}

	if err := state.Delete(home, "task-1"); err != nil {
		t.Fatal(err)
	}
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if len(states) != 0 {
		t.Fatalf("states = %+v, want torn-down task forgotten", states)
	}
}

func TestRunFailsWhenHerdrUnreachable(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	home := t.TempDir()
	err := Run(context.Background(), Config{Home: home, PollInterval: time.Second, StaleThreshold: time.Minute}, &bytes.Buffer{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "herdr unreachable") {
		t.Fatalf("got err %v, want herdr unreachable", err)
	}
}

func TestRunExitsCleanlyOnContextCancel(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Minute}, &bytes.Buffer{}, io.Discard)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}

func readDashboard(t *testing.T, path string) dashboard.Dashboard {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := dashboard.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
