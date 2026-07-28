package watcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/project"
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
// transitions between ticks, and counts its own calls in $CALL_LOG when a test
// sets one (see logPaneGets), so a test driving a live watcher can wait for a
// probe to have happened instead of sleeping; failure paths belong to
// internal/herdr/client_test.go.
// paneGoneStatus drives the fake into herdr's failure shape for `pane get`: an error
// envelope on stdout with exit code 0. A fake that exited nonzero would also reach
// ClassifyStatus's probeErr branch, but through the client's empty-stdout path rather
// than the envelope check that runs ahead of the exit status - and this is the shape
// real herdr uses, which is the one that check exists for.
const paneGoneStatus = "pane-gone"

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
	# Logged after the read, never before: a test that flips the status on seeing
	# the Nth call would otherwise still be racing the Nth read of the file.
	if [ -n "$CALL_LOG" ]; then
		echo "pane get" >> "$CALL_LOG"
	fi
	if [ "$status" = "` + paneGoneStatus + `" ]; then
		printf '{"id":"cli:1","error":{"code":"not_found","message":"pane p1 not found"}}'
		exit 0
	fi
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
	writeFakeGhWithHook(t, prState, "")
}

// writeFakeGhWithHook runs hook (a shell snippet) before the gh double answers.
// project.ValidatePR shells out to gh before the auto-record takes the task lock,
// so this is the only place a test can mutate task state at the one instant that
// matters: after tick's own state.List snapshot, before the auto-record re-reads.
func writeFakeGhWithHook(t *testing.T, prState, hook string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'Warning: gh version is out of date' >&2\n" + hook + "\nprintf '{\"state\":\"" + prState + "\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// registerProject gives a watcher home the two things the auto-record path's
// validation reads: a registry entry and a clone whose origin remote names the
// repo a reported PR URL has to belong to.
func registerProject(t *testing.T, home, name, remote string) {
	t.Helper()
	clonePath := filepath.Join(home, "projects", name)
	if err := os.MkdirAll(clonePath, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "remote.origin.url", remote}} {
		c := exec.Command("git", args...)
		c.Dir = clonePath
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, out)
		}
	}
	if err := project.Add(home, project.Project{Name: name, URL: remote, Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
}

// setStatus publishes a pane's status by atomic rename. The tests that drive a
// live watcher have the fake herdr catting this file from another process, and a
// truncating in-place write would let it read a phantom empty status mid-update -
// which classifies as a transition to an unknown state and swallows the real one.
func setStatus(t *testing.T, statusFile, status string) {
	t.Helper()
	tmp := statusFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, statusFile); err != nil {
		t.Fatal(err)
	}
}

// logPaneGets points the herdr fake's call counter at a file, so a test can wait
// for the watcher to have probed rather than guessing at a sleep.
func logPaneGets(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pane-get-calls")
	t.Setenv("CALL_LOG", path)
	return path
}

func waitForPaneGets(t *testing.T, callLog string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(callLog)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Count(data, []byte("\n")) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d pane get calls", want)
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

// TestTickClassifiesNotBusyAsIdleUnreportedRegardlessOfHerdrSpelling proves the fix
// for #30/#32/#33's working->idle bug against the herdr spelling hand's headless
// polling actually observes: herdr renders a working/blocked->idle transition as
// "done", not "idle", unless a live OS-focused herdr client has that pane's tab
// active at the instant of the transition (see herdr.Status's doc comment) - a
// condition hand's pure-polling model never satisfies. An earlier version of this
// test drove the fake herdr's status file to "done" and expected an unconditional
// "done" event; that was itself the bug the fix corrects, so it's now expected to
// behave exactly like an unexplained idle transition: idle-unreported.
func TestTickClassifiesNotBusyAsIdleUnreportedRegardlessOfHerdrSpelling(t *testing.T) {
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
	if !strings.Contains(buf.String(), "idle-unreported task-1") {
		t.Fatalf("output = %q, want idle-unreported task-1: herdr's done, with nothing explaining the stop, is not task completion", buf.String())
	}
	if errBuf.Len() != 0 {
		t.Fatalf("errOut = %q, want actionable events on out only", errBuf.String())
	}

	d := readDashboard(t, dashPath)
	if len(d.ActiveTasks) != 1 || d.ActiveTasks[0].State != KindIdleUnreported {
		t.Fatalf("ActiveTasks = %+v", d.ActiveTasks)
	}
	if len(d.RecentEvents) != 1 || !strings.Contains(d.RecentEvents[0], "idle-unreported task-1") {
		t.Fatalf("RecentEvents = %+v", d.RecentEvents)
	}

	logData, err := os.ReadFile(filepath.Join(home, "state", "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "idle-unreported task-1") {
		t.Fatalf("events.log = %q, want idle-unreported task-1", string(logData))
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if buf.Len() != 0 {
		t.Fatalf("repeated not-busy state fired again: %q", buf.String())
	}
}

// TestTickUpdatesDashboardToDoneOnlyOnceAReportedDoneIsVerified is the only
// remaining source of a verified-done dashboard update: a worker's own "done"
// report, cross-checked against a task the caller has already recorded as merged.
// herdr's agent_status never drives this by itself - see the test above.
func TestTickUpdatesDashboardToDoneOnlyOnceAReportedDoneIsVerified(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{
		ID: "task-1", Project: "nsr", Kind: state.KindShip,
		PR: "https://github.com/atqamz/secondhand/pull/1", MergeExecuted: true,
		Herdr: state.Herdr{PaneID: "p1"},
	})
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

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR https://github.com/atqamz/secondhand/pull/1 checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "done task-1:") {
		t.Fatalf("output = %q, want a verified done event", buf.String())
	}

	d := readDashboard(t, dashPath)
	if len(d.ActiveTasks) != 1 || d.ActiveTasks[0].State != state.ReportDone {
		t.Fatalf("ActiveTasks = %+v", d.ActiveTasks)
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

func TestTickFiresIdleUnreportedWhenPaneGoesNotBusyWithNoReport(t *testing.T) {
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

	// Real herdr renders this transition as "done", not "idle" - see
	// herdr.Status's doc comment.
	setStatus(t, statusFile, "done")
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "idle-unreported task-1") {
		t.Fatalf("output = %q, want idle-unreported task-1 when nothing explained the stop", buf.String())
	}

	d := readDashboard(t, dashPath)
	if len(d.PendingDecisions) != 1 || !strings.HasPrefix(d.PendingDecisions[0], "task-1:") {
		t.Fatalf("PendingDecisions = %+v, want an idle-unreported task flagged actionable", d.PendingDecisions)
	}
}

func TestTickAbsorbsNotBusyWhenReportExplainsTheStop(t *testing.T) {
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

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("needs-decision: waiting on approval\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Real herdr renders this transition as "done", not "idle" - see
	// herdr.Status's doc comment.
	setStatus(t, statusFile, "done")
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)

	if !strings.Contains(buf.String(), "needs-decision task-1: waiting on approval") {
		t.Fatalf("output = %q, want the report line surfaced", buf.String())
	}
	if strings.Contains(buf.String(), "idle-unreported") {
		t.Fatalf("output = %q, want the not-busy transition absorbed since needs-decision explains the stop", buf.String())
	}
}

func TestTickAutoRecordsPRFromReportLine(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "OPEN")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/secondhand.git")
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

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR https://github.com/atqamz/secondhand/pull/31 checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)

	if !strings.Contains(buf.String(), "reported-done task-1:") {
		t.Fatalf("output = %q, want an unverified reported-done event (no merged PR yet at classification time)", buf.String())
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "https://github.com/atqamz/secondhand/pull/31" {
		t.Fatalf("task.PR = %q, want the embedded URL auto-recorded", task.PR)
	}

	d := readDashboard(t, dashPath)
	if len(d.ActiveTasks) != 1 || d.ActiveTasks[0].PR != "https://github.com/atqamz/secondhand/pull/31" {
		t.Fatalf("ActiveTasks = %+v, want the dashboard PR column updated", d.ActiveTasks)
	}
}

// TestTickAutoRecordsPRButAnnouncesAMissingDashboardRow is the auto-record path's
// half of what every caller of the dashboard PR update owes: recordAutoPR shares
// SetPR with hand pr, and could write the PR to task state, find no active row to
// carry it, and still return nil - the exact silent no-op hand pr exits non-zero
// to refuse, just in the sibling caller. That is why the missing row is now an
// error from dashboard.Update rather than a flag to remember. No active row ever
// gets created here (that would fabricate state); it must be pr-not-recorded.
func TestTickAutoRecordsPRButAnnouncesAMissingDashboardRow(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "OPEN")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/secondhand.git")
	// Deliberately no AddActiveTask: the dashboard has no row for task-1, so the
	// watcher's own auto-record has nothing to reconcile either.

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf, errBuf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, &errBuf)

	url := "https://github.com/atqamz/secondhand/pull/31"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR "+url+" checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	errBuf.Reset()
	tick(ctx, cfg, client, states, &buf, &errBuf)

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != url {
		t.Fatalf("task.PR = %q, want the URL recorded on the task despite the missing row", task.PR)
	}

	if !strings.Contains(buf.String(), "pr-not-recorded task-1: "+url) {
		t.Fatalf("out = %q, want pr-not-recorded for the unreconciled dashboard row", buf.String())
	}
	if strings.Contains(buf.String(), "pr-record-unknown") {
		t.Fatalf("out = %q, want no pr-record-unknown - the outcome is known, not contended", buf.String())
	}
	if !strings.Contains(errBuf.String(), "task-1") {
		t.Fatalf("errOut = %q, want a stderr diagnostic naming the task", errBuf.String())
	}

	dashPath := filepath.Join(home, "data", "dashboard.md")
	d := readDashboard(t, dashPath)
	if len(d.ActiveTasks) != 0 {
		t.Fatalf("ActiveTasks = %+v, want no row fabricated for a task the dashboard never had", d.ActiveTasks)
	}
}

func TestTickDoesNotOverwriteAlreadyRecordedPR(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "OPEN")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, PR: "https://github.com/atqamz/secondhand/pull/1", Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/secondhand.git")

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR https://github.com/atqamz/secondhand/pull/99 checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tick(ctx, cfg, client, states, &buf, io.Discard)

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "https://github.com/atqamz/secondhand/pull/1" {
		t.Fatalf("task.PR = %q, want the already-recorded PR left untouched", task.PR)
	}
}

// TestTickRefusesToAutoRecordAForeignRepoPR is the guard against the worst
// outcome of trusting a worker's text: a PR URL from an unrelated repo becoming
// the task's PR, which `hand merge` would then merge for real.
func TestTickRefusesToAutoRecordAForeignRepoPR(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "OPEN")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/secondhand.git")
	dashPath := filepath.Join(home, "data", "dashboard.md")

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf, errBuf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, &errBuf)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: same as https://github.com/other-org/other-repo/pull/9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tick(ctx, cfg, client, states, &buf, &errBuf)

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "" {
		t.Fatalf("task.PR = %q, want a PR from another repo refused", task.PR)
	}
	if !strings.Contains(buf.String(), "pr-not-recorded task-1: https://github.com/other-org/other-repo/pull/9") {
		t.Fatalf("out = %q, want the refusal surfaced as an actionable event", buf.String())
	}
	if !strings.Contains(errBuf.String(), "auto-record PR for task-1 failed") {
		t.Fatalf("errOut = %q, want the refusal also diagnosed on stderr", errBuf.String())
	}

	log, err := os.ReadFile(filepath.Join(state.Dir(home), "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "pr-not-recorded task-1: https://github.com/other-org/other-repo/pull/9") {
		t.Fatalf("events.log = %q, want the refusal recorded as a durable lifecycle fact", log)
	}

	d := readDashboard(t, dashPath)
	if len(d.PendingDecisions) != 0 {
		t.Fatalf("PendingDecisions = %+v, want an operator notice kept out of the worker's slot", d.PendingDecisions)
	}
}

// TestTickNamesTheCauseWhenOnlyTheDashboardUpdateFails covers the auto-record
// that half-lands: the URL reaches task state and only the dashboard write fails.
// pr-not-recorded is still the honest token - the recording did not complete - but
// the line has to carry the real cause, because `hand pr`, the remedy that token
// names, can only repair the dashboard if the operator learns that is what broke.
func TestTickNamesTheCauseWhenOnlyTheDashboardUpdateFails(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "OPEN")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/secondhand.git")

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf, errBuf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, &errBuf)

	url := "https://github.com/atqamz/secondhand/pull/7"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: "+url+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dashPath := filepath.Join(home, "data", "dashboard.md")
	if err := os.Remove(dashPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dashPath, 0o755); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, &errBuf)

	if !strings.Contains(buf.String(), "pr-not-recorded task-1: "+url) {
		t.Fatalf("out = %q, want an incomplete recording surfaced as pr-not-recorded", buf.String())
	}
	if !strings.Contains(buf.String(), "dashboard update failed") {
		t.Fatalf("out = %q, want the underlying cause named on the durable line", buf.String())
	}
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != url {
		t.Fatalf("task.PR = %q, want the half that did land left on record", task.PR)
	}
}

// TestTickKeepsAWorkerQuestionWhenItsPRURLIsRefused pins the slot ownership: one
// report line can both ask the supervisor something and carry a URL that fails to
// record, and Pending Decisions is upserted by task ID, so the second writer would
// erase the question from the one surface a supervisor is told to read first.
func TestTickKeepsAWorkerQuestionWhenItsPRURLIsRefused(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "OPEN")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/secondhand.git")
	dashPath := filepath.Join(home, "data", "dashboard.md")

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf, errBuf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, &errBuf)

	line := "needs-decision: which base branch? see https://github.com/other-org/other-repo/pull/9\n"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	tick(ctx, cfg, client, states, &buf, &errBuf)

	if !strings.Contains(buf.String(), "pr-not-recorded task-1:") {
		t.Fatalf("out = %q, want the refusal still announced", buf.String())
	}

	d := readDashboard(t, dashPath)
	if len(d.PendingDecisions) != 1 || !strings.Contains(d.PendingDecisions[0], "which base branch?") {
		t.Fatalf("PendingDecisions = %+v, want the worker's own question intact", d.PendingDecisions)
	}
}

// TestTickSurfacesAContendedAutoRecordInsteadOfWaiting covers the poll loop's
// no-blocking-lock rule at the auto-record site: another command holding the task
// lock across network work must not stall the watcher, so the tick reports the
// contention through the ordinary refusal path and moves on.
func TestTickSurfacesAContendedAutoRecordInsteadOfWaiting(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "OPEN")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/secondhand.git")

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf, errBuf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, &errBuf)

	url := "https://github.com/atqamz/secondhand/pull/7"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: "+url+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	unlock, err := state.Lock(home, "task:task-1")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		tick(ctx, cfg, client, states, &buf, &errBuf)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		unlock()
		t.Fatal("tick blocked on a task lock held by another command")
	}
	unlock()

	if !strings.Contains(buf.String(), "pr-record-unknown task-1: "+url) {
		t.Fatalf("out = %q, want the contended auto-record surfaced under its own kind", buf.String())
	}
	if strings.Contains(buf.String(), "pr-not-recorded") {
		t.Fatalf("out = %q, want the outcome not asserted as a failed recording", buf.String())
	}
	if strings.Contains(buf.String(), "hand pr") {
		t.Fatalf("out = %q, want no remedy named for an outcome the watcher cannot know", buf.String())
	}
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "" {
		t.Fatalf("task.PR = %q, want the write skipped under contention", task.PR)
	}
}

// TestTickStaysSilentWhenTheLockHolderRecordedTheSamePR covers the race the
// non-blocking task lock introduced: `hand pr` holds that lock across its own gh
// round-trip while recording the very URL the watcher just read off the report.
// Announcing anything there is a false alarm naming a no-op remedy.
//
// The holder's write has to land after tick's own state.List snapshot, or the
// pre-existing "task already has a PR" guard absorbs the URL and the contention
// path under test is never reached - which is what made an earlier version of
// this test vacuous. Hence the gh double writes it: ValidatePR shells out to gh
// on the way to the lock, so the hook fires inside exactly that window.
func TestTickStaysSilentWhenTheLockHolderRecordedTheSamePR(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/secondhand.git")

	url := "https://github.com/atqamz/secondhand/pull/7"
	snapshot := taskSnapshotWithPR(t, home, "task-1", url)
	writeFakeGhWithHook(t, "OPEN", fmt.Sprintf("cp %q %q", snapshot, state.Path(home, "task-1")))

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf, errBuf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, &errBuf)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: "+url+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	unlock, err := state.Lock(home, "task:task-1")
	if err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	errBuf.Reset()
	tick(ctx, cfg, client, states, &buf, &errBuf)
	unlock()

	if strings.Contains(buf.String(), "pr-record-unknown") || strings.Contains(buf.String(), "pr-not-recorded") {
		t.Fatalf("out = %q, want silence when the lock holder recorded the same URL", buf.String())
	}
	if strings.Contains(errBuf.String(), "auto-record PR") {
		t.Fatalf("errOut = %q, want no diagnostic for a race that resolved itself", errBuf.String())
	}
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != url {
		t.Fatalf("task.PR = %q, want the lock holder's own record left intact", task.PR)
	}
}

// taskSnapshotWithPR copies home's task file with pr recorded into a scratch
// directory, giving a test a file it can drop over the live one to stand in for
// a concurrent writer that finished while hand was mid-call.
func taskSnapshotWithPR(t *testing.T, home, id, pr string) string {
	t.Helper()
	task, err := state.Read(home, id)
	if err != nil {
		t.Fatal(err)
	}
	task.PR = pr
	scratch := t.TempDir()
	if err := state.Write(scratch, task); err != nil {
		t.Fatal(err)
	}
	return state.Path(scratch, id)
}

// TestTickReportsAnUnreadableTaskWhenTheLockIsContended pins the honest message
// for the other half of the contention path: with the task file unreadable, the
// operator must be told that, not sent to a hand status that reads the same file.
func TestTickReportsAnUnreadableTaskWhenTheLockIsContended(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/secondhand.git")

	url := "https://github.com/atqamz/secondhand/pull/7"
	corrupt := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFakeGhWithHook(t, "OPEN", fmt.Sprintf("cp %q %q", corrupt, state.Path(home, "task-1")))

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf, errBuf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, &errBuf)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: "+url+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	unlock, err := state.Lock(home, "task:task-1")
	if err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, &errBuf)
	unlock()

	if !strings.Contains(buf.String(), "pr-record-unknown task-1: "+url) {
		t.Fatalf("out = %q, want the contention surfaced", buf.String())
	}
	if !strings.Contains(buf.String(), "state could not be read") {
		t.Fatalf("out = %q, want the read failure named", buf.String())
	}
	if strings.Contains(buf.String(), "hand status") {
		t.Fatalf("out = %q, want no remedy that reads the same unreadable file", buf.String())
	}
}

// TestTickAnnouncesPRMergedBeforePersistingIt pins the ordering the durable
// marker depends on. Reading the task state at the instant the line hits stdout
// is exactly what a restarted watcher would find had the process died there: the
// marker must still be unset, so the announcement is re-derivable. Persisting
// first would trade a tolerable duplicate for a permanently lost event.
func TestTickAnnouncesPRMergedBeforePersistingIt(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "MERGED")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, PR: "https://github.com/atqamz/secondhand/pull/1", Herdr: state.Herdr{PaneID: "p1"}})

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	out := &stateAtWriteWriter{t: t, home: home, id: "task-1", observed: map[string]bool{}}
	tick(ctx, cfg, client, states, out, io.Discard)
	tick(ctx, cfg, client, states, out, io.Discard)

	if _, ok := out.observed["pr-merged task-1"]; !ok {
		t.Fatalf("out = %q, want pr-merged task-1 announced", out.buf.String())
	}
	if out.observed["pr-merged task-1"] {
		t.Fatal("pr_merged_observed was already persisted when the event was announced: a crash there loses the event for good")
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !task.MergeAnnounced {
		t.Fatal("task.MergeAnnounced = false, want the announced merge persisted after the fact")
	}
}

// stateAtWriteWriter records what a task's durable state held at the moment each
// event line was written, keyed by the line.
type stateAtWriteWriter struct {
	t        *testing.T
	home     string
	id       string
	buf      bytes.Buffer
	observed map[string]bool
}

func (w *stateAtWriteWriter) Write(p []byte) (int, error) {
	task, err := state.Read(w.home, w.id)
	if err != nil {
		w.t.Fatal(err)
	}
	w.observed[strings.TrimSpace(string(p))] = task.MergeAnnounced
	return w.buf.Write(p)
}

// TestTickDoesNotReannounceAPollObservedMergeAfterRestart covers the other half
// of the durable marker: a merge only this watcher's gh poll ever saw, and the
// verified done that followed it, must not be re-emitted by the next process.
func TestTickDoesNotReannounceAPollObservedMergeAfterRestart(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "MERGED")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, PR: "https://github.com/atqamz/secondhand/pull/1", Herdr: state.Herdr{PaneID: "p1"}})

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "pr-merged task-1") || !strings.Contains(buf.String(), "done task-1: checks green") {
		t.Fatalf("out = %q, want the merge and the verified done announced once", buf.String())
	}

	restarted := make(map[string]*TaskState)
	buf.Reset()
	tick(ctx, cfg, client, restarted, &buf, io.Discard)
	tick(ctx, cfg, client, restarted, &buf, io.Discard)
	if buf.Len() != 0 {
		t.Fatalf("out = %q, want nothing re-announced after a restart", buf.String())
	}
}

// TestTickReportsAnUnreadableReportOnResume keeps the unreadable-vs-unreported
// distinction hand status makes: an I/O fault must not degrade into silence.
func TestTickReportsAnUnreadableReportOnResume(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	if err := os.MkdirAll(state.ReportPath(home, "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	var buf, errBuf bytes.Buffer
	tick(context.Background(), cfg, herdr.NewClient(), make(map[string]*TaskState), &buf, &errBuf)

	if !strings.Contains(errBuf.String(), "read report for task-1 failed") {
		t.Fatalf("errOut = %q, want the unreadable report diagnosed, not silently treated as no report", errBuf.String())
	}
}

// TestTickResumesReportTailAfterRestart proves the offset survives the process:
// a fresh states map (a restarted hand watch) must not replay lines the previous
// run already surfaced, and must not forget the report explaining a quiet pane.
func TestTickResumesReportTailAfterRestart(t *testing.T) {
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

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("needs-decision: waiting on approval\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "needs-decision task-1") {
		t.Fatalf("output = %q, want the report line surfaced once", buf.String())
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.ReportOffset == 0 {
		t.Fatal("task.ReportOffset = 0, want the consumed offset persisted")
	}

	restarted := make(map[string]*TaskState)
	buf.Reset()
	tick(ctx, cfg, client, restarted, &buf, io.Discard)
	setStatus(t, statusFile, "done")
	tick(ctx, cfg, client, restarted, &buf, io.Discard)

	if strings.Contains(buf.String(), "needs-decision task-1") {
		t.Fatalf("output = %q, want no replay of an already-surfaced report line", buf.String())
	}
	if strings.Contains(buf.String(), "idle-unreported") {
		t.Fatalf("output = %q, want the not-busy transition still absorbed by the resumed report state", buf.String())
	}
}

// TestTickFiresParkedOnFirstResumedTickWhenTheSilenceAlreadyExceedsTheBound
// proves Ruling 2's park bound survives a restart on the same terms Ruling 1
// demands of stale: anchored to the report file's own mtime, never to anything
// a watch restart resets to now. It asserts that a task already silent past the
// bound before hand watch even starts fires parked on the very first
// post-resume classifying tick, not after a fresh full bound elapses from
// resume.
func TestTickFiresParkedOnFirstResumedTickWhenTheSilenceAlreadyExceedsTheBound(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "idle")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	reportPath := state.ReportPath(home, "task-1")
	if err := os.WriteFile(reportPath, []byte("working: still on the migration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Backdate the report file's mtime to well past the bound, standing in for a
	// worker that had already gone silent before this watch process ever started.
	old := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(reportPath, old, old); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Home:           home,
		PollInterval:   time.Hour,
		StaleThreshold: time.Hour,
		ParkedBounds:   ParkedBounds{Paused: time.Hour, Other: 20 * time.Minute},
	}
	client := herdr.NewClient()

	// A fresh, empty states map is exactly what a hand watch restart produces:
	// nothing survives in memory, only what's durable on disk. The first tick
	// only seeds tracking (see tick's !tracked branch) - the same shape
	// RunUntilEvent's own baseline tick takes - so the earliest a classifier can
	// fire is the second.
	states := make(map[string]*TaskState)
	var buf bytes.Buffer
	tick(context.Background(), cfg, client, states, &buf, io.Discard)
	if strings.Contains(buf.String(), "parked task-1") {
		t.Fatal("parked fired on the seeding tick, before resume had even finished reading durable state")
	}

	buf.Reset()
	tick(context.Background(), cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "parked task-1") {
		t.Fatalf("output = %q, want parked task-1 on the first classifying tick after resume: the silence predates this process and must not need to reaccumulate from resume time", buf.String())
	}
}

// TestTickTiesTheStaleDwellToDurableEvidenceAcrossARestart is issue #75's
// Ruling 1 regression test. --until-event makes "restart" the normal steady
// state, not a rare crash recovery: every delivered event re-arms a fresh
// process with a fresh, empty TaskState map. Before this fix, resumeTaskState
// seeded ClassifyStale's dwell clock (ts.ChangedAt) to the resume time itself,
// so a task that genuinely dwelt in one status for longer than the threshold
// before this process ever started had that entire dwell erased on every
// single re-arm - on a fleet busy enough to re-arm faster than the threshold
// elapses, stale never fires at all. The fix persists StatusChangedAt and
// seeds the dwell clock from it (or from CreatedAt, if the task has never
// transitioned) instead of from "now", so the dwell survives a restart the way
// report_offset already does.
//
// This test asserts stale fires on the first classifying tick after a restart
// for a task whose durable StatusChangedAt already exceeds the threshold - not
// after a fresh full threshold elapses counted from resume, which is what
// decoupling the dwell from that durable evidence would silently reintroduce.
func TestTickTiesTheStaleDwellToDurableEvidenceAcrossARestart(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "dashboard.md"), []byte(dashboardSkeleton), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stands in for a task that has genuinely dwelt in "working" for 30
	// minutes across one or more prior hand watch invocations, before this
	// process ever started.
	dwelling := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	task := state.Task{
		ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"},
		CreatedAt: dwelling, StatusChangedAt: dwelling,
	}
	if err := state.Write(home, task); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: 20 * time.Minute}
	client := herdr.NewClient()
	ctx := context.Background()

	// A fresh, empty states map is exactly what a hand watch restart produces.
	states := make(map[string]*TaskState)
	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if strings.Contains(buf.String(), "stale task-1") {
		t.Fatal("stale fired on the seeding tick, before resume had even read durable state")
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "stale task-1") {
		t.Fatalf("output = %q, want stale task-1 on the first classifying tick: the task has genuinely dwelt 30m in one status, already past the 20m threshold, and a restart must not reset that clock to zero", buf.String())
	}
}

// TestTickAnnouncesAVerifiedDoneAfterARestartThatMissedTheEvidence covers the
// window between the two halves: the worker reports done, hand watch stops, and
// hand merge lands the work - writing merged, and touching no dashboard row. On
// restart the evidence is already on disk, so a marker re-derived from current
// evidence would conclude the verified line had gone out and never print it,
// leaving the row and any pending decision stuck where the report left them.
func TestTickAnnouncesAVerifiedDoneAfterARestartThatMissedTheEvidence(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	dashPath := filepath.Join(home, "data", "dashboard.md")
	if err := dashboard.Update(dashPath, dashboard.UpdateOpts{AddActiveTask: &dashboard.ActiveTask{
		ID: "task-1", Project: "nsr", Kind: state.KindShip, State: "working", Age: "just now",
	}}); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !hasEventLine(buf.String(), "reported-done task-1: checks green") {
		t.Fatalf("out = %q, want an unverified reported-done while nothing has landed", buf.String())
	}

	// hand merge, with the watcher stopped: it writes merged and leaves the
	// dashboard to the events the watcher never got to emit.
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.DoneVerified {
		t.Fatal("task.DoneVerified = true, want the unverified report to leave the marker unset")
	}
	task.MergeExecuted = true
	if err := state.Write(home, task); err != nil {
		t.Fatal(err)
	}

	restarted := make(map[string]*TaskState)
	buf.Reset()
	tick(ctx, cfg, client, restarted, &buf, io.Discard)
	tick(ctx, cfg, client, restarted, &buf, io.Discard)
	if !hasEventLine(buf.String(), "done task-1: checks green") {
		t.Fatalf("out = %q, want the verified done announced by the restarted watcher", buf.String())
	}

	d := readDashboard(t, dashPath)
	if len(d.ActiveTasks) != 1 || d.ActiveTasks[0].State != state.ReportDone {
		t.Fatalf("ActiveTasks = %+v, want the row moved to done", d.ActiveTasks)
	}

	task, err = state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !task.DoneVerified {
		t.Fatal("task.DoneVerified = false, want the announcement persisted after the fact")
	}

	again := make(map[string]*TaskState)
	buf.Reset()
	tick(ctx, cfg, client, again, &buf, io.Discard)
	tick(ctx, cfg, client, again, &buf, io.Discard)
	if buf.Len() != 0 {
		t.Fatalf("out = %q, want the verified done not re-announced by a later restart", buf.String())
	}
}

// TestTickAnnouncesTheShipsOwnVerifiedDoneAfterPromoteResetsTheStaleMarker covers
// the third layer of the inherited-bookkeeping family (SPECS.md's "What survives
// a hand watch restart", the hand promote row): a promoted task keeps CreatedAt,
// so the identity check that resets state across a torn-down-and-respawned ID
// never fires here, and the watcher's own long-running in-memory TaskState - not
// just the on-disk marker a restart would re-read - carries the scout's
// DoneVerified into the ship run. Clearing the disk field in cmd/promote.go alone
// is not the fix: syncTaskState's ts.DoneVerified-OR would resurrect it on the
// very next tick unless the cached copy is forgotten too.
func TestTickAnnouncesTheShipsOwnVerifiedDoneAfterPromoteResetsTheStaleMarker(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindScout, Herdr: state.Herdr{PaneID: "p1"}})
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("scout findings"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)

	// The scout's own done is verified by the report.md the scout deliverable
	// requires, with nothing else in play.
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: scout findings\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !hasEventLine(buf.String(), "done task-1: scout findings") {
		t.Fatalf("out = %q, want the scout's verified done", buf.String())
	}
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !task.DoneVerified {
		t.Fatal("task.DoneVerified = false, want the scout's announcement persisted")
	}

	// hand promote: same rewrite cmd/promote.go makes - kind changes, CreatedAt
	// and the report channel do not - with the DoneVerified reset this test exists
	// to cover.
	task.Kind = state.KindShip
	task.DoneVerified = false
	if err := state.Write(home, task); err != nil {
		t.Fatal(err)
	}

	// The ship's own report line lands on the same continuous report stream,
	// before any merge evidence exists - the ordinary ordering ClassifyDeferredDone
	// exists for.
	f, err := os.OpenFile(state.ReportPath(home, "task-1"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("done: ship work\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !hasEventLine(buf.String(), "reported-done task-1: ship work") {
		t.Fatalf("out = %q, want an unverified reported-done - the ship has not merged yet", buf.String())
	}
	task, err = state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.DoneVerified {
		t.Fatal("task.DoneVerified = true, want promote's reset not resurrected by the cached copy")
	}

	// hand merge lands the evidence the ship's own done needs.
	task.MergeExecuted = true
	if err := state.Write(home, task); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !hasEventLine(buf.String(), "done task-1: ship work") {
		t.Fatalf("out = %q, want the ship's own verified done announced", buf.String())
	}
}

// hasEventLine matches want as a whole output line, so "reported-done <id>" and
// "done <id>" - one a substring of the other - can't be confused.
func hasEventLine(out, want string) bool {
	for _, line := range strings.Split(out, "\n") {
		if line == want {
			return true
		}
	}
	return false
}

// TestTickKeepsAMultiLineAutoRecordFailureOnOneLine pins the one-line-per-event
// invariant against its noisiest real cause: ghutil wraps gh's stderr into the
// error verbatim, and gh emits several lines for auth and network failures. A
// multi-line Event.Text breaks the stdout contract, makes events.log's 200-line
// bound count one event as several, and splits one dashboard bullet into several
// fake Recent Events. The cause is preserved - only its line breaks are not.
func TestTickKeepsAMultiLineAutoRecordFailureOnOneLine(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGhFailingMultiline(t)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/secondhand.git")
	dashPath := filepath.Join(home, "data", "dashboard.md")

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf, errBuf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, &errBuf)

	url := "https://github.com/atqamz/secondhand/pull/7"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: "+url+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, &errBuf)

	// The same report line also emits reported-done, so the invariant under test
	// is per event: the failure occupies exactly one line, carrying the whole
	// cause, and no fragment of it lands on a line of its own.
	printed := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	var failures []string
	for _, line := range printed {
		if strings.HasPrefix(line, "pr-not-recorded ") {
			failures = append(failures, line)
		}
	}
	if len(failures) != 1 {
		t.Fatalf("out = %q, want exactly one line for one event", buf.String())
	}
	if !strings.HasPrefix(failures[0], "pr-not-recorded task-1: "+url) {
		t.Fatalf("out = %q, want the auto-record failure surfaced", failures[0])
	}
	if !strings.Contains(failures[0], "error connecting to api.github.com") || !strings.Contains(failures[0], "githubstatus.com") {
		t.Fatalf("out = %q, want the whole cause kept, including gh's later stderr lines", failures[0])
	}

	log, err := os.ReadFile(filepath.Join(state.Dir(home), "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	logged := strings.Split(strings.TrimRight(string(log), "\n"), "\n")
	if len(logged) != len(printed) {
		t.Fatalf("events.log = %q, want %d lines for %d events against the 200-line bound", log, len(printed), len(printed))
	}

	d := readDashboard(t, dashPath)
	if len(d.RecentEvents) != len(printed) {
		t.Fatalf("RecentEvents = %+v, want %d bullets, not a mangled section", d.RecentEvents, len(printed))
	}
}

// writeFakeGhFailingMultiline mirrors the real gh's noisiest failure: auth and
// network errors exit non-zero having written several lines to stderr, which
// ghutil.PRIsMerged wraps into the returned error verbatim. Nothing is written
// to stdout, as with the real tool on this path.
func writeFakeGhFailingMultiline(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf 'error connecting to api.github.com\\ncheck your internet connection or https://githubstatus.com\\n' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
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
	handleEvent(Config{Home: home}, &Event{Kind: KindReportDone, Verified: true, TaskID: "task-1", Text: "done task-1"},
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
	// which call()'s empty-stdout-plus-runErr branch handles (len(trimmed) == 0
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

// TestRunUntilEventTakesTheStartupStateAsBaseline is the regression test for the
// delivery failure of 2026-07-28: the grep-on-first-line wrapper this mode
// replaces matched a done worker's startup line, took it for a transition, and
// left the two real events that followed unread for three hours. Whatever the
// fleet already is when the watcher arms cannot be the event it delivers,
// including a report line a previous watcher left unconsumed - which is a fresh
// event to the poll loop, since report_offset still points behind it.
func TestRunUntilEventTakesTheStartupStateAsBaseline(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "done")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 200 * time.Millisecond}
	err := RunUntilEvent(context.Background(), cfg, &out, io.Discard)

	if !errors.Is(err, ErrNoEvent) {
		t.Fatalf("RunUntilEvent = %v, want ErrNoEvent: nothing changed after it armed", err)
	}
	if out.Len() != 0 {
		t.Fatalf("out = %q, want the startup state delivered as nothing", out.String())
	}

	logData, err := os.ReadFile(filepath.Join(home, "state", "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "reported-done task-1") {
		t.Fatalf("events.log = %q, want the baseline's own events still recorded: the report line is consumed either way", string(logData))
	}
}

func TestRunUntilEventDeliversTheFirstTransitionAndReturns(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	callLog := logPaneGets(t)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 10 * time.Second}

	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- RunUntilEvent(context.Background(), cfg, &out, io.Discard) }()

	// The arm-time probe and both baseline ticks have probed the pane, so the
	// status change below is a transition from the baseline rather than a
	// different baseline.
	waitForPaneGets(t, callLog, 3)
	setStatus(t, statusFile, "done")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunUntilEvent = %v, want nil so the exit code reads as a delivered event", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunUntilEvent did not return after a transition")
	}
	if !strings.Contains(out.String(), "idle-unreported task-1") {
		t.Fatalf("out = %q, want idle-unreported task-1", out.String())
	}
}

// TestRunUntilEventDeliversIdleUnreportedForAWorkerThatWentQuiet is issue #75's
// acceptance test, at the layer that decides it: the signal the previous
// workaround had to exclude, because a repeating one would wake its caller every
// poll, is the one this mode has to deliver.
func TestRunUntilEventDeliversIdleUnreportedForAWorkerThatWentQuiet(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	callLog := logPaneGets(t)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("working: still on the migration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 10 * time.Second}

	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- RunUntilEvent(context.Background(), cfg, &out, io.Discard) }()

	// The arm-time probe and both baseline ticks have probed the pane.
	waitForPaneGets(t, callLog, 3)
	setStatus(t, statusFile, "done")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunUntilEvent = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a worker that went quiet without a terminal report never reached the caller")
	}
	if !strings.Contains(out.String(), "idle-unreported task-1") {
		t.Fatalf("out = %q, want idle-unreported task-1", out.String())
	}
}

func TestRunUntilEventReportsNoEventOnTimeout(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 100 * time.Millisecond}

	var out bytes.Buffer
	start := time.Now()
	err := RunUntilEvent(context.Background(), cfg, &out, io.Discard)

	if !errors.Is(err, ErrNoEvent) {
		t.Fatalf("RunUntilEvent = %v, want ErrNoEvent so a re-arm loop can tell a quiet window from an event", err)
	}
	if !strings.Contains(err.Error(), "100ms") {
		t.Fatalf("err = %v, want the elapsed timeout named", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("returned after %s, want the timeout to bound the wait", elapsed)
	}
	if out.Len() != 0 {
		t.Fatalf("out = %q, want stdout to carry events only, never the timeout notice", out.String())
	}
}

func TestRunUntilEventReportsNoEventOnContextCancel(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunUntilEvent(ctx, cfg, &bytes.Buffer{}, io.Discard) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		// A signaled watcher delivered nothing, so it cannot exit 0: the caller
		// would read the exit as fleet news and go looking for a line that is not
		// there.
		if !errors.Is(err, ErrNoEvent) {
			t.Fatalf("RunUntilEvent = %v, want ErrNoEvent", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunUntilEvent did not return after context cancellation")
	}
}

func TestRunUntilEventFailsWhenHerdrUnreachable(t *testing.T) {
	// Same crashed-or-missing-binary shape as TestRunFailsWhenHerdrUnreachable:
	// exit 1 with empty stdout.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := RunUntilEvent(context.Background(), Config{Home: t.TempDir(), PollInterval: time.Second, StaleThreshold: time.Minute}, &bytes.Buffer{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "herdr unreachable") {
		t.Fatalf("got err %v, want herdr unreachable", err)
	}
	if errors.Is(err, ErrNoEvent) {
		t.Fatal("a failed watcher reported ErrNoEvent, which a caller reads as a quiet fleet rather than a broken watcher")
	}
}

// TestRunUntilEventFailsToArmWhenATaskCannotBeProbed is Ruling 3 of issue #75's
// reopening: a watcher that cannot see a worker at arm time must refuse to look
// armed for it, with an exit distinct from both a delivered event and a timeout,
// naming the worker it could not reach.
func TestRunUntilEventFailsToArmWhenATaskCannotBeProbed(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, paneGoneStatus)
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: time.Second}

	err := RunUntilEvent(context.Background(), cfg, &bytes.Buffer{}, io.Discard)
	if !errors.Is(err, ErrArmFailed) {
		t.Fatalf("RunUntilEvent = %v, want ErrArmFailed: an unprobeable worker must not look armed", err)
	}
	if !strings.Contains(err.Error(), "task-1") {
		t.Fatalf("err = %q, want it to name the worker it could not probe", err.Error())
	}
	if errors.Is(err, ErrNoEvent) {
		t.Fatal("an arm failure reported ErrNoEvent too, which a caller cannot tell apart from a quiet fleet")
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

// TestTickResumesTheLastStateAfterATrailingMalformedLine holds resume to what the
// live path already does: a free-text line appended after a real report explains
// nothing, so it must not erase the report it follows. Reading it back as "never
// reported" turns the next quiet pane into idle-unreported, which then overwrites
// the worker's own Pending Decision with "stopped, reason unknown".
func TestTickResumesTheLastStateAfterATrailingMalformedLine(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	ctx := context.Background()

	states := make(map[string]*TaskState)
	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)

	report := "needs-decision: which base branch?\nlooked at both again\n"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "malformed report task-1") {
		t.Fatalf("output = %q, want the free-text line still surfaced", buf.String())
	}

	restarted := make(map[string]*TaskState)
	buf.Reset()
	tick(ctx, cfg, client, restarted, &buf, io.Discard)
	setStatus(t, statusFile, "done")
	tick(ctx, cfg, client, restarted, &buf, io.Discard)

	if strings.Contains(buf.String(), "idle-unreported") {
		t.Fatalf("output = %q, want the stop still explained by the needs-decision the malformed line followed", buf.String())
	}

	d := readDashboard(t, filepath.Join(home, "data", "dashboard.md"))
	for _, pd := range d.PendingDecisions {
		if strings.Contains(pd, "reason unknown") {
			t.Fatalf("PendingDecisions = %+v, want the worker's own question left intact", d.PendingDecisions)
		}
	}
}

// TestTickReseedsARespawnedTaskID keys tracking on identity rather than on ID. A
// teardown and respawn between two ticks is a different task, and inheriting the
// previous run's TaskState suppresses the new one's verified done for good:
// syncTaskState writes that inherited done_verified onto the fresh JSON. Same
// hazard as a surviving report channel, one layer in.
func TestTickReseedsARespawnedTaskID(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindScout, Herdr: state.Herdr{PaneID: "p1"}})
	reportMD := filepath.Join(home, "data", "task-1", "report.md")
	if err := os.MkdirAll(filepath.Dir(reportMD), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportMD, []byte("findings"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: finished\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "done task-1: finished") {
		t.Fatalf("output = %q, want the first run's verified done", buf.String())
	}

	if err := state.Delete(home, "task-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(reportMD); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindScout,
		Herdr:     state.Herdr{PaneID: "p1"},
		CreatedAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: round two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "reported-done task-1: round two") {
		t.Fatalf("output = %q, want the respawned task's own done report read from offset 0", buf.String())
	}

	respawned, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if respawned.DoneVerified {
		t.Fatal("done_verified inherited by a respawned ID, want the previous run's announcement not carried over")
	}

	if err := os.WriteFile(reportMD, []byte("findings again"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "done task-1: round two") {
		t.Fatalf("output = %q, want the respawned task's verified done announced too", buf.String())
	}
}

// TestTickSetsTheStateColumnOnAReportedStop covers the well-behaved worker: it
// says why it stopped, herdr's not-busy transition is then absorbed on purpose,
// and the row would otherwise keep reading "working" - the very bug the report
// channel exists to remove, with the supervisor reading that column first. The
// last step is the way back, the steer-and-continue loop: nothing else in the
// codebase writes "working" to that column, so without report-working the row
// latches on the stop-state and a steered worker shows as awaiting a decision
// forever - the same two-views-disagree defect, inverted.
func TestTickSetsTheStateColumnOnAReportedStop(t *testing.T) {
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
	tick(ctx, cfg, client, states, &bytes.Buffer{}, io.Discard)

	report := ""
	for _, tc := range []struct{ line, wantState, wantPending string }{
		{"paused: waiting on the nightly build\n", KindReportPaused, ""},
		{"blocked: needs an API key\n", KindReportBlocked, "needs an API key"},
		{"needs-decision: which base branch?\n", KindReportNeedsDecision, "which base branch?"},
		{"paused: sleeping on it\n", KindReportPaused, "which base branch?"},
		{"working: main, carrying on\n", state.ReportWorking, ""},
	} {
		report += tc.line
		if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(report), 0o644); err != nil {
			t.Fatal(err)
		}
		tick(ctx, cfg, client, states, &bytes.Buffer{}, io.Discard)

		d := readDashboard(t, dashPath)
		if len(d.ActiveTasks) != 1 || d.ActiveTasks[0].State != tc.wantState {
			t.Fatalf("ActiveTasks = %+v after %q, want state %s", d.ActiveTasks, tc.line, tc.wantState)
		}
		switch {
		case tc.wantPending == "":
			if len(d.PendingDecisions) != 0 {
				t.Fatalf("PendingDecisions = %+v after %q, want the slot empty", d.PendingDecisions, tc.line)
			}
		case len(d.PendingDecisions) != 1 || !strings.Contains(d.PendingDecisions[0], tc.wantPending):
			t.Fatalf("PendingDecisions = %+v after %q, want %q", d.PendingDecisions, tc.line, tc.wantPending)
		}
	}
}

// A pane hand cannot probe says nothing about a question the worker already asked,
// and clearing it would be unrecoverable: the report line is already past
// report_offset, and the recovery tick emits no event because the tracked status
// never changed. ClassifyStatus fires failed on any probe error, so a herdr daemon
// restart would otherwise wipe every tracked task's Pending Decisions slot in one
// tick - fleet-wide loss out of a transient blip.
func TestTickKeepsAPendingQuestionWhenThePaneProbeFails(t *testing.T) {
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
	tick(ctx, cfg, client, states, &bytes.Buffer{}, io.Discard)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("needs-decision: which base branch?\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tick(ctx, cfg, client, states, &bytes.Buffer{}, io.Discard)

	setStatus(t, statusFile, paneGoneStatus)
	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "failed task-1") {
		t.Fatalf("output = %q, want the failed event", buf.String())
	}

	d := readDashboard(t, dashPath)
	if len(d.ActiveTasks) != 1 || d.ActiveTasks[0].State != KindFailed {
		t.Fatalf("ActiveTasks = %+v, want the failed state column", d.ActiveTasks)
	}
	if len(d.PendingDecisions) != 1 || !strings.Contains(d.PendingDecisions[0], "which base branch?") {
		t.Fatalf("PendingDecisions = %+v, want the worker's question left standing", d.PendingDecisions)
	}
}
