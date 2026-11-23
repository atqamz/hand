package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "harness", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWatchNotesABlockOnlyFromARealPrompt(t *testing.T) {
	for name, tc := range map[string]struct {
		harness, model, effort, screen, hint string
		blocked                              bool
	}{
		"omp status box": {"omp", "commandcode/deepseek/deepseek-v4.1-flash", "low", readFixture(t, "omp-18.18.0-working.txt"), "╰─", false},
		"omp idle box":   {"omp", "commandcode/deepseek/deepseek-v4.1-flash", "low", readFixture(t, "omp-18.18.0-idle.txt"), "╰─", false},
		"claude dialog":  {"claude", "sonnet", "low", readFixture(t, "claude-2.1.316-commit.txt"), "Do you want to proceed?", true},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newAttemptFixture(t)
			fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.status, rt.hint = tc.screen, "blocked", tc.hint })
			fx.h.ok("attempt", "start", "--harness", tc.harness, "--model", tc.model, "--effort", tc.effort, "--prompt-file", fx.brief, "t1")
			stop := startWatch(t, fx)
			defer stop()
			if tc.blocked {
				publishBlocked(fx, "2", tc.harness)
				eventually(t, func() bool { return woken(fx, "attempt.blocked") })
				return
			}
			read := make(chan struct{}, 1)
			fx.rt.set(func(rt *fakeRuntime) {
				rt.onRead = func() {
					select {
					case read <- struct{}{}:
					default:
					}
				}
			})
			publishBlocked(fx, "2", tc.harness)
			select {
			case <-read:
			case <-time.After(5 * time.Second):
				t.Fatal("the watcher did not read the confirming screen")
			}
			fx.h.advance(*cli.QuietGrace + time.Second)
			fx.rt.set(func(rt *fakeRuntime) { rt.status = "done" })
			fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": tc.harness})
			eventually(t, func() bool { return woken(fx, "attempt.quiet") })
			if woken(fx, "attempt.blocked") {
				t.Fatal("a box corner woke the supervisor")
			}
			if out := fx.h.ok("orient"); strings.Contains(out, "running blocked") {
				t.Fatalf("orient = %q", out)
			}
		})
	}
}

func TestABlockPendingSurvivesANonPromptAndWakesOnARealPrompt(t *testing.T) {
	fx := newAttemptFixture(t)
	box := readFixture(t, "omp-18.18.0-working.txt")
	fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.status, rt.hint = box, "blocked", "╰─" })
	fx.h.ok("attempt", "start", "--harness", "omp", "--model", "commandcode/deepseek/deepseek-v4.1-flash", "--effort", "low", "--prompt-file", fx.brief, "t1")
	stop := startWatch(t, fx)
	defer stop()
	read := make(chan struct{}, 1)
	fx.rt.set(func(rt *fakeRuntime) {
		rt.onRead = func() {
			select {
			case read <- struct{}{}:
			default:
			}
		}
	})
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "blocked", "agent": "omp"})
	select {
	case <-read:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher did not read the confirming screen")
	}
	if woken(fx, "attempt.blocked") {
		t.Fatal("a box corner woke the supervisor")
	}
	fx.rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.status, rt.hint = "Do you want to proceed?\n❯ 1. Yes\n  2. No", "blocked", "Allow once"
	})
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "blocked", "agent": "omp"})
	eventually(t, func() bool { return woken(fx, "attempt.blocked") })
}

func TestABlockFallsBackToTheHintWhenTheScreenCannotBeRead(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t1")
	fx.rt.set(func(rt *fakeRuntime) {
		rt.status, rt.hint, rt.readFail = "blocked", "Do you want to proceed?", "internal"
	})
	stop := startWatch(t, fx)
	defer stop()
	publishBlocked(fx, "2", "claude")
	eventually(t, func() bool { return woken(fx, "attempt.blocked") })
	if out := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(out, `"a1: Do you want to proceed?"`) {
		t.Fatalf("block detail = %q", out)
	}
	fx.rt.set(func(rt *fakeRuntime) {
		if rt.readFailed == 0 {
			t.Error("the block was recorded without a failed read")
		}
	})
}
