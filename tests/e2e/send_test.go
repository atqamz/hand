//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/state"
)

// Drives two `hand send` processes at one busy pane. The send lock is cross-process by construction, so no
// cmd-level test can cover it: what has to hold is that one sender waits out the other's whole retry loop
// instead of both firing their text into the same composer at once (atqamz/hand#102's lost steer).
func TestConcurrentSendsToTheSameTaskSerialize(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	seedSendTask(t, home)

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")

	dir := binDir(t)
	herdrLog := filepath.Join(t.TempDir(), "herdr-invocations.log")
	writeFakeHerdrSend(t, dir, statusDir, herdrLog)

	first := startHandBackground(t, home, "send", "task-1", "steer one", "--wait", "30s")
	second := startHandBackground(t, home, "send", "task-1", "steer two", "--wait", "30s")

	// Two pane reads prove the lock holder is past its arrival probe and inside
	// its retry loop, so freeing the composer now means the holder observed a
	// busy one first rather than never seeing one at all.
	waitForInvocations(t, herdrLog, "pane get pane-1", 2, 10*time.Second)
	setPaneStatus(t, statusDir, "pane-1", "idle")

	for name, b := range map[string]*backgroundHand{"first": first, "second": second} {
		got := b.waitForExit(t, 40*time.Second, "the composer freeing")
		if got.code != 0 || !strings.Contains(got.stdout, "result: sent\n") {
			t.Fatalf("%s sender: exit %d, stdout %q, stderr %q", name, got.code, got.stdout, got.stderr)
		}
	}

	data, err := os.ReadFile(herdrLog)
	if err != nil {
		t.Fatal(err)
	}
	log := strings.Split(strings.TrimSpace(string(data)), "\n")

	// One switch in the sender column across the whole log is the serialization:
	// a sender that reappears after the other started has been polling the same
	// composer concurrently, whatever the two exit codes said.
	switches := 0
	seen := map[string]bool{}
	prev := ""
	for _, line := range log {
		sender := strings.Fields(line)[0]
		if sender != prev {
			if seen[sender] {
				t.Fatalf("sender %s resumed after the other sender started, so the two overlapped:\n%s",
					sender, strings.Join(log, "\n"))
			}
			switches++
			seen[sender] = true
			prev = sender
		}
	}
	if switches != 2 {
		t.Fatalf("saw %d sender blocks, want one per sender:\n%s", switches, strings.Join(log, "\n"))
	}

	// Both steers still have to land, each with its own submit: serializing them
	// must not collapse, drop, or merge one into the other.
	for _, want := range []string{"pane send-text steer one", "pane send-text steer two"} {
		if got := strings.Count(string(data), want); got != 1 {
			t.Fatalf("%q appears %d times, want exactly once:\n%s", want, got, strings.Join(log, "\n"))
		}
	}
	if got := strings.Count(string(data), "pane send-keys Enter"); got != 2 {
		t.Fatalf("submitted %d times, want one Enter per steer:\n%s", got, strings.Join(log, "\n"))
	}
}

// Covers the outcome an operator or a calling agent actually sees when a composer never frees: no durable
// send exists because no external mutation began, and a later independent steer can proceed safely.
func TestSendDoesNotCreateRecordBeforeComposerMutation(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	seedSendTask(t, home)

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")

	dir := binDir(t)
	herdrLog := filepath.Join(t.TempDir(), "herdr-invocations.log")
	writeFakeHerdrSend(t, dir, statusDir, herdrLog)

	got := runHand(t, home, "send", "task-1", "stop and wait for review", "--wait", "400ms")
	assertInvocation(t, got, 6, "composer stayed busy for 400ms; no external message mutation occurred")

	_, _ = readTaskAttempt(t, home, "task-1")
	sends, err := state.ListSends(home, "task-1")
	if err != nil || len(sends) != 0 {
		t.Fatalf("sends=%+v err=%v, want no durable send before mutation", sends, err)
	}

	setPaneStatus(t, statusDir, "pane-1", "idle")
	delivered := runHand(t, home, "send", "task-1", "different steer entirely")
	if delivered.code != 0 || !strings.Contains(delivered.stdout, "result: sent\n") {
		t.Fatalf("send after the composer freed: exit %d, stdout %q, stderr %q",
			delivered.code, delivered.stdout, delivered.stderr)
	}

	sends, err = state.ListSends(home, "task-1")
	if err != nil || len(sends) != 1 || sends[0].State != state.SendSubmitted {
		t.Fatalf("sends=%+v err=%v, want one submitted durable send", sends, err)
	}
}

func TestSendProcessDeathAfterTextSideEffectDoesNotResend(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	seedSendTask(t, home)
	logPath := filepath.Join(t.TempDir(), "pane.log")
	dir := binDir(t)
	installCrashSendHerdr(t, dir, logPath, "pane send-text")

	first := startHandBackground(t, home, "send", "task-1", "crash after text")
	waitForInvocation(t, logPath+".invocations", "pane send-text", 10*time.Second)
	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	first.waitForExit(t, 10*time.Second, "process death after Text side effect")

	second := runHand(t, home, "send", "task-1", "replacement")
	if second.code != 7 || !strings.Contains(errorMessage(t, second.stderr), "do not blindly retry") {
		t.Fatalf("recovery send = %+v, want exit 7 with uncertainty", second)
	}
	assertSendSideEffects(t, logPath, "crash after text", 0)
	sends, err := state.ListSends(home, "task-1")
	if err != nil || len(sends) != 1 || sends[0].State != state.SendUncertain {
		t.Fatalf("sends=%+v err=%v, want one uncertain send after restart", sends, err)
	}
}

func TestSendProcessDeathAfterEnterSideEffectDoesNotResend(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	seedSendTask(t, home)
	logPath := filepath.Join(t.TempDir(), "pane.log")
	dir := binDir(t)
	installCrashSendHerdr(t, dir, logPath, "pane send-keys")

	first := startHandBackground(t, home, "send", "task-1", "crash after enter")
	waitForInvocation(t, logPath+".invocations", "pane send-text", 10*time.Second)
	waitForInvocation(t, logPath+".invocations", "pane send-keys", 10*time.Second)
	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	first.waitForExit(t, 10*time.Second, "process death after Enter side effect")

	second := runHand(t, home, "send", "task-1", "replacement")
	if second.code != 7 || !strings.Contains(errorMessage(t, second.stderr), "do not blindly retry") {
		t.Fatalf("recovery send = %+v, want exit 7 with uncertainty", second)
	}
	assertSendSideEffects(t, logPath, "crash after enter", 1)
	sends, err := state.ListSends(home, "task-1")
	if err != nil || len(sends) != 1 || sends[0].State != state.SendUncertain {
		t.Fatalf("sends=%+v err=%v, want one uncertain send after restart", sends, err)
	}
}

func TestWatcherRestartAfterResumeSideEffectDoesNotResend(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	seedSendTask(t, home)
	_, attempt := readTaskAttempt(t, home, "task-1")
	attempt.UsageLimitEpisode = 1
	attempt.UsageLimitRetryAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if err := state.UpdateAttempt(home, attempt); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "pane.log")
	dir := binDir(t)
	installCrashSendHerdrWithRead(t, dir, logPath, "pane send-text")

	first := startHandBackground(t, home, "watch", "--poll", "20ms")
	waitForInvocation(t, logPath+".invocations", "pane send-text", 10*time.Second)
	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	first.waitForExit(t, 10*time.Second, "watcher process death after resume Text side effect")

	second := startHandBackground(t, home, "watch", "--poll", "20ms")
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(logPath)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if strings.Count(string(data), "Your previous turn stopped") > 1 {
			t.Fatalf("watcher resent an unresolved resume after restart: %q", data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	second.interrupt(t, 10*time.Second)
	assertSendSideEffects(t, logPath, "Your previous turn stopped", 0)
	sends, err := state.ListSends(home, "task-1")
	if err != nil || len(sends) != 1 || sends[0].State != state.SendUncertain {
		t.Fatalf("sends=%+v err=%v, want one uncertain watcher send after restart", sends, err)
	}
}

func installCrashSendHerdr(t *testing.T, dir, logPath, hang string) {
	installCrashSendHerdrWithRead(t, dir, logPath, hang)
}

func installCrashSendHerdrWithRead(t *testing.T, dir, logPath, hang string) {
	t.Helper()
	faketool.Herdr{
		Workspaces: []faketool.HerdrWorkspace{{ID: "workspace-1", Label: "demo", Tabs: []faketool.HerdrTab{{ID: "tab-1", Label: "task-1", Pane: "pane-1"}}}},
		PaneAgent:  "claude", PaneStatus: "done", PaneReadOut: "Claude usage limit reached.", Hang: []string{hang},
		MutateBeforeHang: true, TextLog: logPath, KeyLog: logPath,
		Log:         logPath + ".invocations",
		LogCommands: []string{"pane get", "pane send-text", "pane send-keys"},
	}.Install(t, dir)
}

func assertSendSideEffects(t *testing.T, logPath, message string, enter int) {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), message); got != 1 {
		t.Fatalf("Text side effects = %d, want one %q: %q", got, message, data)
	}
	if got := strings.Count(string(data), "Enter"); got != enter {
		t.Fatalf("Enter side effects = %d, want %d: %q", got, enter, data)
	}
}

func seedSendTask(t *testing.T, home string) {
	t.Helper()
	writeTaskAttempt(t, home, state.Task{
		ID: "task-1", Project: "demo", Kind: state.KindShip,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, state.Attempt{
		Lifecycle:         state.AttemptRunning,
		Worktree:          filepath.Join(home, "wt-1"),
		Herdr:             state.Herdr{PaneID: "pane-1"},
		LaunchSubmittedAt: time.Now().UTC().Format(time.RFC3339),
		LaunchConfirmedAt: time.Now().UTC().Format(time.RFC3339),
	})
}
