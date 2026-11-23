package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/state"
)

func (fx *attemptFixture) startOmp() {
	fx.h.t.Helper()
	fx.h.ok("attempt", "start", "--harness", "omp", "--model", "commandcode/deepseek/deepseek-v4.1-flash", "--effort", "low", "--prompt-file", fx.brief, "t1")
}

func ompScreen(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "harness", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ompDone(fx *attemptFixture, screen string) {
	fx.rt.set(func(rt *fakeRuntime) { rt.screen = screen })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "omp"})
}

func TestWatchRecordsAnOmpRefusalAsLimitedInsideTheLaunchWindow(t *testing.T) {
	old := *cli.QuietGrace
	*cli.QuietGrace = time.Hour
	t.Cleanup(func() { *cli.QuietGrace = old })

	fx := newAttemptFixture(t)
	fx.startOmp()
	stop := startWatch(t, fx)
	defer stop()
	ompDone(fx, ompScreen(t, "omp-402-refused.txt"))
	eventually(t, func() bool { return woken(fx, "attempt.limited") })
	got := fx.h.ok("wait", "--after", "0", "--timeout", "1ms")
	if !strings.Contains(got, `,attempt.limited,t1,"a1: will not clear by itself: 402 Insufficient Balance (request_id: 00000000-0000-4000-8000-000000000002)`) || strings.Contains(got, ",attempt.quiet,") {
		t.Fatalf("wait = %q", got)
	}
	if out := fx.h.ok("orient"); !strings.Contains(out, "a1 running refused: 402 Insufficient Balance…") {
		t.Fatalf("orient = %q", out)
	}
}

func ompWorking(t *testing.T, fx *attemptFixture) {
	t.Helper()
	count := func() int { return strings.Count(fx.h.ok("orient"), ",attempt.working,") }
	n := count()
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "working", "agent": "omp"})
	eventually(t, func() bool { return count() > n })
}

func TestWatchAnOmpRefusalLineThatReturnsIsNeverAnAPIError(t *testing.T) {
	old := *cli.QuietGrace
	*cli.QuietGrace = time.Hour
	t.Cleanup(func() { *cli.QuietGrace = old })

	fx := newAttemptFixture(t)
	fx.startOmp()
	stop := startWatch(t, fx)
	defer stop()
	screen := ompScreen(t, "omp-402-refused.txt")
	ompDone(fx, screen)
	eventually(t, func() bool { return woken(fx, "attempt.limited") })
	ompWorking(t, fx)
	ompDone(fx, screen)
	eventually(t, func() bool { return len(turnEvents(t, fx)) == 1 })
	got := fx.h.ok("wait", "--after", "0", "--timeout", "1ms")
	if n := strings.Count(got, ",attempt.limited,"); n != 1 {
		t.Fatalf("attempt.limited recorded %d times for one line: %q", n, got)
	}
	if strings.Contains(got, "api error") || !strings.Contains(got, `,attempt.quiet,t1,"a1: turn ended without a new report: will not clear by itself: 402 Insufficient Balance (request_id: 00000000-0000-4000-8000-000000000002) (type=insufficient_balance_error param=invalid_request_error)"`) {
		t.Fatalf("wait = %q", got)
	}
}

func TestWatchAnOmpRefusalWithANewLineIsLimitedAgain(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.startOmp()
	stop := startWatch(t, fx)
	defer stop()
	screen := ompScreen(t, "omp-402-refused.txt")
	ompDone(fx, screen)
	eventually(t, func() bool { return woken(fx, "attempt.limited") })
	ompWorking(t, fx)
	ompDone(fx, strings.ReplaceAll(screen, "000000000002", "000000000003"))
	eventually(t, func() bool {
		return strings.Count(fx.h.ok("wait", "--after", "0", "--timeout", "1ms"), ",attempt.limited,") == 2
	})
}

func TestWatchRecordsAClaudeAPIErrorAsQuietInsideTheLaunchWindow(t *testing.T) {
	old := *cli.QuietGrace
	*cli.QuietGrace = time.Hour
	t.Cleanup(func() { *cli.QuietGrace = old })

	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.screen = "API Error: 529 Overloaded\n\n> " })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
	if got := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(got, `,attempt.quiet,t1,"a1: api error: API Error: 529 Overloaded"`) {
		t.Fatalf("wait = %q", got)
	}
}

func TestWatchRecordsAnOmpRetriableErrorAsQuietInsideTheLaunchWindow(t *testing.T) {
	old := *cli.QuietGrace
	*cli.QuietGrace = time.Hour
	t.Cleanup(func() { *cli.QuietGrace = old })

	fx := newAttemptFixture(t)
	fx.startOmp()
	stop := startWatch(t, fx)
	defer stop()
	ompDone(fx, "Error: 529 Overloaded\n✘ Retry failed after 3 attempts: 529 Overloaded\nDismissed when you send your next message.\nπ > ◒ m > ...\n")
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
	got := fx.h.ok("wait", "--after", "0", "--timeout", "1ms")
	if !strings.Contains(got, `,attempt.quiet,t1,"a1: api error: 529 Overloaded"`) || strings.Contains(got, ",attempt.limited,") {
		t.Fatalf("wait = %q", got)
	}
}

func TestWatchDoesNotCallAFinishedOmpPaneLimited(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.startOmp()
	stop := startWatch(t, fx)
	defer stop()
	screen := ompScreen(t, "omp-18.18.0-idle.txt")
	ompDone(fx, screen)
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
	fx.h.ok("report", "add", "--attempt", "a1", "--status", "done", "--text", "Fixed login")
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "working", "agent": "omp"})
	ompDone(fx, screen)
	eventually(t, func() bool {
		return slices.ContainsFunc(turnEvents(t, fx), func(e state.Event) bool { return e.Detail == "a1: turn ended; reported r1 done" })
	})
	if got := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); strings.Contains(got, ",attempt.limited,") || strings.Contains(got, "api error") {
		t.Fatalf("wait = %q", got)
	}
}
