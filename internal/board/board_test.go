package board_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

const token = "0123456789abcdef0123456789abcdef"

func open(t *testing.T) *state.Store {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time { return time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seed(t *testing.T, st *state.Store) {
	t.Helper()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := st.AddProject(ctx, "hand", "/home/me/hand")
	must(err)
	one, err := st.AddTask(ctx, "hand", "Fix login", "users can log in again")
	must(err)
	_, err = st.Transition(ctx, one.ID, state.StatusActive)
	must(err)
	_, err = st.SetPlan(ctx, one.ID, "1. reproduce\n2. fix the cookie")
	must(err)
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: one.ID, Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}}, "/w")
	must(err)
	_, err = st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "1"})
	must(err)
	_, err = st.AddReport(ctx, a.ID, state.ReportDone, "Cookie fixed\nPR: https://github.com/atqamz/hand/pull/42")
	must(err)
	_, err = st.Ask(ctx, one.ID, "Keep the old cookie name?")
	must(err)
	_, err = st.AddTask(ctx, "hand", "Write docs", "")
	must(err)
	three, err := st.AddTask(ctx, "hand", "Old chore", "")
	must(err)
	_, err = st.Transition(ctx, three.ID, state.StatusAbandoned)
	must(err)
}

func request(h http.Handler, method, path string, form url.Values, cookie bool) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie {
		req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBoardNeedsTheToken(t *testing.T) {
	h := board.New(open(t), token)
	if rec := request(h, "GET", "/", nil, false); rec.Code != http.StatusForbidden {
		t.Fatalf("no token = %d", rec.Code)
	}
	if rec := request(h, "GET", "/?token=wrong", nil, false); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token = %d", rec.Code)
	}
	rec := request(h, "GET", "/?token="+token+"&all=1", nil, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/?all=1" {
		t.Fatalf("token login = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	c := rec.Result().Cookies()
	if len(c) != 1 || c[0].Name != "hand_board" || c[0].Value != token || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie = %+v", c)
	}
	ok := request(h, "GET", "/", nil, true)
	if ok.Code != http.StatusOK || !strings.Contains(ok.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("with cookie = %d, csp %q", ok.Code, ok.Header().Get("Content-Security-Policy"))
	}
}

func TestIndexShowsOneCardPerOpenTask(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token)
	rec := request(h, "GET", "/", nil, true)
	body := rec.Body.String()
	for _, want := range []string{
		`<meta http-equiv="refresh" content="5">`,
		"Fix login", "users can log in again", "plan p1", "a1", "claude sonnet", "running",
		"r1", "Cookie fixed", `href="https://github.com/atqamz/hand/pull/42"`,
		"Keep the old cookie name?", `href="/decision/d1"`, "Write docs",
		`action="/report/r1/ack"`, `name="csrf" value="` + token + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("index missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Old chore") {
		t.Fatal("abandoned task shown without ?all=1")
	}
	if all := request(h, "GET", "/?all=1", nil, true).Body.String(); !strings.Contains(all, "Old chore") {
		t.Fatal("?all=1 hides the abandoned task")
	}
}

func TestBoardEscapesUserText(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", "/home/me/hand")
	_, _ = st.AddTask(ctx, "hand", "<script>alert(1)</script>", `"><img src=x onerror=alert(2)>`)
	body := request(board.New(st, token), "GET", "/", nil, true).Body.String()
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "<img src=x") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("user text not escaped:\n%s", body)
	}
}

func TestTaskPageShowsPlanReportsAndEvents(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token)
	body := request(h, "GET", "/task/t1", nil, true).Body.String()
	for _, want := range []string{"1. reproduce\n2. fix the cookie", "PR: https://github.com/atqamz/hand/pull/42", "attempt.reported", "decision.asked"} {
		if !strings.Contains(body, want) {
			t.Fatalf("task page missing %q:\n%s", want, body)
		}
	}
	if rec := request(h, "GET", "/task/t99", nil, true); rec.Code != http.StatusNotFound {
		t.Fatalf("missing task = %d", rec.Code)
	}
	if rec := request(h, "GET", "/task/nope", nil, true); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id = %d", rec.Code)
	}
}

func TestTaskPageCanAckEveryUnreadReport(t *testing.T) {
	st := open(t)
	seed(t, st)
	if _, err := st.AddReport(context.Background(), 1, state.ReportProgress, "halfway"); err != nil {
		t.Fatal(err)
	}
	body := request(board.New(st, token), "GET", "/task/t1", nil, true).Body.String()
	if !strings.Contains(body, `action="/report/r1/ack"`) || !strings.Contains(body, `action="/report/r2/ack"`) {
		t.Fatalf("task page cannot ack every unread report:\n%s", body)
	}
}

func TestTaskPageSaysWhenHistoryIsCut(t *testing.T) {
	st := open(t)
	seed(t, st)
	ctx := context.Background()
	for i := range 55 {
		if _, err := st.AddReport(ctx, 1, state.ReportProgress, "step "+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	body := request(board.New(st, token), "GET", "/task/t1", nil, true).Body.String()
	for _, want := range []string{"Older reports are not shown", "Only the latest 50 events are shown"} {
		if !strings.Contains(body, want) {
			t.Fatalf("task page missing %q", want)
		}
	}
}

func TestIndexSaysWhenTasksAreHidden(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", "/home/me/hand")
	for range 501 {
		if _, err := st.AddTask(ctx, "hand", "Chore", ""); err != nil {
			t.Fatal(err)
		}
	}
	if body := request(board.New(st, token), "GET", "/", nil, true).Body.String(); !strings.Contains(body, "1 more task is not shown") {
		t.Fatal("index does not say that tasks are hidden")
	}
}
