package board_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

type archive struct {
	st  *state.Store
	now time.Time
}

func newArchive(t *testing.T) *archive {
	t.Helper()
	a := &archive{now: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)}
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time { return a.now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateFleet(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProject(context.Background(), "hand", handRepo); err != nil {
		t.Fatal(err)
	}
	a.st = st
	return a
}

func (a *archive) finish(t *testing.T, project, title, to string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	task, err := a.st.AddTask(ctx, project, title, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Transition(ctx, task.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	a.now = at
	if _, err := a.st.Transition(ctx, task.ID, to); err != nil {
		t.Fatal(err)
	}
}

func (a *archive) handler() http.Handler { return board.New(a.st, token, board.Options{}) }

var rowTitle = regexp.MustCompile(`<a class="check-title"[^>]*>([^<]*)</a>`)

func titles(body string) []string {
	var out []string
	for _, m := range rowTitle.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestArchiveGroupsFinishedTasksByDay(t *testing.T) {
	a := newArchive(t)
	day := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	a.finish(t, "hand", "Fix login", state.StatusDone, day(5, 8))
	a.finish(t, "hand", "Write docs", state.StatusDone, day(5, 20))
	a.finish(t, "hand", "Drop chore", state.StatusAbandoned, day(6, 1))
	a.finish(t, "hand", "Fix search", state.StatusDone, day(6, 23))
	body := get(t, a.handler(), "/tasks")
	contains(t, "archive", body, `<h3 class="group">Tue 6 Oct UTC <span class="badge">2</span></h3>`, `<h3 class="group">Mon 5 Oct UTC <span class="badge">2</span></h3>`, `data-days`, `<time class="when" datetime="2026-10-06T23:00:00Z" data-clock>23:00</time>`, `>4 tasks</p>`)
	if got := strings.Join(titles(body), "|"); got != "Fix search|Drop chore|Write docs|Fix login" {
		t.Fatalf("order = %s", got)
	}
	if strings.Index(body, "Tue 6 Oct") > strings.Index(body, "Mon 5 Oct") {
		t.Fatal("days are not newest first")
	}
	for _, name := range []string{"status", "console", "timeline", "queue", "tasks"} {
		if strings.Contains(body, `data-region="`+name+`"`) {
			t.Fatalf("archive claims the %s region", name)
		}
	}
	if strings.Contains(body, `id="composer"`) {
		t.Fatal("archive carries the composer")
	}
}

func TestArchiveFiltersAndSaysWhenNothingMatches(t *testing.T) {
	a := newArchive(t)
	other, _ := filepath.Abs("/other")
	if _, err := a.st.AddProject(context.Background(), "docs", other); err != nil {
		t.Fatal(err)
	}
	day := func(d int) time.Time { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC) }
	a.finish(t, "hand", "Fix login", state.StatusDone, day(2))
	a.finish(t, "hand", "Fix search", state.StatusAbandoned, day(3))
	a.finish(t, "docs", "Write guide", state.StatusDone, day(4))
	h := a.handler()
	for _, tc := range []struct{ query, want string }{
		{"", "Write guide|Fix search|Fix login"},
		{"?q=FIX", "Fix search|Fix login"},
		{"?q=fix+login", "Fix login"},
		{"?q=t3", "Write guide"},
		{"?status=abandoned", "Fix search"},
		{"?status=done", "Write guide|Fix login"},
		{"?project=docs", "Write guide"},
		{"?since=2026-10-03", "Write guide|Fix search"},
		{"?until=2026-10-03", "Fix search|Fix login"},
		{"?since=2026-10-03&until=2026-10-03", "Fix search"},
	} {
		if got := strings.Join(titles(get(t, h, "/tasks"+tc.query)), "|"); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.query, got, tc.want)
		}
	}
	none := get(t, h, "/tasks?q=nothing")
	contains(t, "no match", none, "No tasks match.", ">0 tasks</p>", `value="nothing"`, `href="/tasks">Clear</a>`)
	if len(titles(none)) != 0 || strings.Contains(none, `class="group"`) {
		t.Fatal("a search with no match still lists tasks")
	}
	contains(t, "form", get(t, h, "/tasks?status=abandoned&since=2026-10-01&project=docs"), `<option value="abandoned" selected>`, `<option value="docs" selected>`, `name="since" value="2026-10-01"`, `method="get" action="/tasks"`)
}

func TestTasksPageSearchesEveryStatus(t *testing.T) {
	a := newArchive(t)
	ctx := context.Background()
	other, _ := filepath.Abs("/other")
	if _, err := a.st.AddProject(ctx, "docs", other); err != nil {
		t.Fatal(err)
	}
	day := func(d int) time.Time { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC) }
	a.finish(t, "hand", "Fix login", state.StatusDone, day(2))
	a.finish(t, "docs", "Fix guide", state.StatusAbandoned, day(3))
	a.now = day(4)
	active, err := a.st.AddTask(ctx, "hand", "Fix search", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Transition(ctx, active.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.AddTask(ctx, "hand", "Fix inbox", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.AddTask(ctx, "hand", "Write docs", ""); err != nil {
		t.Fatal(err)
	}
	h := a.handler()
	for _, tc := range []struct{ query, want string }{
		{"", "Fix guide|Fix login"},
		{"?q=fix", "Fix inbox|Fix search|Fix guide|Fix login"},
		{"?q=fix&status=all", "Fix inbox|Fix search|Fix guide|Fix login"},
		{"?q=fix&status=finished", "Fix guide|Fix login"},
		{"?q=fix&status=done", "Fix login"},
		{"?q=fix&status=active", "Fix search"},
		{"?q=fix&status=inbox", "Fix inbox"},
		{"?q=fix&project=docs", "Fix guide"},
		{"?q=fix&since=2026-10-04", "Fix inbox|Fix search"},
		{"?q=fix&until=2026-10-03", "Fix guide|Fix login"},
		{"?q=t4", "Fix inbox"},
		{"?status=all", "Write docs|Fix inbox|Fix search|Fix guide|Fix login"},
		{"?q=fix&status=junk", "Fix guide|Fix login"},
	} {
		if got := strings.Join(titles(get(t, h, "/tasks"+tc.query)), "|"); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.query, got, tc.want)
		}
	}
	body := get(t, h, "/tasks?q=fix")
	contains(t, "grouped", body, `<h3 class="group">Sun 4 Oct UTC <span class="badge">2</span></h3>`, `<h3 class="group">Sat 3 Oct UTC <span class="badge">1</span></h3>`, `>4 tasks</p>`, `<option value="" selected>Default</option>`, `<h2 class="thread-title">Tasks</h2>`)
	contains(t, "bare", get(t, h, "/tasks"), `<option value="" selected>Default</option>`)
	contains(t, "explicit", get(t, h, "/tasks?status=finished"), `<option value="finished" selected>`)
	contains(t, "scopes", body, `<option value="active">Active</option>`, `<option value="inbox">Inbox</option>`)
}

func TestArchiveSearchFromTheDefaultFormSearchesEveryStatus(t *testing.T) {
	a := newArchive(t)
	a.finish(t, "hand", "Fix login", state.StatusDone, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()
	active, err := a.st.AddTask(ctx, "hand", "Fix search", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Transition(ctx, active.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.AddTask(ctx, "hand", "Fix inbox", ""); err != nil {
		t.Fatal(err)
	}
	h := a.handler()
	form := regexp.MustCompile(`(?s)<form class="filters".*?</form>`).FindString(get(t, h, "/tasks"))
	sent := url.Values{}
	for _, m := range regexp.MustCompile(`<input[^>]*name="(\w+)"[^>]*value="([^"]*)"`).FindAllStringSubmatch(form, -1) {
		sent.Set(m[1], m[2])
	}
	for _, m := range regexp.MustCompile(`(?s)<select name="(\w+)">(.*?)</select>`).FindAllStringSubmatch(form, -1) {
		opts := regexp.MustCompile(`<option value="([^"]*)"( selected)?`).FindAllStringSubmatch(m[2], -1)
		pick := opts[0][1]
		for _, o := range opts {
			if o[2] != "" {
				pick = o[1]
			}
		}
		sent.Set(m[1], pick)
	}
	sent.Set("q", "fix")
	if got := strings.Join(titles(get(t, h, "/tasks?"+sent.Encode())), "|"); got != "Fix inbox|Fix search|Fix login" {
		t.Fatalf("default form with %v = %q", sent, got)
	}
	sent.Set("status", "finished")
	if got := strings.Join(titles(get(t, h, "/tasks?"+sent.Encode())), "|"); got != "Fix login" {
		t.Fatalf("explicit finished = %q", got)
	}
}

func TestArchiveOffersProjectsOnlyWhenThereAreSeveral(t *testing.T) {
	a := newArchive(t)
	if body := get(t, a.handler(), "/tasks"); strings.Contains(body, `name="project"`) {
		t.Fatal("one project still gets a project filter")
	}
	other, _ := filepath.Abs("/other")
	if _, err := a.st.AddProject(context.Background(), "docs", other); err != nil {
		t.Fatal(err)
	}
	contains(t, "two projects", get(t, a.handler(), "/tasks"), `name="project"`, `<option value="docs">docs</option>`)
}

func TestArchivePagesKeepTheQuery(t *testing.T) {
	a := newArchive(t)
	for i := range 120 {
		a.finish(t, "hand", fmt.Sprintf("Chore %03d", i), state.StatusDone, time.Date(2026, 10, 1, 0, i, 0, 0, time.UTC))
	}
	a.finish(t, "hand", "Other work", state.StatusAbandoned, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	h := a.handler()
	first := get(t, h, "/tasks?q=chore&status=done")
	if n := len(titles(first)); n != 50 {
		t.Fatalf("page 1 has %d tasks", n)
	}
	contains(t, "page 1", first, `href="/tasks?page=2&amp;q=chore&amp;status=done" rel="next"`, ">120 tasks</p>")
	if strings.Contains(first, "Previous") {
		t.Fatal("page 1 has Previous")
	}
	second := get(t, h, "/tasks?q=chore&status=done&page=2")
	contains(t, "page 2", second, `href="/tasks?page=1&amp;q=chore&amp;status=done" rel="prev"`, `href="/tasks?page=3&amp;q=chore&amp;status=done" rel="next"`, "Chore 069")
	third := get(t, h, "/tasks?q=chore&status=done&page=3")
	if n := len(titles(third)); n != 20 || strings.Contains(third, "Next") || !strings.Contains(third, "Previous") {
		t.Fatalf("page 3 has %d tasks, body:\n%s", n, third)
	}
	if body := get(t, h, "/tasks?page=9223372036854775807"); !strings.Contains(body, ">121 tasks</p>") || strings.Contains(body, "Previous") || len(titles(body)) != 50 {
		t.Fatal("a huge page number does not fall back to page 1")
	}
	if body := get(t, h, "/tasks?page=junk"); !strings.Contains(body, ">121 tasks</p>") || strings.Contains(body, "Previous") {
		t.Fatal("a bad page number does not fall back to page 1")
	}
}

func TestArchiveNeedsNoScript(t *testing.T) {
	a := newArchive(t)
	a.finish(t, "hand", "Fix login", state.StatusDone, time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC))
	rec := request(a.handler(), "GET", "/tasks?q=fix&status=all", nil, true)
	body := rec.Body.String()
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe") {
		t.Fatalf("csp = %q", csp)
	}
	if strings.Contains(body, " style=") || strings.Contains(body, "<style") || len(regexp.MustCompile(`<script[^>]*>`).FindAllString(body, -1)) != 1 {
		t.Fatal("the archive carries inline script or style")
	}
	contains(t, "server rendered", body, `<form class="filters" method="get"`, "Fix login", "Mon 5 Oct UTC")
	if rec := request(board.New(a.st, token, board.Options{}), "GET", "/tasks", nil, false); rec.Code != 403 {
		t.Fatalf("without the cookie = %d", rec.Code)
	}
}

func TestArchiveRegroupsInTheBrowserZone(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js, "[data-days]", "li.task-row", "dayName(d)")
	if n := strings.Count(js, "toLocaleDateString"); n != 1 {
		t.Fatalf("day labels are built in %d places; the timeline and the archive share one", n)
	}
}

func TestRailShowsFiveFinishedTasksAndLinksToTheArchive(t *testing.T) {
	a := newArchive(t)
	a.finish(t, "hand", "Old one", state.StatusDone, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	for i := range 6 {
		a.finish(t, "hand", fmt.Sprintf("Recent %d", i), state.StatusDone, time.Date(2026, 10, 2, i, 0, 0, 0, time.UTC))
	}
	live := active(t, a.st, "Live work")
	_ = live
	h := a.handler()
	rail := region(get(t, h, "/"), "tasks")
	if got := strings.Join(titles(rail), "|"); got != "Live work|Recent 5|Recent 4|Recent 3|Recent 2|Recent 1" {
		t.Fatalf("rail rows = %s", got)
	}
	contains(t, "rail", rail, `<h3 class="group">Finished <span class="badge">7</span></h3>`, `<a href="/tasks">Browse finished tasks (7)</a>`)
	if strings.Contains(rail, "Show finished tasks") || strings.Contains(rail, "more tasks are not shown") {
		t.Fatalf("rail:\n%s", rail)
	}
	all := region(get(t, h, "/?all=1"), "tasks")
	if n := len(titles(all)); n != 8 {
		t.Fatalf("?all=1 lists %d tasks, want every task", n)
	}
	contains(t, "all", all, `<a href="/#tasks">Hide finished tasks</a>`)
}

func TestRailHasNoArchiveLinkWithoutFinishedTasks(t *testing.T) {
	st := open(t)
	_ = active(t, st, "Live work")
	if rail := region(get(t, board.New(st, token, board.Options{}), "/"), "tasks"); strings.Contains(rail, "Browse finished") || strings.Contains(rail, `class="finished"`) {
		t.Fatalf("rail:\n%s", rail)
	}
}

func TestTheArchiveStyles(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		".filters{display:grid;gap:8px;padding:12px;border:1px solid var(--line);border-radius:var(--r-lg);background:var(--panel)}",
		".filters .find{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:8px}",
		".filters .refine{display:flex;flex-wrap:wrap;gap:8px}",
		".filters .field:focus-within{outline:2px solid var(--accent);outline-offset:-1px;border-color:transparent}",
		".filters .field :is(input,select){flex:1;align-self:stretch;height:auto;padding:0;border:0;border-radius:0;outline:0}",
		".filters .range{display:flex;flex:2 1 340px;align-items:center;min-width:0;height:var(--control);overflow:hidden;",
		".task-search label,.filters .search{position:relative;display:block}",
		".summary{display:flex;flex-wrap:wrap;align-items:center;gap:4px 12px}",
		".applied{display:flex;flex-wrap:wrap;align-items:center;gap:6px 8px;min-width:0;margin:0;padding:0;list-style:none;flex:1}",
		".found{margin:0;color:var(--muted);font-size:12px}",
		".tally{display:flex;align-items:center;gap:12px;line-height:24px}",
		".applied+.tally{align-self:flex-start}",
		".filters .field select{appearance:none;padding-right:20px}",
		".when{white-space:nowrap}",
		".finished{margin:0;padding:8px 0;font-size:12px}",
		".pager a[rel=next]{margin-left:auto}",
		"@media (pointer:coarse){.pager a{display:inline-flex;align-items:center;min-height:44px}}",
		"@media (pointer:coarse){.tally a{display:inline-flex;align-items:center;min-height:var(--control);margin-block:-10px}}",
	)
	contains(t, "phone", block(t, css, "@media (max-width:599px){"),
		".filters .find{grid-template-columns:minmax(0,1fr)}",
		".filters .refine{display:grid}",
		".filters :is(.field,.range){flex:none}",
		".filters .range{display:grid;gap:8px;height:auto;overflow:visible;border:0;background:none}",
	)
	if strings.Index(css, ".filters .field{position:relative") > strings.Index(css, "@media (max-width:599px){") {
		t.Fatal("the desk field rules come after the phone block and win on a phone")
	}
}

func TestTheFilterBarShowsEachAppliedFilterAsALinkThatDropsIt(t *testing.T) {
	a := newArchive(t)
	other, _ := filepath.Abs("/other")
	if _, err := a.st.AddProject(context.Background(), "docs", other); err != nil {
		t.Fatal(err)
	}
	a.finish(t, "docs", "Fix login", state.StatusDone, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	h := a.handler()
	bare := get(t, h, "/tasks")
	contains(t, "bar", bare, `role="search"`, `<div class="find"><label class="search"><span class="sr">Search tasks</span><svg class="icon"`, `<button class="primary">Search</button>`,
		`<label class="field"><span>Status</span>`, `<label class="field"><span>Project</span>`, `<div class="range" role="group"`, `<span>Since</span>`, `<span>Until</span>`, `title="Finished on or after this UTC day"`, `title="Finished on or before this UTC day"`, `<div class="summary">`, `<p class="found" role="status">`)
	lacks(t, "bare", bare, `class="utc"`, "(UTC)", `class="applied"`, ">Clear</a>", `class="badge pill drop"`)
	body := get(t, h, "/tasks?q=fix+login&status=done&project=docs&since=2026-10-01&until=2026-10-05&page=1")
	contains(t, "applied", body, `<ul class="applied" aria-label="Active filters">`,
		`href="/tasks?project=docs&amp;since=2026-10-01&amp;status=done&amp;until=2026-10-05" aria-label="Remove filter Search “fix login”"><span class="word">Search “fix login”</span>`,
		`href="/tasks?project=docs&amp;q=fix&#43;login&amp;since=2026-10-01&amp;until=2026-10-05" aria-label="Remove filter Status Done"`,
		`href="/tasks?q=fix&#43;login&amp;since=2026-10-01&amp;status=done&amp;until=2026-10-05" aria-label="Remove filter Project docs"`,
		`href="/tasks?project=docs&amp;q=fix&#43;login&amp;status=done&amp;until=2026-10-05" aria-label="Remove filter Since 2026-10-01"`,
		`href="/tasks?project=docs&amp;q=fix&#43;login&amp;since=2026-10-01&amp;status=done" aria-label="Remove filter Until 2026-10-05"`,
		`<div class="tally"><a href="/tasks">Clear</a><p class="found" role="status">`)
	if n := strings.Count(body, `class="badge pill drop"`); n != 5 {
		t.Fatalf("%d filter links, want 5", n)
	}
	contains(t, "one filter", get(t, h, "/tasks?status=all"), `href="/tasks" aria-label="Remove filter Status Every status"`)
	contains(t, "wrong status", get(t, h, "/tasks?status=bogus"), `aria-label="Remove filter Status Done and abandoned"`)
	contains(t, "long query", get(t, h, "/tasks?q="+strings.Repeat("x", 60)), `<span class="word">Search “`+strings.Repeat("x", 32)+`…</span>`)
}

func TestTheTasksPageIsTitledTasks(t *testing.T) {
	a := newArchive(t)
	h := a.handler()
	body := get(t, h, "/tasks")
	contains(t, "tasks page", body, `<title>tasks · `, `/ tasks</p>`, `<h2 class="thread-title">Tasks</h2>`)
	lacks(t, "tasks page", body, "Finished tasks", "/ finished tasks")
	empty := get(t, h, "/tasks?q=nothing")
	contains(t, "empty", empty, `<p class="quiet">No tasks match.</p>`)
	lacks(t, "empty", empty, "Try fewer words", "clear the dates")
}
