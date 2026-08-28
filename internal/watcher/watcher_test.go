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
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/orientation"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/shellquote"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/store"
)

func TestEnsureBoundedArmReleasesOwnershipAndDoesNotClaimLiveWatcher(t *testing.T) {
	home := t.TempDir()
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.FleetID(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := Ensure(context.Background(), Config{Home: home}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != orientation.MonitorStateRearmed || result.Live {
		t.Fatalf("result = %#v, want bounded rearm without a live watcher", result)
	}
	attached, err := IsAttached(home)
	if err != nil {
		t.Fatal(err)
	}
	if attached {
		t.Fatal("bounded arm left watcher ownership attached")
	}
}

func TestEnsureReportsAlreadyArmedWithoutTakingOverCurrentOwner(t *testing.T) {
	home := t.TempDir()
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	owner, err := Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Release()

	target := orientation.TargetFor("f_unused", orientation.TargetEvidence{ID: "task-1", Kind: "task", Generation: []string{"one"}})
	result, err := Ensure(context.Background(), Config{Home: home}, []TargetBinding{{TaskID: "task-1", Target: target}})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != orientation.MonitorStateAlreadyArmed || !result.Live {
		t.Fatalf("result = %#v, want already-armed live ownership", result)
	}
	if got := owner.Generation(); got == "" {
		t.Fatal("current owner generation was cleared by idempotent ensure")
	}
}

func TestEnsureUsesAQuietBoundedArmWithoutAZeroTicker(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	fleetID, err := state.FleetIDReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	target := orientation.TaskTarget(fleetID, orientation.TaskTargetFacts{ID: "task-1", Kind: "task", Lifecycle: string(state.TaskOpen), RuntimeIdentity: []string{"", "", "", "p1"}})

	result, err := Ensure(context.Background(), Config{Home: home}, []TargetBinding{{TaskID: "task-1", Target: target}})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != orientation.MonitorStateRearmed {
		t.Fatalf("result = %#v, want quiet bounded arm to rearm", result)
	}
}

func TestEnsureObserveOnlyArmDoesNotAutoRecordWorkerPRClaim(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: https://github.com/owner/repo/pull/7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "notify")
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte("printf '%s' \"$HAND_MESSAGE\" > "+shellquote.Quote(marker)), 0o644); err != nil {
		t.Fatal(err)
	}
	fleetID, err := state.FleetIDReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	target := orientation.TaskTarget(fleetID, orientation.TaskTargetFacts{ID: "task-1", Kind: "task", Lifecycle: string(state.TaskOpen), RuntimeIdentity: []string{"", "", "", "p1"}})

	if _, err := Ensure(context.Background(), Config{Home: home}, []TargetBinding{{TaskID: "task-1", Target: target}}); err != nil {
		t.Fatal(err)
	}
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "" {
		t.Fatalf("task PR = %q, want bounded session arm to leave task metadata unchanged", task.PR)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("notify marker stat error = %v, want observe-only arm to suppress notifications", err)
	}
}

func TestFilterHistoriesHonorsExplicitMonitorTargets(t *testing.T) {
	histories := []state.TaskHistory{
		{Task: state.Task{ID: "task-1"}},
		{Task: state.Task{ID: "task-2"}},
	}
	targets := []TargetBinding{{TaskID: "task-2"}}
	filtered := filterHistories(histories, targets)
	if len(filtered) != 1 || filtered[0].Task.ID != "task-2" {
		t.Fatalf("filtered histories = %#v, want only task-2", filtered)
	}
}

func TestObserveOnlySyncPreservesUsageLimitObservation(t *testing.T) {
	retryAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{
		Lifecycle:          state.AttemptRunning,
		UsageLimitRetryAt:  retryAt,
		UsageLimitAttempts: 2,
		UsageLimitEpisode:  3,
	})
	task, attempt := readTaskAttempt(t, home, "task-1")
	ts := resumeTaskState(task, attempt, herdr.StatusWorking, time.Now())
	ts.LimitRetryAt = time.Time{}
	ts.LimitAttempts = 0
	ts.LimitEpisode = 0
	ts.ReportCursor = state.ReportCursor{Offset: 1, Digest: "observed"}

	var errBuf bytes.Buffer
	syncTaskState(home, task.ID, ts, time.Now(), &errBuf, true)
	if errBuf.Len() != 0 {
		t.Fatalf("sync errOut = %q", errBuf.String())
	}
	_, after := readTaskAttempt(t, home, task.ID)
	if after.UsageLimitRetryAt != retryAt || after.UsageLimitAttempts != 2 || after.UsageLimitEpisode != 3 {
		t.Fatalf("usage-limit observation = %q/%d/%d, want observe-only sync to preserve it", after.UsageLimitRetryAt, after.UsageLimitAttempts, after.UsageLimitEpisode)
	}
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
	bin := faketool.Bin(t)
	// The unexpected-args arm deliberately diverges - a bare stderr line and exit 1 - so a call shape
	// no test anticipated fails loudly instead of parsing. "pane get" reads its status from statusFile,
	// so a test can drive transitions between ticks.
	callLog := filepath.Join(t.TempDir(), "pane-get-calls")
	t.Setenv("HERDR_CALL_LOG", callLog)
	faketool.Herdr{
		Workspaces:     []faketool.HerdrWorkspace{{ID: "wA", Label: "watch", Tabs: []faketool.HerdrTab{{ID: "wA:tA", Label: "task-1", Pane: "p1"}}}},
		PaneStatusFile: statusFile,
		PaneAgentEnv:   true, PaneReadFileEnv: true, KeyLogEnv: true, TextLogEnv: true,
		ReadLogEnv: true, AllowUnknownPane: true,
		Responses: []faketool.HerdrResponse{{Command: "workspace list", Stdout: `{"id":"cli:1","result":{"workspaces":[]}}`}},
		Log:       callLog, LogCommands: []string{"pane get"},
	}.Install(t, bin)
}

func writeFakeHerdrForPanes(t *testing.T, panes ...string) {
	t.Helper()
	tabs := make([]faketool.HerdrTab, 0, len(panes))
	for i, pane := range panes {
		tabs = append(tabs, faketool.HerdrTab{ID: fmt.Sprintf("wA:t%d", i+1), Label: fmt.Sprintf("task-%d", i+1), Pane: pane})
	}
	callLog := filepath.Join(t.TempDir(), "pane-get-calls")
	t.Setenv("HERDR_CALL_LOG", callLog)
	faketool.Herdr{
		Workspaces:       []faketool.HerdrWorkspace{{ID: "wA", Label: "watch", Tabs: tabs}},
		PaneStatus:       "working",
		AllowUnknownPane: false,
		Log:              callLog,
		LogCommands:      []string{"pane get"},
	}.Install(t, faketool.Bin(t))
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
	if hook != "" {
		t.Fatalf("shell hook is not supported by the portable fake: %q", hook)
	}
	faketool.GH{Responses: []faketool.GHResponse{{Command: "pr view", Stdout: `{"state":"` + prState + `"}`, Stderr: "Warning: gh version is out of date\n"}}}.Install(t, faketool.Bin(t))
}

func writeFakeGhWithCopy(t *testing.T, prState, source, dest string) {
	t.Helper()
	faketool.GH{Responses: []faketool.GHResponse{{
		Command: "pr view", Stdout: `{"state":"` + prState + `"}`, Stderr: "Warning: gh version is out of date\n",
		Copy: &faketool.GHCopy{Source: source, Dest: dest},
	}}}.Install(t, faketool.Bin(t))
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
	return os.Getenv("HERDR_CALL_LOG")
}

func waitForPaneGets(t *testing.T, callLog string, want int) {
	waitForHerdrCalls(t, callLog, "pane get", want)
}

func waitForHerdrCalls(t *testing.T, callLog, command string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	needle := []byte(" " + command)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(callLog)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		calls := 0
		for _, line := range bytes.Split(data, []byte("\n")) {
			if bytes.Contains(line, needle) {
				calls++
			}
		}
		if calls >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d %s calls", want, command)
}

func writeTaskAttempt(t *testing.T, home string, task state.Task, attempt state.Attempt) error {
	t.Helper()
	if err := state.CreateTask(home, task); err != nil {
		return err
	}
	attempt.TaskID = task.ID
	if _, err := state.CreateAttempt(home, attempt); err != nil {
		return err
	}
	return nil
}

func readTaskAttempt(t *testing.T, home, id string) (state.Task, state.Attempt) {
	t.Helper()
	history, err := state.ReadHistory(home, id)
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil {
		t.Fatalf("task %q has no active attempt", id)
	}
	return history.Task, *history.ActiveAttempt
}

func setupWatcherHome(t *testing.T, taskOpts state.Task, attempts ...state.Attempt) (home string) {
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
	attempt := state.Attempt{Lifecycle: state.AttemptRunning}
	if len(attempts) != 0 {
		attempt = attempts[0]
	}
	if err := writeTaskAttempt(t, home, taskOpts, attempt); err != nil {
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

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
	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.LastReportState != "" {
		t.Fatalf("LastReportState = %q, want no report state invented from an unexplained herdr transition", attempt.LastReportState)
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

func TestTickSuppressesStaleForTerminalAndDeliveredTasksUsingCurrentState(t *testing.T) {
	writeFakeHerdrForPanes(t, "p1", "p2", "p3", "p4")
	old := "2020-01-02T03:04:05Z"

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	tasks := []struct {
		task    state.Task
		attempt state.Attempt
	}{
		{
			task: state.Task{ID: "terminal-task", Kind: state.KindShip, CreatedAt: old},
			attempt: state.Attempt{
				Lifecycle:        state.AttemptRunning,
				Herdr:            state.Herdr{PaneID: "p1"},
				StatusChangedAt:  old,
				StatusChangedFor: string(herdr.StatusWorking),
				LastReportState:  state.ReportDone,
			},
		},
		{
			task: state.Task{ID: "startup-delivered-task", Kind: state.KindShip, CreatedAt: old, DeliveredAt: old, DeliveredReason: "handed off"},
			attempt: state.Attempt{
				Lifecycle:        state.AttemptRunning,
				Herdr:            state.Herdr{PaneID: "p2"},
				StatusChangedAt:  old,
				StatusChangedFor: string(herdr.StatusWorking),
				LastReportState:  state.ReportWorking,
			},
		},
		{
			task: state.Task{ID: "live-delivery-task", Kind: state.KindShip, CreatedAt: old},
			attempt: state.Attempt{
				Lifecycle:        state.AttemptRunning,
				Herdr:            state.Herdr{PaneID: "p3"},
				StatusChangedAt:  old,
				StatusChangedFor: string(herdr.StatusWorking),
				LastReportState:  state.ReportWorking,
			},
		},
		{
			task: state.Task{ID: "live-task", Kind: state.KindShip, CreatedAt: old},
			attempt: state.Attempt{
				Lifecycle:        state.AttemptRunning,
				Herdr:            state.Herdr{PaneID: "p4"},
				StatusChangedAt:  old,
				StatusChangedFor: string(herdr.StatusWorking),
			},
		},
	}
	for _, tc := range tasks {
		if err := writeTaskAttempt(t, home, tc.task, tc.attempt); err != nil {
			t.Fatal(err)
		}
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Minute}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()
	var out bytes.Buffer
	tick(ctx, cfg, client, states, &out, io.Discard)

	if err := state.SetTaskDelivery(home, "live-delivery-task", old, "handed off after watch started"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	tick(ctx, cfg, client, states, &out, io.Discard)

	if got := out.String(); got != "stale live-task\n" {
		t.Fatalf("output = %q, want only the genuinely live task's stale event", got)
	}
	logData, err := os.ReadFile(filepath.Join(state.Dir(home), "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"terminal-task", "startup-delivered-task", "live-delivery-task"} {
		if strings.Contains(string(logData), "stale "+id) {
			t.Fatalf("events.log = %q, contains false stale event for %s", string(logData), id)
		}
	}
}

func TestTickProcessesTerminalReportBeforeStale(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	old := "2020-01-02T03:04:05Z"

	home := setupWatcherHome(t, state.Task{ID: "task-1", Kind: state.KindShip, CreatedAt: old}, state.Attempt{
		Lifecycle:        state.AttemptRunning,
		Herdr:            state.Herdr{PaneID: "p1"},
		StatusChangedAt:  old,
		StatusChangedFor: string(herdr.StatusWorking),
	})
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Minute}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()
	var out bytes.Buffer
	tick(ctx, cfg, client, states, &out, io.Discard)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: finished\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	tick(ctx, cfg, client, states, &out, io.Discard)

	if !strings.Contains(out.String(), "reported-done task-1: finished") {
		t.Fatalf("output = %q, want the terminal report event", out.String())
	}
	if strings.Contains(out.String(), "stale task-1") {
		t.Fatalf("output = %q, want same-tick terminal report to suppress stale", out.String())
	}
}

func TestTickKeepsIdleUnreportedFactualAfterDelivery(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	old := "2020-01-02T03:04:05Z"

	home := setupWatcherHome(t, state.Task{ID: "task-1", Kind: state.KindShip, CreatedAt: old, DeliveredAt: old, DeliveredReason: "handed off"}, state.Attempt{
		Lifecycle:        state.AttemptRunning,
		Herdr:            state.Herdr{PaneID: "p1"},
		StatusChangedAt:  old,
		StatusChangedFor: string(herdr.StatusWorking),
	})
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()
	var out bytes.Buffer
	tick(ctx, cfg, client, states, &out, io.Discard)

	setStatus(t, statusFile, "done")
	out.Reset()
	tick(ctx, cfg, client, states, &out, io.Discard)

	if !strings.Contains(out.String(), "idle-unreported task-1") {
		t.Fatalf("output = %q, want delivery to leave the factual unexplained stop visible", out.String())
	}
}

// The only remaining source of a verified-done record: a worker's own "done" report, cross-checked
// against a task the caller has already recorded as merged. herdr's agent_status never drives this by
// itself - see the test above.
func TestTickRecordsVerifiedDoneOnlyOnceReportedDoneIsVerified(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip,
		PR: "https://github.com/atqamz/hand/pull/1", MergeExecuted: true}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}},
	)

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

	_, attempt := readTaskAttempt(t, home, "task-1")
	if !attempt.DoneVerified {
		t.Fatal("task.DoneVerified = false, want the verified done persisted")
	}
	if attempt.LastReportState != state.ReportDone {
		t.Fatalf("LastReportState = %q, want %q recorded", attempt.LastReportState, state.ReportDone)
	}
}

// The one event kind that used to break the section's membership rule: a
// blocked pane is something the watcher observed, not something the worker
// said, so it belongs nowhere near the operator's decision queue.
func TestTickClassifiesBlockedWithoutInventingAPendingDecision(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

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

	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.LastReportState != "" {
		t.Fatalf("LastReportState = %q, want nothing inferred from a pane the worker never explained", attempt.LastReportState)
	}
}

func TestTickClassifiesPRMerged(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	writeFakeGh(t, "MERGED")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, PR: "https://example.com/pr/1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

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

// atqamz/hand#268's disagreement 1, closed: a PR behind no completed gate run used to be attention
// in hand status and silence in hand watch, since nothing here ever asked no-mistakes the question.
func TestTickClassifiesGateProblem(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "idle")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "gated", Kind: state.KindShip,
		PR: "https://github.com/atqamz/hand/pull/120"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	faketool.NoMistakes{Stdout: "  completed    other-branch   758d72bf  2026-08-03 04:29  https://github.com/atqamz/hand/pull/999\n"}.Install(t, faketool.Bin(t))
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: shipped\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	// The first tick only seeds tracking for a task not seen before, the same as every other
	// classifier in this suite - see TestTickFiresParkedOnTheFirstCorroboratingTickWhenTheSilenceAlreadyExceedsTheBound.
	tick(ctx, cfg, client, states, &buf, io.Discard)

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "gate-absent task-1") {
		t.Fatalf("output = %q, want gate-absent task-1: the recorded PR is not among the completed runs", buf.String())
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if buf.Len() != 0 {
		t.Fatalf("gate-absent fired again: %q", buf.String())
	}
}

// Mirrors !ts.PRMerged three lines above the gate check in tick(): once ClassifyGateProblem has
// fired for this attempt, later ticks must not keep re-execing no-mistakes to ask a question already
// answered, the way an unbounded poll loop otherwise would forever.
func TestTickStopsAskingNoMistakesOnceGateProblemHasFired(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "idle")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "gated", Kind: state.KindShip,
		PR: "https://github.com/atqamz/hand/pull/120"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(t.TempDir(), "calls")
	faketool.NoMistakes{
		Stdout:   "  completed    other-branch   758d72bf  2026-08-03 04:29  https://github.com/atqamz/hand/pull/999\n",
		CountLog: countFile,
	}.Install(t, faketool.Bin(t))
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: shipped\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	for range 5 {
		tick(ctx, cfg, client, states, &buf, io.Discard)
	}
	if !strings.Contains(buf.String(), "gate-absent task-1") {
		t.Fatalf("output = %q, want gate-absent task-1 to have fired once across five ticks", buf.String())
	}
	calls, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Fields(string(calls))); got != 1 {
		t.Fatalf("no-mistakes ran %d times across five ticks, want 1: the gate-run problem already fired", got)
	}
}

// Neither a task the gate-run check does not apply to nor one whose project is unregistered ever
// shells out to no-mistakes at all - the same silent skip cmd/status.go's own gateRunApplies gives.
func TestTickSkipsGateCheckWhenItDoesNotApply(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "idle")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "ungated", Kind: state.KindShip,
		PR: "https://github.com/atqamz/hand/pull/120"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: shipped\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No no-mistakes fake installed at all: a call through here would fail the test's PATH lookup.

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if strings.Contains(buf.String(), "gate-") {
		t.Fatalf("output = %q, want no gate event for an unregistered project", buf.String())
	}
}

func TestTickFiresIdleUnreportedWhenPaneGoesNotBusyWithNoReport(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

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

	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.LastReportState != "" {
		t.Fatalf("LastReportState = %q, want the idle pane raised as an event and not as a decision the worker never asked for", attempt.LastReportState)
	}
}

func TestTickAbsorbsNotBusyWhenReportExplainsTheStop(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, PR: "https://github.com/atqamz/hand/pull/1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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

	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.LastReportState != state.ReportNeedsDecision || !strings.Contains(attempt.LastReportNote, "which base branch?") {
		t.Fatalf("LastReportState/Note = %q/%q, want the worker's own question intact", attempt.LastReportState, attempt.LastReportNote)
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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
	// This tick still forks a fake gh subprocess for ValidatePR before it ever reaches the
	// non-blocking task lock, so the watchdog has to outlast fork/exec latency under a loaded box,
	// not just the (already non-blocking) lock check the test is actually about.
	case <-time.After(30 * time.Second):
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/hand.git")

	url := "https://github.com/atqamz/hand/pull/7"
	// The holder's write has to land after tick's own state.List snapshot, or the pre-existing "task
	// already has a PR" guard absorbs the URL and the contention path under test is never reached -
	// which is what made an earlier version of this test vacuous.
	snapshot := taskSnapshotWithPR(t, home, "task-1", url)
	// Hence the gh double writes it: ValidatePR shells out to gh on the way to the lock, so the hook
	// fires inside exactly that window.
	writeFakeGhWithCopy(t, "OPEN", snapshot, store.Path(home))

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
	history, err := state.ReadHistory(home, id)
	if err != nil {
		t.Fatal(err)
	}
	task := history.Task
	task.PR = pr
	task.ActiveAttemptID = 0
	scratch := t.TempDir()
	if err := state.CreateTask(scratch, task); err != nil {
		t.Fatal(err)
	}
	for _, attempt := range history.Attempts {
		attempt.ID = 0
		if _, err := state.CreateAttempt(scratch, attempt); err != nil {
			t.Fatal(err)
		}
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	registerProject(t, home, "nsr", "https://github.com/atqamz/hand.git")

	url := "https://github.com/atqamz/hand/pull/7"
	corrupt := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("this is not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFakeGhWithCopy(t, "OPEN", corrupt, store.Path(home))

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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, PR: "https://github.com/atqamz/hand/pull/1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, PR: "https://github.com/atqamz/hand/pull/1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

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

// Covers atqamz/hand#252's third and eighth acceptance tests together: the stop lands while no
// watcher is alive, so recovery has to come from durable state, and the watcher after that one has
// to find the same condition already answered.
func TestTickCatchesUpOnAStopThatLandedBetweenTwoWatchers(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip},
		state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}, LastReportState: state.ReportWorking})
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	ctx := context.Background()

	var buf bytes.Buffer
	watched := make(map[string]*TaskState)
	tick(ctx, cfg, client, watched, &buf, io.Discard)
	tick(ctx, cfg, client, watched, &buf, io.Discard)
	if buf.Len() != 0 {
		t.Fatalf("output = %q, want nothing while the worker is still working", buf.String())
	}

	// No watcher is alive across this transition, so no edge exists for anyone to have observed.
	setStatus(t, statusFile, "done")

	buf.Reset()
	rearmed := make(map[string]*TaskState)
	tick(ctx, cfg, client, rearmed, &buf, io.Discard)
	tick(ctx, cfg, client, rearmed, &buf, io.Discard)
	if !strings.Contains(buf.String(), "idle-unreported task-1") {
		t.Fatalf("output = %q, want idle-unreported task-1 recovered from durable state", buf.String())
	}

	buf.Reset()
	again := make(map[string]*TaskState)
	tick(ctx, cfg, client, again, &buf, io.Discard)
	tick(ctx, cfg, client, again, &buf, io.Discard)
	if buf.Len() != 0 {
		t.Fatalf("output = %q, want silence: the condition was announced once and nothing changed since", buf.String())
	}
}

// A `done:` rewrite landing on the byte count of the `working:` line before it was skipped outright
// (atqamz/hand#149): the offset still sat just past the final newline with nothing after it, so
// nothing was announced, LastReportState stayed `working`, and ClassifyDeferredDone - gated on it - never ran.
func TestTickAnnouncesADoneRewrittenToTheSameLengthAcrossARestart(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

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

	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.LastReportState != state.ReportDone {
		t.Fatalf("LastReportState = %q, want the done rewrite recorded so the deferred verification can fire", attempt.LastReportState)
	}

	task, _ := readTaskAttempt(t, home, "task-1")
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

func TestTickFiresParkedOnTheFirstCorroboratingTickWhenTheSilenceAlreadyExceedsTheBound(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "idle")
	writeFakeHerdr(t, statusFile)

	// Created before the silence it is about to be blamed for: ReportEvidenceTime
	// floors the mtime at the pane's start, so a task younger than its own report
	// file could not accumulate this silence in the first place.
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip,
		CreatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}},
	)
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

	// A restart's first two ticks seed and corroborate tracking before a classifier can fire.
	states := make(map[string]*TaskState)
	var buf bytes.Buffer
	tick(context.Background(), cfg, client, states, &buf, io.Discard)
	if strings.Contains(buf.String(), "parked task-1") {
		t.Fatal("parked fired on the seeding tick, before resume had even finished reading durable state")
	}

	buf.Reset()
	tick(context.Background(), cfg, client, states, &buf, io.Discard)
	if strings.Contains(buf.String(), "parked task-1") {
		t.Fatal("parked fired on the baseline tick, before a second pane sample existed")
	}

	buf.Reset()
	tick(context.Background(), cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "parked task-1") {
		t.Fatalf("output = %q, want parked task-1 on the first corroborating tick after resume: the silence predates this process and must not need to reaccumulate from resume time", buf.String())
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
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip,
		CreatedAt: started.UTC().Format(time.RFC3339)}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},

		PaneStartedAt: started.UTC().Format(time.RFC3339)},
	)
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
	tick(context.Background(), cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "parked task-1") {
		t.Fatalf("output = %q, want parked task-1 from the first corroborating run tick: the done-tier bound is already crossed", buf.String())
	}

	buf.Reset()
	// The whole point of persisting the latch is this second run staying quiet.
	restarted := make(map[string]*TaskState)
	tick(context.Background(), cfg, client, restarted, &buf, io.Discard)
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
	task := state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip,
		CreatedAt: now.Add(-4 * time.Hour).UTC().Format(time.RFC3339)}
	attempt := state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
		PaneStartedAt:    paneStart.UTC().Format(time.RFC3339),
		StatusChangedAt:  now.Add(-time.Minute).UTC().Format(time.RFC3339),
		StatusChangedFor: string(herdr.StatusUnknown)}
	if err := writeTaskAttempt(t, home, task, attempt); err != nil {
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

	got, err := ReportEvidenceTime(home, task, attempt)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(silentSince.Truncate(time.Second)) && !got.Equal(silentSince) {
		t.Fatalf("evidence time = %s, want the report's own mtime %s: an outage stamp must not forget two hours of real silence", got, silentSince)
	}

	promoted := task
	promotedAttempt := attempt
	promotedAttempt.PaneStartedAt = now.Add(-time.Minute).UTC().Format(time.RFC3339)
	got, err = ReportEvidenceTime(home, promoted, promotedAttempt)
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
	task := state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, CreatedAt: dwelling}
	attempt := state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
		StatusChangedAt: dwelling, StatusChangedFor: "working"}
	if err := writeTaskAttempt(t, home, task, attempt); err != nil {
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
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip,
		CreatedAt: dwelling}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
		StatusChangedAt: dwelling, StatusChangedFor: "blocked"},
	); err != nil {
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

	_, gotAttempt := readTaskAttempt(t, home, "task-1")
	if gotAttempt.StatusChangedFor != "working" || gotAttempt.StatusChangedAt == dwelling {
		t.Fatalf("status_changed_at/for = %q/%q, want the dwell restamped for the status actually observed", gotAttempt.StatusChangedAt, gotAttempt.StatusChangedFor)
	}
}

// Covers the window between the two halves: the worker reports done, hand watch stops, and hand merge
// lands the work by writing merged. On restart the evidence is already on disk, so a marker re-derived
// from it would conclude the verified line went out and never print it.
func TestTickAnnouncesAVerifiedDoneAfterARestartThatMissedTheEvidence(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

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
	task, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.DoneVerified {
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

	_, attempt = readTaskAttempt(t, home, "task-1")
	// Without the announcement the recorded state stays stuck where the unverified report left it.
	if attempt.LastReportState != state.ReportDone {
		t.Fatalf("LastReportState = %q, want the task's own recorded state moved to done", attempt.LastReportState)
	}
	if !attempt.DoneVerified {
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
// clearing the disk field in internal/runtime/promote.go alone is not enough.
func TestTickAnnouncesTheShipsOwnVerifiedDoneAfterPromoteResetsTheStaleMarker(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindScout}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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
	task, attempt := readTaskAttempt(t, home, "task-1")
	if !attempt.DoneVerified {
		t.Fatal("task.DoneVerified = false, want the scout's announcement persisted")
	}

	// hand promote's own rewrite: task kind flips to ship and a fresh attempt with its own pane
	// takes over, DoneVerified starting false again - it never rewrites the scout attempt's row.
	shipAttempt, err := state.PromoteTask(home, "task-1", attempt.ID, attempt.Lifecycle, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Herdr: state.Herdr{PaneID: "p2"}, LaunchConfirmedAt: "2026-08-14T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionAttempt(home, shipAttempt.ID, state.AttemptProvisioning, state.AttemptRunning); err != nil {
		t.Fatal(err)
	}

	// One tick to let the watcher observe the ship's fresh pane before its report exists,
	// exactly as it would after a real promote's brand-new pane comes up.
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)

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
	_, attempt = readTaskAttempt(t, home, "task-1")
	if attempt.DoneVerified {
		t.Fatal("task.DoneVerified = true, want promote's reset not resurrected by the cached copy")
	}

	// hand merge lands the evidence the ship's own done needs.
	task, err = state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
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
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindScout,
		CreatedAt: dwelling}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
		StatusChangedAt: dwelling, StatusChangedFor: "working"},
	); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: 20 * time.Minute}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)

	_, attempt := readTaskAttempt(t, home, "task-1")
	restamp := time.Now().UTC().Format(time.RFC3339)
	// hand promote's own rewrite: a fresh attempt with its own pane takes over the task,
	// carrying no observed status of its own yet - it never rewrites the scout attempt's row.
	shipAttempt, err := state.PromoteTask(home, "task-1", attempt.ID, attempt.Lifecycle, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Herdr: state.Herdr{PaneID: "p2"},
		StatusChangedAt: restamp, LaunchConfirmedAt: restamp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionAttempt(home, shipAttempt.ID, state.AttemptProvisioning, state.AttemptRunning); err != nil {
		t.Fatal(err)
	}

	// One tick for the watcher to notice the new attempt identity and reseed its tracking - a
	// promote gives the same task ID a new attempt, which this tick treats as a first sighting
	// rather than a continuation of the scout's cached dwell.
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)

	// Moves report_offset, which is what makes the next tick write task state at all.
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("working: starting the ship run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if strings.Contains(buf.String(), "stale task-1") {
		t.Fatalf("out = %q, want no stale: the ship's dwell starts at the promotion, not at the scout's last observed transition", buf.String())
	}

	_, gotAttempt := readTaskAttempt(t, home, "task-1")
	if gotAttempt.StatusChangedAt == dwelling {
		t.Fatal("status_changed_at = the scout's stamp, want the cached scout dwell not written back over promote's restamp")
	}
	stamped, err := time.Parse(time.RFC3339, gotAttempt.StatusChangedAt)
	if err != nil {
		t.Fatal(err)
	}
	restamped, err := time.Parse(time.RFC3339, restamp)
	if err != nil {
		t.Fatal(err)
	}
	if stamped.Before(restamped) {
		t.Fatalf("status_changed_at = %q, want no earlier than promote's restamp %q", gotAttempt.StatusChangedAt, restamp)
	}
	if gotAttempt.StatusChangedFor != "working" {
		t.Fatalf("status_changed_for = %q, want the status the ship's dwell was stamped for", gotAttempt.StatusChangedFor)
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

	promoted := state.Task{ID: "task-1", Kind: state.KindShip, CreatedAt: scoutDwell.UTC().Format(time.RFC3339)}
	promotedAttempt := state.Attempt{Herdr: state.Herdr{PaneID: "p2"}, StatusChangedAt: now.UTC().Format(time.RFC3339)}
	forgetPaneScopedCache(ts, promoted, promotedAttempt, now)

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

	forgetPaneScopedCache(ts, promotedTask(now), promotedAttempt(now), now)

	if e := ClassifyStatus(ts, "task-1", "", errors.New("pane not found"), now, ""); e != nil {
		t.Fatalf("event = %+v, want none: a blink on the ship's first probe is not a failure", e)
	}
	if e := ClassifyUnreachable(ts, "task-1", now.Add(time.Second), 5*time.Minute, ""); e != nil {
		t.Fatalf("event = %+v, want none: the ship's outage has not outlived the threshold", e)
	}
	e := ClassifyUnreachable(ts, "task-1", now.Add(6*time.Minute), 5*time.Minute, "")
	if e == nil || e.Kind != KindFailed {
		t.Fatalf("event = %+v, want failed: the ship's pane stayed unreachable past the threshold", e)
	}
}

// A completeness guard, not a behavior test: TestForgetPaneScopedCacheClearsEveryPaneAnchoredLatch
// above asserts only the fields already known to need resetting, and would keep passing if a future
// field repeated the defect Status and then Probed both had - in TaskState, absent from the function.
func TestForgetPaneScopedCacheHandlesEveryField(t *testing.T) {
	before := TaskState{
		CreatedAt:                  "created-marker",
		AttemptID:                  99,
		AttemptLifecycle:           state.AttemptRunning,
		Status:                     herdr.Status("scout-status"),
		Probed:                     true,
		ChangedAt:                  time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC),
		Blocked:                    true,
		Stale:                      true,
		PRMerged:                   true,
		GateProblemFired:           true,
		ReportCursor:               state.ReportCursor{Offset: 42, Digest: "consumed-digest"},
		PersistedCursor:            state.ReportCursor{Offset: 43, Digest: "persisted-digest"},
		PersistedPRMerged:          true,
		PersistedDoneVerified:      true,
		PersistedPaneID:            "scout-pane",
		PersistedChangedAt:         time.Date(2002, 2, 2, 0, 0, 0, 0, time.UTC),
		PersistedChangedFor:        "scout-status-for",
		PaneSample:                 "stale-pane-sample",
		PaneSampleObserved:         true,
		LastReportState:            "scout-report-state",
		LastReportNote:             "scout-report-note",
		DoneVerified:               true,
		ParkedFiredFor:             time.Date(2003, 3, 3, 0, 0, 0, 0, time.UTC),
		PersistedParkedFiredFor:    time.Date(2004, 4, 4, 0, 0, 0, 0, time.UTC),
		LimitRetryAt:               time.Date(2005, 5, 5, 0, 0, 0, 0, time.UTC),
		LimitAttempts:              3,
		LimitResumeBlocked:         true,
		LimitEpisode:               5,
		LimitStuckEpisode:          4,
		PersistedLimitRetryAt:      time.Date(2006, 6, 6, 0, 0, 0, 0, time.UTC),
		PersistedLimitAttempts:     4,
		PersistedLimitEpisode:      6,
		PersistedLimitStuckEpisode: 7,
		LimitProbed:                true,
		UnreachableFired:           true,
		CaughtUp:                   true,
	}
	promoted := state.Task{CreatedAt: "2020-01-01T00:00:00Z"}
	promotedAttempt := state.Attempt{
		Herdr: state.Herdr{PaneID: "ship-pane"}, LastReportState: "ship-report-state",
		LastReportNote: "ship-report-note",
	}

	// Every field the PR body's field-by-field table classifies as pane-independent, so
	// forgetPaneScopedCache is right to leave it alone: identity, PR facts, report-file position, and
	// the parked latch, keyed to the report mtime not the pane. Persisted* entries mirror those facts.
	carried := map[string]bool{
		"CreatedAt":        true,
		"AttemptID":        true,
		"AttemptLifecycle": true,
		"PRMerged":         true,
		// The gate-run problem latch is about the recorded PR, not the pane it was announced from -
		// exactly PRMerged's own reasoning, one field down.
		"GateProblemFired":        true,
		"ReportCursor":            true,
		"PersistedCursor":         true,
		"PersistedPRMerged":       true,
		"ParkedFiredFor":          true,
		"PersistedParkedFiredFor": true,
		// CaughtUp records this watcher's own one look at the task, anchored to no pane. The ship's
		// first probe is deliberately a baseline - Status is reset to StatusUnknown right here - so a
		// re-run of the catch-up against it could only ever return nil anyway.
		"CaughtUp": true,
	}

	ts := before
	forgetPaneScopedCache(&ts, promoted, promotedAttempt, time.Now())

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
		ID: "task-1", Kind: state.KindShip,
		CreatedAt: now.Add(-30 * time.Minute).UTC().Format(time.RFC3339),
	}
}

func promotedAttempt(now time.Time) state.Attempt {
	return state.Attempt{Herdr: state.Herdr{PaneID: "p2"}, StatusChangedAt: now.UTC().Format(time.RFC3339)}
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

	forgetPaneScopedCache(ts, promotedTask(now), promotedAttempt(now), now)

	if e := ClassifyStatus(ts, "task-1", herdr.StatusDone, nil, now.Add(time.Second), ""); e != nil {
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

	forgetPaneScopedCache(ts, promotedTask(now), promotedAttempt(now), now)

	e := ClassifyStatus(ts, "task-1", herdr.StatusBlocked, nil, now.Add(time.Second), "")
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

	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip,
		CreatedAt: scoutDwell.UTC().Format(time.RFC3339),

		ReportOffset: 42}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p2"},

		StatusChangedAt: now.UTC().Format(time.RFC3339)},
	); err != nil {
		t.Fatal(err)
	}

	_, observed := readTaskAttempt(t, home, "task-1")
	ts.AttemptID = observed.ID
	ts.AttemptLifecycle = observed.Lifecycle

	var errBuf bytes.Buffer
	syncTaskState(home, "task-1", ts, now, &errBuf)
	if errBuf.Len() != 0 {
		t.Fatalf("errOut = %q, want a clean write", errBuf.String())
	}

	_, gotAttempt := readTaskAttempt(t, home, "task-1")
	if gotAttempt.StatusChangedAt == scoutDwell.UTC().Format(time.RFC3339) {
		t.Fatal("status_changed_at = the scout's stamp, want promote's restamp not overwritten")
	}
	if gotAttempt.StatusChangedFor != string(herdr.StatusUnknown) {
		t.Fatalf("status_changed_for = %q, want the restamped dwell to belong to no observed status: this tick probed the scout's pane", gotAttempt.StatusChangedFor)
	}
	if gotAttempt.DoneVerified {
		t.Fatal("done_verified = true, want the scout's marker not resurrected by the cached copy")
	}
	if gotAttempt.LastReportState != "" || gotAttempt.LastReportNote != "" {
		t.Fatalf("last_report_state/note = %q/%q, want the scout's report evidence not written back", gotAttempt.LastReportState, gotAttempt.LastReportNote)
	}
	if ts.Stale {
		t.Fatal("ts.Stale = true, want the cached stale latch cleared for the ship's pane")
	}
}

// The report cursor is task-owned, so a task terminalized between this tick's read and its write-back
// must not lose it: a surviving report log replayed from offset 0 re-raises resolved decisions and can
// auto-record a stale PR URL onto the id's next attempt.
func TestSyncTaskStateKeepsTheReportCursorWhenTheAttemptIsGone(t *testing.T) {
	home := t.TempDir()
	now := time.Now()

	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip,
		CreatedAt: now.UTC().Format(time.RFC3339)},
		state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}},
	); err != nil {
		t.Fatal(err)
	}
	_, attempt := readTaskAttempt(t, home, "task-1")
	if err := state.TransitionAttempt(home, attempt.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionTask(home, "task-1", state.TaskOpen, state.TaskTerminal); err != nil {
		t.Fatal(err)
	}

	ts := &TaskState{
		Status:           herdr.StatusWorking,
		AttemptID:        attempt.ID,
		AttemptLifecycle: attempt.Lifecycle,
		Probed:           true,
		ChangedAt:        now,
		PersistedPaneID:  "p1",
		ReportCursor:     state.ReportCursor{Offset: 128, Digest: "consumed-digest"},
	}

	var errBuf bytes.Buffer
	syncTaskState(home, "task-1", ts, now, &errBuf)
	if !strings.Contains(errBuf.String(), "read active attempt task-1 failed") {
		t.Fatalf("errOut = %q, want the missing attempt named", errBuf.String())
	}

	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.ReportOffset != 128 || history.Task.ReportDigest != "consumed-digest" {
		t.Fatalf("report cursor = %d/%q, want the consumed cursor persisted", history.Task.ReportOffset, history.Task.ReportDigest)
	}
	if ts.PersistedCursor != ts.ReportCursor {
		t.Fatalf("ts.PersistedCursor = %+v, want the write it just made mirrored", ts.PersistedCursor)
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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
	faketool.GH{Responses: []faketool.GHResponse{{
		Command: "pr view",
		Stderr:  "error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com\n",
		Exit:    1,
	}}}.Install(t, faketool.Bin(t))
}

func TestTickForgetsTornDownTasks(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()

	var buf bytes.Buffer
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if len(states) != 1 {
		t.Fatalf("states = %+v, want task-1 tracked", states)
	}

	attempt, err := state.ActiveAttempt(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, attempt.Lifecycle, state.AttemptCompleted); err != nil {
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
		{Kind: KindUsageLimitStuck, TaskID: "task-1", Text: "usage-limit-stuck task-1: no automatic resend"},
	} {
		t.Run(e.Kind, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(home, "marker.txt")
			template := "printf '%s' \"$HAND_MESSAGE\" > " + shellquote.Quote(marker)
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
	template := "printf '%s' \"$HAND_MESSAGE\" > " + shellquote.Quote(marker)
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
	faketool.Herdr{Unreachable: true}.Install(t, faketool.Bin(t))

	home := t.TempDir()
	err := Run(context.Background(), Config{Home: home, PollInterval: time.Second, StaleThreshold: time.Minute}, &bytes.Buffer{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "herdr unreachable") {
		t.Fatalf("got err %v, want herdr unreachable", err)
	}
}

func TestRunUsesTheFleetScopedHerdrSession(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "herdr-calls")
	faketool.Herdr{Log: callLog, LogCommands: []string{"workspace list"}}.Install(t, faketool.Bin(t))
	home := setupWatcherHome(t, state.Task{ID: "task-1"}, state.Attempt{Lifecycle: state.AttemptProvisioning})
	fleetID, err := state.FleetID(home)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Minute}, &bytes.Buffer{}, io.Discard)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, readErr := os.ReadFile(callLog); readErr == nil && strings.Contains(string(data), "workspace list") {
			break
		}
		select {
		case runErr := <-done:
			t.Fatalf("Run returned before Herdr connection: %v", runErr)
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	if data, readErr := os.ReadFile(callLog); readErr != nil || !strings.Contains(string(data), "workspace list") {
		t.Fatalf("Herdr call log = %q, read error = %v", data, readErr)
	}
	cancel()
	if err := <-done; !errors.Is(err, ErrInterrupted) {
		t.Fatalf("Run returned %v, want ErrInterrupted", err)
	}
	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	want := "--session " + herdr.SessionName(fleetID) + " workspace list"
	if !strings.Contains(string(data), want) {
		t.Fatalf("Herdr calls = %q, want %q", data, want)
	}
}

func TestRunReportsInterruptionOnContextCancel(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "herdr-calls")
	faketool.Herdr{Hang: []string{"workspace list"}, Log: callLog, LogCommands: []string{"workspace list"}}.Install(t, faketool.Bin(t))

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Minute}, &bytes.Buffer{}, io.Discard)
	}()

	waitForHerdrCalls(t, callLog, "workspace list", 1)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("Run returned %v, want ErrInterrupted so a caller can tell interruption from a real event", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}

func TestRunReportsAParentDeadlineAsInterruption(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "herdr-calls")
	faketool.Herdr{Hang: []string{"workspace list"}, Log: callLog, LogCommands: []string{"workspace list"}}.Install(t, faketool.Bin(t))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := Run(ctx, Config{Home: t.TempDir(), PollInterval: time.Hour, StaleThreshold: time.Minute}, &bytes.Buffer{}, io.Discard)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("Run returned %v, want ErrInterrupted for a caller deadline", err)
	}
	if errors.Is(err, ErrNoEvent) {
		t.Fatal("Run returned ErrNoEvent for a caller deadline; only the until-event timeout is a no-event window")
	}
}

func TestRunExitsWhenPaneProbeIsCanceled(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "pane-get-calls")
	faketool.Herdr{
		Workspaces: []faketool.HerdrWorkspace{{ID: "wA", Label: "watch", Tabs: []faketool.HerdrTab{{ID: "wA:tA", Label: "task-1", Pane: "p1"}}}},
		Hang:       []string{"pane get"},
		Log:        callLog, LogCommands: []string{"pane get"},
	}.Install(t, faketool.Bin(t))
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Minute}, &bytes.Buffer{}, io.Discard)
	}()
	waitForPaneGets(t, callLog, 1)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("Run returned %v, want ErrInterrupted", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after canceling a pane probe")
	}
}

// Covers atqamz/hand#252: the report was written while no watcher was alive, so there is no future
// transition left to wake on. Arming has to observe it, not take it as a baseline it may discard.
func TestRunUntilEventDeliversAReportWrittenBeforeTheArm(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "done")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 10 * time.Second}
	if err := RunUntilEvent(context.Background(), cfg, &out, io.Discard); err != nil {
		t.Fatalf("RunUntilEvent = %v, want nil so the exit code reads as a delivered event", err)
	}
	if !strings.Contains(out.String(), "reported-done task-1") {
		t.Fatalf("out = %q, want reported-done task-1 delivered by the arm itself", out.String())
	}
	// The report line explains the stop, so the pane being not-busy is not a second condition.
	if strings.Contains(out.String(), "idle-unreported") {
		t.Fatalf("out = %q, want no idle-unreported alongside the report that explains the stop", out.String())
	}

	logData, err := os.ReadFile(filepath.Join(home, "state", "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "reported-done task-1") {
		t.Fatalf("events.log = %q, want the arm's own events recorded there too", string(logData))
	}
}

// Covers atqamz/hand#252's central case: the worker's pane stopped before the watcher armed, and its
// last word was a `working:` line an earlier watcher already announced. No edge is left to fire.
func TestRunUntilEventWakesOnAWorkerAlreadyStoppedBeforeTheArm(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "done")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip},
		state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
			StatusChangedFor: "working", LastReportState: state.ReportWorking, LastReportNote: "still on the migration"})

	var out bytes.Buffer
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 10 * time.Second}
	if err := RunUntilEvent(context.Background(), cfg, &out, io.Discard); err != nil {
		t.Fatalf("RunUntilEvent = %v, want nil so the exit code reads as a delivered event", err)
	}
	if !strings.Contains(out.String(), "idle-unreported task-1") {
		t.Fatalf("out = %q, want idle-unreported task-1 from the arm-time observation", out.String())
	}

	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.StatusChangedFor != "done" {
		t.Fatalf("status_changed_for = %q, want the caught-up episode recorded so a re-arm has evidence it was announced", attempt.StatusChangedFor)
	}
}

// The other half of the same contract: an arm that finds nothing actionable has to wait for a real
// transition, and a re-arm meeting the same already-announced condition must not fire again.
func TestRunUntilEventStaysArmedWhenTheCaughtUpConditionWasAlreadyAnnounced(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "done")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip},
		state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
			StatusChangedFor: "working", LastReportState: state.ReportWorking})

	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 10 * time.Second}
	var first bytes.Buffer
	if err := RunUntilEvent(context.Background(), cfg, &first, io.Discard); err != nil {
		t.Fatalf("first RunUntilEvent = %v, want the caught-up wake", err)
	}
	if !strings.Contains(first.String(), "idle-unreported task-1") {
		t.Fatalf("first out = %q, want idle-unreported task-1", first.String())
	}

	cfg.Timeout = 300 * time.Millisecond
	var second bytes.Buffer
	err := RunUntilEvent(context.Background(), cfg, &second, io.Discard)
	if !errors.Is(err, ErrNoEvent) {
		t.Fatalf("re-armed RunUntilEvent = %v, want ErrNoEvent: the condition was announced once and nothing has changed since", err)
	}
	if second.Len() != 0 {
		t.Fatalf("re-armed out = %q, want silence rather than one wake per arm", second.String())
	}
}

// atqamz/hand#252 and atqamz/hand#235 together: the arm observes what it missed, and a pane hand
// teardown is releasing is still not one of the things it missed.
func TestRunUntilEventCatchesUpOnAStoppedWorkerWithoutWakingOnATeardownRelease(t *testing.T) {
	faketool.Herdr{
		Workspaces: []faketool.HerdrWorkspace{{ID: "wA", Label: "watch", Tabs: []faketool.HerdrTab{
			{ID: "wA:t1", Label: "torn-down", Pane: "p1"},
			{ID: "wA:t2", Label: "stopped", Pane: "p2"},
		}}},
		PaneStatus: "done",
	}.Install(t, faketool.Bin(t))

	quiet := state.Attempt{Lifecycle: state.AttemptRunning, StatusChangedFor: "working", LastReportState: state.ReportWorking}
	tornDown := quiet
	tornDown.Herdr = state.Herdr{PaneID: "p1"}
	tornDown.TeardownHerdrState = state.TeardownResourceReleasing
	stopped := quiet
	stopped.Herdr = state.Herdr{PaneID: "p2"}

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, tornDown)
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-2", Project: "nsr", Kind: state.KindShip,
		CreatedAt: time.Now().UTC().Format(time.RFC3339)}, stopped); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 10 * time.Second}
	if err := RunUntilEvent(context.Background(), cfg, &out, io.Discard); err != nil {
		t.Fatalf("RunUntilEvent = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "idle-unreported task-2") {
		t.Fatalf("out = %q, want the genuinely stopped worker caught up", out.String())
	}
	if strings.Contains(out.String(), "task-1") {
		t.Fatalf("out = %q, want nothing for the task teardown is releasing", out.String())
	}
}

// Covers atqamz/hand#252's sixth acceptance test. stale is satisfied by any task whose herdr status
// has simply not changed lately, so delivering it at arm would make every re-arm return at once -
// a busy poll wearing an event's clothes.
func TestRunUntilEventKeepsWaitingWhenOnlyStaleWouldFireAtArm(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	dwelling := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	staleTask := state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip, CreatedAt: dwelling}
	staleAttempt := state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
		StatusChangedAt: dwelling, StatusChangedFor: "working"}

	// Drive the arm's own two ticks directly with an unbounded context: asserting on RunUntilEvent's
	// return instead would race that recording against its --timeout clock, which a loaded box can
	// win before the second tick's herdr round trip returns.
	recordHome := t.TempDir()
	if err := writeTaskAttempt(t, recordHome, staleTask, staleAttempt); err != nil {
		t.Fatal(err)
	}
	armCfg := Config{Home: recordHome, StaleThreshold: 20 * time.Minute, catchUp: CatchUpFilter()}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	var caught, armErrOut bytes.Buffer
	tick(context.Background(), armCfg, client, states, &caught, &armErrOut)
	tick(context.Background(), armCfg, client, states, &caught, &armErrOut)
	if caught.Len() != 0 {
		t.Fatalf("arm out = %q, want nothing delivered: a stale-only condition must not wake the arm", caught.String())
	}
	logData, err := os.ReadFile(filepath.Join(recordHome, "state", "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "stale task-1") {
		t.Fatalf("events.log = %q, want stale recorded: the arm withholds the wake, not the record", string(logData))
	}

	// RunUntilEvent's own ErrNoEvent contract, on a fresh home so the direct ticks above cannot leave
	// it any durable state: whatever wall-clock margin the arm needed to record, the same condition
	// must still not wake it.
	waitHome := t.TempDir()
	if err := writeTaskAttempt(t, waitHome, staleTask, staleAttempt); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	waitCfg := Config{Home: waitHome, PollInterval: 10 * time.Millisecond, StaleThreshold: 20 * time.Minute, Timeout: 500 * time.Millisecond}
	err = RunUntilEvent(context.Background(), waitCfg, &out, io.Discard)
	if !errors.Is(err, ErrNoEvent) {
		t.Fatalf("RunUntilEvent = %v, want ErrNoEvent: a working worker whose status has not changed is not actionable", err)
	}
	if out.Len() != 0 {
		t.Fatalf("out = %q, want the watcher still waiting", out.String())
	}
}

// Covers atqamz/hand#252's ninth acceptance test: --until-event returns once, so everything the arm
// found actionable has to leave with it rather than be dropped behind the first task's event.
func TestRunUntilEventDeliversEveryTaskActionableAtArm(t *testing.T) {
	faketool.Herdr{
		Workspaces: []faketool.HerdrWorkspace{{ID: "wA", Label: "watch", Tabs: []faketool.HerdrTab{
			{ID: "wA:t1", Label: "task-1", Pane: "p1"},
			{ID: "wA:t2", Label: "task-2", Pane: "p2"},
		}}},
		PaneStatus: "done",
	}.Install(t, faketool.Bin(t))

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip},
		state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
			StatusChangedFor: "working", LastReportState: state.ReportWorking})
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-2", Project: "nsr", Kind: state.KindShip,
		CreatedAt: time.Now().UTC().Format(time.RFC3339)},
		state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p2"},
			StatusChangedFor: "working", LastReportState: state.ReportWorking}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 10 * time.Second}
	if err := RunUntilEvent(context.Background(), cfg, &out, io.Discard); err != nil {
		t.Fatalf("RunUntilEvent = %v, want nil", err)
	}
	for _, want := range []string{"idle-unreported task-1", "idle-unreported task-2"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("out = %q, want %q: the second actionable task must not be lost behind the first", out.String(), want)
		}
	}
}

func TestRunUntilEventDeliversTheFirstTransitionAndReturns(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	callLog := logPaneGets(t)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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

// Covers atqamz/hand#235: releaseHerdr commits TeardownResourceReleasing before it closes the pane, so
// a caller armed on failed while a deliberate hand teardown is in flight must not read that release as
// a worker failure and wake early.
func TestRunUntilEventDoesNotWakeFailedWhileTeardownIsReleasingTheHerdrResource(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	armDone := make(chan struct{})
	watchTickDone := make(chan struct{})
	oldAfterArmTick := afterArmTick
	oldAfterWatchTick := afterWatchTick
	t.Cleanup(func() {
		afterArmTick = oldAfterArmTick
		afterWatchTick = oldAfterWatchTick
	})
	armTicks := 0
	afterArmTick = func() {
		armTicks++
		if armTicks == 2 {
			close(armDone)
		}
	}
	afterWatchTick = func() {
		select {
		case <-watchTickDone:
		default:
			close(watchTickDone)
		}
	}

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip},
		state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}, TeardownHerdrState: state.TeardownResourceReleasing})
	cfg := Config{
		Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour,
		EventFilter: NewEventFilter([]string{KindFailed}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- RunUntilEvent(ctx, cfg, &out, io.Discard) }()

	select {
	case <-armDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for RunUntilEvent to arm")
	}
	setStatus(t, statusFile, paneGoneStatus)

	select {
	case <-watchTickDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the post-transition watch tick")
	}
	select {
	case err := <-done:
		t.Fatalf("RunUntilEvent = %v, want it to keep waiting: teardown's own release must not read as a failed worker", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	if err := <-done; !errors.Is(err, ErrInterrupted) {
		t.Fatalf("RunUntilEvent = %v, want ErrInterrupted after the test cancellation", err)
	}
	if out.Len() != 0 {
		t.Fatalf("out = %q, want nothing woken: the pane going unreachable here is teardown's own release, not a failure", out.String())
	}
}

func TestRunUntilEventDeliversIdleUnreportedForAWorkerThatWentQuiet(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	callLog := logPaneGets(t)

	// Durable, not a fresh report file: an unconsumed line is itself news the arm would deliver, and
	// this is about the transition after the arm.
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip},
		state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
			StatusChangedFor: "working", LastReportState: state.ReportWorking, LastReportNote: "still on the migration"})
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

func TestRunUntilEventSuppressesStaleAfterLiveTerminalAndDeliveryUpdates(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "pane-get-calls")
	faketool.Herdr{
		Workspaces: []faketool.HerdrWorkspace{{ID: "wA", Label: "watch", Tabs: []faketool.HerdrTab{
			{ID: "wA:t1", Label: "terminal-task", Pane: "p1"},
			{ID: "wA:t2", Label: "delivered-task", Pane: "p2"},
			{ID: "wA:t3", Label: "live-task", Pane: "p3"},
		}}},
		PaneStatusSequence: []string{
			"working", "working", "working",
			"working", "working", "working",
			"working", "working", "working",
			"working", "working", "working",
			"working", "working", "working",
			"working", "working", "blocked",
		},
		Log: callLog, LogCommands: []string{"pane get"},
	}.Install(t, faketool.Bin(t))

	old := "2020-01-02T03:04:05Z"
	future := "2099-01-02T03:04:05Z"
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		task    state.Task
		attempt state.Attempt
	}{
		{state.Task{ID: "terminal-task", Kind: state.KindShip, CreatedAt: old}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}, StatusChangedAt: future, StatusChangedFor: string(herdr.StatusWorking)}},
		{state.Task{ID: "delivered-task", Kind: state.KindShip, CreatedAt: old}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p2"}, StatusChangedAt: future, StatusChangedFor: string(herdr.StatusWorking)}},
		{state.Task{ID: "live-task", Kind: state.KindShip, CreatedAt: old}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p3"}, StatusChangedAt: future, StatusChangedFor: string(herdr.StatusWorking)}},
	} {
		if err := writeTaskAttempt(t, home, tc.task, tc.attempt); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- RunUntilEvent(context.Background(), Config{
			Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: 0, Timeout: 5 * time.Second,
			EventFilter: NewEventFilter([]string{KindStale}),
		}, &out, io.Discard)
	}()

	waitForHerdrCalls(t, callLog, "pane get", 9)
	if err := os.WriteFile(state.ReportPath(home, "terminal-task"), []byte("done: finished\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskDelivery(home, "delivered-task", old, "handed off while watch was alive"); err != nil {
		t.Fatal(err)
	}
	waitForHerdrCalls(t, callLog, "pane get", 15)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunUntilEvent = %v, want nil for the live stale event", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunUntilEvent did not return for the live stale event")
	}
	if got := out.String(); got != "stale live-task\n" {
		t.Fatalf("out = %q, want only the live stale event", got)
	}
	logData, err := os.ReadFile(filepath.Join(state.Dir(home), "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"terminal-task", "delivered-task"} {
		if strings.Contains(string(logData), "stale "+id) {
			t.Fatalf("events.log = %q, contains false stale event for %s", string(logData), id)
		}
	}
	if !strings.Contains(string(logData), "stale live-task") {
		t.Fatalf("events.log = %q, want the live stale event", string(logData))
	}
}

func TestRunUntilEventReportsNoEventOnTimeout(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: time.Second}

	var out bytes.Buffer
	start := time.Now()
	err := RunUntilEvent(context.Background(), cfg, &out, io.Discard)

	if !errors.Is(err, ErrNoEvent) {
		t.Fatalf("RunUntilEvent = %v, want ErrNoEvent so a re-arm loop can tell a quiet window from an event", err)
	}
	if !strings.Contains(err.Error(), "1s") {
		t.Fatalf("err = %v, want the elapsed timeout named", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("returned after %s, want the timeout to bound the wait", elapsed)
	}
	if out.Len() != 0 {
		t.Fatalf("out = %q, want stdout to carry events only, never the timeout notice", out.String())
	}
}

func TestRunUntilEventReportsInterruptionOnContextCancel(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "herdr-calls")
	faketool.Herdr{Hang: []string{"workspace list"}, Log: callLog, LogCommands: []string{"workspace list"}}.Install(t, faketool.Bin(t))

	home := t.TempDir()
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunUntilEvent(ctx, cfg, &bytes.Buffer{}, io.Discard) }()

	waitForHerdrCalls(t, callLog, "workspace list", 1)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("RunUntilEvent = %v, want ErrInterrupted: a generic cancellation is not a no-event window", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunUntilEvent did not return after context cancellation")
	}
}

func TestRunUntilEventReportsAParentDeadlineAsInterruption(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "herdr-calls")
	faketool.Herdr{Hang: []string{"workspace list"}, Log: callLog, LogCommands: []string{"workspace list"}}.Install(t, faketool.Bin(t))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := RunUntilEvent(ctx, Config{Home: t.TempDir(), PollInterval: time.Hour, StaleThreshold: time.Minute}, &bytes.Buffer{}, io.Discard)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("RunUntilEvent returned %v, want ErrInterrupted for a parent deadline", err)
	}
	if errors.Is(err, ErrNoEvent) {
		t.Fatal("RunUntilEvent returned ErrNoEvent for a parent deadline without Config.Timeout")
	}
}

// An explicit replacement cause (what ownership.TakeoverRequested() feeds the
// watch context) must surface as ErrReplaced all the way out of the runner, not
// as a generic interruption or a no-event result.
func TestRunReportsReplacementOnTakeoverCause(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "herdr-calls")
	faketool.Herdr{Hang: []string{"workspace list"}, Log: callLog, LogCommands: []string{"workspace list"}}.Install(t, faketool.Bin(t))

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Minute}, &bytes.Buffer{}, io.Discard)
	}()

	waitForHerdrCalls(t, callLog, "workspace list", 1)
	cancel(ErrReplaced)

	select {
	case err := <-done:
		if !errors.Is(err, ErrReplaced) {
			t.Fatalf("Run returned %v, want ErrReplaced for an explicit takeover cause", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after a replacement cause")
	}
}

func TestRunUntilEventReportsReplacementOnTakeoverCause(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "herdr-calls")
	faketool.Herdr{Hang: []string{"workspace list"}, Log: callLog, LogCommands: []string{"workspace list"}}.Install(t, faketool.Bin(t))

	home := t.TempDir()
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 10 * time.Second}

	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunUntilEvent(ctx, cfg, &bytes.Buffer{}, io.Discard) }()

	waitForHerdrCalls(t, callLog, "workspace list", 1)
	cancel(ErrReplaced)

	select {
	case err := <-done:
		if !errors.Is(err, ErrReplaced) {
			t.Fatalf("RunUntilEvent = %v, want ErrReplaced for an explicit takeover cause", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunUntilEvent did not return after a replacement cause")
	}
}

// The same precedence at the other boundary: an arm that found something actionable still belongs to
// a watcher that has since been replaced, so the replacement is what the caller hears about.
func TestRunUntilEventDoesNotDeliverAnArmObservationAfterCancellation(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "done")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip},
		state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
			StatusChangedFor: "working", LastReportState: state.ReportWorking})

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	oldAfterArmTick := afterArmTick
	t.Cleanup(func() { afterArmTick = oldAfterArmTick })
	arms := 0
	afterArmTick = func() {
		arms++
		if arms == 2 {
			cancel(ErrReplaced)
		}
	}

	var out bytes.Buffer
	err := RunUntilEvent(ctx, Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour, Timeout: 10 * time.Second}, &out, io.Discard)
	if !errors.Is(err, ErrReplaced) {
		t.Fatalf("RunUntilEvent = %v, want ErrReplaced when cancellation follows the arm-time observation", err)
	}
	if out.Len() != 0 {
		t.Fatalf("out = %q, want no delivery from a watcher that has been replaced", out.String())
	}
}

func TestRunUntilEventDoesNotDeliverAnEventAfterCancellationAtTheTickBoundary(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	oldAfterWatchTick := afterWatchTick
	t.Cleanup(func() { afterWatchTick = oldAfterWatchTick })
	ticks := 0
	afterWatchTick = func() {
		ticks++
		if ticks == 2 {
			setStatus(t, statusFile, "done")
		}
		if ticks == 3 {
			cancel(ErrReplaced)
		}
	}

	var out bytes.Buffer
	err := RunUntilEvent(ctx, Config{Home: home, PollInterval: 10 * time.Millisecond, Timeout: 10 * time.Second}, &out, io.Discard)
	if !errors.Is(err, ErrReplaced) {
		t.Fatalf("RunUntilEvent = %v, want ErrReplaced when cancellation follows event collection", err)
	}
	if out.Len() != 0 {
		t.Fatalf("out = %q, want no event after cancellation at the tick boundary", out.String())
	}
}

func TestRunUntilEventFailsWhenHerdrUnreachable(t *testing.T) {
	faketool.Herdr{Unreachable: true}.Install(t, faketool.Bin(t))

	err := RunUntilEvent(context.Background(), Config{Home: t.TempDir(), PollInterval: time.Second, StaleThreshold: time.Minute}, &bytes.Buffer{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "herdr unreachable") {
		t.Fatalf("got err %v, want herdr unreachable", err)
	}
	if errors.Is(err, ErrNoEvent) {
		t.Fatal("a failed watcher reported ErrNoEvent, which a caller reads as a quiet fleet rather than a broken watcher")
	}
}

func TestRunUntilEventReportsNoEventWhenConnectHangs(t *testing.T) {
	faketool.Herdr{Hang: []string{"workspace list"}}.Install(t, faketool.Bin(t))

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
	faketool.Herdr{
		Workspaces: []faketool.HerdrWorkspace{{ID: "wA", Label: "watch", Tabs: []faketool.HerdrTab{{ID: "wA:tA", Label: "task-1", Pane: "p1"}}}},
		Responses:  []faketool.HerdrResponse{{Command: "workspace list", Stdout: `{"id":"cli:1","result":{"workspaces":[]}}`}},
		Hang:       []string{"pane get"},
	}.Install(t, faketool.Bin(t))

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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

func TestRunUntilEventReportsInterruptionWhenTheArmProbeIsCanceled(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "pane-get-calls")
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{{Command: "workspace list", Stdout: `{"id":"cli:1","result":{"workspaces":[]}}`}},
		Hang:      []string{"pane get"},
		Log:       callLog, LogCommands: []string{"pane get"},
	}.Install(t, faketool.Bin(t))
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunUntilEvent(ctx, Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}, &bytes.Buffer{}, io.Discard)
	}()
	waitForPaneGets(t, callLog, 1)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, ErrInterrupted) || errors.Is(err, ErrArmFailed) {
			t.Fatalf("RunUntilEvent = %v, want ErrInterrupted and not ErrArmFailed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunUntilEvent did not exit after canceling its arm probe")
	}
}

func TestRunUntilEventReportsReplacementWhenTheArmProbeIsCanceled(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "pane-get-calls")
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{{Command: "workspace list", Stdout: `{"id":"cli:1","result":{"workspaces":[]}}`}},
		Hang:      []string{"pane get"},
		Log:       callLog, LogCommands: []string{"pane get"},
	}.Install(t, faketool.Bin(t))
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunUntilEvent(ctx, Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}, &bytes.Buffer{}, io.Discard)
	}()
	waitForPaneGets(t, callLog, 1)
	cancel(ErrReplaced)

	select {
	case err := <-done:
		if !errors.Is(err, ErrReplaced) || errors.Is(err, ErrArmFailed) {
			t.Fatalf("RunUntilEvent = %v, want ErrReplaced and not ErrArmFailed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunUntilEvent did not exit after replacing its arm probe")
	}
}

func TestRunUntilEventFailsToArmWhenATaskCannotBeProbed(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, paneGoneStatus)
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	// No cfg.Timeout: ErrArmFailed is a logical result of the probe, not a wall-clock one, and
	// racing it against a --timeout clock is what let a loaded box report ErrNoEvent instead.
	cfg := Config{Home: home, PollInterval: 10 * time.Millisecond, StaleThreshold: time.Hour}

	done := make(chan error, 1)
	go func() { done <- RunUntilEvent(context.Background(), cfg, &bytes.Buffer{}, io.Discard) }()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunUntilEvent did not exit after an unprobeable worker")
	}
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

// atqamz/hand#455: a task the supervisor tore down deliberately must never fail an arm, whether the
// teardown mark is already on the histories snapshot armProbe reads (this test) or commits after it
// (TestProbeAllTasksClosesTheRaceWhenATeardownCommitsAfterTheHistoriesSnapshot, below).
func TestProbeAllTasksSkipsATornDownAttemptWithoutProbingItsPane(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "pane-get-calls")
	t.Setenv("HERDR_CALL_LOG", callLog)
	faketool.Herdr{
		Workspaces: []faketool.HerdrWorkspace{{ID: "wA", Label: "watch", Tabs: []faketool.HerdrTab{{ID: "wA:t1", Label: "task-1", Pane: "p1"}}}},
		PaneStatus: "working",
		Log:        callLog, LogCommands: []string{"pane get"},
	}.Install(t, faketool.Bin(t))

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}}); err != nil {
		t.Fatal(err)
	}
	// task-2's pane ("p-gone") is deliberately never registered with the fake: if probeAllTasks ever
	// called PaneGetContext for it, the call log below would catch it.
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-2", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p-gone"}}); err != nil {
		t.Fatal(err)
	}
	_, attempt2 := readTaskAttempt(t, home, "task-2")
	if err := state.SetAttemptTeardownResourceState(home, "task-2", attempt2.ID, state.AttemptRunning, "herdr", state.TeardownResourceReleasing); err != nil {
		t.Fatal(err)
	}

	histories, err := state.ListOpenHistories(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := probeAllTasks(context.Background(), home, herdr.NewClient(), nil, histories); err != nil {
		t.Fatalf("probeAllTasks = %v, want nil: a torn-down attempt must not fail the arm", err)
	}
	data, _ := os.ReadFile(callLog)
	if strings.Contains(string(data), "p-gone") {
		t.Fatalf("pane-get calls = %q, want the torn-down attempt's pane never probed", data)
	}
	if !strings.Contains(string(data), "p1") {
		t.Fatalf("pane-get calls = %q, want the remaining open task still probed", data)
	}
}

// The failure mode reported in atqamz/hand#455: teardown's herdr-release mark commits after the
// histories snapshot was taken but before this task's probe runs. The snapshot has to be taken while
// task-2 was still genuinely open and unmarked - a pre-torn-down fixture would not exercise this.
func TestProbeAllTasksClosesTheRaceWhenATeardownCommitsAfterTheHistoriesSnapshot(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "pane-get-calls")
	t.Setenv("HERDR_CALL_LOG", callLog)
	faketool.Herdr{
		Workspaces: []faketool.HerdrWorkspace{{
			ID: "wA", Label: "watch", Tabs: []faketool.HerdrTab{
				{ID: "wA:t1", Label: "task-1", Pane: "p1"},
				{ID: "wA:t2", Label: "task-2", Pane: "p-gone"},
			},
		}},
		PaneStatus: "working",
		// Only p-gone's probe answers not-found - real herdr's shape for a closed pane, per
		// internal/runtime/resources.go's isHerdrNotFound: a JSON error envelope on stdout, exit 0.
		Responses: []faketool.HerdrResponse{{
			Command: "pane get", Args: []string{"p-gone"},
			Stdout: `{"id":"cli:pane:get","error":{"code":"pane_not_found","message":"pane p-gone not found"}}`,
		}},
		Log: callLog, LogCommands: []string{"pane get"},
	}.Install(t, faketool.Bin(t))

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}}); err != nil {
		t.Fatal(err)
	}
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-2", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p-gone"}}); err != nil {
		t.Fatal(err)
	}

	// The stale snapshot: both tasks genuinely open, task-2's attempt genuinely running and carrying
	// no teardown mark yet.
	histories, err := state.ListOpenHistories(home)
	if err != nil {
		t.Fatal(err)
	}

	// The race: a concurrent `hand teardown task-2` commits its herdr-release mark after the snapshot
	// above was taken, using the same primitive teardown.go itself calls.
	_, attempt2 := readTaskAttempt(t, home, "task-2")
	if err := state.SetAttemptTeardownResourceState(home, "task-2", attempt2.ID, state.AttemptRunning, "herdr", state.TeardownResourceReleasing); err != nil {
		t.Fatal(err)
	}

	if err := probeAllTasks(context.Background(), home, herdr.NewClient(), nil, histories); err != nil {
		t.Fatalf("probeAllTasks = %v, want nil: a teardown that commits after the histories snapshot must not fail the arm", err)
	}
	data, _ := os.ReadFile(callLog)
	if !strings.Contains(string(data), "p1") {
		t.Fatalf("pane-get calls = %q, want the remaining open task still watched", data)
	}
}

// A pane missing for an open task that is not being torn down is still a real arm failure, even
// through the same not-found path the race above must forgive: attemptStillNeedsArm's re-read has to
// tell the two apart rather than forgiving every not-found probe.
func TestProbeAllTasksFailsWhenAnOpenTasksPaneIsGoneWithoutATeardown(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, paneGoneStatus)
	writeFakeHerdr(t, statusFile)

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}}); err != nil {
		t.Fatal(err)
	}
	histories, err := state.ListOpenHistories(home)
	if err != nil {
		t.Fatal(err)
	}

	err = probeAllTasks(context.Background(), home, herdr.NewClient(), nil, histories)
	if !errors.Is(err, ErrArmFailed) {
		t.Fatalf("probeAllTasks = %v, want ErrArmFailed: no teardown explains this task's missing pane", err)
	}
	if !strings.Contains(err.Error(), "task-1") {
		t.Fatalf("err = %q, want it to name task-1", err.Error())
	}
}

// Unchanged branches: a live pane arms normally, and a provisioning attempt is still skipped without
// ever being probed.
func TestProbeAllTasksArmsForAnOpenTaskWithALivePane(t *testing.T) {
	writeFakeHerdrForPanes(t, "p1")
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}}); err != nil {
		t.Fatal(err)
	}
	histories, err := state.ListOpenHistories(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := probeAllTasks(context.Background(), home, herdr.NewClient(), nil, histories); err != nil {
		t.Fatalf("probeAllTasks = %v, want nil for a live pane", err)
	}
}

func TestProbeAllTasksSkipsAProvisioningAttemptWithoutProbingItsPane(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "pane-get-calls")
	t.Setenv("HERDR_CALL_LOG", callLog)
	faketool.Herdr{Log: callLog, LogCommands: []string{"pane get"}}.Install(t, faketool.Bin(t))

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptProvisioning}); err != nil {
		t.Fatal(err)
	}
	histories, err := state.ListOpenHistories(home)
	if err != nil {
		t.Fatal(err)
	}

	if err := probeAllTasks(context.Background(), home, herdr.NewClient(), nil, histories); err != nil {
		t.Fatalf("probeAllTasks = %v, want nil: a provisioning attempt has no pane contract yet", err)
	}
	if data, readErr := os.ReadFile(callLog); readErr == nil && len(data) != 0 {
		t.Fatalf("pane-get calls = %q, want none for a provisioning attempt", data)
	}
}

// Holds resume to what the live path already does: a free-text line appended after a real report
// explains nothing, so it must not erase the report it follows. Reading it back as "never reported"
// turns the next quiet pane into idle-unreported, replacing the explanation with a bare stop.
func TestTickResumesTheLastStateAfterATrailingMalformedLine(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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

	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.LastReportState != state.ReportNeedsDecision || !strings.Contains(attempt.LastReportNote, "which base branch?") {
		t.Fatalf("LastReportState/Note = %q/%q, want the worker's own question left intact", attempt.LastReportState, attempt.LastReportNote)
	}
}

// Keys tracking on identity rather than on ID. A teardown and respawn between two ticks is a
// different task, and inheriting the previous run's TaskState suppresses the new one's verified done
// for good: syncTaskState writes that inherited done_verified onto the fresh JSON.
func TestTickReseedsARespawnedTaskID(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindScout}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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

	firstRun, err := state.ActiveAttempt(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", firstRun.ID, firstRun.Lifecycle, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	// Same hazard as a surviving report channel, one layer in, so the scout's report.md and the
	// volatile wake log both go with the torn-down run - a surviving log would replay as this
	// run's, and a surviving deliverable would misreport what the second run actually produced.
	if err := os.Remove(reportMD); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(state.ReportPath(home, "task-1")); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskReportState(home, "task-1", 0, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReopenTask(home, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Herdr: state.Herdr{PaneID: "p1"},
		LaunchConfirmedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	respawned, err := state.ActiveAttempt(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionAttempt(home, respawned.ID, state.AttemptProvisioning, state.AttemptRunning); err != nil {
		t.Fatal(err)
	}

	// One tick for the watcher to notice the reopened task's new attempt identity and reseed
	// its tracking, before either report exists for this run.
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: round two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, io.Discard)
	if !strings.Contains(buf.String(), "reported-done task-1: round two") {
		t.Fatalf("output = %q, want the respawned task's own done report read from offset 0", buf.String())
	}

	_, respawnedAttempt := readTaskAttempt(t, home, "task-1")
	if respawnedAttempt.DoneVerified {
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

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

		_, attempt := readTaskAttempt(t, home, "task-1")
		if attempt.LastReportState != tc.wantState {
			t.Fatalf("LastReportState = %q after %q, want state %s", attempt.LastReportState, tc.line, tc.wantState)
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
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
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip,
		CreatedAt: dwelling}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
		StatusChangedAt: dwelling, StatusChangedFor: "unknown"},
	)

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

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})

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

	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.LastReportState != state.ReportNeedsDecision || !strings.Contains(attempt.LastReportNote, "which base branch?") {
		t.Fatalf("LastReportState/Note = %q/%q, want the worker's question left standing", attempt.LastReportState, attempt.LastReportNote)
	}
}
