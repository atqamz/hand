//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/state"
	"github.com/atqamz/secondhand/internal/watcher"
)

// TestWatchIsASingletonPerFleetHome drives atqamz/secondhand#73 end to end with
// real processes, which is the only way to prove the contract that matters: a
// second watcher is refused rather than added to the fleet's pool of pollers,
// --takeover replaces a genuinely live incumbent that has to be signaled and
// reaped, and ownership then belongs to the replacement.
func TestWatchIsASingletonPerFleetHome(t *testing.T) {
	home := seedOneTaskHome(t)

	first := startHandBackground(t, home, "watch", "--poll", "30ms")
	waitForOwner(t, home, first.cmd.Process.Pid)

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
	if got := first.waitForExit(t, 10*time.Second, "--takeover by a second watcher"); got.code != 0 {
		t.Fatalf("displaced watch: exit %d, want a clean 0 (stderr %q)", got.code, got.stderr)
	}
	waitForOwner(t, home, second.cmd.Process.Pid)

	// The replacement is a full owner, not a squatter: it refuses a third watcher
	// exactly as the incumbent it displaced did.
	third := runHand(t, home, "watch", "--poll", "30ms")
	if third.code != 3 || !strings.Contains(third.stderr, "pid "+strconv.Itoa(second.cmd.Process.Pid)) {
		t.Fatalf("third watch: exit %d stderr %q, want 3 naming pid %d", third.code, third.stderr, second.cmd.Process.Pid)
	}

	second.stop(t, 10*time.Second)

	// Ownership outlives no watcher: the lock has to be free again the moment the
	// last one exits, or the next hand watch inherits a home it can never watch.
	after := runHand(t, home, "watch", "--until-event", "--poll", "30ms", "--timeout", "100ms")
	if after.code != 4 {
		t.Fatalf("watch after the last watcher exited: exit %d, want 4 (no event), not an ownership refusal (stderr %q)", after.code, after.stderr)
	}
}

// TestWatchStartsOverACrashedWatchersPidFile is the strand test: a watcher killed
// with SIGKILL never runs its own release, so the pid file it leaves behind holds
// a dead pid. The kernel drops its flock anyway, and the next watcher has to start
// with no operator intervention at all - a lock that refuses forever after a crash
// is worse than no lock.
func TestWatchStartsOverACrashedWatchersPidFile(t *testing.T) {
	home := seedOneTaskHome(t)

	crashed := startHandBackground(t, home, "watch", "--poll", "30ms")
	waitForOwner(t, home, crashed.cmd.Process.Pid)

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
		t.Fatalf("watch after a SIGKILLed watcher: exit %d, want 4 (no event) - a leftover pid file must not lock this home out of watching itself (stderr %q)", got.code, got.stderr)
	}
}

// seedOneTaskHome gives the watcher one task to poll, so waitForOwner is
// waiting on a loop that has really started rather than on a process that has
// merely been forked.
func seedOneTaskHome(t *testing.T) string {
	t.Helper()
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip,
		Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")
	writeFakeHerdrWatch(t, binDir(t), statusDir, filepath.Join(t.TempDir(), "herdr-invocations.log"))
	return home
}

func waitForOwner(t *testing.T, home string, pid int) {
	t.Helper()
	path := watcher.OwnerPath(home)
	want := strconv.Itoa(pid)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(data)) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("timed out waiting for %s to name pid %d; it holds %q", path, pid, data)
}
