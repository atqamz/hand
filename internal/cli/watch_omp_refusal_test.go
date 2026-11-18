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
	if out := fx.h.ok("orient"); !strings.Contains(out, "a1 running limited: will not clear by itself") {
		t.Fatalf("orient = %q", out)
	}
	ompDone(fx, ompScreen(t, "omp-402-refused.txt"))
	time.Sleep(200 * time.Millisecond)
	if n := strings.Count(fx.h.ok("wait", "--after", "0", "--timeout", "1ms"), ",attempt.limited,"); n != 1 {
		t.Fatalf("attempt.limited recorded %d times for one screen", n)
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
