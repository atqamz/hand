package board_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

type railFleet struct {
	id, name, token string
	st              *state.Store
}

func railFleets(t *testing.T, names ...string) []railFleet {
	t.Helper()
	var out []railFleet
	for i, name := range names {
		st := open(t)
		f, err := st.RenameFleet(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, railFleet{id: f.ID, name: name, token: fmt.Sprintf("%032x", i+1), st: st})
	}
	return out
}

func railHost(fleets []railFleet, loopback bool, list func() ([]board.FleetLink, error)) *board.Host {
	byID := map[string]railFleet{}
	var all []board.FleetLink
	for _, f := range fleets {
		byID[f.id] = f
		all = append(all, board.FleetLink{ID: f.id, Name: f.name})
	}
	if list == nil {
		list = func() ([]board.FleetLink, error) { return all, nil }
	}
	return board.NewHost(board.HostOptions{
		Loopback: loopback,
		Resolve: func(id string) (string, error) {
			if _, ok := byID[id]; !ok {
				return "", state.ErrNotFound
			}
			return "/homes/" + id, nil
		},
		Open: func(id, _ string) (http.Handler, io.Closer, error) {
			f := byID[id]
			return board.New(f.st, f.token, board.Options{Base: "/" + id, Controls: loopback}), closer(func() error { return nil }), nil
		},
		List:  list,
		Valid: func(id, token string) bool { f, ok := byID[id]; return ok && f.token == token },
	})
}

func visit(t *testing.T, h http.Handler, path string, held map[string]string) string {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	for id, token := range held {
		req.AddCookie(&http.Cookie{Name: "hand_board_" + id, Value: token})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d:\n%s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func railOf(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `<nav class="nav rail"`)
	j := strings.Index(body, `<header class="masthead">`)
	if i < 0 || j < i {
		t.Fatalf("the page has no rail before its masthead:\n%s", body)
	}
	return body[i:j]
}

func sheetOf(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `<nav class="nav menu-body sheet"`)
	if i < 0 {
		t.Fatalf("the page has no sheet:\n%s", body)
	}
	return body[i : i+strings.Index(body[i:], "</nav>")]
}

func listed(f railFleet) string { return `href="/` + f.id + `/"` }

func TestTheRailListsOnlyTheFleetsTheViewerCanProve(t *testing.T) {
	f := railFleets(t, "alpha", "bravo", "charlie")
	alpha, bravo, charlie := f[0], f[1], f[2]
	h := railHost(f, true, nil)
	cases := []struct {
		what         string
		on           railFleet
		held         map[string]string
		seen, hidden []railFleet
	}{
		{"one token", alpha, map[string]string{alpha.id: alpha.token}, []railFleet{alpha}, []railFleet{bravo, charlie}},
		{"two tokens", alpha, map[string]string{alpha.id: alpha.token, bravo.id: bravo.token}, []railFleet{alpha, bravo}, []railFleet{charlie}},
		{"another page", bravo, map[string]string{alpha.id: alpha.token, bravo.id: bravo.token}, []railFleet{alpha, bravo}, []railFleet{charlie}},
		{"a stale token", alpha, map[string]string{alpha.id: alpha.token, bravo.id: strings.Repeat("f", 32)}, []railFleet{alpha}, []railFleet{bravo, charlie}},
		{"another fleet's token", alpha, map[string]string{alpha.id: alpha.token, bravo.id: alpha.token}, []railFleet{alpha}, []railFleet{bravo, charlie}},
		{"an unknown fleet", alpha, map[string]string{alpha.id: alpha.token, "f0000000000ff": bravo.token}, []railFleet{alpha}, []railFleet{bravo, charlie}},
	}
	for _, c := range cases {
		for _, path := range []string{"/", "/tasks", "/supervisor/log"} {
			body := visit(t, h, "/"+c.on.id+path, c.held)
			rail := railOf(t, body)
			for _, s := range c.seen {
				contains(t, c.what+" "+path, rail, listed(s), `<span class="label">`+s.name+`</span>`)
			}
			for _, s := range c.hidden {
				lacks(t, c.what+" "+path, body, s.id, s.name)
			}
			contains(t, c.what+" "+path, rail, `<a class="tint-`, `href="/`+c.on.id+`/" aria-current="true"`, `<span class="label">All fleets</span>`)
			if got := strings.Count(rail, `aria-current="true"`) + strings.Count(sheetOf(t, body), `aria-current="true"`); got != 2 {
				t.Fatalf("%s %s: %d current fleets in the rail and the sheet", c.what, path, got)
			}
		}
	}
}

func TestTheCurrentFleetIsAlwaysListed(t *testing.T) {
	f := railFleets(t, "alpha", "bravo")
	alpha, bravo := f[0], f[1]
	held := map[string]string{alpha.id: alpha.token, bravo.id: bravo.token}
	for _, c := range []struct {
		what string
		list func() ([]board.FleetLink, error)
	}{
		{"the host cannot list", func() ([]board.FleetLink, error) { return nil, fmt.Errorf("no fleets folder") }},
		{"the list omits it", func() ([]board.FleetLink, error) { return []board.FleetLink{{ID: bravo.id, Name: "bravo"}}, nil }},
	} {
		rail := railOf(t, visit(t, railHost(f, true, c.list), "/"+alpha.id+"/", held))
		contains(t, c.what, rail, listed(alpha), `<span class="label">alpha</span>`, `href="/`+alpha.id+`/" aria-current="true"`)
	}
}

func TestANetworkBoardListsOnlyItsOwnFleet(t *testing.T) {
	f := railFleets(t, "alpha", "bravo")
	alpha, bravo := f[0], f[1]
	h := railHost(f, false, nil)
	body := visit(t, h, "/"+alpha.id+"/", map[string]string{alpha.id: alpha.token, bravo.id: bravo.token})
	rail := railOf(t, body)
	contains(t, "network rail", rail, listed(alpha), `<span class="label">Board</span>`, `<span class="label">Tasks</span>`, `<span class="label">Conversation</span>`)
	lacks(t, "network board", body, bravo.id, "bravo", "All fleets")
}

func TestARailPageCarriesNoRailWithoutALogin(t *testing.T) {
	f := railFleets(t, "alpha", "bravo")
	h := railHost(f, true, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/"+f[0].id+"/", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no login = %d", rec.Code)
	}
	lacks(t, "403", rec.Body.String(), `class="nav rail"`, "nav-menu", `class="nav-fleets"`, f[1].id, "bravo")
	index := fetch(h, "/").Body.String()
	lacks(t, "host index", index, `class="nav rail"`, "nav-menu")
}

func TestTheRailMarksTheCurrentPage(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token, board.Options{Controls: true})
	current := regexp.MustCompile(`<a href="([^"]*)"[^>]* aria-current="page"`)
	for _, c := range []struct{ path, want string }{
		{"/", "/"},
		{"/tasks", "/tasks"},
		{"/task/t1", "/tasks"},
		{"/supervisor/log", "/supervisor/log"},
		{"/decision/d1", ""},
	} {
		body := get(t, h, c.path)
		var marked []string
		for _, m := range current.FindAllStringSubmatch(railOf(t, body)+sheetOf(t, body), -1) {
			marked = append(marked, m[1])
		}
		if c.want == "" && len(marked) != 0 || c.want != "" && (len(marked) != 2 || marked[0] != c.want || marked[1] != c.want) {
			t.Fatalf("%s marks %q, want %q in the rail and the sheet", c.path, marked, c.want)
		}
	}
}

func TestTheRailNamesTheBoardAndKeepsItsNeedsCountLive(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token, board.Options{Controls: true})
	body := get(t, h, "/")
	rail := railOf(t, body)
	contains(t, "rail", rail, `<span class="label">Board</span>`, `<span class="label">Tasks</span>`, `<span class="label">Conversation</span>`, `<span class="badge" data-needs data-waiting="`)
	lacks(t, "board page", body, "Desk")
	tab := regexp.MustCompile(`data-tab="needs">Needs <span class="badge n" data-waiting="(\d+)" data-worst="(\w*)"`).FindStringSubmatch(body)
	if tab == nil || tab[1] == "0" {
		t.Fatalf("the Needs tab shows no count:\n%s", body)
	}
	contains(t, "badge", rail, `<span class="badge" data-needs data-waiting="`+tab[1]+`" data-worst="`+tab[2]+`">`+tab[1]+`</span>`)
	lacks(t, "badge", get(t, h, "/tasks"), "data-needs")
	calm := get(t, board.New(open(t), token, board.Options{Controls: true}), "/")
	contains(t, "calm", railOf(t, calm), `<span class="badge" data-needs data-waiting="0" data-worst="" hidden>0</span>`)
	js := asset(t, "app.js")
	contains(t, "app.js", js, `document.querySelectorAll("[data-tab=needs] .n, [data-needs]")`, `if (n.matches("[data-needs]")) n.hidden = waiting === 0;`)
}

func TestThePhoneSheetHoldsTheRailsLinks(t *testing.T) {
	f := railFleets(t, "alpha", "bravo")
	body := visit(t, railHost(f, true, nil), "/"+f[0].id+"/", map[string]string{f[0].id: f[0].token, f[1].id: f[1].token})
	contains(t, "sheet", body, `<details class="menu nav-menu" id="nav-menu"><summary aria-label="Menu"><svg class="icon"`, `<nav class="nav menu-body sheet" aria-label="Pages and fleets">`)
	hrefs := regexp.MustCompile(`href="([^"]*)"`)
	links := func(html string) []string {
		var out []string
		for _, m := range hrefs.FindAllStringSubmatch(html, -1) {
			out = append(out, m[1])
		}
		return out
	}
	rail := railOf(t, body)
	if r, s := links(rail[:strings.Index(rail, "</nav>")]), links(sheetOf(t, body)); len(r) != 7 || strings.Join(r, " ") != strings.Join(s, " ") {
		t.Fatalf("rail %q, sheet %q", r, s)
	}
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		".rail{display:none}",
		".sheet{position:fixed;top:0;bottom:0;left:0;z-index:8;",
		".nav-menu[open]>summary::before{content:\"\";position:fixed;inset:0;",
		"@media (min-width:900px){.nav-menu{display:none}",
		"body:has(>.rail){padding-left:56px}",
		".rail{position:fixed;inset:0 auto 0 0;z-index:7;display:flex;flex-direction:column;width:56px;",
		".nav a{position:relative;display:flex;align-items:center;gap:12px;height:max(40px,var(--control));",
		".rail .nav-pages a,.rail .nav-fleets a,.rail-toggle{width:var(--control);height:var(--control)}")
	if strings.Index(css, ".rail{display:none}") > strings.Index(css, "@media (min-width:900px){.nav-menu{display:none}") {
		t.Fatal("the rail is shown below board width")
	}
}

func TestTheRailIsSevenTabStopsBeforeTheMasthead(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	body := get(t, fx.handler(), "/")
	before := body[:strings.Index(body, `<header class="masthead">`)]
	before = before[strings.Index(before, `<a class="skip"`):]
	stops := regexp.MustCompile(`<(?:a|button)\b[^>]*>`).FindAllString(before, -1)
	if len(stops) != 8 {
		t.Fatalf("%d links and buttons before the masthead, want the skip link, the label button, four pages and two fleet links: %q", len(stops), stops)
	}
	for i, want := range []string{`class="skip"`, `class="rail-toggle"`, `href="/"`, `href="/tasks"`, `href="/supervisor/log"`, `href="/connect"`} {
		if !strings.Contains(stops[i], want) {
			t.Fatalf("stop %d = %s, want %s", i, stops[i], want)
		}
	}
	if !strings.Contains(stops[1], " hidden") {
		t.Fatalf("the label button shows without script: %s", stops[1])
	}
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const fleets = [...rail.querySelectorAll(".nav-fleets a")];`,
		`for (const a of fleets) a.tabIndex = a === home ? 0 : -1;`,
		`{ ArrowDown: 1, ArrowUp: -1 }[e.key]`,
		`if (e.key !== "Escape" || !rail.hasAttribute("data-open")) return;`,
		`toggle.setAttribute("aria-expanded", String(open));`)
}

func TestTheRailsFleetLinksStayInThePlace(t *testing.T) {
	f := railFleets(t, "alpha", "bravo")
	rail := railOf(t, visit(t, railHost(f, true, nil), "/"+f[0].id+"/", map[string]string{f[0].id: f[0].token, f[1].id: f[1].token}))
	lacks(t, "rail", rail, "target=")
}

func TestTheRailListsSevenFleetsBesideTheCurrentOne(t *testing.T) {
	for _, n := range []int{8, 9} {
		names := []string{"a1", "b2", "c3", "d4", "e5", "f6", "g7", "h8", "i9"}[:n]
		f := railFleets(t, names...)
		h := railHost(f, true, nil)
		held := map[string]string{}
		for _, x := range f {
			held[x.id] = x.token
		}
		for _, i := range []int{0, n / 2, n - 1} {
			body := visit(t, h, "/"+f[i].id+"/", held)
			for _, rail := range []string{railOf(t, body), sheetOf(t, body)} {
				if got := strings.Count(rail, `class="tint-`) - 1; got != 7 {
					t.Fatalf("%d fleets, current %d: %d listed besides the current one, want 7:\n%s", n, i, got, rail)
				}
				contains(t, "current fleet", rail, `href="/`+f[i].id+`/" aria-current="true"`, `<span class="label">All fleets</span>`)
			}
		}
	}
}

func TestTheRailsLinksScrollInsideTheRail(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		".rail-top{display:flex;align-items:center;justify-content:center;flex:none;",
		".rail-scroll{flex:1;min-height:0;margin:0 -7px;padding:0 7px;overflow-y:auto;",
		".rail .label{position:fixed;left:66px;")
	lacks(t, "board.css", css, "position:absolute;left:calc(100% + 10px)")
	body := get(t, newFixture(t).handler(), "/")
	rail := railOf(t, body)
	if i, j := strings.Index(rail, `<div class="rail-scroll">`), strings.Index(rail, `<ul class="nav-pages">`); i < 0 || j < i || strings.Index(rail, `class="rail-toggle"`) > i {
		t.Fatalf("the label button must sit above the scroll area, and the links in it:\n%s", rail)
	}
	lacks(t, "sheet", sheetOf(t, body), "rail-scroll")
	contains(t, "app.js", asset(t, "app.js"),
		`label.style.top = `,
		`rail.querySelector(".rail-scroll").addEventListener("scroll", tip);`)
}
