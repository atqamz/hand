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
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/store"
)

func notifyMessageTemplate(marker string) string {
	if runtime.GOOS == "windows" {
		return `<nul set /p "=%HAND_MESSAGE%" > "` + marker + `"`
	}
	return "printf '%s' \"$HAND_MESSAGE\" > '" + marker + "'"
}

// Drives the fake into herdr's failure shape for `pane get` - an error envelope on stdout with exit 0,
// the shape the envelope check exists for. Exiting nonzero would reach ClassifyStatus's probeErr
// branch too, but through the client's empty-stdout path rather than that check, which runs first.
const paneGoneStatus = "pane-gone"

// Fakes the two query commands a tick makes, mirroring real herdr: a JSON envelope with a non-null
// result on stdout and exit 0, failures as an envelope error rather than bare stderr, per
// internal/herdr/client.go's call doc. Those failure paths belong to internal/herdr/client_test.go.
func writeFakeHerdr(t *testing.T, statusFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake herdr is a POSIX shell script, not supported on windows")
	}
	bin := t.TempDir()
	// The unexpected-args arm deliberately diverges - a bare stderr line and exit 1 - so a call shape
	// no test anticipated fails loudly instead of parsing. "pane get" reads its status from statusFile,
	// so a test can drive transitions between ticks.
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
	# PANE_AGENT is empty unless a test sets it, which is what keeps every test written before the
	# usage-limit check from reading a pane: an unclassified pane names no harness, so no harness
	# capability applies to it.
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"p1","agent_status":"%s","agent":"%s"}}}' "$status" "$PANE_AGENT"
	;;
"pane read")
	echo "read" >> "${PANE_LOG:-/dev/null}"
	# Raw text on stdout, not an envelope: herdr's own contract for this one
	# command, mirrored here because the client parses it that way.
	cat "$PANE_TEXT_FILE" 2>/dev/null
	;;
"pane send-text"|"pane send-keys")
	# Empty stdout and exit 0 is real herdr's success shape for a void command.
	echo "$2 $4" >> "${PANE_LOG:-/dev/null}"
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

// Fakes `gh pr view --json state`, the only gh call a tick makes (watcher.go's ghutil.PRIsMerged),
// which real gh answers with that JSON object on stdout and exit 0.
func writeFakeGh(t *testing.T, prState string) {
	writeFakeGhWithHook(t, prState, "")
}

// Runs hook (a shell snippet) before the gh double answers. project.ValidatePR shells out to gh
// before the auto-record takes the task lock, so this is the only place a test can mutate task state
// at the instant that matters: after tick's state.List snapshot, before the auto-record re-reads.
func writeFakeGhWithHook(t *testing.T, prState, hook string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a POSIX shell script, not supported on windows")
	}
	bin := t.TempDir()
	// Real gh prefixes its own warnings on stderr and PRIsMerged reads stdout alone, so the fake emits
	// a stderr line too: a CombinedOutput regression there must fail this watcher path as well, not
	// only internal/ghutil/pr_test.go.
	script := "#!/bin/sh\necho 'Warning: gh version is out of date' >&2\n" + hook + "\nprintf '{\"state\":\"" + prState + "\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Gives a watcher home the two things the auto-record path's validation reads: a registry entry and a
// clone whose origin remote names the repo a reported PR URL has to belong to.
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

// Publishes by atomic rename because the fake herdr cats this file from another process: a
// truncating write would let it read a phantom empty status, which classifies as a transition to
// unknown and swallows the real one.
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
	// hand init creates this; the project registry (data/projects.md) expects it
	// to already exist rather than creating it itself.
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if taskOpts.CreatedAt == "" {
		taskOpts.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := state.Write(home, taskOpts); err != nil {
		t.Fatal(err)
	}
	return home
}

// Proves the working->idle fix (atqamz/hand#30, atqamz/hand#32, atqamz/hand#33)
// against the spelling hand's headless polling observes: herdr renders working/blocked->idle as "done",
// not "idle", unless a live OS-focused client has that tab active then (see herdr.Status's doc).
func TestTickClassifiesNotBusyAsIdleUnreportedRegardlessOfHerdrSpelling(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf, errBuf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, &errBuf)
	if buf.Len() != 0 {
		t.Fatalf("first tick printed output for newly seen task: %q", buf.String())
	}

	// Driving the status file to "done" is what an earlier version of this test did while expecting an
	// unconditional "done" event - itself the bug the fix corrects, since hand's pure-polling model
	// never satisfies that focus condition, so this is exactly an unexplained idle transition.
	setStatus(t, statusFile, "done")
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, &errBuf)
	if !strings.Contains(buf.String(), "idle-unreported task-1") {
		t.Fatalf("output = %q, want idle-unreported task-1: herdr's done, with nothing explaining the stop, is not task completion", buf.String())
	}
	if errBuf.Len() != 0 {
		t.Fatalf("errOut = %q, want actionable events on out only", errBuf.String())
	}

	// The task's own recorded report state stays empty rather than turning into
	// "done": it is derived from what the worker wrote, and an idle pane is an
	// observation about the harness, not a word the worker said.
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.LastReportState != "" {
		t.Fatalf("LastReportState = %q, want no report state invented from an unexplained herdr transition", task.LastReportState)
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

// The only remaining source of a verified-done record: a worker's own "done" report, cross-checked
// against a task the caller has already recorded as merged. herdr's agent_status never drives this by
// itself - see the test above.
func TestTickRecordsVerifiedDoneOnlyOnceReportedDoneIsVerified(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{
		ID: "task-1", Project: "nsr", Kind: state.KindShip,
		PR: "https://github.com/atqamz/hand/pull/1", MergeExecuted: true,
		Herdr: state.Herdr{PaneID: "p1"},
	})

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR https://github.com/atqamz/hand/pull/1 checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "done task-1:") {
		t.Fatalf("output = %q, want a verified done event", buf.String())
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !task.DoneVerified {
		t.Fatal("task.DoneVerified = false, want the verified done persisted")
	}
	if task.LastReportState != state.ReportDone {
		t.Fatalf("LastReportState = %q, want %q recorded", task.LastReportState, state.ReportDone)
	}
}

// The one event kind that used to break the section's membership rule: a
// blocked pane is something the watcher observed, not something the worker
// said, so it belongs nowhere near the operator's decision queue.
func TestTickClassifiesBlockedWithoutInventingAPendingDecision(t *testing.T) {
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

	setStatus(t, statusFile, "blocked")
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "blocked task-1:") {
		t.Fatalf("output = %q, want blocked task-1", buf.String())
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.LastReportState != "" {
		t.Fatalf("LastReportState = %q, want nothing inferred from a pane the worker never explained", task.LastReportState)
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

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.LastReportState != "" {
		t.Fatalf("LastReportState = %q, want the idle pane raised as an event and not as a decision the worker never asked for", task.LastReportState)
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
	registerProject(t, home, "nsr", "https://github.com/atqamz/hand.git")

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR https://github.com/atqamz/hand/pull/31 checks green\n"), 0o644); err != nil {
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
	if task.PR != "https://github.com/atqamz/hand/pull/31" {
		t.Fatalf("task.PR = %q, want the embedded URL auto-recorded", task.PR)
	}
}

func TestTickDoesNotOverwriteAlreadyRecordedPR(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "OPEN")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, PR: "https://github.com/atqamz/hand/pull/1", Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/hand.git")

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR https://github.com/atqamz/hand/pull/99 checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tick(ctx, cfg, client, states, &buf, io.Discard)

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "https://github.com/atqamz/hand/pull/1" {
		t.Fatalf("task.PR = %q, want the already-recorded PR left untouched", task.PR)
	}
}

// The guard against the worst outcome of trusting a worker's text: a PR URL from an unrelated repo
// becoming the task's PR, which `hand merge` would then merge for real.
func TestTickRefusesToAutoRecordAForeignRepoPR(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "OPEN")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/hand.git")

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
}

// Pins the slot ownership: one report line can both ask the supervisor something and carry a URL that
// fails to record, and the last-reported state/note is keyed by task ID, so a second writer touching
// it for the refused PR would erase the question from the surface a supervisor reads first.
func TestTickKeepsAWorkerQuestionWhenItsPRURLIsRefused(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "OPEN")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/hand.git")

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

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.LastReportState != state.ReportNeedsDecision || !strings.Contains(task.LastReportNote, "which base branch?") {
		t.Fatalf("LastReportState/Note = %q/%q, want the worker's own question intact", task.LastReportState, task.LastReportNote)
	}
}

// Covers the poll loop's no-blocking-lock rule at the auto-record site: another command holding the
// task lock across network work must not stall the watcher, so the tick reports the contention
// through the ordinary refusal path and moves on.
func TestTickSurfacesAContendedAutoRecordInsteadOfWaiting(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "OPEN")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/hand.git")

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf, errBuf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, &errBuf)

	url := "https://github.com/atqamz/hand/pull/7"
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

// Covers the race the non-blocking task lock introduced: `hand pr` holds that lock across its own gh
// round-trip while recording the very URL the watcher just read off the report. Announcing anything
// there is a false alarm naming a no-op remedy.
func TestTickStaysSilentWhenTheLockHolderRecordedTheSamePR(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/hand.git")

	url := "https://github.com/atqamz/hand/pull/7"
	// The holder's write has to land after tick's own state.List snapshot, or the pre-existing "task
	// already has a PR" guard absorbs the URL and the contention path under test is never reached -
	// which is what made an earlier version of this test vacuous.
	snapshot := taskSnapshotWithPR(t, home, "task-1", url)
	// Hence the gh double writes it: ValidatePR shells out to gh on the way to the lock, so the hook
	// fires inside exactly that window.
	writeFakeGhWithHook(t, "OPEN", fmt.Sprintf("cp %q %q", snapshot, store.Path(home)))

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

// A file a test can drop over the live database to stand in for a concurrent
// writer. The store runs in rollback journal mode, not WAL, so the one file is
// the whole database and copying it is the whole substitution.
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
	return store.Path(scratch)
}

// The other half of the contention path: with machine state unreadable, the
// operator must be told that, not sent to a hand status that opens the same
// database.
func TestTickReportsAnUnreadableTaskWhenTheLockIsContended(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/hand.git")

	url := "https://github.com/atqamz/hand/pull/7"
	corrupt := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("this is not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFakeGhWithHook(t, "OPEN", fmt.Sprintf("cp %q %q", corrupt, store.Path(home)))

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

// Pins the ordering the durable marker depends on. Reading task state at the instant the line hits
// stdout is what a restarted watcher would find had the process died there: the marker must still be
// unset, so the announcement is re-derivable; persisting first trades a duplicate for a lost event.
func TestTickAnnouncesPRMergedBeforePersistingIt(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "MERGED")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, PR: "https://github.com/atqamz/hand/pull/1", Herdr: state.Herdr{PaneID: "p1"}})

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

// Records what a task's durable state held at the moment each event line was written, keyed by the
// line.
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

// The other half of the durable marker: a merge only this watcher's gh poll ever saw, and the
// verified done that followed it, must not be re-emitted by the next process.
func TestTickDoesNotReannounceAPollObservedMergeAfterRestart(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "MERGED")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, PR: "https://github.com/atqamz/hand/pull/1", Herdr: state.Herdr{PaneID: "p1"}})

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

func TestTickReportsAnUnreadableReport(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	if err := os.MkdirAll(state.ReportPath(home, "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	states := make(map[string]*TaskState)
	var buf, errBuf bytes.Buffer
	tick(context.Background(), cfg, herdr.NewClient(), states, &buf, &errBuf)
	tick(context.Background(), cfg, herdr.NewClient(), states, &buf, &errBuf)

	if !strings.Contains(errBuf.String(), "tail report task-1 failed") {
		t.Fatalf("errOut = %q, want the unreadable report diagnosed, not silently treated as no report", errBuf.String())
	}
}

// Proves the offset survives the process: a fresh states map (a restarted hand watch) must not replay
// lines the previous run already surfaced, and must not forget the report explaining a quiet pane.
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

// A `done:` rewrite landing on the byte count of the `working:` line before it was skipped outright
// (atqamz/hand#149): the offset still sat just past the final newline with nothing after it, so
// nothing was announced, LastReportState stayed `working`, and ClassifyDeferredDone - gated on it - never ran.
func TestTickAnnouncesADoneRewrittenToTheSameLengthAcrossARestart(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	working := "working: gate green, merge in flight\n"
	done := "done: PR 149 merged and issue closed\n"
	if len(working) != len(done) {
		t.Fatalf("working report is %d bytes and done report %d, want the collision this test exists for", len(working), len(done))
	}

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(working), 0o644); err != nil {
		t.Fatal(err)
	}
	tick(ctx, cfg, client, states, &buf, io.Discard)

	// Restarting mid-test is the point of doing this at tick level: what makes the rewrite detectable has
	// to survive the process, exactly as the offset does.
	restarted := make(map[string]*TaskState)
	buf.Reset()
	tick(ctx, cfg, client, restarted, &buf, io.Discard)
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(done), 0o644); err != nil {
		t.Fatal(err)
	}
	tick(ctx, cfg, client, restarted, &buf, io.Discard)
	if !hasEventLine(buf.String(), "reported-done task-1: PR 149 merged and issue closed") {
		t.Fatalf("out = %q, want the same-length done rewrite announced", buf.String())
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.LastReportState != state.ReportDone {
		t.Fatalf("LastReportState = %q, want the done rewrite recorded so the deferred verification can fire", task.LastReportState)
	}

	task.MergeExecuted = true
	if err := state.Write(home, task); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, restarted, &buf, io.Discard)
	if !hasEventLine(buf.String(), "done task-1: PR 149 merged and issue closed") {
		t.Fatalf("out = %q, want ClassifyDeferredDone to announce the verified done once the merge landed", buf.String())
	}
}

func TestTickFiresParkedOnFirstResumedTickWhenTheSilenceAlreadyExceedsTheBound(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "idle")
	writeFakeHerdr(t, statusFile)

	// Created before the silence it is about to be blamed for: reportEvidenceTime
	// floors the mtime at the pane's start, so a task younger than its own report
	// file could not accumulate this silence in the first place.
	home := setupWatcherHome(t, state.Task{
		ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"},
		CreatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	})
	reportPath := state.ReportPath(home, "task-1")
	if err := os.WriteFile(reportPath, []byte("working: still on the migration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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

	// A restart's first tick only seeds tracking, so the earliest any classifier can
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

// A done worker's report file never grows again, so the silence instant parked fired against is
// frozen: a re-derived latch fires against that same instant on every restart, and state/events.log
// is capped, so the duplicates evict real history.
func TestTickDoesNotRefireParkedForADoneTaskAcrossARestart(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	started := time.Now().Add(-2 * time.Hour)
	home := setupWatcherHome(t, state.Task{
		ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"},
		CreatedAt:     started.UTC().Format(time.RFC3339),
		PaneStartedAt: started.UTC().Format(time.RFC3339),
	})
	reportPath := state.ReportPath(home, "task-1")
	if err := os.WriteFile(reportPath, []byte("done: shipped the migration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	silentSince := time.Now().Add(-time.Hour)
	if err := os.Chtimes(reportPath, silentSince, silentSince); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Home:           home,
		PollInterval:   time.Hour,
		StaleThreshold: time.Hour,
		ParkedBounds:   ParkedBounds{Paused: 3 * time.Hour, Done: 30 * time.Minute, Other: 3 * time.Hour},
	}
	client := herdr.NewClient()

	var buf bytes.Buffer
	states := make(map[string]*TaskState)
	tick(context.Background(), cfg, client, states, &buf, io.Discard)
	tick(context.Background(), cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "parked task-1") {
		t.Fatalf("output = %q, want parked task-1 from the first run: the done-tier bound is already crossed", buf.String())
	}

	buf.Reset()
	// The whole point of persisting the latch is this second run staying quiet.
	restarted := make(map[string]*TaskState)
	tick(context.Background(), cfg, client, restarted, &buf, io.Discard)
	tick(context.Background(), cfg, client, restarted, &buf, io.Discard)
	if strings.Contains(buf.String(), "parked task-1") {
		t.Fatalf("output = %q, want no second parked line: the report file has not grown, so this is the same silence the first run already announced", buf.String())
	}
}

// The two facts the floor has to keep apart. An outage restamps status_changed_at for a pane the
// watcher could not reach, which must not move the floor at all; a promote restamps pane_started_at,
// which must move it past the scout's whole silence. Reading either field for both jobs breaks one.
func TestReportEvidenceTimeFloorsOnThePaneStartNotTheOutageStamp(t *testing.T) {
	now := time.Now()
	home := t.TempDir()
	paneStart := now.Add(-3 * time.Hour)
	task := state.Task{
		ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"},
		CreatedAt:        now.Add(-4 * time.Hour).UTC().Format(time.RFC3339),
		PaneStartedAt:    paneStart.UTC().Format(time.RFC3339),
		StatusChangedAt:  now.Add(-time.Minute).UTC().Format(time.RFC3339),
		StatusChangedFor: string(herdr.StatusUnknown),
	}
	if err := state.Write(home, task); err != nil {
		t.Fatal(err)
	}
	reportPath := state.ReportPath(home, "task-1")
	if err := os.WriteFile(reportPath, []byte("working: still on the migration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	silentSince := now.Add(-2 * time.Hour)
	if err := os.Chtimes(reportPath, silentSince, silentSince); err != nil {
		t.Fatal(err)
	}

	got, err := reportEvidenceTime(home, task)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(silentSince.Truncate(time.Second)) && !got.Equal(silentSince) {
		t.Fatalf("evidence time = %s, want the report's own mtime %s: an outage stamp must not forget two hours of real silence", got, silentSince)
	}

	promoted := task
	promoted.PaneStartedAt = now.Add(-time.Minute).UTC().Format(time.RFC3339)
	got, err = reportEvidenceTime(home, promoted)
	if err != nil {
		t.Fatal(err)
	}
	if got.Before(now.Add(-2 * time.Minute)) {
		t.Fatalf("evidence time = %s, want the promotion instant: a pane a minute old cannot have been silent for two hours", got)
	}
}

func TestTickTiesTheStaleDwellToDurableEvidenceAcrossARestart(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := t.TempDir()
	dwelling := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	task := state.Task{
		ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"},
		CreatedAt: dwelling, StatusChangedAt: dwelling, StatusChangedFor: "working",
	}
	if err := state.Write(home, task); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: 20 * time.Minute}
	client := herdr.NewClient()
	ctx := context.Background()

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

func TestTickRefusesADurableDwellStampedForADifferentStatus(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := t.TempDir()
	dwelling := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	if err := state.Write(home, state.Task{
		ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"},
		CreatedAt: dwelling, StatusChangedAt: dwelling, StatusChangedFor: "blocked",
	}); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: 20 * time.Minute}
	client := herdr.NewClient()
	ctx := context.Background()

	states := make(map[string]*TaskState)
	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	tick(ctx, cfg, client, states, &buf, io.Discard)

	if strings.Contains(buf.String(), "stale task-1") {
		t.Fatalf("output = %q, want no stale: the 30m stamp was recorded for blocked, so it says nothing about how long working has been held", buf.String())
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusChangedFor != "working" || got.StatusChangedAt == dwelling {
		t.Fatalf("status_changed_at/for = %q/%q, want the dwell restamped for the status actually observed", got.StatusChangedAt, got.StatusChangedFor)
	}
}

// Covers the window between the two halves: the worker reports done, hand watch stops, and hand merge
// lands the work by writing merged. On restart the evidence is already on disk, so a marker re-derived
// from it would conclude the verified line went out and never print it.
func TestTickAnnouncesAVerifiedDoneAfterARestartThatMissedTheEvidence(t *testing.T) {
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

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !hasEventLine(buf.String(), "reported-done task-1: checks green") {
		t.Fatalf("out = %q, want an unverified reported-done while nothing has landed", buf.String())
	}

	// hand merge, with the watcher stopped: it writes merged, leaving the
	// verified announcement to whichever watcher restarts and rereads the evidence.
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

	task, err = state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	// Without the announcement the recorded state stays stuck where the unverified report left it.
	if task.LastReportState != state.ReportDone {
		t.Fatalf("LastReportState = %q, want the task's own recorded state moved to done", task.LastReportState)
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

// A promoted task keeps CreatedAt, so tick's identity check never fires and
// clearing the disk field in cmd/promote.go alone is not enough.
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

	// hand promote: same rewrite cmd/promote.go makes - kind and pane change,
	// CreatedAt and the report channel do not - with the DoneVerified reset this
	// test exists to cover.
	task.Kind = state.KindShip
	task.Herdr.PaneID = "p2"
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

// The ship's first probe reads the same "working" the scout last held, so no
// observed transition reseeds ChangedAt - which is why the forget rule cannot be
// conditioned on one.
func TestTickDropsTheCachedDwellWhenPromoteMovesTheTaskToANewPane(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := t.TempDir()
	dwelling := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	if err := state.Write(home, state.Task{
		ID: "task-1", Project: "nsr", Kind: state.KindScout, Herdr: state.Herdr{PaneID: "p1"},
		CreatedAt: dwelling, StatusChangedAt: dwelling, StatusChangedFor: "working",
	}); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: 20 * time.Minute}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)

	promoted, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	promoted.Kind = state.KindShip
	promoted.Herdr.PaneID = "p2"
	promoted.StatusChangedAt = time.Now().UTC().Format(time.RFC3339)
	promoted.StatusChangedFor = ""
	if err := state.Write(home, promoted); err != nil {
		t.Fatal(err)
	}

	// Moves report_offset, which is what makes the next tick write task state at all.
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("working: starting the ship run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if strings.Contains(buf.String(), "stale task-1") {
		t.Fatalf("out = %q, want no stale: the ship's dwell starts at the promotion, not at the scout's last observed transition", buf.String())
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusChangedAt == dwelling {
		t.Fatal("status_changed_at = the scout's stamp, want the cached scout dwell not written back over promote's restamp")
	}
	stamped, err := time.Parse(time.RFC3339, got.StatusChangedAt)
	if err != nil {
		t.Fatal(err)
	}
	restamped, err := time.Parse(time.RFC3339, promoted.StatusChangedAt)
	if err != nil {
		t.Fatal(err)
	}
	if stamped.Before(restamped) {
		t.Fatalf("status_changed_at = %q, want no earlier than promote's restamp %q", got.StatusChangedAt, promoted.StatusChangedAt)
	}
	if got.StatusChangedFor != "working" {
		t.Fatalf("status_changed_for = %q, want the status the ship's dwell was stamped for", got.StatusChangedFor)
	}
}

// The latches are what make each announcement fire only once, so any one of them
// surviving a promote silences that announcement for the ship's own pane.
func TestForgetPaneScopedCacheClearsEveryPaneAnchoredLatch(t *testing.T) {
	now := time.Now()
	scoutDwell := now.Add(-30 * time.Minute)
	ts := &TaskState{
		Status:                herdr.StatusWorking,
		Probed:                true,
		ChangedAt:             scoutDwell,
		PersistedChangedAt:    scoutDwell,
		PersistedChangedFor:   "working",
		PersistedPaneID:       "p1",
		Blocked:               true,
		Stale:                 true,
		DoneVerified:          true,
		PersistedDoneVerified: true,
		LastReportState:       state.ReportDone,
		LastReportNote:        "scout findings",
	}

	promoted := state.Task{
		ID: "task-1", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p2"},
		CreatedAt:       scoutDwell.UTC().Format(time.RFC3339),
		StatusChangedAt: now.UTC().Format(time.RFC3339),
	}
	forgetPaneScopedCache(ts, promoted, now)

	if ts.Stale || ts.Blocked {
		t.Fatalf("Stale = %v, Blocked = %v, want both latches cleared for the ship's new pane", ts.Stale, ts.Blocked)
	}
	if ts.DoneVerified || ts.PersistedDoneVerified {
		t.Fatal("DoneVerified survived, want the scout's verified done forgotten")
	}
	if ts.LastReportState != "" || ts.LastReportNote != "" {
		t.Fatalf("LastReportState/Note = %q/%q, want the scout's report evidence dropped", ts.LastReportState, ts.LastReportNote)
	}
	if ts.ChangedAt.Before(now) || !ts.ChangedAt.Equal(ts.PersistedChangedAt) {
		t.Fatalf("ChangedAt = %s, want a fresh dwell mirrored into PersistedChangedAt (%s)", ts.ChangedAt, ts.PersistedChangedAt)
	}
	if ts.PersistedChangedFor != "" {
		t.Fatalf("PersistedChangedFor = %q, want the disk value mirrored so the next write restamps it", ts.PersistedChangedFor)
	}
	if ts.Status != herdr.StatusUnknown {
		t.Fatalf("Status = %q, want the scout's status forgotten so the ship's first probe is a baseline", ts.Status)
	}
	if ts.Probed {
		t.Fatal("Probed = true, want the scout's probe forgotten so the ship's first probe is a first sighting")
	}
}

// The ship's first probe of its new pane is a first sighting, so it earns the same
// dwell a fresh spawn's does: a blink on the tick right after promote must announce
// nothing, and the outage must still be announced once it outlives the threshold.
func TestForgetPaneScopedCacheGivesTheShipsFirstProbeFailureADwell(t *testing.T) {
	now := time.Now()
	ts := &TaskState{
		Status:          herdr.StatusWorking,
		Probed:          true,
		ChangedAt:       now.Add(-30 * time.Minute),
		PersistedPaneID: "p1",
	}

	forgetPaneScopedCache(ts, promotedTask(now), now)

	if e := ClassifyStatus(ts, "task-1", "", errors.New("pane not found"), now); e != nil {
		t.Fatalf("event = %+v, want none: a blink on the ship's first probe is not a failure", e)
	}
	if e := ClassifyUnreachable(ts, "task-1", now.Add(time.Second), 5*time.Minute); e != nil {
		t.Fatalf("event = %+v, want none: the ship's outage has not outlived the threshold", e)
	}
	e := ClassifyUnreachable(ts, "task-1", now.Add(6*time.Minute), 5*time.Minute)
	if e == nil || e.Kind != KindFailed {
		t.Fatalf("event = %+v, want failed: the ship's pane stayed unreachable past the threshold", e)
	}
}

// A completeness guard, not a behavior test: TestForgetPaneScopedCacheClearsEveryPaneAnchoredLatch
// above asserts only the fields already known to need resetting, and would keep passing if a future
// field repeated the defect Status and then Probed both had - in TaskState, absent from the function.
func TestForgetPaneScopedCacheHandlesEveryField(t *testing.T) {
	before := TaskState{
		CreatedAt:               "created-marker",
		Status:                  herdr.Status("scout-status"),
		Probed:                  true,
		ChangedAt:               time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC),
		Blocked:                 true,
		Stale:                   true,
		PRMerged:                true,
		ReportCursor:            state.ReportCursor{Offset: 42, Digest: "consumed-digest"},
		PersistedCursor:         state.ReportCursor{Offset: 43, Digest: "persisted-digest"},
		PersistedPRMerged:       true,
		PersistedDoneVerified:   true,
		PersistedPaneID:         "scout-pane",
		PersistedChangedAt:      time.Date(2002, 2, 2, 0, 0, 0, 0, time.UTC),
		PersistedChangedFor:     "scout-status-for",
		LastReportState:         "scout-report-state",
		LastReportNote:          "scout-report-note",
		DoneVerified:            true,
		ParkedFiredFor:          time.Date(2003, 3, 3, 0, 0, 0, 0, time.UTC),
		PersistedParkedFiredFor: time.Date(2004, 4, 4, 0, 0, 0, 0, time.UTC),
		LimitRetryAt:            time.Date(2005, 5, 5, 0, 0, 0, 0, time.UTC),
		LimitAttempts:           3,
		PersistedLimitRetryAt:   time.Date(2006, 6, 6, 0, 0, 0, 0, time.UTC),
		PersistedLimitAttempts:  4,
		LimitProbed:             true,
		UnreachableFired:        true,
	}
	promoted := state.Task{
		Herdr:            state.Herdr{PaneID: "ship-pane"},
		DoneVerified:     false,
		CreatedAt:        "2020-01-01T00:00:00Z",
		StatusChangedFor: "",
		LastReportState:  "ship-report-state",
		LastReportNote:   "ship-report-note",
	}

	// Every field the PR body's field-by-field table classifies as pane-independent, so
	// forgetPaneScopedCache is right to leave it alone: identity, PR facts, report-file position, and
	// the parked latch, keyed to the report mtime not the pane. Persisted* entries mirror those facts.
	carried := map[string]bool{
		"CreatedAt":               true,
		"PRMerged":                true,
		"ReportCursor":            true,
		"PersistedCursor":         true,
		"PersistedPRMerged":       true,
		"ParkedFiredFor":          true,
		"PersistedParkedFiredFor": true,
	}

	ts := before
	forgetPaneScopedCache(&ts, promoted, time.Now())

	// Walking every field by reflection: one neither reset/re-derived nor named in the carried map fails
	// automatically by staying equal to its deliberately-stale "before" value. Which of the two fixes it
	// gets does not matter here, only that the decision was made on purpose.
	beforeVal, afterVal := reflect.ValueOf(before), reflect.ValueOf(ts)
	typ := beforeVal.Type()
	seen := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		seen[name] = true
		changed := !reflect.DeepEqual(beforeVal.Field(i).Interface(), afterVal.Field(i).Interface())
		switch {
		case carried[name] && changed:
			t.Errorf("%s: carried field changed from %v to %v, want it left alone", name, beforeVal.Field(i).Interface(), afterVal.Field(i).Interface())
		case !carried[name] && !changed:
			t.Errorf("%s: pane-scoped field left at its stale value %v, want forgetPaneScopedCache to reset or re-derive it (or add it to the carried map with a reason)", name, beforeVal.Field(i).Interface())
		}
	}
	for name := range carried {
		if !seen[name] {
			t.Errorf("carried map names %q, which is not a field of TaskState - fix the map", name)
		}
	}
}

func promotedTask(now time.Time) state.Task {
	return state.Task{
		ID: "task-1", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p2"},
		CreatedAt:       now.Add(-30 * time.Minute).UTC().Format(time.RFC3339),
		StatusChangedAt: now.UTC().Format(time.RFC3339),
	}
}

// The scout's status is the baseline the ship's first probe would be diffed
// against, so carrying it invents a transition the ship never made.
func TestForgetPaneScopedCacheStopsIdleUnreportedForAStatusTheShipNeverHeld(t *testing.T) {
	now := time.Now()
	ts := &TaskState{
		Status:          herdr.StatusWorking,
		Probed:          true,
		ChangedAt:       now.Add(-30 * time.Minute),
		PersistedPaneID: "p1",
		LastReportState: state.ReportDone,
		LastReportNote:  "scout findings",
	}

	forgetPaneScopedCache(ts, promotedTask(now), now)

	if e := ClassifyStatus(ts, "task-1", herdr.StatusDone, nil, now.Add(time.Second)); e != nil {
		t.Fatalf("event = %+v, want none: the ship was never observed working, so its first probe cannot be an unexplained stop", e)
	}
}

// The mirror image: the ship's own blocked is suppressed when the scout happened
// to be blocked too, because ClassifyStatus short-circuits on the stale equality.
func TestForgetPaneScopedCacheLetsTheShipsOwnBlockedFireAfterABlockedScout(t *testing.T) {
	now := time.Now()
	ts := &TaskState{
		Status:          herdr.StatusBlocked,
		Probed:          true,
		Blocked:         true,
		ChangedAt:       now.Add(-30 * time.Minute),
		PersistedPaneID: "p1",
	}

	forgetPaneScopedCache(ts, promotedTask(now), now)

	e := ClassifyStatus(ts, "task-1", herdr.StatusBlocked, nil, now.Add(time.Second))
	if e == nil || e.Kind != KindBlocked {
		t.Fatalf("event = %+v, want blocked: the ship's pane raised its own question", e)
	}
}

// A promote landing after this tick's state.List but before the write-back is the
// one window tick's own forget rules structurally cannot see.
func TestSyncTaskStateDropsCachePromoteInvalidatedMidTick(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	scoutDwell := now.Add(-30 * time.Minute)

	ts := &TaskState{
		Status:                herdr.StatusWorking,
		Probed:                true,
		ChangedAt:             scoutDwell,
		PersistedChangedAt:    scoutDwell,
		PersistedChangedFor:   "working",
		PersistedPaneID:       "p1",
		Stale:                 true,
		DoneVerified:          true,
		PersistedDoneVerified: true,
		LastReportState:       state.ReportDone,
		LastReportNote:        "scout findings",
		ReportCursor:          state.ReportCursor{Offset: 42, Digest: "cached-digest"},
	}

	if err := state.Write(home, state.Task{
		ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p2"},
		CreatedAt:       scoutDwell.UTC().Format(time.RFC3339),
		StatusChangedAt: now.UTC().Format(time.RFC3339),
		ReportOffset:    42,
	}); err != nil {
		t.Fatal(err)
	}

	var errBuf bytes.Buffer
	syncTaskState(home, "task-1", ts, now, &errBuf)
	if errBuf.Len() != 0 {
		t.Fatalf("errOut = %q, want a clean write", errBuf.String())
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusChangedAt == scoutDwell.UTC().Format(time.RFC3339) {
		t.Fatal("status_changed_at = the scout's stamp, want promote's restamp not overwritten")
	}
	if got.StatusChangedFor != string(herdr.StatusUnknown) {
		t.Fatalf("status_changed_for = %q, want the restamped dwell to belong to no observed status: this tick probed the scout's pane", got.StatusChangedFor)
	}
	if got.DoneVerified {
		t.Fatal("done_verified = true, want the scout's marker not resurrected by the cached copy")
	}
	if got.LastReportState != "" || got.LastReportNote != "" {
		t.Fatalf("last_report_state/note = %q/%q, want the scout's report evidence not written back", got.LastReportState, got.LastReportNote)
	}
	if ts.Stale {
		t.Fatal("ts.Stale = true, want the cached stale latch cleared for the ship's pane")
	}
}

// Matches want as a whole output line, so "reported-done <id>" and "done <id>" - one a substring of
// the other - cannot be confused.
func hasEventLine(out, want string) bool {
	for _, line := range strings.Split(out, "\n") {
		if line == want {
			return true
		}
	}
	return false
}

// Pins the one-line-per-event invariant against its noisiest real cause: ghutil wraps gh's stderr
// into the error verbatim, and gh emits several lines for auth and network failures. A multi-line
// Event.Text breaks the stdout contract and makes events.log's 200-line bound count one as several.
func TestTickKeepsAMultiLineAutoRecordFailureOnOneLine(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGhFailingMultiline(t)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/hand.git")

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf, errBuf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, &errBuf)

	url := "https://github.com/atqamz/hand/pull/7"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: "+url+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, &errBuf)

	// The same report line also emits reported-done, so the invariant under test is per event: the
	// failure occupies exactly one line, keeping the whole cause and losing only its line breaks, with
	// no fragment of it on a line of its own.
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
}

// Mirrors the real gh's noisiest failure: auth and network errors exit non-zero having written
// several lines to stderr, which ghutil.PRIsMerged wraps into the returned error verbatim. Nothing is
// written to stdout, as with the real tool on this path.
func writeFakeGhFailingMultiline(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a POSIX shell script, not supported on windows")
	}
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
	handleEvent(Config{Home: home}, &Event{Kind: KindReportDone, Verified: true, TaskID: "task-1", Text: "done task-1"}, &buf, &errBuf)

	if buf.String() != "done task-1\n" {
		t.Fatalf("out = %q, want the event text only", buf.String())
	}
	if !strings.Contains(errBuf.String(), "watch: append events.log failed") {
		t.Fatalf("errOut = %q, want append events.log failed diagnostic", errBuf.String())
	}
}

func TestHandleEventRunsConfigNotifyInProcessForEveryNotifiableKind(t *testing.T) {
	for _, e := range []Event{
		{Kind: KindBlocked, TaskID: "task-1", Text: "blocked task-1: agent needs help"},
		{Kind: KindReportBlocked, TaskID: "task-1", Text: "report-blocked task-1: waiting on credentials"},
		{Kind: KindFailed, TaskID: "task-1", Text: "failed task-1"},
		{Kind: KindReportFailed, TaskID: "task-1", Text: "report-failed task-1: build broke"},
		{Kind: KindReportNeedsDecision, TaskID: "task-1", Text: "needs-decision task-1: which API?"},
		{Kind: KindReportDone, TaskID: "task-1", Text: "done task-1"},
	} {
		t.Run(e.Kind, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(home, "marker.txt")
			template := notifyMessageTemplate(marker)
			if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte(template), 0o644); err != nil {
				t.Fatal(err)
			}

			var buf, errBuf bytes.Buffer
			handleEvent(Config{Home: home}, &e, &buf, &errBuf)

			got, err := os.ReadFile(marker)
			if err != nil {
				t.Fatalf("config/notify was not run in-process for a %s event: %v", e.Kind, err)
			}
			if string(got) != e.Text {
				t.Fatalf("marker content = %q, want the event text %q", got, e.Text)
			}
			if errBuf.Len() != 0 {
				t.Fatalf("errOut = %q, want no diagnostics for a successful notify", errBuf.String())
			}
		})
	}
}

func TestHandleEventSkipsConfigNotifyForANonNotifiableKind(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "marker.txt")
	template := notifyMessageTemplate(marker)
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf, errBuf bytes.Buffer
	handleEvent(Config{Home: home}, &Event{Kind: KindStale, TaskID: "task-1", Text: "stale task-1"}, &buf, &errBuf)

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("config/notify ran for a stale event, want it skipped: err=%v", err)
	}
}

func TestHandleEventStaysSilentWhenNotifyIsUnconfigured(t *testing.T) {
	home := t.TempDir()

	var buf, errBuf bytes.Buffer
	handleEvent(Config{Home: home}, &Event{Kind: KindBlocked, TaskID: "task-1", Text: "blocked task-1: agent needs help"}, &buf, &errBuf)

	if errBuf.Len() != 0 {
		t.Fatalf("errOut = %q, want no diagnostic when config/notify is simply absent", errBuf.String())
	}
}

func TestHandleEventReportsAFailingNotifyTemplateToErrOut(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte("exit 1"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf, errBuf bytes.Buffer
	handleEvent(Config{Home: home}, &Event{Kind: KindBlocked, TaskID: "task-1", Text: "blocked task-1: agent needs help"}, &buf, &errBuf)

	if buf.String() != "blocked task-1: agent needs help\n" {
		t.Fatalf("out = %q, want the event text unaffected by a failing notify", buf.String())
	}
	if !strings.Contains(errBuf.String(), "watch: notify failed") {
		t.Fatalf("errOut = %q, want a notify failed diagnostic", errBuf.String())
	}
}

func TestRunFailsWhenHerdrUnreachable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake herdr is a POSIX shell script, not supported on windows")
	}
	// exit 1 with empty stdout is the faithful crashed-or-missing-binary shape, which call()'s
	// empty-stdout-plus-runErr branch handles. A distinct shape from herdr's ordinary failure (exit 0
	// plus an error envelope), and only this one means "unreachable".
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

// The regression test for the delivery failure of 2026-07-28: the grep-on-first-line wrapper this
// mode replaces matched a done worker's startup line, took it for a transition, and left the two real
// events that followed unread for three hours.
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

	// Three probes: the arm-time one plus both baseline ticks. Only after them is a
	// status change a transition rather than a different baseline.
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

// Covers atqamz/hand#85: a caller that only wants to wake on blocked must not be woken by a
// routine idle-unreported transition, but the filtered-out event still has to reach events.log exactly
// like a baseline tick's events already do - the filter gates the wake, not the record.
func TestRunUntilEventFiltersWakesToTheRequestedKinds(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	callLog := logPaneGets(t)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{
		Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 10 * time.Second,
		EventFilter: NewEventFilter([]string{KindBlocked}),
	}

	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- RunUntilEvent(context.Background(), cfg, &out, io.Discard) }()

	waitForPaneGets(t, callLog, 3)
	setStatus(t, statusFile, "idle")
	waitForPaneGets(t, callLog, 6)
	if out.Len() != 0 {
		t.Fatalf("out = %q, want nothing yet: idle-unreported is not in the filter", out.String())
	}
	setStatus(t, statusFile, "blocked")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunUntilEvent = %v, want nil so the exit code reads as a delivered event", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunUntilEvent did not return after the filtered-in transition")
	}
	if !strings.Contains(out.String(), "blocked task-1") {
		t.Fatalf("out = %q, want blocked task-1", out.String())
	}
	if strings.Contains(out.String(), "idle-unreported task-1") {
		t.Fatalf("out = %q, want idle-unreported excluded: it is not in the filter", out.String())
	}

	logData, err := os.ReadFile(filepath.Join(home, "state", "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "idle-unreported task-1") {
		t.Fatalf("events.log = %q, want the filtered-out event still recorded", string(logData))
	}
}

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
		if !errors.Is(err, ErrNoEvent) {
			t.Fatalf("RunUntilEvent = %v, want ErrNoEvent", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunUntilEvent did not return after context cancellation")
	}
}

func TestRunUntilEventFailsWhenHerdrUnreachable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake herdr is a POSIX shell script, not supported on windows")
	}
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

func TestRunUntilEventReportsNoEventWhenConnectHangs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake herdr is a POSIX shell script, not supported on windows")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nsleep 5\nprintf '{\"id\":\"cli:1\",\"result\":{\"workspaces\":[]}}'\n"
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := Config{Home: t.TempDir(), PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 100 * time.Millisecond}
	start := time.Now()
	err := RunUntilEvent(context.Background(), cfg, &bytes.Buffer{}, io.Discard)

	if !errors.Is(err, ErrNoEvent) {
		t.Fatalf("RunUntilEvent = %v, want ErrNoEvent: the timeout is exit 4 wherever in arming it elapses", err)
	}
	if errors.Is(err, ErrArmFailed) {
		t.Fatal("a hung connect reported ErrArmFailed, whose exit promises the name of the task that could not be reached")
	}
	if !strings.Contains(err.Error(), "herdr") {
		t.Fatalf("err = %v, want it to say the connection was what timed out, not a task probe", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("returned after %s, want --timeout to bound a hung connect probe", elapsed)
	}
}

func TestRunUntilEventReportsNoEventWhenTheArmProbeHangs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake herdr is a POSIX shell script, not supported on windows")
	}
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
"workspace list")
	printf '{"id":"cli:1","result":{"workspaces":[]}}'
	;;
"pane get")
	sleep 5
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"p1","agent_status":"working"}}}'
	;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 100 * time.Millisecond}

	start := time.Now()
	err := RunUntilEvent(context.Background(), cfg, &bytes.Buffer{}, io.Discard)

	if !errors.Is(err, ErrNoEvent) {
		t.Fatalf("RunUntilEvent = %v, want ErrNoEvent: the timeout passed with nothing delivered, and no single task can be named as the cause", err)
	}
	if errors.Is(err, ErrArmFailed) {
		t.Fatal("a hung arm probe reported ErrArmFailed, whose exit promises the name of the task that could not be reached")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("returned after %s, want --timeout to bound a hung pane probe", elapsed)
	}
}

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

// Holds resume to what the live path already does: a free-text line appended after a real report
// explains nothing, so it must not erase the report it follows. Reading it back as "never reported"
// turns the next quiet pane into idle-unreported, replacing the explanation with a bare stop.
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

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.LastReportState != state.ReportNeedsDecision || !strings.Contains(task.LastReportNote, "which base branch?") {
		t.Fatalf("LastReportState/Note = %q/%q, want the worker's own question left intact", task.LastReportState, task.LastReportNote)
	}
}

// Keys tracking on identity rather than on ID. A teardown and respawn between two ticks is a
// different task, and inheriting the previous run's TaskState suppresses the new one's verified done
// for good: syncTaskState writes that inherited done_verified onto the fresh JSON.
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
	// Same hazard as a surviving report channel, one layer in, so the scout's report.md goes with the
	// state row.
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

// Covers the well-behaved worker: it says why it stopped, herdr's not-busy transition is then absorbed
// on purpose, and the recorded state would otherwise keep reading "working" - the very bug the report
// channel exists to remove, with the supervisor reading that state first.
func TestTickSetsTheStateColumnOnAReportedStop(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()
	tick(ctx, cfg, client, states, &bytes.Buffer{}, io.Discard)

	report := ""
	for _, tc := range []struct{ line, wantState string }{
		{"paused: waiting on the nightly build\n", state.ReportPaused},
		{"blocked: needs an API key\n", state.ReportBlocked},
		{"needs-decision: which base branch?\n", state.ReportNeedsDecision},
		{"paused: sleeping on it\n", state.ReportPaused},
		// The way back, the steer-and-continue loop: nothing else in the codebase writes "working" to that
		// state, so without report-working the task latches on the stop-state and a steered worker shows
		// as awaiting a decision forever - the same two-views-disagree defect, inverted.
		{"working: main, carrying on\n", state.ReportWorking},
	} {
		report += tc.line
		if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(report), 0o644); err != nil {
			t.Fatal(err)
		}
		tick(ctx, cfg, client, states, &bytes.Buffer{}, io.Discard)

		task, err := state.Read(home, "task-1")
		if err != nil {
			t.Fatal(err)
		}
		if task.LastReportState != tc.wantState {
			t.Fatalf("LastReportState = %q after %q, want state %s", task.LastReportState, tc.line, tc.wantState)
		}
	}
}

// Covers atqamz/hand#81's hard part: a task whose very first sighting finds its pane unreachable
// must not be dropped (the old !tracked branch's bare continue), but a probe failure that clears before
// the dwell matures - a blink - must produce nothing at all.
func TestTickStaysSilentOnABlinkAtFirstSighting(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, paneGoneStatus)
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if _, tracked := states["task-1"]; !tracked {
		t.Fatal("task-1 was not tracked after a probe failure at first sighting")
	}
	if buf.Len() != 0 {
		t.Fatalf("output = %q, want nothing on the seeding tick", buf.String())
	}

	setStatus(t, statusFile, "working")
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if buf.Len() != 0 {
		t.Fatalf("output = %q, want nothing: the pane recovered before the hour-long dwell matured", buf.String())
	}
}

// The other half: a pane that stays dark must produce exactly one failed event, not one per tick, and
// not never.
func TestTickAnnouncesATaskUnreachableAtFirstSightingOnceTheDwellMatures(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, paneGoneStatus)
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: 20 * time.Minute}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if buf.Len() != 0 {
		t.Fatalf("output = %q, want nothing on the seeding tick", buf.String())
	}

	ts := states["task-1"]
	ts.ChangedAt = ts.ChangedAt.Add(-30 * time.Minute)

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "failed task-1") {
		t.Fatalf("output = %q, want failed task-1 once the outage outlasts the stale threshold", buf.String())
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if strings.Contains(buf.String(), "failed task-1") {
		t.Fatalf("output = %q, want no duplicate failed event for the same outage", buf.String())
	}
}

// Ties the outage clock to durable evidence the same way stale and parked already do: a restart
// mid-outage must not reset the dwell to zero, or a long-dark task would silently buy itself a fresh
// grace period every time the watcher restarts.
func TestTickResumesAnUnreachableDwellAcrossARestart(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, paneGoneStatus)
	writeFakeHerdr(t, statusFile)

	dwelling := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	home := setupWatcherHome(t, state.Task{
		ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"},
		CreatedAt: dwelling, StatusChangedAt: dwelling, StatusChangedFor: "unknown",
	})

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: 20 * time.Minute}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if strings.Contains(buf.String(), "failed task-1") {
		t.Fatal("failed fired on the seeding tick, before resume had even read durable state")
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "failed task-1") {
		t.Fatalf("output = %q, want failed task-1 on the first classifying tick after resume: the outage predates this process and must not need to reaccumulate from resume time", buf.String())
	}
}

// A pane hand cannot probe says nothing about a question the worker already asked, and clearing it
// would be unrecoverable: the report line is already past report_offset, and the recovery tick emits
// no event because the tracked status never changed.
func TestTickKeepsAPendingQuestionWhenThePaneProbeFails(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, Herdr: state.Herdr{PaneID: "p1"}})

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()
	tick(ctx, cfg, client, states, &bytes.Buffer{}, io.Discard)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("needs-decision: which base branch?\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tick(ctx, cfg, client, states, &bytes.Buffer{}, io.Discard)

	// ClassifyStatus fires failed on any probe error, so a herdr daemon restart would otherwise wipe
	// every tracked task's last-reported state in one tick - fleet-wide loss out of a transient blip.
	setStatus(t, statusFile, paneGoneStatus)
	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "failed task-1") {
		t.Fatalf("output = %q, want the failed event", buf.String())
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.LastReportState != state.ReportNeedsDecision || !strings.Contains(task.LastReportNote, "which base branch?") {
		t.Fatalf("LastReportState/Note = %q/%q, want the worker's question left standing", task.LastReportState, task.LastReportNote)
	}
}
