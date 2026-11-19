package board_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

func historyFixture(t *testing.T) *fixture {
	t.Helper()
	fx := apiFixture(t, func() time.Time { return frozen })
	ctx := context.Background()
	if _, err := fx.st.AddSupervisorInput(ctx, "Please check the cookie rename before we release"); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.AddSupervisorReplies(ctx, 1, claudeSession, []state.Reply{
		{At: "2026-11-01T04:41:00Z", N: 0, Body: "Two options for the cookie:\n1. Keep the old name\n2. Rename it"},
		{At: "2026-11-01T04:42:00Z", N: 0, Body: "Renaming; nothing else changes."},
	}); err != nil {
		t.Fatal(err)
	}
	return fx
}

func TestAPIHistoryGolden(t *testing.T) {
	h := historyFixture(t).handler()
	for _, c := range []struct{ name, path string }{
		{"history-search", "/api/v1/history/search?q=cookie&context=1"},
		{"history-search-empty", "/api/v1/history/search?q=nothing"},
		{"history-entry-reply", "/api/v1/history/y1"},
		{"history-entry-report", "/api/v1/history/r1"},
		{"history-entry-input", "/api/v1/history/i1"},
	} {
		t.Run(c.name, func(t *testing.T) { checkAPIGolden(t, h, c.name, c.path) })
	}
}

func TestAPIHistorySearchTakesItsFilters(t *testing.T) {
	h := historyFixture(t).handler()
	get := func(query string) board.APIHistorySearch {
		t.Helper()
		_, got := apiInto[board.APIHistorySearch](t, h, "/api/v1/history/search?"+query)
		return got
	}
	refs := func(s board.APIHistorySearch) string {
		var out []string
		for _, hit := range s.Hits {
			out = append(out, hit.Ref)
		}
		return strings.Join(out, " ")
	}
	if got := get("q=cookie&who=operator"); refs(got) != "i1" || got.Who != "operator" || got.Total != 1 || got.More {
		t.Fatalf("who = %+v", got)
	}
	if got := get("q=cookie&who=report"); refs(got) != "r1" || got.Hits[0].Task != "t1" {
		t.Fatalf("report = %+v", got)
	}
	if got := get("q=cookie&who=supervisor"); refs(got) != "y1" || got.Hits[0].Supervisor != "s1" || got.Hits[0].Task != "" {
		t.Fatalf("supervisor = %+v", got)
	}
	if got := get("q=cookie&limit=1"); len(got.Hits) != 1 || got.Total != 3 || !got.More || got.Limit != 1 {
		t.Fatalf("limit = %+v", got)
	}
	if got := get("q=cookie"); got.Limit != 5 || got.Context != 0 || got.Hits[0].Context == nil {
		t.Fatalf("defaults = %+v", got)
	}
	if got := get("q=cookie&task=t1"); got.Task != "t1" || refs(got) == "" {
		t.Fatalf("task = %+v", got)
	}
	if got := get("q=cookie&task=t2"); got.Total != 0 || got.Hits == nil {
		t.Fatalf("other task = %+v", got)
	}
	if got := get("q=cookie&since=2026-12-01"); got.Total != 0 || got.Since != "2026-12-01" {
		t.Fatalf("since = %+v", got)
	}
	if got := get("q=cookie&since=1h&who=supervisor"); got.Total != 1 || got.Since != "1h" {
		t.Fatalf("since 1h = %+v", got)
	}
	if got := get("q=" + url.QueryEscape(`"unterminated`)); got.Total != 0 || got.Q != `"unterminated` {
		t.Fatalf("a bad query = %+v", got)
	}
	got := get("q=cookie&who=supervisor&context=2")
	if len(got.Hits[0].Context) != 2 || got.Hits[0].Context[0].Ref != "i1" || got.Hits[0].Context[1].Ref != "y2" {
		t.Fatalf("context = %+v", got.Hits[0].Context)
	}
}

func TestAPIHistoryAnswersWithTheUsualHeaders(t *testing.T) {
	h := historyFixture(t).handler()
	for _, path := range []string{"/api/v1/history/search?q=cookie", "/api/v1/history/i1"} {
		rec := request(h, "GET", path, nil, true)
		if rec.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Hand-Cursor") == "" {
			t.Errorf("%s headers = %v", path, rec.Header())
		}
		if head := request(h, "HEAD", path, nil, true); head.Code != http.StatusOK {
			t.Errorf("HEAD %s = %d", path, head.Code)
		}
		if post := request(h, "POST", path, url.Values{"csrf": {token}}, true); post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("POST %s = %d, Allow %q", path, post.Code, post.Header().Get("Allow"))
		}
	}
}

func TestAPIHistorySearchRefusesBadParameters(t *testing.T) {
	h := historyFixture(t).handler()
	for _, c := range []struct{ query, want string }{
		{"", "a search needs words to look for"},
		{"q=", "a search needs words to look for"},
		{"q=%20%20", "a search needs words to look for"},
		{"q=" + strings.Repeat("a", 1025), "a search takes at most 1024 bytes"},
		{"q=a&who=worker", `who "worker" must be operator, supervisor or report`},
		{"q=a&since=soon", `since "soon" is not Nd, Nh or a date; want 7d, 36h or YYYY-MM-DD`},
		{"q=a&since=2026-13-01", `since "2026-13-01" is not Nd, Nh or a date; want 7d, 36h or YYYY-MM-DD`},
		{"q=a&limit=0", "limit must be a number from 1 to 50"},
		{"q=a&limit=51", "limit must be a number from 1 to 50"},
		{"q=a&limit=x", "limit must be a number from 1 to 50"},
		{"q=a&context=6", "context must be a number from 0 to 5"},
		{"q=a&context=-1", "context must be a number from 0 to 5"},
		{"q=a&task=12", `task "12" must be a task ref like t12`},
		{"q=a&task=t0", `task "t0" must be a task ref like t12`},
		{"q=a&task=t99999999999999999999", `task "t99999999999999999999" must be a task ref like t12`},
	} {
		err := testAPIError(t, request(h, "GET", "/api/v1/history/search?"+c.query, nil, true), http.StatusBadRequest, "bad_request")
		if err.Message != c.want {
			t.Errorf("%s = %q, want %q", c.query, err.Message, c.want)
		}
	}
}

func TestAPIHistoryEntryAnswersOneEntryWhole(t *testing.T) {
	h := historyFixture(t).handler()
	_, y := apiInto[board.APIHistoryEntry](t, h, "/api/v1/history/y1")
	if y.Ref != "y1" || y.Who != "supervisor" || y.Supervisor != "s1" || y.Task != "" || y.Truncated || y.Text != "Two options for the cookie:\n1. Keep the old name\n2. Rename it" {
		t.Fatalf("y1 = %+v", y)
	}
	_, r := apiInto[board.APIHistoryEntry](t, h, "/api/v1/history/r1")
	if r.Who != "report" || r.Task != "t1" || r.Supervisor != "" {
		t.Fatalf("r1 = %+v", r)
	}
	for _, ref := range []string{"i99", "r99", "y99", "t1", "x", "i0", "y-1", "search1"} {
		testAPIError(t, request(h, "GET", "/api/v1/history/"+ref, nil, true), http.StatusNotFound, "not_found")
	}
}

func TestAPIHistoryEntryIsCappedAndRedacted(t *testing.T) {
	fx := historyFixture(t)
	h := fx.handler()
	secret := strings.Join([]string{"gh", "p_", strings.Repeat("Zq9xK", 8)}, "")
	if _, err := fx.st.AddSupervisorReplies(context.Background(), 1, claudeSession, []state.Reply{{At: "2026-11-01T04:43:00Z", Body: "token " + secret + " " + strings.Repeat("é", 20000)}}); err != nil {
		t.Fatal(err)
	}
	_, e := apiInto[board.APIHistoryEntry](t, h, "/api/v1/history/y3")
	if !e.Truncated || len(e.Text) > state.MaxHistoryBody || strings.Contains(e.Text, secret[4:]) || !strings.Contains(e.Text, "[redacted:github-token]") {
		t.Fatalf("entry = %d bytes, truncated %v: %.80q", len(e.Text), e.Truncated, e.Text)
	}
	_, s := apiInto[board.APIHistorySearch](t, h, "/api/v1/history/search?q=token&context=1")
	for _, hit := range s.Hits {
		if strings.Contains(hit.Text, secret[4:]) {
			t.Fatalf("search leaks: %q", hit.Text)
		}
	}
}

func TestAPIHistoryNeedsTheHostsCookie(t *testing.T) {
	fx := historyFixture(t)
	h := fx.handler()
	for _, path := range []string{"/api/v1/history/search?q=cookie", "/api/v1/history/i1"} {
		testAPIError(t, request(h, "GET", path, nil, false), http.StatusUnauthorized, "unauthorized")
		testAPIError(t, request(h, "GET", path+"&token="+token, nil, false), http.StatusUnauthorized, "unauthorized")
		for name, header := range map[string][2]string{"a proxy": {"X-Forwarded-For", "203.0.113.9"}, "a funnel": {"Tailscale-Funnel-Request", "1"}} {
			req := httptest.NewRequest("GET", path, nil)
			req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
			req.Header.Set(header[0], header[1])
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if testAPIError(t, rec, http.StatusForbidden, "forbidden"); strings.Contains(rec.Body.String(), "cookie") && name == "" {
				t.Fatal(rec.Body.String())
			}
		}
		network := board.New(fx.st, token, board.Options{Now: func() time.Time { return frozen }})
		testAPIError(t, request(network, "GET", path, nil, true), http.StatusForbidden, "forbidden")
	}
}

func TestAPIRoutesIncludeHistorySearch(t *testing.T) {
	var found bool
	for _, rt := range board.APIRoutes() {
		found = found || rt.Method == "GET" && rt.Path == "/history/search"
	}
	if !found {
		t.Fatal("the API table lacks GET /history/search")
	}
}
