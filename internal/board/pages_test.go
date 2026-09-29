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
		`<span class="ref">t1</span>`, `<span class="pill" data-state="running">active</span>`,
		"Plan p1", "<ol><li>reproduce</li><li>fix the cookie</li></ol>", ">Mark read</button>",
		`<article class="comment report" id="r1"`, `<article class="comment report" id="r2"`, "<strong>tests</strong>",
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
		row := regexp.MustCompile(`<li class="fleet">[\s\S]*?</li>`).FindAllString(loop, -1)
		found := false
		for _, r := range row {
			if strings.Contains(r, `href="/`+l.ID+`/"`) {
				found = true
				contains(t, "fleet row", r, l.Name, `<span class="ref">`+l.ID+`</span>`, `class="swatch tint-`)
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
	contains(t, "first run", body, "Nothing needs you", `action="/supervisor/start"`, `name="harness"`, "No tasks yet", "No supervisor yet")
}

func TestTheReadOnlyBoard(t *testing.T) {
	fx := newFixture(t)
	fx.options.Controls = false
	fx.status = "blocked"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	seed(t, fx.st)
	body := get(t, fx.handler(), "/")
	lacks(t, "read-only board", body, `id="composer"`, `action="/supervisor/keys"`, `action="/supervisor/resume"`, `action="/supervisor/stop"`, `action="/supervisor/interrupt"`, `action="/supervisor/start"`)
	contains(t, "read-only board", body, `action="/decision/d1/answer"`, `action="/report/r1/ack"`)
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
	contains(t, "error page", body, `<section class="card error">`, `href="/"`)
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
