//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/watcher"
)

// Drives atqamz/hand#73 end to end with real processes, which is the only way to prove the contract
// that matters: a second watcher is refused rather than added to the fleet's pool of pollers, and
// --takeover replaces a genuinely live incumbent that has to be cooperatively requested and reaped.
func TestWatchIsASingletonPerFleetHome(t *testing.T) {
	home := seedOneTaskHome(t)

	first := startHandBackground(t, home, "watch", "--poll", "30ms")
	firstRec := waitForCoherentOwner(t, home, first.cmd.Process.Pid)

	refused := runHand(t, home, "watch", "--poll", "30ms")
	if refused.code != 3 {
		t.Fatalf("second watch: exit %d, want 3 (stderr %q)", refused.code, refused.stderr)
	}
	if !strings.Contains(refused.stderr, "already attached") {
		t.Fatalf("second watch stderr %q, want it to say a watcher is already attached", refused.stderr)
	}
	if !strings.Contains(refused.stderr, "pid "+strconv.Itoa(first.cmd.Process.Pid)) {
		t.Fatalf("second watch stderr %q, want it to name the incumbent pid %d", refused.stderr, first.cmd.Process.Pid)
	}

	second := startHandBackground(t, home, "watch", "--poll", "30ms", "--takeover")
	// The displaced incumbent exits 9 / watch-replaced - an explicit Hand takeover,
	// distinguishable from generic SIGTERM interruption (exit 8).
	if got := first.waitForExit(t, 10*time.Second, "--takeover by a second watcher"); got.code != 9 {
		t.Fatalf("displaced watch: exit %d, want 9 watch-replaced (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	secondRec := waitForCoherentOwner(t, home, second.cmd.Process.Pid)
	if secondRec.Generation == firstRec.Generation {
		t.Fatalf("replacement published the incumbent's generation %q, want a fresh generation", secondRec.Generation)
	}

	// Ownership then belongs to the replacement, a full owner rather than a squatter: it refuses a third
	// watcher exactly as the incumbent it displaced did.
	third := runHand(t, home, "watch", "--poll", "30ms")
	if third.code != 3 || !strings.Contains(third.stderr, "pid "+strconv.Itoa(second.cmd.Process.Pid)) {
		t.Fatalf("third watch: exit %d stderr %q, want 3 naming pid %d", third.code, third.stderr, second.cmd.Process.Pid)
	}

	second.interrupt(t, 10*time.Second)

	// Ownership outlives no watcher: the lock has to be free again the moment the last one exits, or the next
	// hand watch inherits a home it can never watch.
	after := runHand(t, home, "watch", "--until-event", "--poll", "30ms", "--timeout", "100ms")
	if after.code != 4 {
		t.Fatalf("watch after the last watcher exited: exit %d, want 4 (no event), not an ownership refusal (stderr %q)", after.code, after.stderr)
	}
}

// The strand test: a watcher killed with SIGKILL never runs its own release, so
// the pid file and routing record it leaves behind are stale. The kernel drops
// its flock anyway and the next watcher starts with no operator intervention.
func TestWatchStartsOverACrashedWatchersStaleFiles(t *testing.T) {
	home := seedOneTaskHome(t)

	crashed := startHandBackground(t, home, "watch", "--poll", "30ms")
	waitForCoherentOwner(t, home, crashed.cmd.Process.Pid)

	if err := crashed.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	crashed.reaping = true
	_ = crashed.cmd.Wait()

	stale, err := os.ReadFile(watcher.OwnerPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stale)) != strconv.Itoa(crashed.cmd.Process.Pid) {
		t.Fatalf("got %q in %s, want the killed watcher's pid still recorded - without it this test is not about a stale lock", stale, watcher.OwnerPath(home))
	}

	got := runHand(t, home, "watch", "--until-event", "--poll", "30ms", "--timeout", "100ms")
	if got.code != 4 {
		t.Fatalf("watch after a SIGKILLed watcher: exit %d, want 4 (no event) - leftover pid/routing files must not lock this home out of watching itself (stderr %q)", got.code, got.stderr)
	}
}

// Gives the watcher one task to poll, so the coherent-owner wait is waiting on a loop that has really
// started rather than on a process that has merely been forked.
func seedOneTaskHome(t *testing.T) string {
	t.Helper()
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip,
		CreatedAt: time.Now().UTC().Format(time.RFC3339)}, state.Attempt{Lifecycle: state.AttemptRunning,
		Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"}})

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")
	writeFakeHerdrWatch(t, binDir(t), statusDir, filepath.Join(t.TempDir(), "herdr-invocations.log"))
	return home
}
