package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func accessPolicy(t *testing.T, fx *attemptFixture, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fx.h.home, "routing.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const readonlyPolicy = `{"profiles":{"look":{"harness":"claude","model":"sonnet","effort":"low","access":"readonly"},"plain":{"harness":"claude","model":"sonnet","effort":"low"}}}`

func TestAReadonlyProfileStartsTheWorkerInDontAskModeWithOnlyTheReportCommand(t *testing.T) {
	fx := newAttemptFixture(t)
	accessPolicy(t, fx, readonlyPolicy)
	fx.startProfile("look")
	argv := dropSession(t, fx.rt.lastCreate().Command)
	want := []string{"--permission-mode", "dontAsk", "--model", "sonnet", "--effort", "low", "--disallowedTools=Edit", "--disallowedTools=Write", "--disallowedTools=NotebookEdit", "--allowedTools=Bash(gh pr view *)"}
	n := len(argv)
	if n < 13 || filepath.Base(argv[0]) != exe("", "claude") || !slices.Equal(argv[1:11], want) || !strings.HasPrefix(argv[n-1], "Fix the login bug") {
		t.Fatalf("argv = %q", argv)
	}
	for _, rule := range argv[11 : n-1] {
		if !strings.HasPrefix(rule, "--allowedTools=Bash(") || !strings.HasSuffix(rule, " report add *)") {
			t.Fatalf("rule %q in argv = %q", rule, argv)
		}
	}
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "access: readonly\n") {
		t.Fatalf("show = %q", show)
	}
}

func TestAnOpenProfileLaunchesAsBeforeAndShowsNoAccess(t *testing.T) {
	fx := newAttemptFixture(t)
	accessPolicy(t, fx, readonlyPolicy)
	fx.startProfile("plain")
	argv := dropSession(t, fx.rt.lastCreate().Command)
	if len(argv) != 7 || !slices.Equal(argv[1:6], []string{"--dangerously-skip-permissions", "--model", "sonnet", "--effort", "low"}) {
		t.Fatalf("argv = %q", argv)
	}
	if show := fx.h.ok("attempt", "show", "a1"); strings.Contains(show, "access") {
		t.Fatalf("show = %q", show)
	}
}

func TestRouteListShowsEachProfilesAccess(t *testing.T) {
	fx := newAttemptFixture(t)
	accessPolicy(t, fx, readonlyPolicy)
	out := fx.h.ok("route", "list")
	for _, want := range []string{"{name,harness,model,effort,valid,warn,access}", "look,claude,sonnet,low,yes,\"\",readonly\n", "plain,claude,sonnet,low,yes,\"\",open\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("route list lacks %q:\n%s", want, out)
		}
	}
}

func TestReadonlyIsRefusedForOtherHarnessesAskAndTheSupervisor(t *testing.T) {
	fx := newAttemptFixture(t)
	for body, want := range map[string]string{
		`{"profiles":{"x":{"harness":"codex","model":"gpt-6-luna","effort":"low","access":"readonly"}}}`:              `access "readonly" is only available for claude, not codex`,
		`{"profiles":{"x":{"harness":"opencode","access":"readonly"}}}`:                                               `access "readonly" is only available for claude, not opencode`,
		`{"profiles":{"x":{"harness":"claude","model":"sonnet","effort":"low","access":"readonly","approve":"ask"}}}`: `cannot ask for approval`,
		`{"profiles":{"x":{"harness":"claude","model":"sonnet","effort":"low","access":"write"}}}`:                    `access "write" must be "open" or "readonly"`,
	} {
		accessPolicy(t, fx, body)
		for _, args := range [][]string{{"route", "list"}, {"attempt", "start", "--profile", "x", "--prompt-file", fx.brief, "t1"}} {
			if _, errOut, code := fx.h.run(args...); code != 2 || !strings.Contains(errOut, want) {
				t.Fatalf("%s %q code=%d stderr=%q", body, args, code, errOut)
			}
		}
	}
	accessPolicy(t, fx, readonlyPolicy)
	if _, errOut, code := fx.h.run("supervisor", "start", "--profile", "look"); code != 2 || !strings.Contains(errOut, "read-only") {
		t.Fatalf("supervisor code=%d stderr=%q", code, errOut)
	}
}
