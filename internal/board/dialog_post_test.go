package board_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

func fromTheDialog(h http.Handler, path, shown, csrf string, form url.Values) *httptest.ResponseRecorder {
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf", csrf)
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Hand-Fetch", "1")
	req.Header.Set("Referer", "http://127.0.0.1:7700"+shown)
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func said(rec *httptest.ResponseRecorder, name string) string {
	v, _ := url.PathUnescape(rec.Header().Get(name))
	return v
}

func TestTheDialogAnswersADecisionThroughThePagesHandler(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token, board.Options{})
	ctx := context.Background()

	bad := fromTheDialog(h, "/decision/d1/answer", "/decision/d1", "stale", url.Values{"answer": {"yes"}})
	if bad.Code != http.StatusForbidden || said(bad, "X-Hand-Error") != "this form is stale; reload the page and try again" {
		t.Fatalf("bad token = %d %q", bad.Code, said(bad, "X-Hand-Error"))
	}
	if d, _ := st.Decision(ctx, 1); d.Status != state.DecisionOpen {
		t.Fatalf("a refused token closed the decision: %+v", d)
	}

	ok := fromTheDialog(h, "/decision/d1/answer", "/decision/d1", token, url.Values{"answer": {"yes, keep it"}})
	if ok.Code != http.StatusNoContent || ok.Header().Get("Location") != "" || ok.Body.Len() != 0 {
		t.Fatalf("answer = %d %q (%d bytes)", ok.Code, ok.Header().Get("Location"), ok.Body.Len())
	}
	if got := said(ok, "X-Hand-Receipt"); !strings.HasPrefix(got, "d1 answered") {
		t.Fatalf("receipt = %q", got)
	}
	if d, _ := st.Decision(ctx, 1); d.Status != state.DecisionAnswered || d.Answer != "yes, keep it" || d.AnsweredBy != "operator (board)" {
		t.Fatalf("decision = %+v", d)
	}

	stale := fromTheDialog(h, "/decision/d1/answer", "/decision/d1", token, url.Values{"answer": {"no"}})
	if stale.Code != http.StatusConflict || said(stale, "X-Hand-Error") != "d1 is already answered: yes, keep it" {
		t.Fatalf("answered elsewhere = %d %q", stale.Code, said(stale, "X-Hand-Error"))
	}
	contains(t, "stale answer", stale.Body.String(), `<section class="answered-block"`, `<p class="form-error" role="alert">d1 is already answered.</p>`)
	if d, _ := st.Decision(ctx, 1); d.Answer != "yes, keep it" {
		t.Fatalf("a stale answer replaced the first: %+v", d)
	}

	again := get(t, h, "/decision/d1")
	lacks(t, "answered page the dialog re-reads", again, "<textarea", `action="/decision/d1/answer"`)
	contains(t, "answered page the dialog re-reads", again, `<main class="thread`, "yes, keep it")
}

func TestTheDialogMarksAReportReadThroughThePagesHandler(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token, board.Options{})
	ctx := context.Background()

	bad := fromTheDialog(h, "/report/r1/ack", "/task/t1", "stale", nil)
	if bad.Code != http.StatusForbidden || said(bad, "X-Hand-Error") != "this form is stale; reload the page and try again" {
		t.Fatalf("bad token = %d %q", bad.Code, said(bad, "X-Hand-Error"))
	}
	if r, _ := st.Report(ctx, 1); r.AckedAt != "" {
		t.Fatalf("a refused token acknowledged the report: %+v", r)
	}

	ok := fromTheDialog(h, "/report/r1/ack", "/task/t1", token, nil)
	if ok.Code != http.StatusNoContent || ok.Header().Get("Location") != "" || said(ok, "X-Hand-Receipt") != "r1 marked read" {
		t.Fatalf("ack = %d %q receipt %q", ok.Code, ok.Header().Get("Location"), said(ok, "X-Hand-Receipt"))
	}
	if r, _ := st.Report(ctx, 1); r.AckedBy != "operator (board)" {
		t.Fatalf("report = %+v", r)
	}

	stale := fromTheDialog(h, "/report/r1/ack", "/task/t1", token, nil)
	if stale.Code != http.StatusConflict || !strings.Contains(said(stale, "X-Hand-Error"), "already acknowledged") {
		t.Fatalf("read elsewhere = %d %q", stale.Code, said(stale, "X-Hand-Error"))
	}
	if page := get(t, h, "/task/t1"); !strings.Contains(page, "read by operator (board)") || strings.Contains(page, `action="/report/r1/ack"`) {
		t.Fatalf("the page the dialog re-reads still offers Mark read:\n%s", page)
	}
}
