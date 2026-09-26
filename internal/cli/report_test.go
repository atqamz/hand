package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerReportsFromInsideItsWorktree(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	sub := filepath.Join(fx.h.home, "worktrees", "t1-a1", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	fx.h.cwd = sub
	if out := fx.h.ok("report", "add", "--status", "done", "--text", "Fixed login\nCommit abc123"); !strings.Contains(out, "report: r1") || !strings.Contains(out, "attempt: a1") {
		t.Fatalf("report add = %q", out)
	}
	if show := fx.h.ok("report", "show", "r1"); !strings.Contains(show, `body: "Fixed login\nCommit abc123"`) || !strings.Contains(show, "acked: no") || strings.Index(show, "help[") < strings.Index(show, "body:") {
		t.Fatalf("show = %q", show)
	}
	if list := fx.h.ok("report", "list", "--unacked"); !strings.Contains(list, "r1,a1,t1,done,no,Fixed login") {
		t.Fatalf("list = %q", list)
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.reported,t1,"a1: r1 done"`) {
		t.Fatalf("wait = %q", woke)
	}
	if show := fx.h.ok("task", "show", "t1"); !strings.Contains(show, "report: r1 done unacked") {
		t.Fatalf("task show = %q", show)
	}
	if out := fx.h.ok("report", "ack", "r1"); !strings.Contains(out, "acked_by: supervisor") {
		t.Fatalf("ack = %q", out)
	}
	if _, errOut, code := fx.h.run("report", "ack", "r1"); code != 3 || !strings.Contains(errOut, "already acknowledged") {
		t.Fatalf("second ack code=%d stderr=%q", code, errOut)
	}
	if list := fx.h.ok("report", "list", "--unacked"); !strings.Contains(list, "reports[0]") {
		t.Fatalf("unacked after ack = %q", list)
	}
}

func TestReportOutsideAWorktreeNeedsAnAttempt(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	for _, dir := range []string{t.TempDir(), fx.repo} {
		fx.h.cwd = dir
		if _, errOut, code := fx.h.run("report", "add", "--status", "progress", "--text", "x"); code != 2 || !strings.Contains(errOut, "pass --attempt") {
			t.Fatalf("report from %s: code=%d stderr=%q", dir, code, errOut)
		}
	}
	if out := fx.h.ok("report", "add", "--attempt", "a1", "--status", "progress", "--text", "halfway"); !strings.Contains(out, "report: r1") {
		t.Fatalf("explicit attempt = %q", out)
	}
}

func TestReportForAStoppedAttemptIsRefused(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.ok("attempt", "stop", "a1")
	if _, errOut, code := fx.h.run("report", "add", "--attempt", "a1", "--status", "done", "--text", "late"); code != 3 || !strings.Contains(errOut, "only a running attempt can report") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestBriefingTellsTheWorkerHowToReport(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	prompt := fx.rt.lastCreate().Command[len(fx.rt.lastCreate().Command)-1]
	if !strings.HasPrefix(prompt, "Fix the login bug, commit, then stop.") || !strings.Contains(prompt, "' report add --status done --file SUMMARY.md\n") || strings.Contains(prompt, "done|stuck") {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestReportInsideAWorktreeCannotClaimAnotherAttempt(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.ok("task", "add", "app", "Second task")
	fx.h.ok("task", "start", "t2")
	fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t2")
	fx.h.cwd = filepath.Join(fx.h.home, "worktrees", "t1-a1")
	if _, errOut, code := fx.h.run("report", "add", "--attempt", "a2", "--status", "done", "--text", "not mine"); code != 3 || !strings.Contains(errOut, "belongs to attempt a1") {
		t.Fatalf("cross-attempt report: code=%d stderr=%q", code, errOut)
	}
	if out := fx.h.ok("report", "add", "--attempt", "a1", "--status", "progress", "--text", "mine"); !strings.Contains(out, "attempt: a1") {
		t.Fatalf("own attempt = %q", out)
	}
}

func TestTaskShowPairsTheReportWithTheLatestAttempt(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.ok("report", "add", "--attempt", "a1", "--status", "done", "--text", "first try")
	fx.h.ok("attempt", "stop", "a1")
	fx.start()
	if show := fx.h.ok("task", "show", "t1"); !strings.Contains(show, "attempt: a2 running") || !strings.Contains(show, "report: none") {
		t.Fatalf("task show = %q", show)
	}
}
