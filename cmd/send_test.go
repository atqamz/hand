package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func setupSendHome(t *testing.T, herdrScript string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake herdr is a POSIX shell script, not supported on windows")
	}
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(herdrScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home
}

func TestSendHappyPathWhenIdle(t *testing.T) {
	// "pane send-text"/"pane send-keys" are void commands: real success is empty stdout, not this envelope
	// (callVoid's doc comment, client.go). callVoid only checks env.Error, which is nil here, so the extra
	// body is harmless and this still exercises the real success path.
	home := setupSendHome(t, `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pane get")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"idle"}}}'
	;;
"pane send-text")
 printf '{"id":"cli:1","result":{}}'
	;;
"pane send-keys")
 printf '{"id":"cli:1","result":{}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "hello worker"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestSendFailsWhenPaneNotFound(t *testing.T) {
	// "pane get" is a query command (call(), client.go); call() checks env.Error before runErr, so this fake
	// would behave identically without the exit 1 - kept only because it is a plausible real exit status for
	// a failed query, not because call() requires it.
	home := setupSendHome(t, `#!/bin/sh
printf '{"id":"cli:1","error":{"code":"pane_not_found","message":"no such pane"}}'
exit 1
`)
	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:gone"}}); err != nil {
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
	counterFile := filepath.Join(t.TempDir(), "calls")
	home := setupSendHome(t, `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pane get")
	n=0
	if [ -f "`+counterFile+`" ]; then n=$(cat "`+counterFile+`"); fi
	n=$((n+1))
	echo "$n" > "`+counterFile+`"
	if [ "$n" -lt 2 ]; then
		printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"working"}}}'
	else
		printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"idle"}}}'
	fi
	;;
"pane send-text")
 printf '{"id":"cli:1","result":{}}'
	;;
"pane send-keys")
 printf '{"id":"cli:1","result":{}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
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
	home := setupSendHome(t, `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pane get")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"idle"}}}'
	;;
"pane send-text")
	printf '%s\n' "$4" >> "`+logPath+`"
	printf '{"id":"cli:1","result":{}}'
	;;
"pane send-keys")
	printf '{"id":"cli:1","result":{}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
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
	setupSendHome(t, `#!/bin/sh
echo "unexpected herdr invocation: $@" >&2
exit 1
`)

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "hello", "--file", "/does/not/matter"})
	assertExitCode2(t, cmd.Execute())
}

func TestSendRequiresMessageOrFile(t *testing.T) {
	setupSendHome(t, `#!/bin/sh
echo "unexpected herdr invocation: $@" >&2
exit 1
`)

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1"})
	assertExitCode2(t, cmd.Execute())
}

func TestSendRejectsEmptyFileFlag(t *testing.T) {
	// An unset shell variable expanding into --file is neither a message source
	// nor two positional arguments, so it has to land on the usage error rather
	// than on args[1].
	setupSendHome(t, `#!/bin/sh
echo "unexpected herdr invocation: $@" >&2
exit 1
`)

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "--file", ""})
	assertExitCode2(t, cmd.Execute())
}

func TestSendFailsWhenPaneDisappearsDuringWait(t *testing.T) {
	// The pane answers "working" once, then stops answering at all: no retry can
	// succeed, so this must not take the retryable exit-6 busy-composer path.
	counterFile := filepath.Join(t.TempDir(), "calls")
	home := setupSendHome(t, `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pane get")
	n=0
	if [ -f "`+counterFile+`" ]; then n=$(cat "`+counterFile+`"); fi
	n=$((n+1))
	echo "$n" > "`+counterFile+`"
	if [ "$n" -lt 2 ]; then
		printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"working"}}}'
	else
		printf '{"id":"cli:1","error":{"code":"pane_not_found","message":"no such pane"}}'
		exit 1
	fi
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
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

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SendUndeliveredMessage != "" {
		t.Fatalf("SendUndeliveredMessage = %q, want no trace for an unreachable pane", got.SendUndeliveredMessage)
	}
}

func TestSendRecordsUndeliveredMessageWhenSubmitFails(t *testing.T) {
	// The text reached the composer but was never submitted - a steer with no
	// evidence it landed, so it owes the same trace the wait bound does.
	home := setupSendHome(t, `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pane get")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"idle"}}}'
	;;
"pane send-text")
	printf '{"id":"cli:1","result":{}}'
	;;
"pane send-keys")
	printf '{"id":"cli:1","error":{"code":"send_failed","message":"keystroke rejected"}}'
	exit 1
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
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

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SendUndeliveredMessage != "stop and wait for review" {
		t.Fatalf("SendUndeliveredMessage = %q, want the unsubmitted message", got.SendUndeliveredMessage)
	}
	if got.SendUndeliveredAt == "" {
		t.Fatal("SendUndeliveredAt not recorded")
	}
}

func TestSendRecordsUndeliveredMessageWhenComposerStaysBusy(t *testing.T) {
	// The composer never frees, so WaitComposerEmpty always exhausts --wait;
	// a short --wait keeps the test itself fast.
	home := setupSendHome(t, `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pane get")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"working"}}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "stop and wait for review", "--wait", "300ms"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 6 {
		t.Fatalf("got %v, want ExitError code 6", err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SendUndeliveredMessage != "stop and wait for review" {
		t.Fatalf("SendUndeliveredMessage = %q, want the abandoned message", got.SendUndeliveredMessage)
	}
	if got.SendUndeliveredAt == "" {
		t.Fatal("SendUndeliveredAt not recorded")
	}
}

func TestSendClearsAPreviouslyRecordedUndeliveredSendOnSuccess(t *testing.T) {
	home := setupSendHome(t, `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pane get")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"idle"}}}'
	;;
"pane send-text")
	printf '{"id":"cli:1","result":{}}'
	;;
"pane send-keys")
	printf '{"id":"cli:1","result":{}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	if err := state.Write(home, state.Task{
		ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"},
		SendUndeliveredMessage: "an earlier abandoned steer",
		SendUndeliveredAt:      "2026-08-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SendUndeliveredMessage != "" || got.SendUndeliveredAt != "" {
		t.Fatalf("undelivered send trace not cleared: %+v", got)
	}
}

func TestSendFailsForUnknownTask(t *testing.T) {
	// send resolves the task before it ever reaches herdr, so this fake refuses every invocation instead of
	// imitating a herdr response: a regression that called herdr first would surface that message here
	// rather than the expected "not found".
	setupSendHome(t, `#!/bin/sh
echo "unexpected herdr invocation: $@" >&2
exit 1
`)

	cmd := newSendCmd()
	cmd.SetArgs([]string{"missing-task", "hello"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), `task "missing-task" not found`) {
		t.Fatalf("got err %v, want the task-not-found precondition", err)
	}
}
