package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatchNotesABlockOnlyFromARealPrompt(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "harness", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for name, tc := range map[string]struct {
		harness, model, effort, screen, hint string
		blocked                              bool
	}{
		"omp status box": {"omp", "commandcode/deepseek/deepseek-v4.1-flash", "low", read("omp-18.18.0-working.txt"), "╰─", false},
		"omp idle box":   {"omp", "commandcode/deepseek/deepseek-v4.1-flash", "low", read("omp-18.18.0-idle.txt"), "╰─", false},
		"claude dialog":  {"claude", "sonnet", "low", read("claude-2.1.316-commit.txt"), "Do you want to proceed?", true},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newAttemptFixture(t)
			fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.status, rt.hint = tc.screen, "blocked", tc.hint })
			fx.h.ok("attempt", "start", "--harness", tc.harness, "--model", tc.model, "--effort", tc.effort, "--prompt-file", fx.brief, "t1")
			stop := startWatch(t, fx)
			defer stop()
			publishBlocked(fx, "2", tc.harness)
			if tc.blocked {
				eventually(t, func() bool { return woken(fx, "attempt.blocked") })
				return
			}
			time.Sleep(500 * time.Millisecond)
			if woken(fx, "attempt.blocked") {
				t.Fatal("a box corner woke the supervisor")
			}
			if out := fx.h.ok("orient"); strings.Contains(out, "running blocked") {
				t.Fatalf("orient = %q", out)
			}
		})
	}
}
