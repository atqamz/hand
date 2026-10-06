package board_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

func TestTheTaskPageIsAThread(t *testing.T) {
	st := open(t)
	seed(t, st)
	ctx := context.Background()
	if _, err := st.AddReport(ctx, 1, state.ReportProgress, "Next: **tests**"); err != nil {
		t.Fatal(err)
	}
	body := get(t, board.New(st, token, board.Options{}), "/task/t1")
	if n := strings.Count(body, "data-region="); n != 1 || !strings.Contains(body, `data-region="task"`) {
		t.Fatalf("task page regions = %d:\n%s", n, body)
	}
	contains(t, "task thread", body,
		`<span class="ref">t1</span>`, `<span class="status pill" data-state="running">active</span>`,
		"plan p1", "<ol><li>reproduce</li><li>fix the cookie</li></ol>", ">Mark read</button>",
		`<article class="card report" id="r1"`, `<article class="card report" id="r2"`, "<strong>tests</strong>",
		`<li class="check" data-state="running" id="a1">`,
		"Keep the old cookie name?", `href="/decision/d1"`)
}

func TestTheDecisionPageAnswers(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token, board.Options{})
	contains(t, "open decision", get(t, h, "/decision/d1"), `<form class="answer" method="post" action="/decision/d1/answer"`, "Keep the old cookie name?", `href="/task/t1"`, `data-state="waiting"`)
	if res := post(h, "/decision/d1/answer", url.Values{"answer": {"yes, keep it"}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("answer = %d", res.StatusCode)
	}
	answered := get(t, h, "/decision/d1")
	contains(t, "answered decision", answered, "yes, keep it", "operator (board)", `data-state="passing"`)
	lacks(t, "answered decision", answered, `action="/decision/d1/answer"`)
}

func TestTheFleetListIsQuiet(t *testing.T) {
	links := []board.FleetLink{{ID: "f6b63e98d4af2", Name: "hand"}, {ID: "fda07bc0c8b0f", Name: "work"}}
	host := func(loopback bool, fleets []board.FleetLink) string {
		h := board.NewHost(board.HostOptions{Loopback: loopback, Resolve: func(string) (string, error) { return "", state.ErrNotFound }, List: func() ([]board.FleetLink, error) { return fleets, nil }})
		return fetch(h, "/").Body.String()
	}
	loop := host(true, links)
	for _, l := range links {
		row := regexp.MustCompile(`<li class="fleet tint-[0-9]+">[\s\S]*?</li>`).FindAllString(loop, -1)
		found := false
		for _, r := range row {
			if strings.Contains(r, `href="/`+l.ID+`/"`) {
				found = true
				contains(t, "fleet row", r, l.Name, `<span class="ref">`+l.ID+`</span>`, `class="fleet-name"`)
			}
		}
		if !found {
			t.Fatalf("no row for %s:\n%s", l.ID, loop)
		}
	}
	network := host(false, links)
	contains(t, "network list", network, "fleet's link")
	lacks(t, "network list", network, "f6b63e98d4af2", "work", `class="fleet"`)
	contains(t, "empty list", host(true, nil), "Run <code>hand init</code> in a folder")
}

func TestTheFirstRunState(t *testing.T) {
	body := get(t, board.New(open(t), token, board.Options{Controls: true, Control: func(context.Context, ...string) error { return nil }}), "/")
	contains(t, "first run", body, "All clear", `action="/supervisor/start"`, `name="harness"`, "No tasks yet. They appear here when you ask the supervisor for something.", `<span class="word">no supervisor</span>`)
	lacks(t, "first run", body, "hand task add")
}

func TestTheReadOnlyBoard(t *testing.T) {
	fx := newFixture(t)
	fx.options.Controls = false
	fx.status = "blocked"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	seed(t, fx.st)
	body := get(t, fx.handler(), "/")
	lacks(t, "read-only board", body, `id="composer"`, `action="/supervisor/keys"`, `action="/supervisor/resume"`, `action="/supervisor/stop"`, `action="/supervisor/interrupt"`, `action="/supervisor/start"`, `class="skip"`, `#composer-text`)
	contains(t, "read-only board", body, `action="/decision/d1/answer"`, `action="/report/r1/ack"`, "This board is read-only", "from a board on loopback")
}

func TestTheReadOnlyBoardSaysWhyWithoutASupervisor(t *testing.T) {
	fx := newFixture(t)
	fx.options.Controls = false
	workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	body := get(t, fx.handler(), "/")
	contains(t, "network board", region(body, "queue"), `data-kind="nosup"`, "Start it from a board on loopback.")
	contains(t, "network board", body, "This board is read-only")
	lacks(t, "network board", body, "Start one in Chat", `class="skip"`, `id="composer-text"`)
}

func TestErrorPagesKeepNoPaths(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	fx.fail = errors.New("stop failed at /home/me/.secret/fleet/hand.db")
	rec := request(fx.handler(), "POST", "/supervisor/stop", url.Values{"csrf": {token}}, true)
	body := rec.Body.String()
	if rec.Code != http.StatusInternalServerError || strings.Contains(body, "/home/me") || !strings.Contains(body, "hand.db") {
		t.Fatalf("error page = %d:\n%s", rec.Code, body)
	}
	contains(t, "error page", body, `<article class="card error">`, `href="/"`)
	missing := request(fx.handler(), "GET", "/task/t99", nil, true)
	if missing.Code != http.StatusNotFound || strings.Contains(missing.Body.String(), "/home/") {
		t.Fatalf("missing task = %d:\n%s", missing.Code, missing.Body.String())
	}
}

func TestRefRouteRedirects(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token, board.Options{})
	for ref, want := range map[string]string{"t1": "/task/t1", "d1": "/decision/d1", "a1": "/task/t1#a1", "r1": "/task/t1#r1"} {
		rec := request(h, "GET", "/ref/"+ref, nil, true)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
			t.Errorf("/ref/%s = %d %q, want %q", ref, rec.Code, rec.Header().Get("Location"), want)
		}
	}
	for _, ref := range []string{"t99", "x1", "d0"} {
		if rec := request(h, "GET", "/ref/"+ref, nil, true); rec.Code != http.StatusNotFound {
			t.Errorf("/ref/%s = %d", ref, rec.Code)
		}
	}
}

func TestThreadPagesUseTheDispatchModule(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token, board.Options{})
	task := get(t, h, "/task/t1")
	contains(t, "task page", task,
		`<article class="card" data-role="goal"><header class="slug"><span class="who">goal</span>`,
		`<article class="card" data-role="plan"><header class="slug"><span class="who">plan p1</span>`,
		`<article class="card report" id="r1" data-status="done"><header class="slug"><span class="who">r1</span>`,
		`<li class="event"><time datetime=`,
		`<li class="check" data-state="running" id="a1">`,
		`<span class="code" data-tone="wait">DECISION</span>`,
		`<div class="strip">`)
	lacks(t, "task page", task, `data-region="status"`)
	contains(t, "decision page", get(t, h, "/decision/d1"), `<p class="slug"><span class="status pill" data-state="waiting">open</span><span class="ref">d1</span>`, `<form class="answer" method="post" action="/decision/d1/answer"`)
	list := board.NewHost(board.HostOptions{Loopback: true, Resolve: func(string) (string, error) { return "", state.ErrNotFound }, List: func() ([]board.FleetLink, error) {
		return []board.FleetLink{{ID: "f6b63e98d4af2", Name: "hand"}}, nil
	}})
	contains(t, "fleet list", fetch(list, "/").Body.String(), `<li class="fleet tint-`, `<a class="fleet-name" href="/f6b63e98d4af2/">hand</a>`)
	contains(t, "error page", request(h, "GET", "/task/t99", nil, true).Body.String(), `<header class="masthead">`, `<span class="code" data-tone="fail">404</span>`)
}

func TestThreadPagesCarryAStaticStrip(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token, board.Options{})
	for _, path := range []string{"/task/t1", "/decision/d1", "/supervisor/log"} {
		body := get(t, h, path)
		contains(t, path, body, `<div class="strip"><div class="status-line"`, `<span class="word">no supervisor</span>`)
		lacks(t, path, body, `data-region="status"`)
	}
	contains(t, "decision slug", get(t, h, "/decision/d1"), `<span class="status pill" data-state="waiting">open</span>`)
}

func TestDecisionShowsItsHeadlineAndMarkdownBody(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	task := active(t, fx.st, "Fix login")
	if _, err := fx.st.Ask(ctx, task.ID, "Pick how to publish the docs\n\n1. From CI, see t1\n2. From a wiki"); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Ask(ctx, task.ID, "Ship it?"); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	q := region(get(t, h, "/"), "queue")
	contains(t, "needs", q, `<span class="wait-title">Pick how to publish the docs</span>`, "<ol>", `href="/ref/t1"`, `data-fill="From CI, see t1"`, `data-fill="From a wiki"`)
	page := get(t, h, "/decision/d1")
	contains(t, "page", page, `<h2 class="thread-title">Pick how to publish the docs</h2>`, "<ol>", `data-fill="From a wiki"`, `data-fetch`)
	lacks(t, "one-line page", get(t, h, "/decision/d2"), `data-fill=`, `<div class="question md">`)
}

func TestAnsweringAnAnsweredDecisionKeepsTheDraft(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	task := active(t, fx.st, "Fix login")
	if _, err := fx.st.Ask(ctx, task.ID, "Keep it?"); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Answer(ctx, 1, "yes, keep", "operator"); err != nil {
		t.Fatal(err)
	}
	rec := fetchPost(fx.handler(), "/decision/d1/answer", url.Values{"answer": {"no"}})
	if msg, _ := url.PathUnescape(rec.Header().Get("X-Hand-Error")); rec.Code != http.StatusConflict || !strings.Contains(msg, "yes, keep") {
		t.Fatalf("second answer = %d %q", rec.Code, msg)
	}
}

func TestDecisionSlugFollowsItsStatus(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	task := active(t, fx.st, "Fix login")
	if _, err := fx.st.Ask(ctx, task.ID, "Keep it?"); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Answer(ctx, 1, "yes", "operator"); err != nil {
		t.Fatal(err)
	}
	lacks(t, "answered page", get(t, fx.handler(), "/decision/d1"), `data-state="waiting">open`)
}

func TestUnknownPathsUseTheErrorPage(t *testing.T) {
	rec := request(board.New(open(t), token, board.Options{}), "GET", "/nope", nil, true)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `<header class="masthead">`) || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("unknown path = %d %q:\n%s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func TestWrongMethodUsesTheErrorPage(t *testing.T) {
	rec := request(board.New(open(t), token, board.Options{}), "GET", "/supervisor/stop", nil, true)
	if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Body.String(), `<header class="masthead">`) {
		t.Fatalf("wrong method = %d:\n%s", rec.Code, rec.Body.String())
	}
}

func TestThe403PageNamesOpenPrint(t *testing.T) {
	rec := request(board.New(open(t), token, board.Options{}), "GET", "/", nil, false)
	body := rec.Body.String()
	if rec.Code != http.StatusForbidden || !strings.Contains(body, "<code>hand open --print</code>") || strings.Contains(body, "`") {
		t.Fatalf("403 = %d:\n%s", rec.Code, body)
	}
}

func TestTheFleetPageHasASkipLinkAndALinkState(t *testing.T) {
	fx := newFixture(t)
	page := get(t, fx.handler(), "/")
	contains(t, "fleet page", page, `<a class="skip" href="#composer-text">Skip to the message box</a>`, `id="composer-text"`, `<p id="link" class="link-state" role="status" hidden></p>`)
	fx.options.Controls = false
	lacks(t, "network page", get(t, fx.handler(), "/"), `class="skip"`)
	if n := strings.Index(page, `class="skip"`); n < 0 || n > strings.Index(page, `<header class="masthead">`) {
		t.Fatal("the skip link must come before the masthead")
	}
}

func TestALostLinkDimsTheStatusAndQuietsTheIcon(t *testing.T) {
	js, css := asset(t, "app.js"), asset(t, "board.css")
	if !strings.Contains(js, `document.body.dataset.link = "lost";`) || !strings.Contains(js, "delete document.body.dataset.link;") || !strings.Contains(js, `document.body.dataset.link === "lost" ? plain`) {
		t.Fatal("app.js does not mark a lost link on the body, clear it, and quiet the icon")
	}
	contains(t, "css", css, "body[data-link=lost] .strip .status-line{opacity:.45}")
	contains(t, "app.js", js, `.querySelector(".pill .word")`, `now.dataset.was = now.textContent;`, `now.textContent = "unknown";`, `back.textContent = back.dataset.was;`)
}

func TestThreadPagesCarryTheToastAndReloadAfterAnAnswer(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.st.Ask(context.Background(), active(t, fx.st, "Fix login").ID, "Keep it?"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/decision/d1", "/task/t1", "/"} {
		if n := strings.Count(get(t, fx.handler(), path), `id="toast"`); n != 1 {
			t.Fatalf("%s holds %d toasts, want 1", path, n)
		}
	}
	js := asset(t, "app.js")
	if !strings.Contains(js, "if (!toast) return;") || !strings.Contains(js, "if (regions.size === 0) location.reload();") {
		t.Fatal("app.js does not guard the toast or refresh a page without live regions after an action")
	}
}

func TestChipsSkipCodeFences(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.st.Ask(context.Background(), active(t, fx.st, "Fix login").ID, "Pick one\n\n```\n1. make build\n```\n\n1. Ship it\n2. Wait"); err != nil {
		t.Fatal(err)
	}
	body := get(t, fx.handler(), "/decision/d1")
	contains(t, "chips", body, `data-fill="Ship it"`, `data-fill="Wait"`)
	lacks(t, "chips", body, `data-fill="make build"`)
}

func TestAPollThatWorksReopensTheStream(t *testing.T) {
	js := asset(t, "app.js")
	_, rest, _ := strings.Cut(js, "const refresh = async () => {")
	body, _, _ := strings.Cut(rest, "const follow = () => {")
	if !strings.Contains(body, "follow();") {
		t.Fatal("a successful refresh does not reopen the event stream")
	}
}

func TestTheTaskPageHead(t *testing.T) {
	fx := newFixture(t)
	task := active(t, fx.st, "Fix login")
	body := get(t, fx.handler(), "/task/t1")
	contains(t, "head", body, `<p class="crumbs">`, `<h2 class="thread-title">Fix login</h2>`, `<span class="status pill" data-state="`, `<hr class="rule">`)
	_ = task
}

func TestTheTaskPageListsDecisions(t *testing.T) {
	fx := newFixture(t)
	task := active(t, fx.st, "Fix login")
	ctx := context.Background()
	d, err := fx.st.Ask(ctx, task.ID, "Keep the cookie?\n\nIt is old.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Answer(ctx, d.ID, "yes, keep it", "operator"); err != nil {
		t.Fatal(err)
	}
	section := get(t, fx.handler(), "/task/t1")
	section = section[strings.Index(section, `<h3>Decisions</h3>`):]
	contains(t, "decisions", section, `<a class="ref" href="/decision/d1">d1</a>`, "Keep the cookie?", "yes, keep it")
}

func TestTaskEventsAreAGrid(t *testing.T) {
	fx := newFixture(t)
	task := active(t, fx.st, "Fix login")
	attempt(t, fx.st, task.ID, state.AttemptRunning, "")
	body := get(t, fx.handler(), "/task/t1")
	contains(t, "events", body, `<ol class="events">`, `<li class="event"><time datetime=`, `<span class="kind">task added</span>`, `<span class="detail">`)
	lacks(t, "events", body, `<li class="hand-line"><span class="slug"><span class="who">task.`)
}

func TestReportsLeadWithTheirSummary(t *testing.T) {
	fx := newFixture(t)
	task := active(t, fx.st, "Fix login")
	a := attempt(t, fx.st, task.ID, state.AttemptRunning, "")
	if _, err := fx.st.AddReport(context.Background(), a.ID, state.ReportDone, "Shipped the fix\n\nAll tests pass."); err != nil {
		t.Fatal(err)
	}
	body := get(t, fx.handler(), "/task/t1")
	contains(t, "report", body, `<span class="report-title">Shipped the fix</span>`, "<p>All tests pass.</p>")
	lacks(t, "report", body, "<p>Shipped the fix</p>")
}

func TestAnAttemptPassesOnlyWithADoneReport(t *testing.T) {
	fx := newFixture(t)
	task := active(t, fx.st, "Fix login")
	attempt(t, fx.st, task.ID, state.AttemptExited, "")
	lacks(t, "exited", get(t, fx.handler(), "/task/t1"), `data-state="passing" id="a1"`)
}

func TestDecisionPageCtrlEnter(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.st.Ask(context.Background(), active(t, fx.st, "Fix login").ID, "Keep it?"); err != nil {
		t.Fatal(err)
	}
	contains(t, "form", get(t, fx.handler(), "/decision/d1"), `<form class="answer" method="post" action="/decision/d1/answer" data-fetch>`, `<textarea name="answer"`)
	contains(t, "app.js", asset(t, "app.js"), `(e.ctrlKey || e.metaKey)`, `form?.hasAttribute("data-fetch")`, "form.requestSubmit()")
}

func TestAnAnsweredDecisionShowsYourCard(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	d, err := fx.st.Ask(ctx, active(t, fx.st, "Fix login").ID, "Keep it?")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Answer(ctx, d.ID, "yes", "operator"); err != nil {
		t.Fatal(err)
	}
	contains(t, "answered", get(t, fx.handler(), "/decision/d1"), `<article class="card from-you" data-role="operator"><header class="slug"><span class="who">you</span>`, "yes")
}
