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

// writeFakeHerdr fakes the two query commands a tick makes: real herdr answers
// both with a JSON envelope carrying a non-null result on stdout and exit 0,
// and reports failures as an envelope error rather than bare stderr
// (internal/herdr/client.go's call doc comment), which this fake mirrors for
// success and diverges from for the unexpected-args arm - a bare stderr line
// and exit 1, so a call shape no test anticipated fails loudly instead of
// parsing. "pane get" reads its status from statusFile so a test can drive
// transitions between ticks; failure paths belong to
// internal/herdr/client_test.go.
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

// writeFakeGh fakes `gh pr view --json state`, the only gh call a tick makes
// (watcher.go's ghutil.PRIsMerged). Real gh prints that JSON object on stdout
// with exit 0 and prefixes its own warnings on stderr, so the fake emits a
// stderr line too: PRIsMerged reads stdout alone, and a CombinedOutput
// regression there must fail this watcher path as well, not only
// internal/ghutil/pr_test.go.
func writeFakeGh(t *testing.T, prState string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'Warning: gh version is out of date' >&2\nprintf '{\"state\":\"" + prState + "\"}'\n"
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

	var buf, errBuf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, &errBuf)
	if buf.Len() != 0 {
		t.Fatalf("first tick printed output for newly seen task: %q", buf.String())
	}

	setStatus(t, statusFile, "done")
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, &errBuf)
	if !strings.Contains(buf.String(), "done task-1") {
		t.Fatalf("output = %q, want done task-1", buf.String())
	}
	if errBuf.Len() != 0 {
		t.Fatalf("errOut = %q, want actionable events on out only", errBuf.String())
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

func TestTickSendsDiagnosticsToErrOut(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state", "task-1.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	var buf, errBuf bytes.Buffer
	tick(context.Background(), cfg, herdr.NewClient(), make(map[string]*TaskState), &buf, &errBuf)

	if !strings.Contains(errBuf.String(), "watch: list tasks failed") {
		t.Fatalf("errOut = %q, want list tasks failed diagnostic", errBuf.String())
	}
	if buf.Len() != 0 {
		t.Fatalf("out = %q, want diagnostics on errOut only", buf.String())
	}
}

func TestHandleEventSendsLogFailureToErrOut(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state", "events.log"), 0o755); err != nil {
		t.Fatal(err)
	}

	var buf, errBuf bytes.Buffer
	handleEvent(Config{Home: home}, &Event{Kind: KindDone, TaskID: "task-1", Text: "done task-1"},
		state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, &buf, &errBuf)

	if buf.String() != "done task-1\n" {
		t.Fatalf("out = %q, want the event text only", buf.String())
	}
	if !strings.Contains(errBuf.String(), "watch: append events.log failed") {
		t.Fatalf("errOut = %q, want append events.log failed diagnostic", errBuf.String())
	}
}

func TestRunFailsWhenHerdrUnreachable(t *testing.T) {
	// exit 1 with empty stdout is the faithful crashed-or-missing-binary shape,
	// which call() handles at internal/herdr/client.go:52-57 (len(trimmed) == 0
	// && runErr != nil). It is a distinct shape from herdr's ordinary failure
	// (exit 0 plus an error envelope), and only this one means "unreachable".
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
