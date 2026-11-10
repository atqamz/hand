package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func TestWatchRemovesOnlyProvablySafeFinishedWorktrees(t *testing.T) {
	fx := newAttemptFixture(t)
	for _, n := range []string{"2", "3", "4"} {
		fx.h.ok("task", "add", "app", "Task "+n)
		fx.h.ok("task", "start", "t"+n)
	}
	for _, n := range []string{"1", "2", "3", "4"} {
		fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t"+n)
	}
	scratch := func(n string) string { return scratchOf(fx.h, fx.h.worktree("t"+n+"-a"+n)) }
	for _, n := range []string{"1", "2", "3", "4"} {
		eventually(t, func() bool { return exists(scratch(n)) })
	}
	for _, n := range []string{"1", "2", "3"} {
		fx.h.ok("attempt", "stop", "a"+n)
	}
	fx.h.advance(5 * day)
	fx.h.ok("attempt", "stop", "a4")
	fx.h.advance(3 * day)
	wt := func(n string) string { return fx.h.worktree("t" + n + "-a" + n) }
	if err := os.WriteFile(filepath.Join(wt("2"), "wip.txt"), []byte("draft"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt("3"), "commit", "-q", "--allow-empty", "-m", "unpushed")
	eventsOf := func() string { return retentionEvents(t, fx) }
	stop := startWatch(t, fx, "--every", "20ms")
	eventually(t, func() bool { return strings.Count(eventsOf(), "\n") >= 2 })
	stop()
	out := eventsOf()
	for _, want := range []string{"a1: removed by retention after 7d", "a2: it has uncommitted changes", "a3: 1 commit(s) are on no remote branch: not pushed or merged"} {
		if !strings.Contains(out, want) {
			t.Fatalf("events lack %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"a4: it has", "a4: removed"} {
		if strings.Contains(out, bad) {
			t.Fatalf("a worktree inside the window was judged: %s", out)
		}
	}
	if _, err := os.Stat(wt("1")); !os.IsNotExist(err) {
		t.Fatalf("a1 worktree stayed: %v", err)
	}
	for _, n := range []string{"2", "3", "4"} {
		if _, err := os.Stat(wt(n)); err != nil {
			t.Fatalf("a%s worktree lost: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(wt("2"), "wip.txt")); err != nil {
		t.Fatalf("uncommitted work lost: %v", err)
	}
	mustExist(t, scratch("1"), false)
	for _, n := range []string{"2", "3", "4"} {
		mustExist(t, scratch(n), true)
	}
}

func retentionEvents(t *testing.T, fx *attemptFixture) string {
	t.Helper()
	st, err := state.Open(filepath.Join(fx.h.home, "hand.db"), fx.h.clock)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	events, err := st.RecentEventsOf(context.Background(), []string{"attempt.cleaned", "attempt.kept"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, e := range events {
		lines = append(lines, e.Kind+" "+e.Detail)
	}
	return strings.Join(lines, "\n")
}

func endAttempts(fx *attemptFixture, n int) func(int) string {
	for i := 2; i <= n; i++ {
		fx.h.ok("task", "add", "app", "Task "+strconv.Itoa(i))
		fx.h.ok("task", "start", "t"+strconv.Itoa(i))
	}
	for i := 1; i <= n; i++ {
		fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t"+strconv.Itoa(i))
	}
	for i := 1; i <= n; i++ {
		fx.h.ok("attempt", "stop", "a"+strconv.Itoa(i))
	}
	fx.h.advance(8 * day)
	return func(i int) string { return fx.h.worktree("t" + strconv.Itoa(i) + "-a" + strconv.Itoa(i)) }
}

func mustExist(t *testing.T, path string, want bool) {
	t.Helper()
	if _, err := os.Stat(path); (err == nil) != want {
		t.Fatalf("%s exists = %v, want %v (%v)", path, err == nil, want, err)
	}
}

func quiet(d time.Duration) { time.Sleep(d) }

func TestWatchKeepsAWorktreeWithATerminalOpenInIt(t *testing.T) {
	fx := newAttemptFixture(t)
	wt := endAttempts(fx, 1)
	shell := fx.rt.addShell(t, wt(1), "operator shell")
	stop := startWatch(t, fx, "--every", "20ms")
	eventually(t, func() bool { return strings.Contains(retentionEvents(t, fx), "a1: a terminal is open in its worktree") })
	stop()
	mustExist(t, wt(1), true)
	if fx.rt.isClosed(shell) {
		t.Fatal("the reaper closed the operator's terminal")
	}
}

func TestWatchKeepsIgnoredConfigAndRemovesBuildCaches(t *testing.T) {
	fx := newAttemptFixture(t)
	if err := os.WriteFile(filepath.Join(fx.repo, ".git", "info", "exclude"), []byte("dist/\n.env*\n*.pem\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := endAttempts(fx, 2)
	write := func(path string, n int) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(wt(1), "dist", "app.bin"), 100)
	write(filepath.Join(wt(1), ".env.local"), 10)
	write(filepath.Join(wt(1), "sub", "tls.pem"), 10)
	write(filepath.Join(wt(2), "dist", "one.bin"), 3000)
	write(filepath.Join(wt(2), "dist", "deep", "two.bin"), 1000)
	stop := startWatch(t, fx, "--every", "20ms")
	eventually(t, func() bool { return strings.Count(retentionEvents(t, fx), "\n") >= 1 })
	stop()
	out := retentionEvents(t, fx)
	for _, want := range []string{"a1: ignored files that look like config: .env.local, sub/tls.pem", "a2: removed by retention after 7d; 2 ignored file(s), 3.9 KB removed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("events lack %q:\n%s", want, out)
		}
	}
	mustExist(t, filepath.Join(wt(1), ".env.local"), true)
	mustExist(t, wt(2), false)
}

func TestWatchSkipsWhenRetentionIsOffAndNeverTouchesALiveAttempt(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.h.ok("task", "add", "app", "Task 2")
	fx.h.ok("task", "start", "t2")
	fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t1")
	fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t2")
	fx.h.ok("attempt", "stop", "a1")
	fx.h.advance(8 * day)
	routing := filepath.Join(fx.h.home, "routing.json")
	if err := os.WriteFile(routing, []byte(`{"retention":{"worktrees":"off"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := startWatch(t, fx, "--every", "20ms")
	quiet(300 * time.Millisecond)
	stop()
	mustExist(t, fx.h.worktree("t1-a1"), true)
	mustExist(t, fx.h.worktree("t2-a2"), true)
	if out := retentionEvents(t, fx); out != "" {
		t.Fatalf("events with retention off:\n%s", out)
	}
	if err := os.WriteFile(routing, []byte(`{"retention":{"worktrees":"7d"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stop = startWatch(t, fx, "--every", "20ms")
	eventually(t, func() bool { return strings.Contains(retentionEvents(t, fx), "a1: removed by retention") })
	stop()
	mustExist(t, fx.h.worktree("t2-a2"), true)
}

func TestWatchChecksOnceADay(t *testing.T) {
	fx := newAttemptFixture(t)
	wt := endAttempts(fx, 1)
	wip := filepath.Join(wt(1), "wip.txt")
	if err := os.WriteFile(wip, []byte("draft"), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := startWatch(t, fx, "--every", "20ms")
	eventually(t, func() bool { return strings.Contains(retentionEvents(t, fx), "a1: it has uncommitted changes") })
	if err := os.Remove(wip); err != nil {
		t.Fatal(err)
	}
	quiet(300 * time.Millisecond)
	mustExist(t, wt(1), true)
	fx.h.advance(day)
	eventually(t, func() bool { return strings.Contains(retentionEvents(t, fx), "a1: removed by retention") })
	stop()
	mustExist(t, wt(1), false)
}
