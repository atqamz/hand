package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/state"
)

func TestAttemptStartLaunchesAWorkerInItsOwnWorktree(t *testing.T) {
	fx := newAttemptFixture(t)
	out := fx.start()
	for _, want := range []string{"attempt: a1", "status: running", "branch: hand/t1-a1", "pane: 2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("start = %q, missing %q", out, want)
		}
	}
	call := fx.rt.lastCreate()
	wt := filepath.Join(fx.h.home, "worktrees", "t1-a1")
	if call.CWD != wt || call.Label != "hand-a1" || call.Command[len(call.Command)-1] != "Fix the login bug, commit, then stop." {
		t.Fatalf("create = %+v", call)
	}
	if !strings.HasSuffix(call.Command[0], "/claude") || call.Command[1] != "--dangerously-skip-permissions" {
		t.Fatalf("argv = %q", call.Command)
	}
	if _, err := os.Stat(filepath.Join(wt, ".git")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}
	show := fx.h.ok("attempt", "show", "a1")
	for _, want := range []string{"status: running", "agent: working", "launch: " + call.Command[0] + " --dangerously-skip-permissions --model sonnet --effort low"} {
		if !strings.Contains(show, want) {
			t.Fatalf("show = %q, missing %q", show, want)
		}
	}
	if list := fx.h.ok("attempt", "list"); !strings.Contains(list, "a1,t1,claude,sonnet,running") {
		t.Fatalf("list = %q", list)
	}
	_, errOut, code := fx.h.run("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t1")
	if code != 3 || !strings.Contains(errOut, "already has live attempt a1") {
		t.Fatalf("second start code=%d stderr=%q", code, errOut)
	}
}

func TestAttemptStartRejectsBadRoutingBeforeTouchingGit(t *testing.T) {
	fx := newAttemptFixture(t)
	_, _, code := fx.h.run("attempt", "start", "--harness", "claude", "--model", "gpt-5.5", "--effort", "low", "--prompt-file", fx.brief, "t1")
	if code != 2 {
		t.Fatalf("bad model code = %d, want 2", code)
	}
	if _, err := os.Stat(filepath.Join(fx.h.home, "worktrees")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktrees dir created: %v", err)
	}
}

func TestFailedLaunchRemovesItsWorktreeAndBranch(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.rt.srv.Handle("terminal.backend.create", func(json.RawMessage) (any, error) {
		return nil, fakeuhp.Fail{Code: "create_failed", Message: "terminal failed to start"}
	})
	_, errOut, code := fx.h.run("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t1")
	if code != 3 || !strings.Contains(errOut, "terminal failed to start") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(fx.h.home, "worktrees", "t1-a1")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktree left behind: %v", err)
	}
	if out, err := exec.Command("git", "-C", fx.repo, "rev-parse", "--verify", "--quiet", "hand/t1-a1").CombinedOutput(); err == nil {
		t.Fatalf("branch left behind: %s", out)
	}
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: failed") || !strings.Contains(show, "terminal failed to start") {
		t.Fatalf("show = %q", show)
	}
}

func TestRestartedServerInterruptsRunningAttempts(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.srv.SetGeneration("gen-2")
	show := fx.h.ok("attempt", "show", "a1")
	if !strings.Contains(show, "status: interrupted") || !strings.Contains(show, "reason: luvus server restarted") {
		t.Fatalf("show = %q", show)
	}
	if out := fx.h.ok("orient"); !strings.Contains(out, "a1 interrupted") {
		t.Fatalf("orient = %q", out)
	}
	if out := fx.start(); !strings.Contains(out, "attempt: a2") {
		t.Fatalf("restart after interrupt = %q", out)
	}
}

func TestExitedWorkerIsRecorded(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.exitAll()
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: exited") || !strings.Contains(show, "reason: terminal exited") {
		t.Fatalf("show = %q", show)
	}
}

func TestStaleLaunchingAttemptBecomesFailed(t *testing.T) {
	fx := newAttemptFixture(t)
	st, err := state.Open(filepath.Join(fx.h.home, "hand.db"), func() time.Time { return fx.h.now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAttempt(context.Background(), state.AttemptSpec{TaskID: 1, Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}}, filepath.Join(fx.h.home, "worktrees")); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	wt := filepath.Join(fx.h.home, "worktrees", "t1-a1")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := fx.rt.addShell(t, wt, "hand-a1")
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: launching") {
		t.Fatalf("fresh launching = %q", show)
	}
	fx.h.now = fx.h.now.Add(3 * time.Minute)
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: failed") || !strings.Contains(show, "launch did not finish") {
		t.Fatalf("stale launching = %q", show)
	}
	if !fx.rt.isClosed(orphan) {
		t.Fatal("a terminal left by the crashed launch is still open")
	}
}

func TestLaunchStopsTheWorkerWhenRecordingFails(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.rt.srv.Handle("terminal.backend.create", func(params json.RawMessage) (any, error) {
		res, err := fx.rt.create(params)
		st, openErr := state.Open(filepath.Join(fx.h.home, "hand.db"), func() time.Time { return fx.h.now })
		if openErr != nil {
			return nil, openErr
		}
		defer st.Close()
		if _, endErr := st.EndAttempt(context.Background(), 1, state.AttemptFailed, "raced"); endErr != nil {
			return nil, endErr
		}
		return res, err
	})
	if _, _, code := fx.h.run("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t1"); code == 0 {
		t.Fatal("start succeeded although the attempt could not be recorded")
	}
	if !gone(fx.rt.lastPID()) {
		t.Fatal("worker left running after its attempt could not be recorded")
	}
}

func TestLostCreateReplyStopsTheWorkerAndKeepsTheWorktree(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.rt.srv.Handle("terminal.backend.create", func(params json.RawMessage) (any, error) {
		if _, err := fx.rt.create(params); err != nil {
			return nil, err
		}
		return nil, fakeuhp.Drop
	})
	if _, _, code := fx.h.run("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t1"); code == 0 {
		t.Fatal("start succeeded without a create reply")
	}
	if !gone(fx.rt.lastPID()) {
		t.Fatal("worker from a lost create reply left running")
	}
	if _, err := os.Stat(filepath.Join(fx.h.home, "worktrees", "t1-a1")); err != nil {
		t.Fatalf("worktree removed after an ambiguous failure: %v", err)
	}
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: launching") {
		t.Fatalf("ambiguous launch = %q, want it left launching for sync", show)
	}
}

func TestLateWorkerAfterALostReplyIsStoppedBySync(t *testing.T) {
	fx := newAttemptFixture(t)
	late := make(chan int, 1)
	fx.rt.srv.Handle("terminal.backend.create", func(params json.RawMessage) (any, error) {
		go func() {
			time.Sleep(300 * time.Millisecond)
			_, _ = fx.rt.create(params)
			late <- fx.rt.lastPID()
		}()
		return nil, fakeuhp.Drop
	})
	if _, _, code := fx.h.run("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t1"); code == 0 {
		t.Fatal("start succeeded without a create reply")
	}
	pid := <-late
	fx.h.now = fx.h.now.Add(3 * time.Minute)
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: failed") {
		t.Fatalf("show = %q", show)
	}
	if !gone(pid) {
		t.Fatalf("late worker %d left running without a live attempt", pid)
	}
}

func TestSyncLeavesTheOperatorsTerminalsAlone(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	mine := fx.rt.addShell(t, filepath.Join(fx.h.home, "worktrees", "t1-a1"), "")
	fx.rt.srv.SetGeneration("gen-2")
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: interrupted") {
		t.Fatalf("show = %q", show)
	}
	if fx.rt.isClosed(mine) {
		t.Fatal("sync closed a terminal that does not belong to the attempt")
	}
}

func TestRestartStopsAWorkerThatSurvivedIt(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	pid := fx.rt.lastPID()
	fx.rt.srv.SetGeneration("gen-2")
	fx.h.ok("attempt", "show", "a1")
	if !gone(pid) {
		t.Fatalf("worker %d survived the restart and was left running", pid)
	}
}

func TestStaleLaunchStaysLiveWhileItsCleanupFails(t *testing.T) {
	fx := newAttemptFixture(t)
	st, err := state.Open(filepath.Join(fx.h.home, "hand.db"), func() time.Time { return fx.h.now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAttempt(context.Background(), state.AttemptSpec{TaskID: 1, Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}}, filepath.Join(fx.h.home, "worktrees")); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	fx.rt.srv.Handle("terminal.backend.inventory", func(json.RawMessage) (any, error) {
		return nil, fakeuhp.Fail{Code: "unavailable", Message: "inventory is unavailable"}
	})
	fx.h.now = fx.h.now.Add(3 * time.Minute)
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: launching") {
		t.Fatalf("show = %q, want the attempt kept live until cleanup succeeds", show)
	}
	fx.rt.srv.Handle("terminal.backend.inventory", fx.rt.inventory)
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: failed") {
		t.Fatalf("retry show = %q", show)
	}
}
