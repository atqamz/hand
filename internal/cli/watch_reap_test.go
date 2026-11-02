package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	eventsOf := func() string {
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
}
