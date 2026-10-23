package board_test

import (
	"context"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

const hostile = "<script>alert(1)</script> ]]> \x1b[31mred\x1b[0m \u202eevil & \"quoted\""

func briefed(t *testing.T, st *state.Store, taskID int64, worktrees string, argv []string) state.Attempt {
	t.Helper()
	ctx := context.Background()
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: taskID, Harness: "claude", Model: "sonnet", Effort: "low", Argv: argv}, worktrees)
	if err != nil {
		t.Fatal(err)
	}
	if a, err = st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "7", PID: 1, StartMarker: "1"}); err != nil {
		t.Fatal(err)
	}
	return a
}

func note(t *testing.T, st *state.Store, id int64, kind, detail string) {
	t.Helper()
	if err := st.NoteAttempt(context.Background(), id, kind, detail); err != nil {
		t.Fatal(err)
	}
}

func TestTheAttemptPageShowsWhatHandRecorded(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	task := active(t, st, "Fix login")
	a := briefed(t, st, task.ID, "/w", []string{"/bin/claude", "--model", "sonnet", "do it " + hostile})
	note(t, st, a.ID, "blocked", "Do you want to proceed? "+hostile)
	note(t, st, a.ID, "keys", "enter")
	note(t, st, a.ID, "trusted", "trusted /w/t1-a1")
	if _, err := st.AddReport(ctx, a.ID, state.ReportDone, "Fixed "+hostile+"\nPR: https://github.com/atqamz/hand/pull/42"); err != nil {
		t.Fatal(err)
	}
	other := briefed(t, st, active(t, st, "Other").ID, "/w", []string{"/bin/claude", "other brief"})
	note(t, st, other.ID, "blocked", "elsewhere")
	body := get(t, board.New(st, token, board.Options{}), "/attempt/a1")

	if n := strings.Count(body, "data-region="); n != 1 || !strings.Contains(body, `data-region="attempt"`) {
		t.Fatalf("attempt page regions = %d:\n%s", n, body)
	}
	contains(t, "attempt page", body,
		`<title>a1 · test</title>`, `data-page="attempt"`,
		`<a class="ref" href="/task/t1">t1</a> / <span class="ref">a1</span>`,
		`<h2 class="thread-title">claude sonnet low</h2>`, `<span class="badge status" data-state="running">running</span>`,
		`<dt>Pane</dt><dd>7</dd>`, `<time data-since datetime=`,
		`<dd class="doc-path">hand/`, `<span class="doc-path">`+filepath.Join("/w", "t1-a1")+`</span>`, `data-copy="path" data-text="`+filepath.Join("/w", "t1-a1")+`"`,
		`<pre class="brief" tabindex="0">do it &lt;script&gt;alert(1)&lt;/script&gt; ]]&gt; U&#43;001B[31mredU&#43;001B[0m U&#43;202Eevil &amp; &#34;quoted&#34;</pre>`,
		`<pre class="brief" tabindex="0">/bin/claude --model sonnet</pre>`,
		`<article class="card report" id="r1" data-status="done">`, `action="/report/r1/ack"`, ">Mark read</button>",
		`<a class="badge" data-tone="link" href="https://github.com/atqamz/hand/pull/42"`)
	lacks(t, "attempt page", body, `>a1</a>: `, "<script>alert", "\x1b", "\u202e", "elsewhere", "other brief", ` style=`, "<style")

	var kinds []string
	for _, m := range regexp.MustCompile(`<li class="event"><time[^>]*>[^<]*</time><span class="kind">([^<]*)</span>`).FindAllStringSubmatch(body, -1) {
		kinds = append(kinds, m[1])
	}
	want := []string{"attempt launching", "attempt running", "attempt blocked", "attempt keys", "attempt trusted", "attempt reported"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("timeline = %q, want %q", kinds, want)
	}
	contains(t, "timeline", body, `<a class="ref" href="/ref/r1" title="Fixed &lt;script&gt;`, `">r1</a> done`, "Do you want to proceed? &lt;script&gt;", "U+202E")
}

func TestTheAttemptPageKeepsWindowsLineEndingsAndShowsALoneReturn(t *testing.T) {
	st := open(t)
	task := active(t, st, "Fix login")
	briefed(t, st, task.ID, "/w", []string{"/bin/claude", "line one\r\nline two\rovertyped"})
	body := get(t, board.New(st, token, board.Options{}), "/attempt/a1")
	contains(t, "brief", body, "line one\nline two"+"U&#43;000Dovertyped")
	lacks(t, "brief", body, "\r", "U&#43;000D\n")
}

func TestTheAttemptPageLeavesOtherAttemptsOutOfTheTimeline(t *testing.T) {
	st := open(t)
	task := active(t, st, "Fix login")
	one := briefed(t, st, task.ID, "/w", []string{"/bin/claude", "one"})
	if _, err := st.EndAttempt(context.Background(), one.ID, state.AttemptExited, "done"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		a := briefed(t, st, task.ID, "/w", []string{"/bin/claude", "x"})
		if _, err := st.EndAttempt(context.Background(), a.ID, state.AttemptExited, "done"); err != nil {
			t.Fatal(err)
		}
	}
	last := briefed(t, st, task.ID, "/w", []string{"/bin/claude", "x"})
	if last.ID != 11 {
		t.Fatalf("last attempt = a%d", last.ID)
	}
	note(t, st, last.ID, "blocked", "continues a1 and not a110")
	h := board.New(st, token, board.Options{})
	for path, in := range map[string]bool{"/attempt/a1": true, "/attempt/a11": true, "/attempt/a2": false} {
		body := get(t, h, path)
		if got := strings.Contains(body, "continues "); got != in {
			t.Errorf("%s names the blocked event = %v, want %v", path, got, in)
		}
	}
}

func TestTheAttemptPageEndsWithItsStopCleanedAndKeepsPathsIntact(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	task := active(t, st, "Fix login")
	root := `C:\Users\Zoë Doe\fleet\worktrees`
	worktree := filepath.Join(root, "t1-a1")
	a := briefed(t, st, task.ID, root, []string{"/bin/claude", "go"})
	if _, err := st.EndAttempt(ctx, a.ID, state.AttemptStopped, "stopped by <b>you</b>"); err != nil {
		t.Fatal(err)
	}
	h := board.New(st, token, board.Options{})
	running := get(t, h, "/attempt/a1")
	contains(t, "stopped attempt", running, `<span class="badge status" data-state="idle">stopped</span>`, "stopped by &lt;b&gt;you&lt;/b&gt;", "<dt>Stopped</dt>", "<dt>Duration</dt>", `data-copy="path" data-text="`+worktree+`"`)
	lacks(t, "stopped attempt", running, "data-since", "<dt>Pane</dt>", "Removed")

	if _, err := st.CleanAttempt(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	cleaned := get(t, h, "/attempt/a1")
	contains(t, "cleaned attempt", cleaned, "the branch is kept.", "attempt cleaned", `<span class="doc-path">`+worktree+`</span>`)
	lacks(t, "cleaned attempt", cleaned, `data-copy="path"`)
}

func TestTheAttemptPageSaysSoWithoutAReportOrABrief(t *testing.T) {
	st := open(t)
	task := active(t, st, "Fix login")
	briefed(t, st, task.ID, "/w", []string{"/bin/claude"})
	body := get(t, board.New(st, token, board.Options{}), "/attempt/a1")
	contains(t, "bare attempt", body, `<p class="quiet">No reports yet.</p>`, `<p class="quiet">No brief is recorded for this attempt.</p>`, `<pre class="brief" tabindex="0">/bin/claude</pre>`)
	lacks(t, "bare attempt", body, "<h3>Delivered</h3>", "Mark read")
}

func TestTheAttemptPageNamesWhatItContinuesAndAcceptsABareNumber(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	task := active(t, st, "Fix login")
	one := briefed(t, st, task.ID, "/w", []string{"/bin/claude", "one"})
	if _, err := st.EndAttempt(ctx, one.ID, state.AttemptExited, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CleanAttempt(ctx, one.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "two"}, Continues: one.ID}, "/w"); err != nil {
		t.Fatal(err)
	}
	h := board.New(st, token, board.Options{})
	contains(t, "continuing attempt", get(t, h, "/attempt/2"), `<dt>Continues</dt><dd><a class="ref" href="/attempt/a1">a1</a></dd>`, `data-state="running">launching`)
}

func TestAMissingAttemptGetsThePlain404(t *testing.T) {
	h := board.New(open(t), token, board.Options{})
	for path, want := range map[string]string{"/attempt/a99": "Attempt a99 was not found.", "/attempt/99": "Attempt a99 was not found.", "/attempt/x1": `&#34;x1&#34; is not a a id`, "/attempt/a0": "is not a a id", "/attempt/": "There is no page here."} {
		rec := request(h, "GET", path, nil, true)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		contains(t, path, rec.Body.String(), want, `<p class="back">`)
	}
	if rec := request(h, "GET", "/attempt/a1", nil, false); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "test") && strings.Contains(rec.Body.String(), "Fix") {
		t.Fatalf("no cookie = %d", rec.Code)
	}
}

func TestEveryAttemptRefLeadsToTheAttemptPage(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token, board.Options{})
	contains(t, "task page", get(t, h, "/task/t1"),
		`<li class="row check" data-state="running" id="a1">`, `<a class="ref" href="/attempt/a1">a1</a>`,
		`<span class="meta">from <a class="ref" href="/attempt/a1">a1</a></span>`)
	if rec := request(h, "GET", "/ref/a1", nil, true); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/attempt/a1" {
		t.Fatalf("/ref/a1 = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	contains(t, "chat ref", get(t, h, "/task/t1"), `href="/ref/a1"`)
}

func TestAPhoneSeesTheAttemptPageWithoutTheFleetToken(t *testing.T) {
	p := newPhoneRig(t)
	seed(t, p.fx.st)
	note(t, p.fx.st, 1, "blocked", "Do you want to proceed?")
	phone := p.pair()
	rec := p.do("GET", "/attempt/a1", nil, phone, viaServe)
	if rec.Code != http.StatusOK {
		t.Fatalf("phone GET /attempt/a1 = %d:\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	contains(t, "phone attempt page", body, `data-region="attempt"`, "Cookie fixed", "Do you want to proceed?", `<pre class="brief" tabindex="0">x</pre>`)
	if strings.Contains(body, token) {
		t.Fatalf("the attempt page holds the fleet token:\n%s", body)
	}
	if rec := p.do("GET", "/ref/a1", nil, phone, viaServe); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/attempt/a1" {
		t.Fatalf("phone GET /ref/a1 = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestTheReadOnlyBoardServesTheAttemptPage(t *testing.T) {
	fx := newFixture(t)
	fx.options.Controls = false
	seed(t, fx.st)
	body := get(t, fx.handler(), "/attempt/a1")
	contains(t, "read-only attempt page", body, `data-region="attempt"`, `action="/report/r1/ack"`, "Cookie fixed")
	lacks(t, "read-only attempt page", body, `action="/supervisor/`, `<a href="/">Fleets</a>`)
}

func TestTheAttemptPageStylesItsFactsBriefAndPhoneOrder(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "facts", css,
		".facts{display:grid;grid-template-columns:auto minmax(0,1fr);gap:4px 12px;margin:0;font-size:12px;line-height:18px}",
		".facts dt{color:var(--muted)}",
		".facts dd{margin:0;min-width:0;overflow-wrap:anywhere}",
		".facts .meta{margin:4px 0 0}",
		".facts .doc-copy{display:block;margin-top:4px}")
	contains(t, "brief", css, ".brief{margin:0;padding:10px 12px;border:1px solid var(--line);border-radius:var(--r-lg);background:var(--card);max-height:24em;overflow:auto;font:12px/18px var(--mono);white-space:pre-wrap;overflow-wrap:anywhere}")
	contains(t, "phone order", css, "@media (max-width:999px){.task-side:has(.facts){order:0}}")
}
