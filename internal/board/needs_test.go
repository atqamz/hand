package board_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

var needsSlug = regexp.MustCompile(`<details class="wait" data-kind="([a-z]+)" id="wait-[a-z]+-[a-z0-9]+"[^>]*>\s*<summary>[\s\S]*?<span class="slug"><span class="code" data-tone="([a-z]+)">([A-Z ]+)</span><span class="ref">([a-z0-9]+)</span>`)

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

func TestTasksAreOneLinePerTask(t *testing.T) {
	fx := newFixture(t)
	one := active(t, fx.st, "Fix login")
	attempt(t, fx.st, one.ID, state.AttemptRunning, "")
	active(t, fx.st, "Write docs")
	tasks := region(get(t, fx.handler(), "/"), "tasks")
	if n := strings.Count(tasks, `<li class="check"`); n != 2 {
		t.Fatalf("task rows = %d:\n%s", n, tasks)
	}
	contains(t, "tasks", tasks, `<a class="ref" href="/task/t1">t1</a><span class="check-title">Fix login</span><span class="meta">a1 codex gpt-6-luna · running</span>`)
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

const permission = "────────────────────────\n Bash command\n  rm -f $r/$f\n Claude Code will automatically deny this request in 1:59\n Do you want to proceed?\n❯ 1. Yes\n  2. No\n Esc to cancel · Tab to amend"

func TestNeedsItemsCarryTheirTone(t *testing.T) {
	fx := newFixture(t)
	fx.status, fx.hint, fx.screen = "blocked", "Esc to cancel · Tab to amend", permission
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	task := active(t, fx.st, "Fix login")
	a := workerAttempt(t, fx.st, task.ID)
	if _, err := fx.st.AddReport(ctx, a.ID, state.ReportDone, "Shipped"); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Ask(ctx, task.ID, "Keep it?"); err != nil {
		t.Fatal(err)
	}
	q := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "tones", q, `data-kind="blocked" id="wait-blocked-s1" open data-tone="fail">`, `data-kind="decision" id="wait-decision-d1" data-tone="wait">`, `data-kind="report" id="wait-report-r1" data-tone="neutral">`)
	blocked := q[:strings.Index(q, `id="wait-decision-d1"`)]
	contains(t, "blocked item", blocked, `<svg class="icon"`, `<span class="wait-title">Bash: rm -f $r/$f · Do you want to proceed?</span>`, `<span class="countdown live" data-countdown="1:59">1:59</span>`, `>2 No</button>`, `>Esc</button>`, `>1 Yes</button>`)
	if strings.Index(blocked, ">2 No</button>") > strings.Index(blocked, ">1 Yes</button>") {
		t.Fatal("No comes after Yes")
	}
}

func TestTheCountdownNeverGoesNegative(t *testing.T) {
	contains(t, "app.js", asset(t, "app.js"), "[data-countdown]", "Math.max(0,", `"denying…"`)
}

func TestAllClear(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	task := active(t, fx.st, "Fix login")
	workerAttempt(t, fx.st, task.ID)
	d, err := fx.st.Ask(ctx, task.ID, "Keep it?")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Answer(ctx, d.ID, "yes", "operator"); err != nil {
		t.Fatal(err)
	}
	if err := fx.st.AdvanceWakeCursor(ctx, 1, 1<<40); err != nil {
		t.Fatal(err)
	}
	q := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "all clear", q, `<p class="all-clear">`, "All clear", `since <time`, "1 running")
}

func TestTheNoSupervisorItemCarriesStartAndResume(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptStopped, "gen-1")
	workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	q := region(get(t, fx.handler(), "/"), "queue")
	item := q[strings.Index(q, `data-kind="nosup"`):]
	item = item[:strings.Index(item, "</details>")]
	contains(t, "nosup", item, `action="/supervisor/resume"`, `action="/supervisor/start"`)
}

func TestTasksGroupUnderActiveAndInbox(t *testing.T) {
	fx := newFixture(t)
	fx.panes = map[string]string{"3": "blocked"}
	ctx := context.Background()
	one := active(t, fx.st, "Fix login")
	a := workerAttempt(t, fx.st, one.ID)
	if err := fx.st.NoteAttempt(ctx, a.ID, "blocked", "Pick one"); err != nil {
		t.Fatal(err)
	}
	active(t, fx.st, "Ship it")
	if _, err := fx.st.AddTask(ctx, "hand", "Later", ""); err != nil {
		t.Fatal(err)
	}
	tasks := region(get(t, fx.handler(), "/"), "tasks")
	contains(t, "tasks", tasks, `<h3 class="group">Active <span class="n">2</span></h3>`, `<h3 class="group">Inbox <span class="n">1</span></h3>`, `<span class="check-title">Fix login</span><span class="meta">a1 claude sonnet · blocked</span>`)
	lacks(t, "tasks", tasks, "A1 CLAUDE")
}

var summaries = regexp.MustCompile(`<summary>[\s\S]*?</summary>`)

func TestANeedsItemKeepsLinksOutOfItsSummary(t *testing.T) {
	fx := newFixture(t)
	fx.panes = map[string]string{"3": "blocked"}
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if err := fx.st.NoteAttempt(context.Background(), a.ID, "blocked", "Esc to cancel · Tab to amend"); err != nil {
		t.Fatal(err)
	}
	attempt(t, fx.st, active(t, fx.st, "Flaky test").ID, state.AttemptFailed, "launch did not finish")
	q := region(get(t, fx.handler(), "/"), "queue")
	found := summaries.FindAllString(q, -1)
	for _, s := range found {
		if strings.Contains(s, "<a ") {
			t.Fatalf("a summary holds a link: %s", s)
		}
	}
	if len(found) < 2 {
		t.Fatalf("summaries = %q", found)
	}
	contains(t, "worker item", q, `a1 on <a class="ref" href="/task/t1">t1</a>`)
}

func TestTabTargetsClearTheStickyTabs(t *testing.T) {
	contains(t, "board.css", asset(t, "board.css"), "#needs,#chat{scroll-margin-top:48px}")
}

func TestABlockedAndAResumeItemNeverShareAnID(t *testing.T) {
	blocked := newFixture(t)
	blocked.status, blocked.hint = "blocked", "Trust this folder?"
	blocked.supervisor(t, state.AttemptRunning, "gen-1")
	contains(t, "blocked", region(get(t, blocked.handler(), "/"), "queue"), `id="wait-blocked-s1"`)
	resume := newFixture(t)
	resume.supervisor(t, state.AttemptInterrupted, "gen-1")
	contains(t, "resume", region(get(t, resume.handler(), "/"), "queue"), `id="wait-resume-s1"`)
}

func TestNeedsListsTheOldestUnreadReports(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	task := active(t, fx.st, "Chatty")
	a := attempt(t, fx.st, task.ID, state.AttemptRunning, "")
	for range 51 {
		if _, err := fx.st.AddReport(ctx, a.ID, state.ReportProgress, "step"); err != nil {
			t.Fatal(err)
		}
	}
	queue := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "queue", queue, `<span class="ref">r1</span>`, `<span class="ref">r2</span>`)
	lacks(t, "queue", queue, `<span class="ref">r51</span>`)
}

func TestEveryNeedsSummaryEndsInAChevron(t *testing.T) {
	fx := newFixture(t)
	attempt(t, fx.st, active(t, fx.st, "Flaky test").ID, state.AttemptFailed, "launch did not finish")
	attempt(t, fx.st, active(t, fx.st, "Fix login").ID, state.AttemptFailed, "launch did not finish")
	found := summaries.FindAllString(region(get(t, fx.handler(), "/"), "queue"), -1)
	if len(found) < 2 {
		t.Fatalf("summaries = %q", found)
	}
	for _, s := range found {
		if !strings.HasSuffix(s, `<path d="M4.5 6.25L8 9.75l3.5-3.5"/></svg></summary>`) {
			t.Fatalf("a summary does not end in the chevron: %s", s)
		}
	}
}
