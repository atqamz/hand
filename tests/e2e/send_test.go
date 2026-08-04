//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/state"
)

// Drives two `hand send` processes at one busy pane. The send lock is cross-process by construction, so no
// cmd-level test can cover it: what has to hold is that one sender waits out the other's whole retry loop
// instead of both firing their text into the same composer at once (atqamz/secondhand#102's lost steer).
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

// Covers the outcome an operator or a calling agent actually sees when a composer never frees: the
// documented exit code off the real process, and a trace of the abandoned message that outlives the process
// that tried to send it (SPECS.md, `hand send`).
func TestSendRecordsAnUndeliveredSteerAndExitsSix(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	seedSendTask(t, home)

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")

	dir := binDir(t)
	herdrLog := filepath.Join(t.TempDir(), "herdr-invocations.log")
	writeFakeHerdrSend(t, dir, statusDir, herdrLog)

	got := runHand(t, home, "send", "task-1", "stop and wait for review", "--wait", "400ms")
	assertInvocation(t, got, 6, "composer still busy after 400ms, message recorded as undelivered")

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.SendUndeliveredMessage != "stop and wait for review" || task.SendUndeliveredAt == "" {
		t.Fatalf("task row = %+v, want the abandoned message and a timestamp recorded", task)
	}

	setPaneStatus(t, statusDir, "pane-1", "idle")
	delivered := runHand(t, home, "send", "task-1", "different steer entirely")
	if delivered.code != 0 || !strings.Contains(delivered.stdout, "result: sent\n") {
		t.Fatalf("send after the composer freed: exit %d, stdout %q, stderr %q",
			delivered.code, delivered.stdout, delivered.stderr)
	}

	task, err = state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.SendUndeliveredMessage != "" || task.SendUndeliveredAt != "" {
		t.Fatalf("task row = %+v, want the undelivered trace cleared by a send that reached the pane", task)
	}
}

func seedSendTask(t *testing.T, home string) {
	t.Helper()
	if err := state.Write(home, state.Task{
		ID: "task-1", Project: "demo", Kind: state.KindShip,
		Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
}
