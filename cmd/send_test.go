package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/state"
)

func setupSendHome(t *testing.T, h faketool.Herdr) string {
	t.Helper()
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	if len(h.Workspaces) == 0 {
		h.Workspaces = []faketool.HerdrWorkspace{{ID: "wA", Tabs: []faketool.HerdrTab{{
			ID: "wA:tB", Pane: "wA:pB",
		}}}}
	}
	bin := faketool.Bin(t)
	h.Install(t, bin)
	return home
}

func TestSendHappyPathWhenIdle(t *testing.T) {
	// "pane send-text"/"pane send-keys" are void commands: real success is empty stdout, not this envelope
	// (callVoid's doc comment, client.go). callVoid only checks env.Error, which is nil here, so the extra
	// body is harmless and this still exercises the real success path.
	home := setupSendHome(t, faketool.Herdr{PaneStatus: "idle"})
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "hello worker"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"id: task-1\n", "result: sent\n", "chars: 12\n"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output = %q, want field %q", out.String(), want)
		}
	}
}

func TestSendRefusesUnconfirmedProvisioningAttempt(t *testing.T) {
	home := setupSendHome(t, faketool.Herdr{PaneStatus: "idle"})
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1"}, state.Attempt{Lifecycle: state.AttemptProvisioning, Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "hello worker"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want exit code 3", err)
	}
	if !strings.Contains(err.Error(), "confirmed running attempt") {
		t.Fatalf("got err %v, want unconfirmed-attempt refusal", err)
	}
}

func TestSendFailsWhenPaneNotFound(t *testing.T) {
	// "pane get" is a query command (call(), client.go); call() checks env.Error before runErr, so this fake
	// would behave identically without the exit 1 - kept only because it is a plausible real exit status for
	// a failed query, not because call() requires it.
	home := setupSendHome(t, faketool.Herdr{Responses: []faketool.HerdrResponse{{
		Command: "pane get",
		Stdout:  "{\"id\":\"cli:1\",\"error\":{\"code\":\"pane_not_found\",\"message\":\"no such pane\"}}",
		Exit:    1,
	}}})
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "wA:gone"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "hello"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got err %v, want pane not found", err)
	}
}

func TestSendWaitsWhileBusyThenSends(t *testing.T) {
	// Same send-text/send-keys envelope simplification as
	// TestSendHappyPathWhenIdle above.
	home := setupSendHome(t, faketool.Herdr{PaneStatusSequence: []string{"working", "idle"}})
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestSendReadsMessageFromFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "sent.log")
	home := setupSendHome(t, faketool.Herdr{PaneStatus: "idle", TextLog: logPath})
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	msgPath := filepath.Join(t.TempDir(), "message.txt")
	if err := os.WriteFile(msgPath, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "--file", msgPath})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(string(got), "\n") != "line one\nline two" {
		t.Fatalf("sent text = %q, want file contents with trailing newline trimmed", got)
	}
}

func TestSendRejectsFileAndMessageTogether(t *testing.T) {
	setupSendHome(t, faketool.Herdr{})

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "hello", "--file", "/does/not/matter"})
	assertExitCode2(t, cmd.Execute())
}

func TestSendRequiresMessageOrFile(t *testing.T) {
	setupSendHome(t, faketool.Herdr{})

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1"})
	assertExitCode2(t, cmd.Execute())
}

func TestSendRejectsEmptyFileFlag(t *testing.T) {
	// An unset shell variable expanding into --file is neither a message source
	// nor two positional arguments, so it has to land on the usage error rather
	// than on args[1].
	setupSendHome(t, faketool.Herdr{})

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "--file", ""})
	assertExitCode2(t, cmd.Execute())
}

func TestSendFailsWhenPaneDisappearsDuringWait(t *testing.T) {
	// The pane answers "working" once, then stops answering at all: no retry can
	// succeed, so this must not take the retryable exit-6 busy-composer path.
	home := setupSendHome(t, faketool.Herdr{
		PaneStatusSequence: []string{"working", "pane-gone"},
	})
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "hello", "--wait", "5s"})
	err := cmd.Execute()
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		t.Fatalf("got ExitError code %d, want a plain exit-1 error", exitErr.Code)
	}
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got err %v, want pane not found", err)
	}

	active, err := state.ActiveAttempt(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if active.SendUndeliveredMessage != "" {
		t.Fatalf("SendUndeliveredMessage = %q, want no trace for an unreachable pane", active.SendUndeliveredMessage)
	}
}

func TestSendRecordsUndeliveredMessageWhenSubmitFails(t *testing.T) {
	// The text reached the composer but was never submitted - a steer with no
	// evidence it landed, so it owes the same trace the wait bound does.
	home := setupSendHome(t, faketool.Herdr{
		PaneStatus: "idle",
		Responses: []faketool.HerdrResponse{{
			Command: "pane send-keys",
			Stdout:  "{\"id\":\"cli:1\",\"error\":{\"code\":\"send_failed\",\"message\":\"keystroke rejected\"}}",
			Exit:    1,
		}},
	})
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "stop and wait for review"})
	err := cmd.Execute()
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		t.Fatalf("got ExitError code %d, want a plain exit-1 error", exitErr.Code)
	}
	if err == nil || !strings.Contains(err.Error(), "submit message failed") {
		t.Fatalf("got err %v, want the submit failure", err)
	}

	active, err := state.ActiveAttempt(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if active.SendUndeliveredMessage != "stop and wait for review" {
		t.Fatalf("SendUndeliveredMessage = %q, want the unsubmitted message", active.SendUndeliveredMessage)
	}
	if active.SendUndeliveredAt == "" {
		t.Fatal("SendUndeliveredAt not recorded")
	}
}

func TestSendRecordsUndeliveredMessageWhenTextSendFails(t *testing.T) {
	home := setupSendHome(t, faketool.Herdr{
		PaneStatus: "idle",
		Responses: []faketool.HerdrResponse{{
			Command: "pane send-text",
			Stdout:  "{\"id\":\"cli:1\",\"error\":{\"code\":\"send_failed\",\"message\":\"text rejected\"}}",
			Exit:    1,
		}},
	})
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "stop and wait for review"})
	err := cmd.Execute()
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		t.Fatalf("got ExitError code %d, want the underlying operational failure", exitErr.Code)
	}
	if err == nil || !strings.Contains(err.Error(), "send message failed") {
		t.Fatalf("got err %v, want the text-send failure", err)
	}

	active, err := state.ActiveAttempt(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if active.SendUndeliveredMessage != "stop and wait for review" || active.SendUndeliveredAt == "" {
		t.Fatalf("undelivered trace = %q/%q, want the failed message and timestamp", active.SendUndeliveredMessage, active.SendUndeliveredAt)
	}
}

func TestSendRecordsUndeliveredMessageWhenComposerStaysBusy(t *testing.T) {
	// The composer never frees, so WaitComposerEmpty always exhausts --wait;
	// a short --wait keeps the test itself fast.
	home := setupSendHome(t, faketool.Herdr{PaneStatus: "working"})
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "stop and wait for review", "--wait", "300ms"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 6 {
		t.Fatalf("got %v, want ExitError code 6", err)
	}

	active, err := state.ActiveAttempt(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if active.SendUndeliveredMessage != "stop and wait for review" {
		t.Fatalf("SendUndeliveredMessage = %q, want the abandoned message", active.SendUndeliveredMessage)
	}
	if active.SendUndeliveredAt == "" {
		t.Fatal("SendUndeliveredAt not recorded")
	}
}

func TestSendClearsAPreviouslyRecordedUndeliveredSendOnSuccess(t *testing.T) {
	home := setupSendHome(t, faketool.Herdr{PaneStatus: "idle"})
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "wA:pB"}, SendUndeliveredMessage: "an earlier abandoned steer", SendUndeliveredAt: "2026-08-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	active, err := state.ActiveAttempt(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if active.SendUndeliveredMessage != "" || active.SendUndeliveredAt != "" {
		t.Fatalf("undelivered send trace not cleared: %+v", active)
	}
}

func TestSendFailsForUnknownTask(t *testing.T) {
	// send resolves the task before it ever reaches herdr, so this fake refuses every invocation instead of
	// imitating a herdr response: a regression that called herdr first would surface that message here
	// rather than the expected "not found".
	setupSendHome(t, faketool.Herdr{})

	cmd := newSendCmd()
	cmd.SetArgs([]string{"missing-task", "hello"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), `task "missing-task" not found`) {
		t.Fatalf("got err %v, want the task-not-found precondition", err)
	}
}
