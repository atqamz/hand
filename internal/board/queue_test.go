package board_test

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

var waitItem = regexp.MustCompile(`<details class="wait" data-kind="([a-z]+)" id="wait-[a-z]+-([a-z0-9]*)"( open)?[^>]*>`)

func waits(body string) (kinds, refs []string, open []bool) {
	for _, m := range waitItem.FindAllStringSubmatch(region(body, "queue"), -1) {
		kinds, refs, open = append(kinds, m[1]), append(refs, m[2]), append(open, m[3] != "")
	}
	return kinds, refs, open
}

func region(body, name string) string {
	start := strings.Index(body, `data-region="`+name+`">`)
	if start < 0 {
		return ""
	}
	start += len(`data-region="` + name + `">`)
	depth := 1
	for i := start; i < len(body); i++ {
		switch {
		case strings.HasPrefix(body[i:], "<section"):
			depth++
		case strings.HasPrefix(body[i:], "</section>"):
			if depth--; depth == 0 {
				return body[start:i]
			}
		}
	}
	return body[start:]
}

func active(t *testing.T, st *state.Store, title string) state.Task {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Project(ctx, "hand"); err != nil {
		if _, err := st.AddProject(ctx, "hand", handRepo); err != nil {
			t.Fatal(err)
		}
	}
	task, err := st.AddTask(ctx, "hand", title, "")
	if err != nil {
		t.Fatal(err)
	}
	if task, err = st.Transition(ctx, task.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	return task
}

func attempt(t *testing.T, st *state.Store, taskID int64, end, reason string) state.Attempt {
	t.Helper()
	ctx := context.Background()
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: taskID, Harness: "codex", Model: "gpt-6-luna", Effort: "low", Argv: []string{"/bin/codex", "x"}}, "/w")
	if err != nil {
		t.Fatal(err)
	}
	if end == state.AttemptFailed {
		if a, err = st.EndAttempt(ctx, a.ID, end, reason); err != nil {
			t.Fatal(err)
		}
		return a
	}
	if a, err = st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "3", PID: 1, StartMarker: "1"}); err != nil {
		t.Fatal(err)
	}
	if end != state.AttemptRunning {
		if a, err = st.EndAttempt(ctx, a.ID, end, reason); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func TestQueueOrdersWhatNeedsYou(t *testing.T) {
	fx := newFixture(t)
	fx.status, fx.hint = "blocked", "Trust this folder?"
	fx.supervisor(t, state.AttemptInterrupted, "gen-1")
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	one := active(t, fx.st, "Fix login")
	for _, q := range []string{"Keep the old cookie name?", "Ship on Friday?"} {
		if _, err := fx.st.Ask(ctx, one.ID, q); err != nil {
			t.Fatal(err)
		}
	}
	two := active(t, fx.st, "Flaky test")
	attempt(t, fx.st, two.ID, state.AttemptFailed, "launch did not finish")
	three := active(t, fx.st, "Docs")
	a := attempt(t, fx.st, three.ID, state.AttemptRunning, "")
	if _, err := fx.st.AddReport(ctx, a.ID, state.ReportProgress, "Halfway there"); err != nil {
		t.Fatal(err)
	}
	body := get(t, fx.handler(), "/")
	kinds, refs, open := waits(body)
	if !slices.Equal(kinds, []string{"blocked", "failure", "decision", "decision", "report"}) || !slices.Equal(refs, []string{"s2", "a1", "d1", "d2", "r1"}) {
		t.Fatalf("queue = %q %q:\n%s", kinds, refs, region(body, "queue"))
	}
	if !slices.Equal(open, []bool{true, false, false, false, false}) {
		t.Fatalf("open = %v", open)
	}
	contains(t, "queue", region(body, "queue"), `data-waiting="5"`)
}

func TestDecisionsAnswerInPlace(t *testing.T) {
	st := open(t)
	task := active(t, st, "Fix login")
	if _, err := st.Ask(context.Background(), task.ID, "Keep the old cookie name?"); err != nil {
		t.Fatal(err)
	}
	h := board.New(st, token, board.Options{})
	q := region(get(t, h, "/"), "queue")
	contains(t, "decision item", q, `action="/decision/d1/answer" data-fetch`, `name="csrf" value="`+token+`"`, `<textarea name="answer"`, "Keep the old cookie name?")
	if res := post(h, "/decision/d1/answer", url.Values{"answer": {"yes"}}); res.StatusCode != 303 {
		t.Fatalf("answer = %d", res.StatusCode)
	}
	if kinds, _, _ := waits(get(t, h, "/")); slices.Contains(kinds, "decision") {
		t.Fatalf("after the answer the queue holds %q", kinds)
	}
}

func TestTheBlockedScreenJoinsTheQueue(t *testing.T) {
	fx := newFixture(t)
	fx.status, fx.hint = "blocked", "Trust this folder?"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	body := get(t, fx.handler(), "/")
	q := region(body, "queue")
	contains(t, "blocked item", q, `data-kind="blocked"`, "Trust this folder?", "❯ 1. Yes", `action="/supervisor/keys" data-fetch`, `action="/supervisor/force" data-fetch`)
	if n := strings.Count(q, `name="revision" value="7"`); n != 2 || !strings.Contains(q, `value="esc">Esc</button>`) || !strings.Contains(q, `value="1">1 Yes</button>`) {
		t.Fatalf("the screen's options = %d key forms:\n%s", n, q)
	}
	bare := newFixture(t)
	bare.status, bare.hint, bare.screen = "blocked", "Press Enter", "Press Enter to continue"
	bare.supervisor(t, state.AttemptRunning, "gen-1")
	if n := strings.Count(region(get(t, bare.handler(), "/"), "queue"), `name="revision" value="7"`); n != len(state.SupervisorKeys) {
		t.Fatalf("a screen without options offers %d keys, want the %d fixed keys", n, len(state.SupervisorKeys))
	}
	if n := strings.Count(q, `name="screen" value="`+luvus.ScreenDigest(fx.screen)+`"`); n != 2 {
		t.Fatalf("key forms with the screen digest = %d, want 2", n)
	}
	lacks(t, "status region", region(body, "status"), "<pre", `action="/supervisor/keys"`)
	fx.options.Controls = false
	q = region(get(t, fx.handler(), "/"), "queue")
	contains(t, "read-only blocked item", q, "❯ 1. Yes")
	lacks(t, "read-only blocked item", q, `action="/supervisor/keys"`, `action="/supervisor/force"`)
}

func TestFailingChecksComeAndGo(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	task := active(t, st, "Flaky test")
	attempt(t, st, task.ID, state.AttemptInterrupted, "pane gone")
	h := board.New(st, token, board.Options{})
	q := region(get(t, h, "/"), "queue")
	contains(t, "failure item", q, `data-kind="failure"`, `href="/task/t1#a1"`, "pane gone")
	attempt(t, st, task.ID, state.AttemptRunning, "")
	if kinds, _, _ := waits(get(t, h, "/")); slices.Contains(kinds, "failure") {
		t.Fatalf("a running attempt left the failure in the queue: %q", kinds)
	}
	done := active(t, st, "Done work")
	a := attempt(t, st, done.ID, state.AttemptRunning, "")
	r, err := st.AddReport(ctx, a.ID, state.ReportDone, "Done")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AckReport(ctx, r.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EndAttempt(ctx, a.ID, state.AttemptExited, "terminal exited"); err != nil {
		t.Fatal(err)
	}
	if kinds, _, _ := waits(get(t, h, "/")); slices.Contains(kinds, "failure") {
		t.Fatalf("an exited attempt with a done report is a failure: %q", kinds)
	}
}

func TestReportsAckInPlace(t *testing.T) {
	st := open(t)
	task := active(t, st, "Docs")
	a := attempt(t, st, task.ID, state.AttemptRunning, "")
	if _, err := st.AddReport(context.Background(), a.ID, state.ReportDone, "Docs written\n**All** pages"); err != nil {
		t.Fatal(err)
	}
	h := board.New(st, token, board.Options{})
	q := region(get(t, h, "/"), "queue")
	contains(t, "report item", q, `data-kind="report"`, `action="/report/r1/ack" data-fetch`, "<strong>All</strong>", ">Mark read</button>")
	if res := post(h, "/report/r1/ack", nil); res.StatusCode != 303 {
		t.Fatalf("ack = %d", res.StatusCode)
	}
	if kinds, _, _ := waits(get(t, h, "/")); slices.Contains(kinds, "report") {
		t.Fatalf("after the ack the queue holds %q", kinds)
	}
}

func TestAnInterruptedSupervisorAsksToResume(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptInterrupted, "gen-1")
	body := get(t, fx.handler(), "/")
	kinds, refs, _ := waits(body)
	if !slices.Equal(kinds, []string{"resume"}) || !slices.Equal(refs, []string{"s1"}) {
		t.Fatalf("queue = %q %q", kinds, refs)
	}
	contains(t, "resume item", region(body, "queue"), `action="/supervisor/resume" data-fetch`, `action="/supervisor/start" data-fetch`)
	contains(t, "console region", region(body, "console"), `action="/supervisor/resume"`)
	fx.options.Controls = false
	lacks(t, "read-only resume item", region(get(t, fx.handler(), "/"), "queue"), `action="/supervisor/resume"`)
	stopped := newFixture(t)
	stopped.supervisor(t, state.AttemptStopped, "gen-1")
	body = get(t, stopped.handler(), "/")
	if kinds, _, _ := waits(body); len(kinds) != 0 {
		t.Fatalf("a supervisor stopped on purpose waits: %q", kinds)
	}
	contains(t, "stopped console", region(body, "console"), `action="/supervisor/resume"`)
}

func TestLongWaitingContentIsBounded(t *testing.T) {
	st := open(t)
	task := active(t, st, "Big report")
	a := attempt(t, st, task.ID, state.AttemptRunning, "")
	lines := make([]string, 400)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	if _, err := st.AddReport(context.Background(), a.ID, state.ReportDone, strings.Join(lines, "\n")); err != nil {
		t.Fatal(err)
	}
	q := region(get(t, board.New(st, token, board.Options{}), "/"), "queue")
	contains(t, "report excerpt", q, "line 13", `href="/task/t1#r1"`, "read more")
	lacks(t, "report excerpt", q, "line 14")
	fx := newFixture(t)
	fx.status = "blocked"
	screen := make([]string, 60)
	for i := range screen {
		screen[i] = fmt.Sprintf("row %d", i+1)
	}
	fx.screen = strings.Join(screen, "\n")
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	q = region(get(t, fx.handler(), "/"), "queue")
	contains(t, "screen excerpt", q, "row 41", "row 60")
	lacks(t, "screen excerpt", q, "row 40\n")
	long := active(t, st, "One long line")
	b := attempt(t, st, long.ID, state.AttemptRunning, "")
	if _, err := st.AddReport(context.Background(), b.ID, state.ReportDone, strings.Repeat("x", 64<<10)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Ask(context.Background(), long.ID, "Why is it slow?\n"+strings.Repeat("why ", 490)+"really?"); err != nil {
		t.Fatal(err)
	}
	q = region(get(t, board.New(st, token, board.Options{}), "/"), "queue")
	if len(q) > 24<<10 {
		t.Fatalf("a 64 KB one-line report makes the queue %d bytes", len(q))
	}
	title := regexp.MustCompile(`<span class="wait-title">([^<]*)</span>`)
	for _, m := range title.FindAllStringSubmatch(q, -1) {
		if n := len([]rune(m[1])); n > 201 {
			t.Fatalf("a waiting title holds %d runes", n)
		}
	}
	contains(t, "long question", q, `<div class="question md"><p>why why`, "really?")
	css := asset(t, "board.css")
	for _, want := range []string{".wait{", "overflow-wrap:anywhere", "pre{", "overflow-x:auto", "grid-template-columns:minmax(0,1fr)"} {
		if !strings.Contains(css, want) {
			t.Fatalf("board.css lacks %q", want)
		}
	}
}

func TestTheQueueShowsAtMostFifty(t *testing.T) {
	st := open(t)
	task := active(t, st, "Many questions")
	for i := range 80 {
		if _, err := st.Ask(context.Background(), task.ID, fmt.Sprintf("question %d?", i)); err != nil {
			t.Fatal(err)
		}
	}
	body := get(t, board.New(st, token, board.Options{}), "/")
	if kinds, _, _ := waits(body); len(kinds) != 50 {
		t.Fatalf("queue shows %d items", len(kinds))
	}
	contains(t, "queue", region(body, "queue"), "30 more waiting", `data-waiting="80"`)
}

func TestAnEmptyQueueSaysSo(t *testing.T) {
	q := region(get(t, board.New(open(t), token, board.Options{}), "/"), "queue")
	contains(t, "empty queue", q, "All clear", `data-waiting="0"`)
}

func asset(t *testing.T, name string) string {
	t.Helper()
	h := board.New(open(t), token, board.Options{})
	stem, ext, _ := strings.Cut(name, ".")
	m := regexp.MustCompile(`/static/` + stem + `\.[0-9a-f]{12}\.` + ext).FindString(get(t, h, "/"))
	if m == "" {
		t.Fatalf("the page links no %s", name)
	}
	return request(h, "GET", m, nil, false).Body.String()
}

func TestReportTitlesDropMarkdownMarkers(t *testing.T) {
	st := open(t)
	task := active(t, st, "Docs")
	a := attempt(t, st, task.ID, state.AttemptRunning, "")
	if _, err := st.AddReport(context.Background(), a.ID, state.ReportDone, "**Done.** The `renderer` escapes first"); err != nil {
		t.Fatal(err)
	}
	q := region(get(t, board.New(st, token, board.Options{}), "/"), "queue")
	contains(t, "report title", q, `<span class="wait-title">Done. The renderer escapes first</span>`)
}

func TestResumeSurvivesAFullQueue(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptInterrupted, "gen-1")
	task := active(t, fx.st, "Many questions")
	for i := range 50 {
		if _, err := fx.st.Ask(context.Background(), task.ID, fmt.Sprintf("question %d?", i)); err != nil {
			t.Fatal(err)
		}
	}
	contains(t, "fleet page", get(t, fx.handler(), "/"), `action="/supervisor/resume"`, "1 more waiting")
}

func TestAFailureBeyondFiveHundredTasksStillWaits(t *testing.T) {
	st := open(t)
	for i := range 500 {
		active(t, st, fmt.Sprintf("quiet %d", i))
	}
	late := active(t, st, "Newest task")
	attempt(t, st, late.ID, state.AttemptFailed, "launch did not finish")
	body := get(t, board.New(st, token, board.Options{}), "/")
	kinds, refs, _ := waits(body)
	if !slices.Equal(kinds, []string{"failure"}) || !slices.Equal(refs, []string{"a1"}) {
		t.Fatalf("queue = %q %q", kinds, refs)
	}
}

func TestADoneReportBuriedUnderProgressStillCounts(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	task := active(t, st, "Chatty worker")
	a := attempt(t, st, task.ID, state.AttemptRunning, "")
	done, err := st.AddReport(ctx, a.ID, state.ReportDone, "Done. PR: https://github.com/atqamz/hand/pull/9")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AckReport(ctx, done.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	for i := range 25 {
		r, err := st.AddReport(ctx, a.ID, state.ReportProgress, fmt.Sprintf("note %d", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AckReport(ctx, r.ID, "operator"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.EndAttempt(ctx, a.ID, state.AttemptExited, "terminal exited"); err != nil {
		t.Fatal(err)
	}
	body := get(t, board.New(st, token, board.Options{}), "/")
	if kinds, _, _ := waits(body); len(kinds) != 0 {
		t.Fatalf("an exited attempt with a buried done report waits: %q", kinds)
	}
	row := checks(body)["t1"]
	if s, _, _ := strings.Cut(row, "|"); s != "passing" || !strings.Contains(row, "pull/9") {
		t.Fatalf("check row = %q", row)
	}
}

func workerAttempt(t *testing.T, st *state.Store, taskID int64) state.Attempt {
	t.Helper()
	ctx := context.Background()
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: taskID, Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}}, "/w")
	if err != nil {
		t.Fatal(err)
	}
	if a, err = st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "3", PID: 1, StartMarker: "1"}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAWorkerBlockedWithoutASupervisorJoinsNeeds(t *testing.T) {
	fx := newFixture(t)
	fx.panes = map[string]string{"3": "blocked"}
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if err := fx.st.NoteAttempt(context.Background(), a.ID, "blocked", "Esc to cancel · Tab to amend"); err != nil {
		t.Fatal(err)
	}
	fx.screen = "Bash command\n  rm -f $r/$f\n Do you want to proceed?\n❯ 1. Yes\n  2. No"
	q := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "worker item", q, `data-kind="worker"`, "Bash: rm -f $r/$f · Do you want to proceed?", "Fix login", "Do you want to proceed?", `action="/attempt/a1/keys" data-fetch`, `name="screen" value="`+luvus.ScreenDigest(fx.screen)+`"`, `data-kind="nosup"`)
}

func TestAQuietWorkerJoinsNeedsOnlyAfterTheGrace(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if _, err := fx.st.RecordQuiet(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	fx.options.Now = func() time.Time { return at.Add(9 * time.Minute) }
	if kinds, _, _ := waits(get(t, fx.handler(), "/")); slices.Contains(kinds, "quiet") {
		t.Fatalf("a quiet worker joined Needs inside the grace: %q", kinds)
	}
	fx.options.Now = func() time.Time { return at.Add(11 * time.Minute) }
	if kinds, _, _ := waits(get(t, fx.handler(), "/")); !slices.Contains(kinds, "quiet") {
		t.Fatalf("a quiet worker stayed out of Needs after the grace: %q", kinds)
	}
	b := workerAttempt(t, fx.st, active(t, fx.st, "Ship it").ID)
	if _, err := fx.st.AddReport(ctx, b.ID, state.ReportDone, "Shipped"); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.RecordQuiet(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	_, refs, _ := waits(get(t, fx.handler(), "/"))
	if slices.Contains(refs, "a2") {
		t.Fatalf("a quiet turn that reported joined Needs: %q", refs)
	}
}

func TestNoSupervisorItemShowsWhileWorkIsLive(t *testing.T) {
	fx := newFixture(t)
	if kinds, _, _ := waits(get(t, fx.handler(), "/")); slices.Contains(kinds, "nosup") {
		t.Fatalf("an idle fleet shows the no-supervisor item: %q", kinds)
	}
	workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	q := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "no-supervisor item", q, `data-kind="nosup"`, "Work is waiting for a supervisor", `href="/#chat"`)
}

func TestTasksRowsNameTheAgentState(t *testing.T) {
	fx := newFixture(t)
	fx.panes = map[string]string{"3": "blocked"}
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if err := fx.st.NoteAttempt(context.Background(), a.ID, "blocked", "Pick one"); err != nil {
		t.Fatal(err)
	}
	contains(t, "tasks row", region(get(t, fx.handler(), "/"), "tasks"), "a1 claude sonnet · blocked")
}

func TestTheQueueNamesItsWorstItem(t *testing.T) {
	fx := newFixture(t)
	fx.panes = map[string]string{"3": "blocked"}
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if err := fx.st.NoteAttempt(context.Background(), a.ID, "blocked", "Do you want to proceed?"); err != nil {
		t.Fatal(err)
	}
	contains(t, "queue head", region(get(t, fx.handler(), "/"), "queue"), `data-worst="worker"`, `data-worst-text="a1 Trust this folder?"`)
	calm := newFixture(t)
	contains(t, "calm queue head", region(get(t, calm.handler(), "/"), "queue"), `data-worst=""`)
}

func TestAReportItemSplitsItsSummaryFromItsBody(t *testing.T) {
	st := open(t)
	a := attempt(t, st, active(t, st, "Fix login").ID, state.AttemptRunning, "")
	if _, err := st.AddReport(context.Background(), a.ID, state.ReportDone, "Cookie fixed\nThe redirect now keeps the session."); err != nil {
		t.Fatal(err)
	}
	q := region(get(t, board.New(st, token, board.Options{}), "/"), "queue")
	contains(t, "report item", q, `<span class="wait-title">Cookie fixed</span>`, "The redirect now keeps the session.")
	if strings.Count(q, ">Cookie fixed<") != 1 {
		t.Fatalf("the summary repeats in the body:\n%s", q)
	}
}

func TestAWorkerThatMovedOnLeavesNeedsAndTasks(t *testing.T) {
	fx := newFixture(t)
	fx.panes = map[string]string{"3": "working"}
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if err := fx.st.NoteAttempt(context.Background(), a.ID, "blocked", "Do you want to proceed?"); err != nil {
		t.Fatal(err)
	}
	body := get(t, fx.handler(), "/")
	lacks(t, "moved on", region(body, "queue"), `data-kind="worker"`)
	lacks(t, "moved on", region(body, "tasks"), "BLOCKED")
}

func TestHandsOwnBlockedNoteStaysWhileThePaneIsIdle(t *testing.T) {
	fx := newFixture(t)
	fx.panes = map[string]string{"3": "idle"}
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if err := fx.st.NoteAttempt(context.Background(), a.ID, "blocked", "agy asks to trust a folder that is not its worktree; check the screen"); err != nil {
		t.Fatal(err)
	}
	body := get(t, fx.handler(), "/")
	contains(t, "idle", region(body, "queue"), `data-kind="worker"`)
	contains(t, "idle", region(body, "tasks"), "blocked")
}

func TestKeyFormsCarryTheLastPress(t *testing.T) {
	fx := newFixture(t)
	fx.status = "blocked"
	fx.panes = map[string]string{"3": "blocked"}
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if err := fx.st.NoteAttempt(ctx, a.ID, "keys", "2"); err != nil {
		t.Fatal(err)
	}
	if err := fx.st.NoteAttempt(ctx, a.ID, "blocked", "Pick one"); err != nil {
		t.Fatal(err)
	}
	if err := fx.st.NoteSupervisor(ctx, 1, "keys", "1"); err != nil {
		t.Fatal(err)
	}
	sup, _ := fx.st.LastKeys(ctx, "s1")
	att, _ := fx.st.LastKeys(ctx, "a1")
	fx.options.Now = func() time.Time { return time.Now().Add(time.Hour) }
	q := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "last press", q, `action="/supervisor/keys" data-fetch><input type="hidden" name="csrf" value="`+token+`"><input type="hidden" name="revision" value="7"><input type="hidden" name="after" value="`+strconv.FormatInt(sup, 10)+`">`, `action="/attempt/a1/keys" data-fetch><input type="hidden" name="csrf" value="`+token+`"><input type="hidden" name="revision" value="7"><input type="hidden" name="after" value="`+strconv.FormatInt(att, 10)+`">`)
}

func TestBlockedWorkersComeBeforeQuietOnes(t *testing.T) {
	fx := newFixture(t)
	fx.panes = map[string]string{"3": "blocked"}
	ctx := context.Background()
	quiet := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if _, err := fx.st.RecordQuiet(ctx, quiet.ID); err != nil {
		t.Fatal(err)
	}
	blocked := workerAttempt(t, fx.st, active(t, fx.st, "Ship it").ID)
	if err := fx.st.NoteAttempt(ctx, blocked.ID, "blocked", "Pick one"); err != nil {
		t.Fatal(err)
	}
	if kinds, _, _ := waits(get(t, fx.handler(), "/")); slices.Index(kinds, "worker") < 0 || slices.Index(kinds, "worker") > slices.Index(kinds, "quiet") {
		t.Fatalf("kinds = %q", kinds)
	}
}

func TestTheEventLoopLeavesWorkerScreensUnread(t *testing.T) {
	fx := newFixture(t)
	fx.panes = map[string]string{"3": "blocked"}
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if err := fx.st.NoteAttempt(context.Background(), a.ID, "blocked", "Pick one"); err != nil {
		t.Fatal(err)
	}
	base, _ := serve(t, quick(fx.st, fx.options))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	before := len(fx.srv.Calls("agent.read"))
	collect(ch, 300*time.Millisecond)
	if n := len(fx.srv.Calls("agent.read")) - before; n != 0 {
		t.Fatalf("the event loop read worker screens %d times with nothing changed", n)
	}
}

func TestARepeatedQuietTurnKeepsTheWorkerQuiet(t *testing.T) {
	fx := newFixture(t)
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	for range 2 {
		if _, err := fx.st.RecordQuiet(context.Background(), a.ID); err != nil {
			t.Fatal(err)
		}
	}
	page := get(t, fx.handler(), "/")
	contains(t, "tasks row", region(page, "tasks"), "a1 claude sonnet · quiet")
	if kinds, _, _ := waits(page); !slices.Contains(kinds, "quiet") {
		t.Fatalf("a worker quiet for two turns left Needs: %q", kinds)
	}
}

func TestALimitedWorkerJoinsNeedsAtOnce(t *testing.T) {
	fx := newFixture(t)
	fx.panes = map[string]string{"3": "idle"}
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if err := fx.st.NoteAttempt(context.Background(), a.ID, "limited", "You've hit your session limit · resets 6:10am (Asia/Jakarta)"); err != nil {
		t.Fatal(err)
	}
	fx.options.Now = func() time.Time { return time.Date(2026, 9, 26, 0, 1, 0, 0, time.UTC) }
	body := get(t, fx.handler(), "/")
	contains(t, "limited item", region(body, "queue"), `data-kind="limited"`, "LIMITED", "You&#39;ve hit your session limit · resets 6:10am (Asia/Jakarta)", "a1 on t1 “Fix login” stopped at a usage limit.")
	contains(t, "tasks row", region(body, "tasks"), "a1 claude sonnet · limited")
	fx.mu.Lock()
	fx.panes["3"] = "working"
	fx.mu.Unlock()
	body = get(t, fx.handler(), "/")
	lacks(t, "working again", region(body, "queue"), `data-kind="limited"`)
	lacks(t, "working again", region(body, "tasks"), "· limited")
	if _, err := fx.st.AddReport(context.Background(), a.ID, state.ReportDone, "Fixed login"); err != nil {
		t.Fatal(err)
	}
	if kinds, _, _ := waits(get(t, fx.handler(), "/")); slices.Contains(kinds, "limited") {
		t.Fatalf("a worker that reported after its limit stayed limited in Needs: %q", kinds)
	}
}
