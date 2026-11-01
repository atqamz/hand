package cli_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func capture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "harness", "testdata", "claude-2.1.316-"+name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func askPolicy(t *testing.T, fx *attemptFixture, extra string) {
	t.Helper()
	body := `{"profiles":{"ask":{"harness":"claude","model":"sonnet","effort":"low","approve":"ask"},"plain":{"harness":"claude","model":"sonnet","effort":"low"}}` + extra + `}`
	if err := os.WriteFile(filepath.Join(fx.h.home, "routing.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (fx *attemptFixture) startProfile(profile string) string {
	fx.h.t.Helper()
	return fx.h.ok("attempt", "start", "--profile", profile, "--prompt-file", fx.brief, "t1")
}

func TestAnAskProfileStartsTheWorkerWithoutTheSkipFlag(t *testing.T) {
	fx := newAttemptFixture(t)
	askPolicy(t, fx, "")
	fx.startProfile("ask")
	argv := fx.rt.lastCreate().Command
	if len(argv) != 8 || filepath.Base(argv[0]) != exe("", "claude") || !slices.Equal(argv[1:7], []string{"--permission-mode", "default", "--model", "sonnet", "--effort", "low"}) || !strings.HasPrefix(argv[7], "Fix the login bug") {
		t.Fatalf("argv = %q", argv)
	}
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "approve: ask\n") {
		t.Fatalf("show = %q", show)
	}
}

func TestADefaultProfileLaunchesAsBefore(t *testing.T) {
	fx := newAttemptFixture(t)
	askPolicy(t, fx, "")
	fx.startProfile("plain")
	argv := fx.rt.lastCreate().Command
	if len(argv) != 7 || filepath.Base(argv[0]) != exe("", "claude") || !slices.Equal(argv[1:6], []string{"--dangerously-skip-permissions", "--model", "sonnet", "--effort", "low"}) || !strings.HasPrefix(argv[6], "Fix the login bug") {
		t.Fatalf("argv = %q", argv)
	}
	if show := fx.h.ok("attempt", "show", "a1"); strings.Contains(show, "approve") {
		t.Fatalf("show = %q", show)
	}
}

func TestAskIsRefusedForOtherHarnessesAndTheSupervisor(t *testing.T) {
	fx := newAttemptFixture(t)
	bad := `{"profiles":{"x":{"harness":"codex","model":"gpt-6-luna","effort":"low","approve":"ask"}}}`
	if err := os.WriteFile(filepath.Join(fx.h.home, "routing.json"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"route", "list"}, {"attempt", "start", "--profile", "x", "--prompt-file", fx.brief, "t1"}} {
		if _, errOut, code := fx.h.run(args...); code != 2 || !strings.Contains(errOut, `approve "ask" is only available for claude, not codex`) {
			t.Fatalf("%q code=%d stderr=%q", args, code, errOut)
		}
	}
	askPolicy(t, fx, "")
	if _, errOut, code := fx.h.run("supervisor", "start", "--profile", "ask"); code != 2 || !strings.Contains(errOut, "workers only") {
		t.Fatalf("supervisor code=%d stderr=%q", code, errOut)
	}
}

func TestAttemptKeysLeavesAnAskAttemptsPermissionScreenToTheBoard(t *testing.T) {
	fx := newAttemptFixture(t)
	askPolicy(t, fx, "")
	fx.startProfile("ask")
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 7, capture(t, "commit") })
	_, errOut, code := fx.h.run("attempt", "keys", "--revision", "7", "a1", "1")
	if code != 2 || !strings.Contains(errOut, "Board") || !strings.Contains(errOut, "a1") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := fx.rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys sent = %q", got)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 8, "● Working\n\n❯ " })
	if out := fx.h.ok("attempt", "keys", "--revision", "8", "a1", "enter"); !strings.Contains(out, "keys: enter") {
		t.Fatalf("keys = %q", out)
	}
	fx.h.board = true
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 9, capture(t, "commit") })
	if out := fx.h.ok("attempt", "keys", "--revision", "9", "a1", "1"); strings.Contains(out, "grant") {
		t.Fatalf("approve stored a grant: %q", out)
	}
	if got := fx.rt.keysSent(); !slices.Equal(got, []string{"enter", "1"}) {
		t.Fatalf("keys sent = %q", got)
	}
}

func TestAnAttemptWithoutAskStillTakesKeysOnAPermissionScreen(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 7, capture(t, "commit") })
	fx.h.ok("attempt", "keys", "--revision", "7", "a1", "1")
	if got := fx.rt.keysSent(); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("keys sent = %q", got)
	}
}

func TestApproveAlwaysStoresAGrantOnlyTheSameProjectsNextAttemptGets(t *testing.T) {
	fx := newAttemptFixture(t)
	askPolicy(t, fx, "")
	fx.h.ok("project", "add", "other", gitRepo(t))
	fx.h.ok("task", "add", "other", "Fix logout")
	fx.h.ok("task", "start", "t2")
	fx.h.ok("task", "add", "app", "Fix signup")
	fx.h.ok("task", "start", "t3")
	fx.startProfile("ask")
	fx.h.board = true
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 7, capture(t, "commit") })
	for _, key := range []string{"1", "4"} {
		if out := fx.h.ok("attempt", "keys", "--revision", "7", "a1", key); strings.Contains(out, "grant") {
			t.Fatalf("key %s: %q", key, out)
		}
		if grants := fx.h.ok("project", "grants", "app"); strings.Contains(grants, "git commit") {
			t.Fatalf("key %s stored a grant: %q", key, grants)
		}
	}
	if out := fx.h.ok("attempt", "keys", "--revision", "7", "a1", "2"); !strings.Contains(out, "grant: git commit") {
		t.Fatalf("keys = %q", out)
	}
	fx.h.ok("attempt", "keys", "--revision", "7", "a1", "2")
	grants := fx.h.ok("project", "grants", "app")
	if !strings.Contains(grants, "1,claude,git commit,") || strings.Count(grants, "git commit") != 1 {
		t.Fatalf("grants = %q", grants)
	}
	if other := fx.h.ok("project", "grants", "other"); strings.Contains(other, "git commit") {
		t.Fatalf("other grants = %q", other)
	}
	fx.h.ok("attempt", "stop", "a1")
	fx.h.ok("attempt", "start", "--profile", "ask", "--prompt-file", fx.brief, "t3")
	argv := fx.rt.lastCreate().Command
	if len(argv) != 9 || argv[7] != "--allowedTools=Bash(git commit *)" || slices.Contains(argv, "--dangerously-skip-permissions") {
		t.Fatalf("next attempt of the project: %q", argv)
	}
	fx.h.ok("attempt", "start", "--profile", "ask", "--prompt-file", fx.brief, "t2")
	if argv := fx.rt.lastCreate().Command; len(argv) != 8 || slices.ContainsFunc(argv, func(a string) bool { return strings.HasPrefix(a, "--allowedTools") }) {
		t.Fatalf("another project's attempt: %q", argv)
	}
	if out := fx.h.ok("project", "grants", "--remove", "1", "app"); !strings.Contains(out, "removed: 1 claude git commit") || strings.Contains(out, "grants[1]") {
		t.Fatalf("remove = %q", out)
	}
	if _, errOut, code := fx.h.run("project", "grants", "--remove", "1", "app"); code != 3 || !strings.Contains(errOut, "grant 1") {
		t.Fatalf("second remove code=%d stderr=%q", code, errOut)
	}
}

func askWatch(t *testing.T, extra string, screen ...string) (*attemptFixture, func() (string, int)) {
	t.Helper()
	fx := newAttemptFixture(t)
	askPolicy(t, fx, extra)
	startClaudeSupervisor(fx.h)
	fx.startProfile("ask")
	text := capture(t, "commit")
	if len(screen) > 0 {
		text = screen[0]
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 7, text })
	stop := startWatch(t, fx, "--every", "30ms")
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Do you want to proceed?" })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "3", "status": "blocked", "agent": "claude"})
	eventually(t, func() bool {
		out, _, _ := fx.h.run("wait", "--after", "0", "--timeout", "1ms")
		return strings.Contains(out, ",attempt.blocked,")
	})
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "idle", "" })
	return fx, stop
}

func denials(fx *attemptFixture) int {
	out, _, _ := fx.h.run("wait", "--after", "0", "--timeout", "1ms")
	return strings.Count(out, ",attempt.denied,")
}

func denyWakes(fx *attemptFixture) int {
	n := 0
	for _, p := range fx.rt.prompts() {
		n += strings.Count(p, "attempt.denied a1")
	}
	return n
}

func TestNoAnswerForTheTimeoutPressesDenyOnceAndWakesTheSupervisorOnce(t *testing.T) {
	fx, _ := askWatch(t, `,"approve_timeout":"2m"`)
	fx.h.advance(time.Minute + 59*time.Second)
	stallTick(t, fx)
	if got := fx.rt.keysSent(); len(got) != 0 || denials(fx) != 0 {
		t.Fatalf("denied early: keys %q, events %d", got, denials(fx))
	}
	fx.h.advance(time.Second)
	eventually(t, func() bool { return denials(fx) == 1 })
	eventually(t, func() bool { return denyWakes(fx) == 1 })
	for range 3 {
		stallTick(t, fx)
	}
	if got := fx.rt.keysSent(); !slices.Equal(got, []string{"4"}) {
		t.Fatalf("keys sent = %q, want one deny", got)
	}
	if n, w := denials(fx), denyWakes(fx); n != 1 || w != 1 {
		t.Fatalf("denied events %d, wakes %d", n, w)
	}
}

func TestAnAnswerBeforeTheDeadlineRecordsNoDenial(t *testing.T) {
	fx, _ := askWatch(t, "")
	fx.h.advance(9 * time.Minute)
	fx.h.board = true
	fx.h.ok("attempt", "keys", "--revision", "7", "a1", "1")
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "working", "" })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "3", "status": "working", "agent": "claude"})
	fx.h.advance(5 * time.Minute)
	for range 3 {
		stallTick(t, fx)
	}
	if got := fx.rt.keysSent(); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("keys sent = %q, want only the operator's", got)
	}
	if n := denials(fx); n != 0 {
		t.Fatalf("denied events = %d", n)
	}
}

func TestAScreenItDoesNotRecogniseIsNeverDenied(t *testing.T) {
	fx, _ := askWatch(t, "")
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 8, "Something odd\n❯ 1. Go\n  2. Stop" })
	fx.h.advance(time.Hour)
	for range 3 {
		stallTick(t, fx)
	}
	if got := fx.rt.keysSent(); len(got) != 0 || denials(fx) != 0 {
		t.Fatalf("keys %q, denied %d", got, denials(fx))
	}
}

func TestAnUnparsedPermissionDialogIsDeniedWithEscAndAWrappedOneWithItsOwnKey(t *testing.T) {
	for name, want := range map[string]string{"long16_last20": "esc", "pipe": "4", "mcp": "esc", "plan": "esc"} {
		fx, _ := askWatch(t, `,"approve_timeout":"2m"`, capture(t, name))
		fx.h.advance(2 * time.Minute)
		eventually(t, func() bool { return denials(fx) == 1 })
		if got := fx.rt.keysSent(); !slices.Equal(got, []string{want}) {
			t.Fatalf("%s: keys sent = %q, want %s", name, got, want)
		}
	}
}

func TestAttemptKeysRefusesAnAskAttemptsDialogEvenWhenItCannotBeParsed(t *testing.T) {
	fx := newAttemptFixture(t)
	askPolicy(t, fx, "")
	fx.startProfile("ask")
	for _, name := range []string{"long16_last20", "pipe"} {
		fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 7, capture(t, name) })
		if _, errOut, code := fx.h.run("attempt", "keys", "--revision", "7", "a1", "1"); code != 2 || !strings.Contains(errOut, "Board") {
			t.Fatalf("%s: code=%d stderr=%q", name, code, errOut)
		}
	}
	if got := fx.rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys sent = %q", got)
	}
}

func TestAttemptKeysRefusesAnAskAttemptsBlockedScreenUnlessItIsATrustScreen(t *testing.T) {
	fx := newAttemptFixture(t)
	askPolicy(t, fx, "")
	fx.startProfile("ask")
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "blocked" })
	for name, text := range map[string]string{
		"mcp":     capture(t, "mcp"),
		"plan":    capture(t, "plan"),
		"unknown": "Something odd\n❯ 1. Go\n  2. Stop",
	} {
		fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 7, text })
		if _, errOut, code := fx.h.run("attempt", "keys", "--revision", "7", "a1", "enter"); code != 2 || !strings.Contains(errOut, "Board") {
			t.Fatalf("%s: code=%d stderr=%q", name, code, errOut)
		}
	}
	if got := fx.rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys sent = %q", got)
	}
	trust := "Quick safety check: Is this a project you created or one you trust?\n❯ Yes, I trust this folder\n  No, exit"
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 8, trust })
	fx.h.ok("attempt", "keys", "--revision", "8", "a1", "enter")
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen, rt.status = 9, "● Working\n\n❯ ", "idle" })
	fx.h.ok("attempt", "keys", "--revision", "9", "a1", "down")
	if got := fx.rt.keysSent(); !slices.Equal(got, []string{"enter", "down"}) {
		t.Fatalf("keys sent = %q", got)
	}
}

func TestAGrantThatCannotBeSavedAfterTheKeyIsAWarningNotAnError(t *testing.T) {
	fx := newAttemptFixture(t)
	askPolicy(t, fx, "")
	fx.startProfile("ask")
	fx.h.board = true
	db, err := sql.Open("sqlite", filepath.Join(fx.h.home, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE project_grant`); err != nil {
		t.Fatal(err)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 7, capture(t, "commit") })
	out, errOut, code := fx.h.run("attempt", "keys", "--revision", "7", "a1", "2")
	if code != 0 || !strings.Contains(out, "keys: 2") || strings.Contains(out, "grant") || !strings.Contains(errOut, "warning: approved, but the grant for \"git commit\" was not saved") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if got := fx.rt.keysSent(); !slices.Equal(got, []string{"2"}) {
		t.Fatalf("keys sent = %q", got)
	}
}

func TestTheCapturedNoKeyRoundTripsThroughTheBoardPath(t *testing.T) {
	fx := newAttemptFixture(t)
	askPolicy(t, fx, "")
	fx.startProfile("ask")
	fx.h.board = true
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 7, capture(t, "commit") })
	fx.h.ok("attempt", "keys", "--revision", "7", "a1", "4")
	if got := fx.rt.keysSent(); !slices.Equal(got, []string{"4"}) {
		t.Fatalf("keys sent = %q", got)
	}
	if grants := fx.h.ok("project", "grants", "app"); strings.Contains(grants, "git commit") {
		t.Fatalf("deny stored a grant: %q", grants)
	}
}
