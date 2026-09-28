package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestWorkerReportsFromInsideItsWorktree(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	sub := filepath.Join(fx.h.worktree("t1-a1"), "pkg")
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
	if !strings.HasPrefix(prompt, "Fix the login bug, commit, then stop.") || !strings.Contains(prompt, "' report add --status done --file - <<'") || strings.Contains(prompt, "SUMMARY.md") || strings.Contains(prompt, "done|stuck") {
		t.Fatalf("prompt = %q", prompt)
	}
}

func heredocDelimiter(t *testing.T, prompt string) string {
	t.Helper()
	_, after, ok := strings.Cut(prompt, "--file - <<'")
	delim, rest, ok2 := strings.Cut(after, "'\n")
	if !ok || !ok2 || delim == "" {
		t.Fatalf("no heredoc in %q", prompt)
	}
	if !strings.Contains(rest, "\n"+delim+"\n") {
		t.Fatalf("heredoc %q is never closed in %q", delim, prompt)
	}
	return delim
}

func TestEachBriefingClosesItsReportWithAnUnguessableDelimiter(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	first := heredocDelimiter(t, fx.rt.lastCreate().Command[len(fx.rt.lastCreate().Command)-1])
	fx.h.ok("task", "add", "app", "Second task")
	fx.h.ok("task", "start", "t2")
	fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t2")
	second := heredocDelimiter(t, fx.rt.lastCreate().Command[len(fx.rt.lastCreate().Command)-1])
	if first == "EOF" || first == second {
		t.Fatalf("delimiters %q and %q: a report line could end the heredoc early", first, second)
	}
}

type endless struct{ read int }

func (e *endless) Read(p []byte) (int, error) {
	if e.read > 4*state.MaxReportBytes {
		return 0, errors.New("read past the report limit")
	}
	for i := range p {
		p[i] = 'x'
	}
	e.read += len(p)
	return len(p), nil
}

func TestEndlessStdinIsCutAtTheReportLimit(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.cwd = fx.h.worktree("t1-a1")
	fx.h.in = &endless{}
	if _, errOut, code := fx.h.run("report", "add", "--status", "done", "--file", "-"); code != 2 || !strings.Contains(errOut, "report body must be 1-65536 bytes") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestReportInsideAWorktreeCannotClaimAnotherAttempt(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.ok("task", "add", "app", "Second task")
	fx.h.ok("task", "start", "t2")
	fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t2")
	fx.h.cwd = fx.h.worktree("t1-a1")
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

func TestReportBodyFromStdinLeavesTheWorktreeClean(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.cwd = fx.h.worktree("t1-a1")
	fx.h.in = strings.NewReader("Fixed login\nCommit abc123\n")
	if out := fx.h.ok("report", "add", "--status", "done", "--file", "-"); !strings.Contains(out, "report: r1") {
		t.Fatalf("report add = %q", out)
	}
	if show := fx.h.ok("report", "show", "r1"); !strings.Contains(show, `body: "Fixed login\nCommit abc123\n"`) {
		t.Fatalf("show = %q", show)
	}
	fx.h.ok("report", "ack", "r1")
	fx.h.ok("attempt", "stop", "a1")
	if out := fx.h.ok("attempt", "clean", "a1"); !strings.Contains(out, "removed: ") {
		t.Fatalf("clean = %q", out)
	}
}

func TestReportBodyFromAFileOutsideTheWorktree(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.cwd = fx.h.worktree("t1-a1")
	file := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(file, []byte("Done from a file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.h.in = strings.NewReader("not this")
	fx.h.ok("report", "add", "--status", "done", "--file", file)
	if show := fx.h.ok("report", "show", "r1"); !strings.Contains(show, `body: "Done from a file\n"`) {
		t.Fatalf("show = %q", show)
	}
	if _, errOut, code := fx.h.run("report", "add", "--status", "done", "--file", file+".missing"); code != 2 || !strings.Contains(errOut, "no such file") {
		t.Fatalf("missing file: code=%d stderr=%q", code, errOut)
	}
}
