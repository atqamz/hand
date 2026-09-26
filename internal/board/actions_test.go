package board_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

func TestAnswerADecisionFromTheBoard(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token)
	page := request(h, "GET", "/decision/d1", nil, true)
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, "Keep the old cookie name?") || !strings.Contains(body, `action="/decision/d1/answer"`) || strings.Contains(body, `http-equiv="refresh"`) {
		t.Fatalf("decision page = %d:\n%s", page.Code, body)
	}
	if rec := request(h, "POST", "/decision/d1/answer", url.Values{"csrf": {"wrong"}, "answer": {"yes"}}, true); rec.Code != http.StatusForbidden {
		t.Fatalf("bad csrf = %d", rec.Code)
	}
	if d, _ := st.Decision(context.Background(), 1); d.Status != state.DecisionOpen {
		t.Fatalf("bad csrf changed the decision: %+v", d)
	}
	rec := request(h, "POST", "/decision/d1/answer", url.Values{"csrf": {token}, "answer": {"yes, keep it"}}, true)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/decision/d1" {
		t.Fatalf("answer = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	d, _ := st.Decision(context.Background(), 1)
	if d.Status != state.DecisionAnswered || d.Answer != "yes, keep it" || d.AnsweredBy != "operator (board)" {
		t.Fatalf("decision = %+v", d)
	}
	if again := request(h, "POST", "/decision/d1/answer", url.Values{"csrf": {token}, "answer": {"no"}}, true); again.Code != http.StatusConflict {
		t.Fatalf("second answer = %d", again.Code)
	}
	if empty := request(h, "POST", "/decision/d1/answer", url.Values{"csrf": {token}, "answer": {"  "}}, true); empty.Code != http.StatusBadRequest {
		t.Fatalf("empty answer = %d", empty.Code)
	}
	if after := request(h, "GET", "/decision/d1", nil, true).Body.String(); strings.Contains(after, "<textarea") || !strings.Contains(after, "yes, keep it") {
		t.Fatalf("answered page:\n%s", after)
	}
}

func TestAckAReportFromTheBoard(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token)
	if rec := request(h, "POST", "/report/r1/ack", url.Values{}, true); rec.Code != http.StatusForbidden {
		t.Fatalf("missing csrf = %d", rec.Code)
	}
	rec := request(h, "POST", "/report/r1/ack", url.Values{"csrf": {token}}, true)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/task/t1" {
		t.Fatalf("ack = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	r, _ := st.Report(context.Background(), 1)
	if r.AckedBy != "operator (board)" {
		t.Fatalf("report = %+v", r)
	}
	if again := request(h, "POST", "/report/r1/ack", url.Values{"csrf": {token}}, true); again.Code != http.StatusConflict {
		t.Fatalf("second ack = %d", again.Code)
	}
	if missing := request(h, "POST", "/report/r9/ack", url.Values{"csrf": {token}}, true); missing.Code != http.StatusNotFound {
		t.Fatalf("missing report = %d", missing.Code)
	}
	if index := request(h, "GET", "/", nil, true).Body.String(); !strings.Contains(index, "read by operator (board)") {
		t.Fatalf("index after ack:\n%s", index)
	}
}

func TestGetNeverMutates(t *testing.T) {
	st := open(t)
	seed(t, st)
	h := board.New(st, token)
	if rec := request(h, "GET", "/report/r1/ack", nil, true); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Fatalf("GET on an action = %d", rec.Code)
	}
	if r, _ := st.Report(context.Background(), 1); r.AckedAt != "" {
		t.Fatalf("GET acknowledged the report: %+v", r)
	}
}
