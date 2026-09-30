package board_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

var needsSlug = regexp.MustCompile(`<details class="wait" data-kind="([a-z]+)" id="wait-[a-z0-9]+"[^>]*>\s*<summary>[\s\S]*?<span class="slug"><span class="code" data-tone="([a-z]+)">([A-Z ]+)</span><span class="ref">([a-z0-9]+)</span>`)

func TestNeedsItemsCarryTheirWord(t *testing.T) {
	fx := newFixture(t)
	fx.status, fx.hint = "blocked", "Trust this folder?"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	one := active(t, fx.st, "Fix login")
	if _, err := fx.st.Ask(ctx, one.ID, "Keep the old cookie name?"); err != nil {
		t.Fatal(err)
	}
	two := active(t, fx.st, "Flaky test")
	attempt(t, fx.st, two.ID, state.AttemptFailed, "launch did not finish")
	three := active(t, fx.st, "Docs")
	a := attempt(t, fx.st, three.ID, state.AttemptRunning, "")
	if _, err := fx.st.AddReport(ctx, a.ID, state.ReportProgress, "Halfway there"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range needsSlug.FindAllStringSubmatch(region(get(t, fx.handler(), "/"), "queue"), -1) {
		got = append(got, m[1]+" "+m[2]+" "+m[3]+" "+m[4])
	}
	want := []string{"blocked fail BLOCKED s1", "failure fail FAILED a1", "decision wait DECISION d1", "report neutral REPORT r1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("needs = %q", got)
	}
	resume := newFixture(t)
	resume.supervisor(t, state.AttemptInterrupted, "gen-1")
	contains(t, "resume", region(get(t, resume.handler(), "/"), "queue"), `data-tone="fail">INTERRUPTED</span>`)
}

func TestTheBudgetIsOneLinePerTask(t *testing.T) {
	fx := newFixture(t)
	one := active(t, fx.st, "Fix login")
	attempt(t, fx.st, one.ID, state.AttemptRunning, "")
	active(t, fx.st, "Write docs")
	tasks := region(get(t, fx.handler(), "/"), "tasks")
	if n := strings.Count(tasks, `<li class="check"`); n != 2 {
		t.Fatalf("budget lines = %d:\n%s", n, tasks)
	}
	contains(t, "budget", tasks, `<span class="slug"><a class="ref" href="/task/t1">T1</a><span class="status">ACTIVE</span><span class="attempt">A1 CODEX GPT-6-LUNA RUNNING</span></span><span class="check-title">Fix login</span>`)
}

var newsroom = regexp.MustCompile(`(?i)\b(wire|flash|bulletin|urgent|routine|service|dispatch|dispatches|news budget|tray)\b|NO\. \d`)

func TestNoNewsroomWords(t *testing.T) {
	fx := newFixture(t)
	fx.status, fx.hint = "blocked", "Trust this folder?"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	task := active(t, fx.st, "Fix login")
	a := workerAttempt(t, fx.st, task.ID)
	if _, err := fx.st.AddReport(ctx, a.ID, state.ReportDone, "Shipped\n\nAll green"); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Ask(ctx, task.ID, "Keep the cookie?\n\n1. Yes\n2. No"); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	sources := map[string]string{"board.css": asset(t, "board.css"), "app.js": asset(t, "app.js")}
	for _, path := range []string{"/", "/task/t1", "/decision/d1", "/nope"} {
		sources[path] = request(h, "GET", path, nil, true).Body.String()
	}
	for name, text := range sources {
		if m := newsroom.FindString(text); m != "" {
			i := strings.Index(text, m)
			t.Errorf("%s still says %q: …%s…", name, m, text[max(0, i-60):min(len(text), i+60)])
		}
	}
}
