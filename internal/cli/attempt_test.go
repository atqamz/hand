package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

func TestAttemptStartLaunchesAWorkerInItsOwnWorktree(t *testing.T) {
	fx := newAttemptFixture(t)
	out := fx.start()
	for _, want := range []string{"attempt: a1", "status: running", "branch: " + fx.h.branch("t1-a1"), "pane: 2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("start = %q, missing %q", out, want)
		}
	}
	call := fx.rt.lastCreate()
	wt := fx.h.worktree("t1-a1")
	if call.CWD != wt || call.Label != "hand-a1" || !strings.HasPrefix(call.Command[len(call.Command)-1], "Fix the login bug, commit, then stop.") {
		t.Fatalf("create = %+v", call)
	}
	if !strings.HasSuffix(call.Command[0], "/claude") || call.Command[1] != "--dangerously-skip-permissions" {
		t.Fatalf("argv = %q", call.Command)
	}
	if _, err := os.Stat(filepath.Join(wt, ".git")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}
	show := fx.h.ok("attempt", "show", "a1")
	for _, want := range []string{"status: running", "agent: working", "launch: " + toon.Value(call.Command[0]+" --dangerously-skip-permissions --model sonnet --effort low")} {
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
	if _, err := os.Stat(filepath.Dir(fx.h.worktree("t1-a1"))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktrees dir created: %v", err)
	}
}

func TestOpencodeRefusesAModelBeforeTouchingGit(t *testing.T) {
	fx := newAttemptFixture(t)
	if _, errOut, code := fx.h.run("attempt", "start", "--harness", "opencode", "--model", "opencode/big-pickle", "--prompt-file", fx.brief, "t1"); code != 2 || !strings.Contains(errOut, "its own configuration") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Dir(fx.h.worktree("t1-a1"))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktrees dir created: %v", err)
	}
}

const opencodeScreen = "┃  Fix the login bug, commit, then stop.\n┃  When you finish, report to Hand from inside this worktree:"

func TestOpencodeBriefingIsSubmittedOnceItIsOnScreen(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.revision, rt.status, rt.afterKeys = opencodeScreen, 7, "idle", "working"
	})
	out := fx.h.ok("attempt", "start", "--harness", "opencode", "--prompt-file", fx.brief, "t1")
	if !strings.Contains(out, "prompt: submitted") || !slices.Equal(fx.rt.keysSent(), []string{"enter"}) {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
	call := fx.rt.lastCreate()
	if !strings.HasSuffix(call.Command[0], "/opencode") || !slices.Equal(call.Command[1:4], []string{"--standalone", "--auto", "--prompt"}) {
		t.Fatalf("argv = %q", call.Command)
	}
	if recent := fx.h.ok("orient"); !strings.Contains(recent, ",attempt.keys,t1") {
		t.Fatalf("orient = %q", recent)
	}
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "harness: opencode\n") {
		t.Fatalf("show = %q", show)
	}
}

func TestOpencodeBriefingThatNeverAppearsIsReported(t *testing.T) {
	fx := newAttemptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, errOut, code := fx.h.runCtx(ctx, "attempt", "start", "--harness", "opencode", "--prompt-file", fx.brief, "t1")
	if code != 0 || !strings.Contains(out, "prompt: not submitted") || !strings.Contains(out, "hand attempt keys --revision N a1 enter") || len(fx.rt.keysSent()) != 0 {
		t.Fatalf("code=%d out=%q stderr=%q keys=%q", code, out, errOut, fx.rt.keysSent())
	}
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: running") {
		t.Fatalf("show = %q", show)
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: briefing not submitted; press Enter"`) {
		t.Fatalf("wait = %q", woke)
	}
}

func TestOpencodeEnterWithoutAStatusChangeIsNotConfirmed(t *testing.T) {
	confirm := *cli.SubmitConfirm
	*cli.SubmitConfirm = 300 * time.Millisecond
	t.Cleanup(func() { *cli.SubmitConfirm = confirm })
	fx := newAttemptFixture(t)
	fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.status = opencodeScreen, "idle" })
	out := fx.h.ok("attempt", "start", "--harness", "opencode", "--prompt-file", fx.brief, "t1")
	if !strings.Contains(out, "prompt: sent unconfirmed") || !strings.Contains(out, "only if the briefing is still in the input box") || !slices.Equal(fx.rt.keysSent(), []string{"enter", "enter", "enter"}) {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: briefing sent but not confirmed; check the screen"`) {
		t.Fatalf("wait = %q", woke)
	}
}

func TestOpencodeGetsEnterOnlyWhileItIsKnownIdle(t *testing.T) {
	for name, set := range map[string]func(*fakeRuntime){
		"working":        func(rt *fakeRuntime) { rt.screen, rt.status = opencodeScreen, "working" },
		"explain failed": func(rt *fakeRuntime) { rt.screen, rt.explainFail = opencodeScreen, "unavailable" },
	} {
		fx := newAttemptFixture(t)
		fx.rt.set(set)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		out, errOut, code := fx.h.runCtx(ctx, "attempt", "start", "--harness", "opencode", "--prompt-file", fx.brief, "t1")
		cancel()
		if code != 0 || !strings.Contains(out, "prompt: not submitted") || len(fx.rt.keysSent()) != 0 {
			t.Fatalf("%s: code=%d out=%q stderr=%q keys=%q", name, code, out, errOut, fx.rt.keysSent())
		}
	}
}

func TestOpencodeStopsWaitingOnAGoneTerminal(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.rt.set(func(rt *fakeRuntime) { rt.readFail = "terminal_gone" })
	start := time.Now()
	out := fx.h.ok("attempt", "start", "--harness", "opencode", "--prompt-file", fx.brief, "t1")
	if !strings.Contains(out, "prompt: not submitted") || time.Since(start) > 5*time.Second {
		t.Fatalf("start = %q after %s", out, time.Since(start))
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
	if _, err := os.Stat(fx.h.worktree("t1-a1")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktree left behind: %v", err)
	}
	if out, err := exec.Command("git", "-C", fx.repo, "rev-parse", "--verify", "--quiet", fx.h.branch("t1-a1")).CombinedOutput(); err == nil {
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
	if _, err := st.AddAttempt(context.Background(), state.AttemptSpec{TaskID: 1, Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}}, filepath.Dir(fx.h.worktree("t1-a1"))); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	wt := fx.h.worktree("t1-a1")
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
	if _, err := os.Stat(fx.h.worktree("t1-a1")); err != nil {
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
	mine := fx.rt.addShell(t, fx.h.worktree("t1-a1"), "")
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
	if _, err := st.AddAttempt(context.Background(), state.AttemptSpec{TaskID: 1, Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}}, filepath.Dir(fx.h.worktree("t1-a1"))); err != nil {
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

func agyFixture(t *testing.T) *attemptFixture {
	t.Helper()
	wait, confirm := *cli.TrustWait, *cli.TrustConfirm
	*cli.TrustWait, *cli.TrustConfirm = 2*time.Second, time.Second
	t.Cleanup(func() { *cli.TrustWait, *cli.TrustConfirm = wait, confirm })
	return newAttemptFixture(t)
}

func (fx *attemptFixture) startAgy() string {
	fx.h.t.Helper()
	return fx.h.ok("attempt", "start", "--harness", "agy", "--model", "gemini-3.8-flash-low", "--prompt-file", fx.brief, "t1")
}

func trustScreen(path string) string {
	return "Accessing workspace:\n" + path + "\nDo you trust the contents of this project?\nAntigravity CLI requires permission to read, edit, and execute files here.\n> Yes, I trust this folder\n  No, exit\n  ↑/↓ Navigate · enter Confirm"
}

func claudeFixture(t *testing.T) *attemptFixture {
	t.Helper()
	wait, confirm := *cli.TrustWait, *cli.TrustConfirm
	*cli.TrustWait, *cli.TrustConfirm = 2*time.Second, time.Second
	t.Cleanup(func() { *cli.TrustWait, *cli.TrustConfirm = wait, confirm })
	return newAttemptFixture(t)
}

func claudeTrustScreen(path string) string {
	return "Accessing workspace:\n\n" + path + "\n\nQuick safety check: Is this a project you created or one you trust? (Like your\nown code, a well-known open source project, or work from your team). If not,\ntake a moment to review what's in this folder first.\n...\n❯ No, exit\n  Yes, I trust this folder\n\nEnter to confirm · Esc to cancel"
}

func claudeTrustScreenYes(path string) string {
	return strings.Replace(claudeTrustScreen(path), "❯ No, exit\n  Yes, I trust this folder", "  No, exit\n❯ Yes, I trust this folder", 1)
}

func TestAgyWorkersStartWithTheirBrief(t *testing.T) {
	fx := agyFixture(t)
	out := fx.startAgy()
	call := fx.rt.lastCreate()
	want := []string{filepath.Join(fx.h.vars["PATH"], "agy"), "--model", "gemini-3.8-flash-low", "--dangerously-skip-permissions", "-i"}
	if !slices.Equal(call.Command[:5], want) || len(call.Command) != 6 || !strings.HasPrefix(call.Command[5], "Fix the login bug, commit, then stop.") {
		t.Fatalf("argv = %q", call.Command)
	}
	if !strings.Contains(out, "trust: not asked") || len(fx.rt.keysSent()) != 0 {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
}

func TestAgyTrustScreenIsAcceptedInItsOwnWorktree(t *testing.T) {
	fx := agyFixture(t)
	wt := fx.h.worktree("t1-a1")
	fx.rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.revision, rt.afterScreen = trustScreen("   \n "+wt[:20]+"    \n "+wt[20:]+"   "), 4, "> \n"
	})
	out := fx.startAgy()
	if !strings.Contains(out, "trust: accepted") || !slices.Equal(fx.rt.keysSent(), []string{"enter"}) {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
	events, err := openStore(t, fx.h).RecentEventsOf(context.Background(), []string{"attempt.keys", "attempt.blocked"}, 10)
	if err != nil || len(events) != 1 || events[0].Kind != "attempt.keys" || events[0].Detail != "a1: enter" {
		t.Fatalf("events = %+v, err %v", events, err)
	}
}

func TestAgyTrustScreenForAnotherPathIsLeftAlone(t *testing.T) {
	for name, path := range map[string]func(wt string) string{
		"elsewhere": func(string) string { return "/tmp/elsewhere" },
		"sibling":   func(wt string) string { return wt + "2" },
		"parent":    filepath.Dir,
		"spaced":    func(wt string) string { return wt[:len(wt)/2] + " " + wt[len(wt)/2:] },
	} {
		t.Run(name, func(t *testing.T) {
			fx := agyFixture(t)
			screen := trustScreen(path(fx.h.worktree("t1-a1")))
			fx.rt.set(func(rt *fakeRuntime) { rt.screen = screen })
			out := fx.startAgy()
			if !strings.Contains(out, "trust: not pressed") || !strings.Contains(out, "hand attempt read a1") || len(fx.rt.keysSent()) != 0 {
				t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
			}
			if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: agy asks to trust a folder that is not its worktree; check the screen"`) {
				t.Fatalf("wait = %q", woke)
			}
		})
	}
}

func TestAgyTrustScreenThatStaysIsNoted(t *testing.T) {
	fx := agyFixture(t)
	screen := trustScreen(fx.h.worktree("t1-a1"))
	fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.afterScreen = screen, screen })
	out := fx.startAgy()
	if !strings.Contains(out, "trust: accepted") || !strings.Contains(out, "hand attempt read a1") || !slices.Equal(fx.rt.keysSent(), []string{"enter"}) {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: agy trust screen did not clear; check the screen"`) {
		t.Fatalf("wait = %q", woke)
	}
}

func TestAgyTrustScreenThatComesLateIsPressed(t *testing.T) {
	fx := agyFixture(t)
	screen := trustScreen(fx.h.worktree("t1-a1"))
	fx.rt.set(func(rt *fakeRuntime) {
		rt.screens, rt.screen, rt.afterScreen, rt.status = []string{"", ""}, screen, "> \n", "idle"
	})
	out := fx.startAgy()
	if !strings.Contains(out, "trust: accepted") || !slices.Equal(fx.rt.keysSent(), []string{"enter"}) {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
}

func TestAgyTrustScreenThatClearsSlowlyIsAccepted(t *testing.T) {
	fx := agyFixture(t)
	*cli.TrustWait, *cli.TrustConfirm = time.Second, 3*time.Second
	screen := trustScreen(fx.h.worktree("t1-a1"))
	fx.rt.set(func(rt *fakeRuntime) {
		rt.screen = "> \n"
		for range 8 {
			rt.screens = append(rt.screens, screen)
		}
	})
	out := fx.startAgy()
	if !strings.Contains(out, "trust: accepted") || strings.Contains(out, "hand attempt read a1") || !slices.Equal(fx.rt.keysSent(), []string{"enter"}) {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
}

func TestAgyTrustClearCheckIsBoundedByItsDeadline(t *testing.T) {
	fx := agyFixture(t)
	screen := trustScreen(fx.h.worktree("t1-a1"))
	fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.afterScreen, rt.afterStall = screen, "> \n", 4*time.Second })
	began := time.Now()
	out := fx.startAgy()
	if took := time.Since(began); took > 3*time.Second || !strings.Contains(out, "trust: accepted") {
		t.Fatalf("start took %s: %q", took, out)
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `"a1: agy trust screen did not clear; check the screen"`) {
		t.Fatalf("wait = %q", woke)
	}
}

func TestAgyTrustScreenSeenButNotPressedIsNoted(t *testing.T) {
	wt := func(fx *attemptFixture) string { return fx.h.worktree("t1-a1") }
	for name, set := range map[string]func(fx *attemptFixture, rt *fakeRuntime){
		"other cursor": func(fx *attemptFixture, rt *fakeRuntime) {
			rt.screen = strings.Replace(trustScreen(wt(fx)), "> Yes", "❯ Yes", 1)
		},
		"conflicts": func(fx *attemptFixture, rt *fakeRuntime) {
			rt.screen, rt.keysFail = trustScreen(wt(fx)), "content_revision_conflict"
		},
		"keys refused": func(fx *attemptFixture, rt *fakeRuntime) {
			rt.screen, rt.keysFail = trustScreen(wt(fx)), "agent_not_ready"
		},
	} {
		t.Run(name, func(t *testing.T) {
			fx := agyFixture(t)
			fx.rt.set(func(rt *fakeRuntime) { set(fx, rt) })
			out := fx.startAgy()
			if !strings.Contains(out, "trust: not pressed") || !strings.Contains(out, "hand attempt read a1") || len(fx.rt.keysSent()) != 0 {
				t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
			}
			if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: agy trust screen was not pressed; check the screen"`) {
				t.Fatalf("wait = %q", woke)
			}
		})
	}
}

func TestClaudeTrustScreenWithCursorOnNoIsAccepted(t *testing.T) {
	fx := claudeFixture(t)
	wt := fx.h.worktree("t1-a1")
	fx.rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.revision, rt.keyScreens = claudeTrustScreen("   \n "+wt[:20]+"    \n "+wt[20:]+"   "), 4, []string{claudeTrustScreenYes(wt), "> \n"}
	})
	out := fx.start()
	if !strings.Contains(out, "trust: accepted") || !slices.Equal(fx.rt.keysSent(), []string{"down", "enter"}) {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
	events, err := openStore(t, fx.h).RecentEventsOf(context.Background(), []string{"attempt.keys", "attempt.blocked"}, 10)
	if err != nil || len(events) != 1 || events[0].Kind != "attempt.keys" || events[0].Detail != "a1: down enter" {
		t.Fatalf("events = %+v, err %v", events, err)
	}
}

func TestClaudeTrustScreenWithCursorOnYesIsAccepted(t *testing.T) {
	fx := claudeFixture(t)
	wt := fx.h.worktree("t1-a1")
	fx.rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.revision, rt.afterScreen = claudeTrustScreenYes(wt), 4, "> \n"
	})
	out := fx.start()
	if !strings.Contains(out, "trust: accepted") || !slices.Equal(fx.rt.keysSent(), []string{"enter"}) {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
	events, err := openStore(t, fx.h).RecentEventsOf(context.Background(), []string{"attempt.keys", "attempt.blocked"}, 10)
	if err != nil || len(events) != 1 || events[0].Kind != "attempt.keys" || events[0].Detail != "a1: enter" {
		t.Fatalf("events = %+v, err %v", events, err)
	}
}

func TestClaudeTrustScreenForAnotherPathIsLeftAlone(t *testing.T) {
	for name, path := range map[string]func(wt string) string{
		"elsewhere": func(string) string { return "/tmp/elsewhere" },
		"sibling":   func(wt string) string { return wt + "2" },
		"parent":    filepath.Dir,
		"spaced":    func(wt string) string { return wt[:len(wt)/2] + " " + wt[len(wt)/2:] },
	} {
		t.Run(name, func(t *testing.T) {
			fx := claudeFixture(t)
			screen := claudeTrustScreen(path(fx.h.worktree("t1-a1")))
			fx.rt.set(func(rt *fakeRuntime) { rt.screen = screen })
			out := fx.start()
			if !strings.Contains(out, "trust: not pressed") || !strings.Contains(out, "hand attempt read a1") || len(fx.rt.keysSent()) != 0 {
				t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
			}
			if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: claude asks to trust a folder that is not its worktree; check the screen"`) {
				t.Fatalf("wait = %q", woke)
			}
		})
	}
}

func TestClaudeTrustScreenThatStaysOnNoIsNotConfirmed(t *testing.T) {
	fx := claudeFixture(t)
	screen := claudeTrustScreen(fx.h.worktree("t1-a1"))
	fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.afterScreen = screen, screen })
	out := fx.start()
	if !strings.Contains(out, "trust: not pressed") || !strings.Contains(out, "hand attempt read a1") || !slices.Equal(fx.rt.keysSent(), []string{"down"}) {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: claude trust screen was not pressed; check the screen"`) {
		t.Fatalf("wait = %q", woke)
	}
	events, err := openStore(t, fx.h).RecentEventsOf(context.Background(), []string{"attempt.keys"}, 10)
	if err != nil || len(events) != 1 || events[0].Detail != "a1: down" {
		t.Fatalf("events = %+v, err %v", events, err)
	}
}

func TestClaudeTrustScreenThatStaysOnYesIsNoted(t *testing.T) {
	fx := claudeFixture(t)
	screen := claudeTrustScreenYes(fx.h.worktree("t1-a1"))
	fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.afterScreen = screen, screen })
	out := fx.start()
	if !strings.Contains(out, "trust: accepted") || !slices.Equal(fx.rt.keysSent(), []string{"enter"}) {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: claude trust screen did not clear; check the screen"`) {
		t.Fatalf("wait = %q", woke)
	}
}

func TestAgyNeedsItsModelList(t *testing.T) {
	fx := agyFixture(t)
	fakebin.Install(t, fx.h.vars["PATH"], "agy", "fake", map[string]string{"exit": "1"})
	if _, errOut, code := fx.h.run("attempt", "start", "--harness", "agy", "--model", "gemini-3.8-flash-low", "--prompt-file", fx.brief, "t1"); code != 2 || !strings.Contains(errOut, "check that agy is logged in") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if list := fx.h.ok("attempt", "list"); strings.Contains(list, "a1,") {
		t.Fatalf("list = %q", list)
	}
}

func TestAttemptStopEndsTheRowWhenItsTerminalWillNotClose(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) { rt.closeFail = "backend_error" })
	out, errOut, code := fx.h.run("attempt", "stop", "a1")
	if code != 0 || !strings.Contains(out, "status: stopped") || !strings.Contains(out, "did not close") {
		t.Fatalf("stop: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}
