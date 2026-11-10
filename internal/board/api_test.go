package board_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
)

func apiSeed(t *testing.T, fx *fixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := fx.st.AddProject(ctx, "hand", handRepo, ""); err != nil {
		t.Fatal(err)
	}
	active := func(title string) state.Task {
		task, err := fx.st.AddTask(ctx, "hand", title, "")
		if err != nil {
			t.Fatal(err)
		}
		if task, err = fx.st.Transition(ctx, task.ID, state.StatusActive); err != nil {
			t.Fatal(err)
		}
		return task
	}
	start := func(taskID int64, harness, model, effort string, end, reason string) state.Attempt {
		a, err := fx.st.AddAttempt(ctx, state.AttemptSpec{TaskID: taskID, Harness: harness, Model: model, Effort: effort, Argv: []string{"/bin/" + harness, "x"}}, filepath.Join(t.TempDir(), "worktrees"))
		if err != nil {
			t.Fatal(err)
		}
		if end == state.AttemptFailed {
			if a, err = fx.st.EndAttempt(ctx, a.ID, end, reason); err != nil {
				t.Fatal(err)
			}
			return a
		}
		if a, err = fx.st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "3", PID: 1, StartMarker: "1"}); err != nil {
			t.Fatal(err)
		}
		if end != state.AttemptRunning {
			if a, err = fx.st.EndAttempt(ctx, a.ID, end, reason); err != nil {
				t.Fatal(err)
			}
		}
		return a
	}
	one := active("Fix login")
	if _, err := fx.st.SetPlan(ctx, one.ID, "1. reproduce\n2. fix the cookie\n3. add a regression test"); err != nil {
		t.Fatal(err)
	}
	a1 := start(one.ID, "claude", "sonnet", "low", state.AttemptRunning, "")
	if _, err := fx.st.AddReport(ctx, a1.ID, state.ReportDone, "Cookie fixed\nPR: https://github.com/example/hand/pull/42\n\nAll tests pass."); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Ask(ctx, one.ID, "Keep the old cookie name?\n\n1. Yes, keep it\n2. No, rename it"); err != nil {
		t.Fatal(err)
	}
	two := active("Flaky test in the nightly pipeline on the arm64 macOS runner after the dependency bump")
	start(two.ID, "codex", "gpt-6-luna", "low", state.AttemptFailed, "launch did not finish: the worktree already exists at a path that is not ours")
	three := active("Docs")
	a3 := start(three.ID, "claude", "sonnet", "low", state.AttemptRunning, "")
	if _, err := fx.st.AddReport(ctx, a3.ID, state.ReportProgress, "Halfway there"); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Write docs", "Add dark mode toggle", "Rename the cookie"} {
		if _, err := fx.st.AddTask(ctx, "hand", title, ""); err != nil {
			t.Fatal(err)
		}
	}
}

const apiTick = 40 * time.Millisecond

func nextTick() { time.Sleep(2 * apiTick) }

func apiFixture(t *testing.T, now func() time.Time) *fixture {
	t.Helper()
	fx := newFixture(t)
	fx.st = openNow(t, "f00000000e0aa")
	fx.options.Now = now
	fx.options.Controls = true
	fx.options.Base = ""
	fx.options.Tick = apiTick
	if _, err := fx.st.RenameFleet(context.Background(), "golden"); err != nil {
		t.Fatal(err)
	}
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	apiSeed(t, fx)
	claudeLog(t, fx, []string{
		rec("user", ago(20), "status?", 0),
		rec("assistant", ago(19), "All good.", 0),
	})
	return fx
}

func apiInto[T any](t *testing.T, h http.Handler, path string) (board.APIEnvelope, T) {
	t.Helper()
	rec := request(h, "GET", path, nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d:\n%s", path, rec.Code, rec.Body.String())
	}
	var env struct {
		Version     int             `json:"version"`
		Fleet       board.APIFleet  `json:"fleet"`
		Cursor      string          `json:"cursor"`
		GeneratedAt string          `json:"generated_at"`
		Data        json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("GET %s is not the envelope: %v:\n%s", path, err, rec.Body.String())
	}
	var data T
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("GET %s data: %v:\n%s", path, err, env.Data)
	}
	return board.APIEnvelope{Version: env.Version, Fleet: env.Fleet, Cursor: env.Cursor, GeneratedAt: env.GeneratedAt, Data: data}, data
}

func testAPIError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) board.APIError {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d:\n%s", rec.Code, status, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type = %q", ct)
	}
	var body board.APIErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error is not JSON: %v:\n%s", err, rec.Body.String())
	}
	if body.Error.Status != status || body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("error = %+v, want %d %s", body.Error, status, code)
	}
	return body.Error
}

func apiPost(t *testing.T, h http.Handler, path string, body any, csrf string, cookie bool) *httptest.ResponseRecorder {
	t.Helper()
	var raw io.Reader = strings.NewReader("")
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		raw = bytes.NewReader(b)
	}
	req := httptest.NewRequest("POST", path, raw)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-Hand-CSRF", csrf)
	}
	if cookie {
		req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func apiPostRaw(t *testing.T, h http.Handler, path, body, csrf string, cookie bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-Hand-CSRF", csrf)
	}
	if cookie {
		req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func apiBody[T any](t *testing.T, rec *httptest.ResponseRecorder) (board.APIEnvelope, T) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("POST = %d:\n%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Version     int             `json:"version"`
		Fleet       board.APIFleet  `json:"fleet"`
		Cursor      string          `json:"cursor"`
		GeneratedAt string          `json:"generated_at"`
		Data        json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("POST is not the envelope: %v:\n%s", err, rec.Body.String())
	}
	var data T
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("POST data: %v:\n%s", err, env.Data)
	}
	return board.APIEnvelope{Version: env.Version, Fleet: env.Fleet, Cursor: env.Cursor, GeneratedAt: env.GeneratedAt, Data: data}, data
}

func TestAPIGolden(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	for _, c := range []struct{ name, path string }{
		{"status", "/api/v1/status"},
		{"timeline", "/api/v1/timeline"},
		{"tasks", "/api/v1/tasks"},
		{"task", "/api/v1/task/t1"},
		{"attempt-running", "/api/v1/attempt/a1"},
		{"attempt-failed", "/api/v1/attempt/a2"},
		{"decision", "/api/v1/decision/d1"},
		{"report", "/api/v1/report/r1"},
	} {
		t.Run(c.name, func(t *testing.T) { checkAPIGolden(t, h, c.name, c.path) })
	}
}

func TestAPINestedGolden(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	ctx := context.Background()
	kid, err := fx.st.AddTaskUnder(ctx, 1, "hand", "Cover the cookie rename with a regression test", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.AddTaskUnder(ctx, kid.ID, "hand", "Run it on Safari", ""); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	for _, c := range []struct{ name, path string }{
		{"tasks-nested", "/api/v1/tasks"},
		{"task-parent", "/api/v1/task/t1"},
		{"task-child", "/api/v1/task/t7"},
	} {
		t.Run(c.name, func(t *testing.T) { checkAPIGolden(t, h, c.name, c.path) })
	}
}

func checkAPIGolden(t *testing.T, h http.Handler, name, path string) {
	t.Helper()
	rec := request(h, "GET", path, nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d:\n%s", path, rec.Code, rec.Body.String())
	}
	var out bytes.Buffer
	if err := json.Indent(&out, rec.Body.Bytes(), "", "  "); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	out.WriteByte('\n')
	if strings.Contains(out.String(), `"worktree":`) || strings.Contains(out.String(), `"launch":`) {
		t.Fatalf("GET %s returns a host path field:\n%s", path, out.String())
	}
	file := filepath.Join("testdata", "api", name+".json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1 to write it)", err)
	}
	if out.String() != string(want) {
		t.Fatalf("GET %s does not match %s:\n%s", path, file, out.String())
	}
}

func TestAPISession(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	env, sess := apiInto[board.APISession](t, h, "/api/v1/session")
	if sess.CSRF != token || env.Cursor == "" {
		t.Fatalf("session = %+v, cursor %q", sess, env.Cursor)
	}
	testAPIError(t, request(h, "GET", "/api/v1/session", nil, false), http.StatusUnauthorized, "unauthorized")
}

func TestAPIAnswerDecision(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	rec := apiPost(t, h, "/api/v1/decision/d1/answer",
		map[string]string{"choice": "1. Yes, keep it", "note": "the docs keep the old name"}, token, true)
	env, got := apiBody[board.APIDecisionDetail](t, rec)
	if env.Cursor == "" || env.Version != 1 || env.Fleet.Name != "golden" {
		t.Fatalf("envelope = %+v", env)
	}
	if got.Decision.Status != "answered" || got.Decision.Answer != "1. Yes, keep it\n\nthe docs keep the old name" ||
		got.Decision.AnsweredBy != "operator (desktop)" || got.Decision.ClosedAt == "" {
		t.Fatalf("decision = %+v", got.Decision)
	}
	if len(got.Options) != 2 || got.Task.Ref != "t1" || got.Body == "" {
		t.Fatalf("detail = %+v", got)
	}
	_, again := apiInto[board.APIDecisionDetail](t, h, "/api/v1/decision/d1")
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(again)
	if !bytes.Equal(a, b) {
		t.Fatalf("the mutation and the GET disagree:\n%s\n%s", a, b)
	}
}

func TestAPIAnswerRefusals(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	path := "/api/v1/decision/d1/answer"
	body := map[string]string{"choice": "1. Yes, keep it"}
	testAPIError(t, apiPost(t, h, path, body, "", false), http.StatusUnauthorized, "unauthorized")
	csrf := testAPIError(t, apiPost(t, h, path, body, "", true), http.StatusForbidden, "csrf_required")
	if strings.Contains(csrf.Message, "/") {
		t.Fatalf("the CSRF refusal names a path: %q", csrf.Message)
	}
	testAPIError(t, apiPost(t, h, path, body, "wrong", true), http.StatusForbidden, "csrf_required")
	testAPIError(t, apiPost(t, h, "/api/v1/decision/d999/answer", body, token, true), http.StatusNotFound, "not_found")
	testAPIError(t, apiPost(t, h, path, map[string]string{"choice": " ", "note": " "}, token, true), http.StatusBadRequest, "bad_request")

	for _, raw := range []string{
		"{",
		`{"choice":"1. Yes, keep it"} {}`,
		`{"choice":"1. Yes, keep it"} trailing`,
		`{"choice":"1. Yes, keep it","note":"` + strings.Repeat("x", 70<<10) + `"}`,
	} {
		testAPIError(t, apiPostRaw(t, h, path, raw, token, true), http.StatusBadRequest, "bad_request")
	}
	_, still := apiInto[board.APIDecisionDetail](t, h, "/api/v1/decision/d1")
	if still.Decision.Status != "open" {
		t.Fatalf("a refused body changed the decision: %s", still.Decision.Status)
	}

	d, err := fx.st.Ask(context.Background(), 1, "Which name?\n\n1. Yes\n2. Other — write it")
	if err != nil {
		t.Fatal(err)
	}
	other := "/api/v1/decision/" + state.DecisionRef(d.ID) + "/answer"
	testAPIError(t, apiPost(t, h, other, map[string]string{"choice": "2. Other"}, token, true), http.StatusBadRequest, "bad_request")
	if got := apiPost(t, h, other, map[string]string{"choice": "2. Other", "note": "mine"}, token, true); got.Code != http.StatusOK {
		t.Fatalf("an other answer with a note = %d:\n%s", got.Code, got.Body.String())
	}

	if got := apiPost(t, h, path, body, token, true); got.Code != http.StatusOK {
		t.Fatalf("the first answer = %d:\n%s", got.Code, got.Body.String())
	}
	conflict := testAPIError(t, apiPost(t, h, path, body, token, true), http.StatusConflict, "conflict")
	if strings.Contains(conflict.Message, "/") {
		t.Fatalf("the conflict names a path: %q", conflict.Message)
	}

	method := apiPost(t, h, "/api/v1/status", nil, token, true)
	testAPIError(t, method, http.StatusMethodNotAllowed, "method_not_allowed")
	if got := method.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("405 Allow = %q", got)
	}
	put := httptest.NewRequest("PUT", "/api/v1/status", nil)
	put.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, put)
	testAPIError(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("PUT /api/v1/status Allow = %q", got)
	}
	putAction := httptest.NewRequest("PUT", path, nil)
	putAction.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, putAction)
	testAPIError(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
	if got := rec.Header().Get("Allow"); got != "POST" {
		t.Fatalf("PUT %s Allow = %q", path, got)
	}
}

func TestAPIAckReport(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	rec := apiPost(t, h, "/api/v1/report/r1/ack", nil, token, true)
	env, got := apiBody[board.APIReportDetail](t, rec)
	if env.Cursor == "" || got.Report.AckedAt == "" || got.Report.AckedBy != "operator (desktop)" {
		t.Fatalf("report = %+v, cursor %q", got.Report, env.Cursor)
	}
	_, again := apiInto[board.APIReportDetail](t, h, "/api/v1/report/r1")
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(again)
	if !bytes.Equal(a, b) {
		t.Fatalf("the ack and the GET disagree:\n%s\n%s", a, b)
	}
	testAPIError(t, apiPost(t, h, "/api/v1/report/r1/ack", nil, "", false), http.StatusUnauthorized, "unauthorized")
	testAPIError(t, apiPost(t, h, "/api/v1/report/r1/ack", nil, "", true), http.StatusForbidden, "csrf_required")
	testAPIError(t, apiPost(t, h, "/api/v1/report/r999/ack", nil, token, true), http.StatusNotFound, "not_found")
	testAPIError(t, apiPost(t, h, "/api/v1/report/r1/ack", nil, token, true), http.StatusConflict, "conflict")
}

func TestAPISendMessage(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	var args []string
	fx.options.Control = func(ctx context.Context, call ...string) (string, error) {
		args = append([]string(nil), call...)
		if len(call) != 4 || call[0] != "supervisor" || call[1] != "send" || call[2] != "--text" {
			return "", fmt.Errorf("unexpected control call %q", call)
		}
		_, err := fx.st.AddSupervisorInput(ctx, call[3])
		return "", err
	}
	h := fx.handler()
	rec := apiPost(t, h, "/api/v1/message", map[string]string{"text": "hello\nworld"}, token, true)
	env, got := apiBody[board.APITimeline](t, rec)
	if want := []string{"supervisor", "send", "--text", "hello\nworld"}; !slices.Equal(args, want) {
		t.Fatalf("control called %q, want %q", args, want)
	}
	if env.Cursor == "" || env.Version != 1 || env.Fleet.Name != "golden" {
		t.Fatalf("envelope = %+v", env)
	}
	found := false
	for _, e := range got.Entries {
		if e.Role == "operator" && e.Text == "hello\nworld" && e.Queued {
			found = true
		}
	}
	if !found {
		t.Fatalf("the returned timeline has no queued message:\n%+v", got.Entries)
	}
	_, again := apiInto[board.APITimeline](t, h, "/api/v1/timeline")
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(again)
	if !bytes.Equal(a, b) {
		t.Fatalf("the mutation and the GET disagree:\n%s\n%s", a, b)
	}
}

func TestAPISendMessageRefusals(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	path := "/api/v1/message"
	body := map[string]string{"text": "hello"}
	testAPIError(t, apiPost(t, h, path, body, "", false), http.StatusUnauthorized, "unauthorized")
	csrf := testAPIError(t, apiPost(t, h, path, body, "", true), http.StatusForbidden, "csrf_required")
	if strings.Contains(csrf.Message, "/") {
		t.Fatalf("the CSRF refusal names a path: %q", csrf.Message)
	}
	testAPIError(t, apiPost(t, h, path, body, "wrong", true), http.StatusForbidden, "csrf_required")
	testAPIError(t, apiPost(t, h, path, map[string]string{"text": "   "}, token, true), http.StatusBadRequest, "bad_request")
	testAPIError(t, apiPost(t, h, path, map[string]string{"text": "\x1b[31mred"}, token, true), http.StatusBadRequest, "bad_request")
	for _, raw := range []string{
		"",
		"{",
		`{"text":"hello"} {}`,
		`{"text":"hello"} trailing`,
		`{"text":"` + strings.Repeat("x", 70<<10) + `"}`,
		"{\"text\":\"a\xffb\"}",
		`{"text":"\ud800"}`,
		`{"text":"\udc00"}`,
		`{"text":"\ud800x"}`,
		`{"text":5}`,
	} {
		testAPIError(t, apiPostRaw(t, h, path, raw, token, true), http.StatusBadRequest, "bad_request")
	}
	if got := fx.called(); len(got) != 0 {
		t.Fatalf("a refused message reached the supervisor: %q", got)
	}

	fx.options.Control = func(context.Context, ...string) (string, error) {
		return "", fmt.Errorf("cannot send from /home/me/fleet: the supervisor is gone")
	}
	h = fx.handler()
	err := testAPIError(t, apiPost(t, h, path, body, token, true), http.StatusInternalServerError, "internal")
	if strings.Contains(err.Message, "/") {
		t.Fatalf("the send error names a path: %q", err.Message)
	}
	if !strings.Contains(err.Message, "the supervisor is gone") {
		t.Fatalf("the send error dropped its message: %q", err.Message)
	}

	ctl := board.New(fx.st, token, board.Options{Controls: true})
	testAPIError(t, apiPost(t, ctl, path, body, token, true), http.StatusForbidden, "forbidden")

	put := httptest.NewRequest("PUT", path, nil)
	put.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, put)
	testAPIError(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
	if got := rec.Header().Get("Allow"); got != "POST" {
		t.Fatalf("PUT %s Allow = %q", path, got)
	}
}

func TestAPISendMessageKeepsAValidSurrogatePair(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	var got string
	fx.options.Control = func(ctx context.Context, call ...string) (string, error) {
		got = call[3]
		_, err := fx.st.AddSupervisorInput(ctx, call[3])
		return "", err
	}
	h := fx.handler()
	apiBody[board.APITimeline](t, apiPostRaw(t, h, "/api/v1/message", `{"text":"\ud83d\ude00 hi"}`, token, true))
	if got != "😀 hi" {
		t.Fatalf("the supervisor got %q", got)
	}
	apiBody[board.APITimeline](t, apiPostRaw(t, h, "/api/v1/message", `{"text":"\\ud800"}`, token, true))
	if got != `\ud800` {
		t.Fatalf("the supervisor got %q for an escaped backslash", got)
	}
}

func TestAPISendMessageIsAcceptedWhenTheTimelineCannotBeRead(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fx.options.Control = func(_ context.Context, call ...string) (string, error) {
		if _, err := fx.st.AddSupervisorInput(context.Background(), call[3]); err != nil {
			return "", err
		}
		cancel()
		return "", nil
	}
	req := httptest.NewRequest("POST", "/api/v1/message", strings.NewReader(`{"text":"hello"}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hand-CSRF", token)
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec := httptest.NewRecorder()
	fx.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/message = %d:\n%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Version int             `json:"version"`
		Fleet   board.APIFleet  `json:"fleet"`
		Cursor  string          `json:"cursor"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var data struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || !data.Accepted {
		t.Fatalf("data = %s, err %v", env.Data, err)
	}
	if env.Cursor == "" || env.Fleet.Name != "golden" {
		t.Fatalf("envelope = %+v", env)
	}
	pending, err := fx.st.PendingSupervisorInputs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Body != "hello" {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestAPISupervisorControls(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	var calls [][]string
	fx.options.Control = func(_ context.Context, call ...string) (string, error) {
		calls = append(calls, append([]string(nil), call...))
		return "", nil
	}
	h := fx.handler()
	for _, c := range []struct {
		action string
		body   any
		want   []string
	}{
		{"start", nil, []string{"supervisor", "start"}},
		{"resume", nil, []string{"supervisor", "resume"}},
		{"stop", map[string]string{"ref": "s1"}, []string{"supervisor", "stop", "--expect", "s1"}},
		{"interrupt", map[string]string{"ref": "s1"}, []string{"supervisor", "interrupt", "--expect", "s1"}},
		{"force", map[string]string{"ref": "s1"}, []string{"supervisor", "force", "--expect", "s1"}},
	} {
		rec := apiPost(t, h, "/api/v1/supervisor/"+c.action, c.body, token, true)
		env, got := apiBody[board.APIStatus](t, rec)
		if env.Cursor == "" || env.Version != 1 || env.Fleet.Name != "golden" {
			t.Fatalf("%s envelope = %+v", c.action, env)
		}
		if len(calls) == 0 || !slices.Equal(calls[len(calls)-1], c.want) {
			t.Fatalf("%s called %q, want %q", c.action, calls, c.want)
		}
		_, again := apiInto[board.APIStatus](t, h, "/api/v1/status")
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(again)
		if !bytes.Equal(a, b) {
			t.Fatalf("%s and the GET disagree:\n%s\n%s", c.action, a, b)
		}
	}
	if len(calls) != 5 {
		t.Fatalf("controls called %q", calls)
	}
}

func TestAPISupervisorControlIsAcceptedWhenTheStatusCannotBeRead(t *testing.T) {
	for _, c := range []struct {
		name, path, body string
	}{
		{"interrupt", "/api/v1/supervisor/interrupt", `{"ref":"s1"}`},
		{"keys", "/api/v1/supervisor/keys", `{"keys":["enter"],"revision":7,"digest":"0123456789abcdef0123456789abcdef","after":0,"ref":"s1"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := apiFixture(t, func() time.Time { return frozen })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fx.options.Control = func(context.Context, ...string) (string, error) {
				cancel()
				return "", nil
			}
			req := httptest.NewRequest("POST", c.path, strings.NewReader(c.body))
			req = req.WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Hand-CSRF", token)
			req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
			rec := httptest.NewRecorder()
			fx.handler().ServeHTTP(rec, req)
			env, got := apiBody[struct {
				Accepted bool `json:"accepted"`
			}](t, rec)
			if !got.Accepted || env.Cursor == "" || env.Fleet.Name != "golden" {
				t.Fatalf("answer = %+v, envelope = %+v", got, env)
			}
		})
	}
}

func TestAPISupervisorControlRefusals(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	body := func(action string) any {
		if action == "stop" || action == "interrupt" || action == "force" {
			return map[string]string{"ref": "s1"}
		}
		return nil
	}
	for _, action := range []string{"start", "resume", "stop", "interrupt", "force"} {
		path := "/api/v1/supervisor/" + action
		testAPIError(t, apiPost(t, h, path, body(action), "", false), http.StatusUnauthorized, "unauthorized")
		csrf := testAPIError(t, apiPost(t, h, path, body(action), "", true), http.StatusForbidden, "csrf_required")
		if strings.Contains(csrf.Message, "/") {
			t.Fatalf("%s CSRF refusal names a path: %q", action, csrf.Message)
		}
		testAPIError(t, apiPost(t, h, path, body(action), "wrong", true), http.StatusForbidden, "csrf_required")
		if got := apiPost(t, h, path, body(action), token, true); got.Code != http.StatusOK {
			t.Fatalf("%s = %d:\n%s", action, got.Code, got.Body.String())
		}
	}
	for _, action := range []string{"stop", "interrupt", "force"} {
		path := "/api/v1/supervisor/" + action
		before := len(fx.called())
		for _, bad := range []any{
			nil,
			map[string]string{},
			map[string]string{"ref": ""},
			map[string]string{"ref": "s0"},
			map[string]string{"ref": "1"},
			map[string]string{"ref": "s1x"},
		} {
			testAPIError(t, apiPost(t, h, path, bad, token, true), http.StatusBadRequest, "bad_request")
		}
		for _, raw := range []string{`{"ref":"s1"} {}`, `{"ref":"s1"} trailing`} {
			testAPIError(t, apiPostRaw(t, h, path, raw, token, true), http.StatusBadRequest, "bad_request")
		}
		if got := len(fx.called()); got != before {
			t.Fatalf("%s ran a refused request: %q", action, fx.called())
		}
	}

	fx.fail = fmt.Errorf("%w: supervisor s1 is not running", state.ErrConflict)
	for _, action := range []string{"stop", "interrupt", "force"} {
		conflict := testAPIError(t, apiPost(t, h, "/api/v1/supervisor/"+action, body(action), token, true), http.StatusConflict, "conflict")
		if strings.Contains(conflict.Message, "/") {
			t.Fatalf("%s conflict names a path: %q", action, conflict.Message)
		}
	}
	fx.fail = fmt.Errorf("%w: there is no supervisor to resume", state.ErrNotFound)
	testAPIError(t, apiPost(t, h, "/api/v1/supervisor/resume", nil, token, true), http.StatusNotFound, "not_found")
	fx.fail = fmt.Errorf("cannot stop from /home/me/fleet: the supervisor is gone")
	err := testAPIError(t, apiPost(t, h, "/api/v1/supervisor/stop", body("stop"), token, true), http.StatusInternalServerError, "internal")
	if strings.Contains(err.Message, "/") || !strings.Contains(err.Message, "the supervisor is gone") {
		t.Fatalf("control error = %q", err.Message)
	}
	testAPIError(t, request(h, "GET", "/api/v1/supervisor/nope", nil, true), http.StatusNotFound, "not_found")
	testAPIError(t, apiPost(t, h, "/api/v1/supervisor/nope", nil, token, true), http.StatusMethodNotAllowed, "method_not_allowed")
	put := httptest.NewRequest("PUT", "/api/v1/supervisor/stop", nil)
	put.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, put)
	testAPIError(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
	if got := rec.Header().Get("Allow"); got != "POST" {
		t.Fatalf("PUT /api/v1/supervisor/stop Allow = %q", got)
	}

	ctl := board.New(fx.st, token, board.Options{Controls: true})
	testAPIError(t, apiPost(t, ctl, "/api/v1/supervisor/stop", body("stop"), token, true), http.StatusForbidden, "forbidden")

	fresh := newFixture(t)
	testAPIError(t, apiPost(t, fresh.handler(), "/api/v1/supervisor/start", nil, token, true), http.StatusConflict, "conflict")
}

func TestAPISupervisorKeys(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.status = "blocked"
	fx.screen = "Allow Bash?\n❯ 1. Yes\n  2. No"
	var args []string
	var callErr error
	fx.options.Control = func(_ context.Context, call ...string) (string, error) {
		args = append([]string(nil), call...)
		return "", callErr
	}
	h := fx.handler()
	_, st := apiInto[board.APIStatus](t, h, "/api/v1/status")
	if st.Supervisor == nil || !st.Supervisor.Blocked || st.Supervisor.Keys == nil {
		t.Fatalf("supervisor = %+v", st.Supervisor)
	}
	keys := st.Supervisor.Keys
	if keys.Revision != 7 || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(keys.Digest) || keys.Pressed != 0 {
		t.Fatalf("keys = %+v", keys)
	}
	body := map[string]any{"keys": []string{"down", "enter"}, "revision": keys.Revision, "digest": keys.Digest, "after": keys.Pressed, "ref": "s1"}
	rec := apiPost(t, h, "/api/v1/supervisor/keys", body, token, true)
	env, got := apiBody[board.APIStatus](t, rec)
	if env.Cursor == "" || got.Supervisor == nil || !got.Supervisor.Blocked {
		t.Fatalf("keys status = %+v", got.Supervisor)
	}
	want := []string{"supervisor", "keys", "--revision", "7", "--screen", keys.Digest, "--after", "0", "--expect", "s1", "down", "enter"}
	if !slices.Equal(args, want) {
		t.Fatalf("control called %q, want %q", args, want)
	}
	_, again := apiInto[board.APIStatus](t, h, "/api/v1/status")
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(again)
	if !bytes.Equal(a, b) {
		t.Fatalf("the keys answer and the GET disagree:\n%s\n%s", a, b)
	}

	for _, bad := range []map[string]any{
		{"keys": []string{}, "revision": 7, "digest": keys.Digest, "after": 0, "ref": "s1"},
		{"keys": []string{"space"}, "revision": 7, "digest": keys.Digest, "after": 0, "ref": "s1"},
		{"keys": []string{"enter"}, "digest": keys.Digest, "after": 0, "ref": "s1"},
		{"keys": []string{"enter"}, "revision": -1, "digest": keys.Digest, "after": 0, "ref": "s1"},
		{"keys": []string{"enter"}, "revision": 7, "after": 0, "ref": "s1"},
		{"keys": []string{"enter"}, "revision": 7, "digest": "short", "after": 0, "ref": "s1"},
		{"keys": []string{"enter"}, "revision": 7, "digest": keys.Digest, "ref": "s1"},
		{"keys": []string{"enter"}, "revision": 7, "digest": keys.Digest, "after": 0},
		{"keys": []string{"enter"}, "revision": 7, "digest": keys.Digest, "after": 0, "ref": "s0"},
	} {
		testAPIError(t, apiPost(t, h, "/api/v1/supervisor/keys", bad, token, true), http.StatusBadRequest, "bad_request")
	}
	if !slices.Equal(args, want) {
		t.Fatalf("a refused keys body reached the supervisor: %q", args)
	}
	callErr = fmt.Errorf("%w: the screen changed since revision 7; nothing was sent", state.ErrConflict)
	conflict := testAPIError(t, apiPost(t, h, "/api/v1/supervisor/keys", body, token, true), http.StatusConflict, "conflict")
	if strings.Contains(conflict.Message, "/") {
		t.Fatalf("keys conflict names a path: %q", conflict.Message)
	}
	testAPIError(t, apiPost(t, h, "/api/v1/supervisor/keys", body, "", true), http.StatusForbidden, "csrf_required")
	testAPIError(t, apiPost(t, h, "/api/v1/supervisor/keys", body, "", false), http.StatusUnauthorized, "unauthorized")
	testAPIError(t, apiPost(t, h, "/api/v1/supervisor/keys", body, "wrong", true), http.StatusForbidden, "csrf_required")
}

func TestAPIBlockedAsk(t *testing.T) {
	for _, c := range []struct {
		name, status, screen string
		want                 *board.APIAsk
	}{
		{"numbered", "blocked", permission, &board.APIAsk{Question: "Do you want to proceed?", Options: []board.APIAskOption{
			{Label: "2 No", Keys: []string{"2"}}, {Label: "1 Yes", Keys: []string{"1"}},
		}}},
		{"circle", "blocked", "Which flavor?\n❯ ◉ Vanilla\n  ◯ Mint\n\n↑/↓ select · enter to confirm · esc cancel", &board.APIAsk{Question: "Which flavor?", Options: []board.APIAskOption{
			{Label: "Vanilla", Keys: []string{"enter"}}, {Label: "Mint", Keys: []string{"down", "enter"}},
		}}},
		{"command line", "blocked", "rm -f id_rsa\n❯ 1. Yes\n  2. No", nil},
		{"token question", "blocked", "Send Bearer sk-abcdef123456 to host?\n❯ 1. Yes\n  2. No", nil},
		{"token label", "blocked", "Proceed?\n❯ 1. Use password=hunter22\n  2. No", nil},
		{"too many options", "blocked", "Pick?\n❯ 1. a\n  2. b\n  3. c\n  4. d\n  5. e\n  6. f\n  7. g\n  8. h\n  9. i", nil},
		{"unparsed", "blocked", "Something is wrong\nPress any key to continue", nil},
		{"unsendable key", "blocked", "Pick one?\n❯ 1. a\n  2. b\n  3. c\n  4. d", nil},
		{"not blocked", "idle", permission, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := apiFixture(t, func() time.Time { return frozen })
			fx.status, fx.screen = c.status, c.screen
			h := fx.handler()
			raw := apiRaw(t, h, "/api/v1/status")
			_, st := apiInto[board.APIStatus](t, h, "/api/v1/status")
			if !reflect.DeepEqual(st.Supervisor.Ask, c.want) {
				t.Fatalf("ask = %+v, want %+v", st.Supervisor.Ask, c.want)
			}
			if c.want == nil && strings.Contains(raw, `"ask"`) {
				t.Fatalf("an absent ask is in the body:\n%s", raw)
			}
		})
	}
}

func TestAPISupervisorGoldens(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.status = "blocked"
	fx.screen = "Allow Bash?\n❯ 1. Yes\n  2. No"
	fx.options.Control = func(context.Context, ...string) (string, error) { return "", nil }
	h := fx.handler()
	_, st := apiInto[board.APIStatus](t, h, "/api/v1/status")
	keys := st.Supervisor.Keys
	for _, c := range []struct {
		name string
		path string
		body any
	}{
		{"status-blocked", "/api/v1/status", nil},
		{"supervisor-started", "/api/v1/supervisor/start", nil},
		{"supervisor-keys", "/api/v1/supervisor/keys", map[string]any{"keys": []string{"down", "enter"}, "revision": keys.Revision, "digest": keys.Digest, "after": keys.Pressed, "ref": "s1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var rec *httptest.ResponseRecorder
			if c.body == nil && c.path == "/api/v1/status" {
				rec = request(h, "GET", c.path, nil, true)
			} else {
				rec = apiPost(t, h, c.path, c.body, token, true)
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("%s %s = %d:\n%s", c.path, c.name, rec.Code, rec.Body.String())
			}
			var out bytes.Buffer
			if err := json.Indent(&out, rec.Body.Bytes(), "", "  "); err != nil {
				t.Fatal(err)
			}
			out.WriteByte('\n')
			file := filepath.Join("testdata", "api", c.name+".json")
			if os.Getenv("UPDATE_GOLDEN") != "" {
				if err := os.WriteFile(file, out.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v (run with UPDATE_GOLDEN=1 to write it)", err)
			}
			if out.String() != string(want) {
				t.Fatalf("%s does not match %s:\n%s", c.name, file, out.String())
			}
		})
	}
}

func TestAPIMutationGolden(t *testing.T) {
	for _, c := range []struct {
		name string
		path string
		body any
	}{
		{"decision-answered", "/api/v1/decision/d1/answer", map[string]string{"choice": "1. Yes, keep it", "note": "keep the docs"}},
		{"report-acked", "/api/v1/report/r1/ack", nil},
		{"message-sent", "/api/v1/message", map[string]string{"text": "hello from the desktop"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := apiFixture(t, func() time.Time { return frozen })
			if c.name == "message-sent" {
				fx.options.Control = func(ctx context.Context, call ...string) (string, error) {
					if len(call) == 4 && call[2] == "--text" {
						_, err := fx.st.AddSupervisorInput(ctx, call[3])
						return "", err
					}
					return "", nil
				}
			}
			rec := apiPost(t, fx.handler(), c.path, c.body, token, true)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST %s = %d:\n%s", c.path, rec.Code, rec.Body.String())
			}
			var out bytes.Buffer
			if err := json.Indent(&out, rec.Body.Bytes(), "", "  "); err != nil {
				t.Fatal(err)
			}
			out.WriteByte('\n')
			file := filepath.Join("testdata", "api", c.name+".json")
			if os.Getenv("UPDATE_GOLDEN") != "" {
				if err := os.WriteFile(file, out.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v (run with UPDATE_GOLDEN=1 to write it)", err)
			}
			if out.String() != string(want) {
				t.Fatalf("POST %s does not match %s:\n%s", c.path, file, out.String())
			}
		})
	}
}

func TestAPICursorFollowsTheData(t *testing.T) {
	clock := frozen
	fx := apiFixture(t, func() time.Time { return clock })
	h := fx.handler()
	firstEnv, first := apiInto[board.APIStatus](t, h, "/api/v1/status")
	clock = frozen.Add(time.Hour)
	env, second := apiInto[board.APIStatus](t, h, "/api/v1/status")
	if first.Needs.Waiting != second.Needs.Waiting {
		t.Fatalf("the fixture changed under the cursor: %d then %d", first.Needs.Waiting, second.Needs.Waiting)
	}
	if env.Cursor == "" || env.GeneratedAt != frozen.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("envelope = %+v", env)
	}
	if env.Cursor != firstEnv.Cursor {
		t.Fatal("the cursor depends on the clock")
	}
	rec := request(h, "GET", "/api/v1/status?since="+url.QueryEscape(env.Cursor), nil, true)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("same cursor = %d:\n%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Hand-Cursor"); got != env.Cursor {
		t.Fatalf("X-Hand-Cursor = %q, want %q", got, env.Cursor)
	}
	if _, err := fx.st.AddTask(context.Background(), "hand", "After the cursor", ""); err != nil {
		t.Fatal(err)
	}
	nextTick()
	rec = request(h, "GET", "/api/v1/status?since="+url.QueryEscape(env.Cursor), nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("old cursor after an event = %d:\n%s", rec.Code, rec.Body.String())
	}
	var next board.APIEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if next.Cursor == env.Cursor {
		t.Fatal("the cursor did not move with the data")
	}
	body := testAPIError(t, request(h, "GET", "/api/v1/status?since=not-a-cursor", nil, true), http.StatusBadRequest, "bad_cursor")
	if strings.Contains(body.Message, "/") {
		t.Fatalf("cursor error names a path: %q", body.Message)
	}
}

func pollCursor(t *testing.T, h http.Handler, path, cursor string) *httptest.ResponseRecorder {
	t.Helper()
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return request(h, "GET", path+sep+"since="+url.QueryEscape(cursor), nil, true)
}

func TestClientsShareOneVersionPerTick(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Tick = 300 * time.Millisecond
	h := fx.handler()
	env, _ := apiInto[board.APIStatus](t, h, "/api/v1/status")
	read := func(clients int) int {
		time.Sleep(fx.options.Tick + 50*time.Millisecond)
		before := fx.explained()
		var wg sync.WaitGroup
		for range clients {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if rec := pollCursor(t, h, "/api/v1/status", env.Cursor); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
					t.Errorf("unchanged poll = %d with %d bytes", rec.Code, rec.Body.Len())
				}
			}()
		}
		wg.Wait()
		return fx.explained() - before
	}
	one := read(1)
	if one == 0 {
		t.Fatal("a new tick did not recompute the version")
	}
	if twenty := read(20); twenty != one {
		t.Fatalf("20 clients cost %d pane reads in a tick, 1 client costs %d", twenty, one)
	}
}

func TestAnUnchangedReadWithinATickBuildsNothing(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Tick = time.Hour
	h := fx.handler()
	for _, path := range []string{"/api/v1/status", "/api/v1/timeline", "/api/v1/tasks", "/api/v1/tasks?all=1"} {
		env, _ := apiInto[json.RawMessage](t, h, path)
		before := fx.explained()
		rec := pollCursor(t, h, path, env.Cursor)
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 || rec.Header().Get("X-Hand-Cursor") != env.Cursor {
			t.Fatalf("%s: unchanged poll = %d %q", path, rec.Code, rec.Body.String())
		}
		if got := fx.explained(); got != before {
			t.Fatalf("%s: a 304 read the pane %d times", path, got-before)
		}
	}
}

func TestAWriteIsSeenOnTheNextTick(t *testing.T) {
	ctx := context.Background()
	writes := map[string]func(t *testing.T, st *state.Store, a state.Attempt) error{
		"event": func(_ *testing.T, st *state.Store, _ state.Attempt) error {
			_, err := st.AddTask(ctx, "hand", "Another", "")
			return err
		},
		"report": func(_ *testing.T, st *state.Store, a state.Attempt) error {
			_, err := st.AddReport(ctx, a.ID, state.ReportProgress, "More")
			return err
		},
		"decision": func(_ *testing.T, st *state.Store, a state.Attempt) error {
			_, err := st.Ask(ctx, a.TaskID, "Which way?")
			return err
		},
		"attempt": func(_ *testing.T, st *state.Store, a state.Attempt) error {
			return st.NoteAttempt(ctx, a.ID, "long", state.AttemptRef(a.ID)+": long")
		},
		"supervisor input": func(_ *testing.T, st *state.Store, _ state.Attempt) error {
			_, err := st.AddSupervisorInput(ctx, "hello")
			return err
		},
		"alert": func(_ *testing.T, st *state.Store, _ state.Attempt) error {
			return st.RecordAlert(ctx, "report", "t1", "text", "")
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			fx := apiFixture(t, func() time.Time { return frozen })
			h := fx.handler()
			env, _ := apiInto[json.RawMessage](t, h, "/api/v1/status")
			live, err := fx.st.LiveAttempts(ctx)
			if err != nil || len(live) == 0 {
				t.Fatalf("live attempts = %d, %v", len(live), err)
			}
			if err := write(t, fx.st, live[0]); err != nil {
				t.Fatal(err)
			}
			nextTick()
			rec := pollCursor(t, h, "/api/v1/status", env.Cursor)
			if rec.Code != http.StatusOK {
				t.Fatalf("a %s write left the cursor unchanged (%d)", name, rec.Code)
			}
		})
	}
}

func TestAnActionIsSeenAtOnce(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Tick = time.Hour
	fx.options.Control = func(ctx context.Context, call ...string) (string, error) {
		_, err := fx.st.AddSupervisorInput(ctx, call[3])
		return "", err
	}
	h := fx.handler()
	env, _ := apiInto[json.RawMessage](t, h, "/api/v1/status")
	if rec := pollCursor(t, h, "/api/v1/status", env.Cursor); rec.Code != http.StatusNotModified {
		t.Fatalf("unchanged poll = %d", rec.Code)
	}
	if rec := apiPost(t, h, "/api/v1/message", map[string]string{"text": "hello"}, token, true); rec.Code >= 300 {
		t.Fatalf("message = %d:\n%s", rec.Code, rec.Body.String())
	}
	if rec := pollCursor(t, h, "/api/v1/status", env.Cursor); rec.Code != http.StatusOK {
		t.Fatalf("poll after a message = %d, want 200", rec.Code)
	}
}

func TestACursorMovesWhenTheFleetIsRenamed(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	env, _ := apiInto[board.APIStatus](t, h, "/api/v1/status")
	if _, err := fx.st.RenameFleet(context.Background(), "renamed"); err != nil {
		t.Fatal(err)
	}
	rec := request(h, "GET", "/api/v1/status?since="+url.QueryEscape(env.Cursor), nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("a fleet rename left the cursor unchanged (%d):\n%s", rec.Code, rec.Body.String())
	}
	var next struct {
		Fleet  board.APIFleet  `json:"fleet"`
		Cursor string          `json:"cursor"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if next.Cursor == env.Cursor {
		t.Fatal("the cursor did not move with the fleet name")
	}
	if next.Fleet.Name != "renamed" {
		t.Fatalf("fleet name = %q", next.Fleet.Name)
	}
}

func TestACursorMovesWhenOnlyATranscriptLineArrives(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	env, before := apiInto[board.APITimeline](t, h, "/api/v1/timeline")
	claudeLog(t, fx, []string{
		rec("user", ago(20), "status?", 0),
		rec("assistant", ago(19), "All good.", 0),
		rec("assistant", ago(1), "A line that arrives without an event.", 0),
	})
	nextTick()
	rec := request(h, "GET", "/api/v1/timeline?since="+url.QueryEscape(env.Cursor), nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("a new transcript line left the cursor unchanged (%d):\n%s", rec.Code, rec.Body.String())
	}
	var next struct {
		Cursor string          `json:"cursor"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if next.Cursor == env.Cursor {
		t.Fatal("the cursor did not move with the transcript")
	}
	var after board.APITimeline
	if err := json.Unmarshal(next.Data, &after); err != nil {
		t.Fatal(err)
	}
	if len(after.Entries) <= len(before.Entries) {
		t.Fatalf("%d entries before, %d after", len(before.Entries), len(after.Entries))
	}
}

func TestACursorMovesWhenOnlyThePaneChanges(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	env, before := apiInto[board.APIStatus](t, h, "/api/v1/status")
	if before.AgentState != "working" {
		t.Fatalf("agent state = %q, want working", before.AgentState)
	}
	fx.status = "idle"
	nextTick()
	rec := request(h, "GET", "/api/v1/status?since="+url.QueryEscape(env.Cursor), nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("a pane change left the cursor unchanged (%d):\n%s", rec.Code, rec.Body.String())
	}
	var next struct {
		Cursor string          `json:"cursor"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if next.Cursor == env.Cursor {
		t.Fatal("the cursor did not move with the pane")
	}
	var after board.APIStatus
	if err := json.Unmarshal(next.Data, &after); err != nil {
		t.Fatal(err)
	}
	if after.AgentState != "ready" {
		t.Fatalf("agent state = %q, want ready", after.AgentState)
	}
}

func TestAPIAuthContract(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	testAPIError(t, request(h, "GET", "/api/v1/status", nil, false), http.StatusUnauthorized, "unauthorized")
	wrong := httptest.NewRequest("GET", "/api/v1/status", nil)
	wrong.AddCookie(&http.Cookie{Name: "hand_board", Value: "wrong"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, wrong)
	testAPIError(t, rec, http.StatusUnauthorized, "unauthorized")
	testAPIError(t, request(h, "GET", "/api/v1/status?token="+token, nil, false), http.StatusUnauthorized, "unauthorized")
	method := request(h, "POST", "/api/v1/status", url.Values{"csrf": {token}}, true)
	testAPIError(t, method, http.StatusMethodNotAllowed, "method_not_allowed")
	if got := method.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("405 Allow = %q", got)
	}
	testAPIError(t, request(h, "GET", "/api/v1/nope", nil, true), http.StatusNotFound, "not_found")
	foreign := base64.RawURLEncoding.EncodeToString([]byte("f00000000e0bb\x00" + strconv.FormatUint(42, 16)))
	testAPIError(t, request(h, "GET", "/api/v1/status?since="+url.QueryEscape(foreign), nil, true), http.StatusBadRequest, "bad_cursor")
	env, _ := apiInto[board.APIStatus](t, h, "/api/v1/status")
	encoded, tag, _ := strings.Cut(env.Cursor, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(encoded)
	fleet, _, _ := strings.Cut(string(raw), "\x00")
	forged := base64.RawURLEncoding.EncodeToString([]byte(fleet+"\x0000")) + "." + tag
	testAPIError(t, request(h, "GET", "/api/v1/status?since="+url.QueryEscape(forged), nil, true), http.StatusBadRequest, "bad_cursor")
	_, sum, _ := strings.Cut(string(raw), "\x00")
	n, _ := strconv.ParseUint(sum, 16, 64)
	tampered := base64.RawURLEncoding.EncodeToString([]byte(fleet+"\x00"+strconv.FormatUint(n+1, 16))) + "." + tag
	testAPIError(t, request(h, "GET", "/api/v1/status?since="+url.QueryEscape(tampered), nil, true), http.StatusBadRequest, "bad_cursor")
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, tag[len(tag)-1])
	if last < 0 || last+1 >= len(alphabet) {
		t.Fatalf("the tag ends in %q; cannot make a noncanonical variant", tag[len(tag)-1])
	}
	variant := tag[:len(tag)-1] + string(alphabet[last+1])
	loose := encoded + "." + variant
	if got, err := base64.RawURLEncoding.DecodeString(variant); err != nil || base64.RawURLEncoding.EncodeToString(got) == variant {
		t.Fatalf("the variant is not a noncanonical tag: %q", variant)
	}
	testAPIError(t, request(h, "GET", "/api/v1/status?since="+url.QueryEscape(loose), nil, true), http.StatusBadRequest, "bad_cursor")
	head := request(h, "HEAD", "/api/v1/status", nil, true)
	if head.Code != http.StatusOK || head.Header().Get("X-Hand-Cursor") == "" {
		t.Fatalf("HEAD /api/v1/status = %d, cursor %q", head.Code, head.Header().Get("X-Hand-Cursor"))
	}
	head = request(h, "HEAD", "/api/v1/status?since="+url.QueryEscape(head.Header().Get("X-Hand-Cursor")), nil, true)
	if head.Code != http.StatusNotModified {
		t.Fatalf("HEAD with the current cursor = %d", head.Code)
	}

	forwarded := httptest.NewRequest("GET", "/api/v1/status", nil)
	forwarded.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	forwarded.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, forwarded)
	testAPIError(t, rec, http.StatusForbidden, "forbidden")

	funneled := httptest.NewRequest("GET", "/api/v1/status", nil)
	funneled.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	funneled.Header.Set("Tailscale-Funnel-Request", "1")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, funneled)
	testAPIError(t, rec, http.StatusForbidden, "forbidden")

	network := board.New(fx.st, token, board.Options{Now: func() time.Time { return frozen }})
	testAPIError(t, request(network, "GET", "/api/v1/status", nil, true), http.StatusForbidden, "forbidden")
}

func TestAPITaskFinishedAtIsPerTask(t *testing.T) {
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if _, err := st.CreateFleet(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProject(ctx, "hand", handRepo, ""); err != nil {
		t.Fatal(err)
	}
	want := make(map[string]string)
	var ids []int64
	for i := range 4 {
		task, err := st.AddTask(ctx, "hand", fmt.Sprintf("Done %d", i+1), "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Transition(ctx, task.ID, state.StatusActive); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(24 * time.Hour)
		if _, err := st.Transition(ctx, task.ID, state.StatusDone); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, task.ID)
		want[state.TaskRef(task.ID)] = clock.UTC().Format(time.RFC3339Nano)
	}
	h := board.New(st, token, board.Options{Controls: true})
	for _, id := range ids {
		ref := state.TaskRef(id)
		_, detail := apiInto[board.APITaskDetail](t, h, "/api/v1/task/"+ref)
		if got, want := detail.Task.FinishedAt, want[ref]; got != want {
			t.Errorf("task %s finished_at = %q, want %q", ref, got, want)
		}
	}
}

func TestAPIUnknownRefs(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	for _, path := range []string{"/api/v1/task/t999", "/api/v1/attempt/a999", "/api/v1/decision/d999", "/api/v1/report/r999", "/api/v1/task/x1"} {
		body := testAPIError(t, request(h, "GET", path, nil, true), http.StatusNotFound, "not_found")
		if strings.Contains(body.Message, "/") || strings.Contains(body.Message, "\\") {
			t.Fatalf("%s error names a path: %q", path, body.Message)
		}
	}
	env, _ := apiInto[board.APIStatus](t, h, "/api/v1/status")
	testAPIError(t, request(h, "GET", "/api/v1/task/t999?since="+url.QueryEscape(env.Cursor), nil, true), http.StatusNotFound, "not_found")
	testAPIError(t, request(h, "GET", "/api/v1/timeline?before=soon", nil, true), http.StatusBadRequest, "bad_request")
	testAPIError(t, request(h, "GET", "/api/v1/timeline?before=-5", nil, true), http.StatusBadRequest, "bad_request")
}

var apiScreenLeaks = []string{"last_screen", "SECRET-SCREEN-TEXT", "rm -f", "Claude Code will automatically deny", "Trust this folder?", "Esc to cancel", "Tab to amend"}

var apiFieldLeaks = []string{"last_screen", token}

var apiScreenKinds = map[string]bool{
	"attempt.blocked":    true,
	"attempt.limited":    true,
	"supervisor.blocked": true,
	"supervisor.limited": true,
}

func scanAPIBody(t *testing.T, path, body string) {
	t.Helper()
	for _, secret := range apiFieldLeaks {
		if strings.Contains(body, secret) {
			t.Fatalf("GET %s returns %q:\n%s", path, secret, body)
		}
	}
}

func scanScreenText(t *testing.T, what, detail string) {
	t.Helper()
	for _, secret := range apiScreenLeaks {
		if strings.Contains(detail, secret) {
			t.Fatalf("%s carries screen text %q: %q", what, secret, detail)
		}
	}
}

func checkFileRefs(t *testing.T, what string, ds []board.APIDeliverable) {
	t.Helper()
	for _, d := range ds {
		if d.Kind != "file" {
			continue
		}
		if strings.ContainsAny(d.Ref, `/\`) || strings.Contains(d.Ref, ":") {
			t.Fatalf("%s file deliverable names a path: %q", what, d.Ref)
		}
	}
}

func apiRaw(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	rec := request(h, "GET", path, nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d:\n%s", path, rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	scanAPIBody(t, path, body)
	scanScreenText(t, "GET "+path, body)
	return body
}

func TestAPINeverReturnsScreenText(t *testing.T) {
	t.Run("fixture", func(t *testing.T) {
		fx := apiFixture(t, func() time.Time { return frozen })
		fx.panes = map[string]string{"2": "blocked"}
		fx.status = "blocked"
		fx.hint = "Esc to cancel · Tab to amend"
		fx.screen = permission
		ctx := context.Background()
		if err := fx.st.NoteAttempt(ctx, 1, "blocked", "Bash command"); err != nil {
			t.Fatal(err)
		}
		if err := fx.st.NoteAttempt(ctx, 1, "limited", "You've hit your limit · resets 3pm"); err != nil {
			t.Fatal(err)
		}
		if err := fx.st.NoteSupervisor(ctx, 1, "blocked", `copilot does not have model "SECRET-SCREEN-TEXT" and runs on auto instead; check the screen`); err != nil {
			t.Fatal(err)
		}
		claudeLog(t, fx, []string{
			rec("user", ago(20), "status?", 0),
			rec("assistant", ago(19), "All good.", 0),
			rec("user", ago(2), "Please investigate api error: timeout", 0),
			rec("user", ago(1), "[hand v1 wake]\nattempt.limited a1: SECRET-SCREEN-TEXT; leaked-after-semicolon", 0),
		})
		h := fx.handler()
		for _, c := range []string{
			"/api/v1/status", "/api/v1/timeline", "/api/v1/tasks",
			"/api/v1/task/t1", "/api/v1/attempt/a1", "/api/v1/decision/d1", "/api/v1/report/r1",
		} {
			rec := request(h, "GET", c, nil, true)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d:\n%s", c, rec.Code, rec.Body.String())
			}
			scanAPIBody(t, c, rec.Body.String())
			scanScreenText(t, c, rec.Body.String())
		}
		_, status := apiInto[board.APIStatus](t, h, "/api/v1/status")
		blocked := 0
		for _, item := range status.Needs.Items {
			if item.Kind != "blocked" && item.Kind != "worker" && item.Kind != "limited" {
				continue
			}
			blocked++
			if item.Title != "" {
				t.Fatalf("%s item carries screen text: %+v", item.Kind, item)
			}
		}
		if blocked == 0 {
			t.Fatal("the fixture has no blocked item")
		}
		_, timeline := apiInto[board.APITimeline](t, h, "/api/v1/timeline")
		var conversation bool
		for _, e := range timeline.Entries {
			if e.Role == "operator" && strings.Contains(e.Text, "api error: timeout") {
				conversation = true
			}
		}
		if !conversation {
			t.Fatal("the timeline dropped conversation text that names an API error")
		}
		if raw := apiRaw(t, h, "/api/v1/timeline"); strings.Contains(raw, "leaked-after-semicolon") {
			t.Fatalf("the timeline kept a screen detail after a semicolon:\n%s", raw)
		}
	})
	t.Run("dialog without a question", func(t *testing.T) {
		fx := apiFixture(t, func() time.Time { return frozen })
		fx.status, fx.screen = "blocked", "rm -f id_rsa\n❯ 1. Yes\n  2. No"
		raw := apiRaw(t, fx.handler(), "/api/v1/status")
		if strings.Contains(raw, "id_rsa") || strings.Contains(raw, `"ask"`) {
			t.Fatalf("status carries a screen line:\n%s", raw)
		}
	})
	for _, sc := range scenes() {
		t.Run(sc.name, func(t *testing.T) {
			fx, h := sceneHandler(t, sc)
			if !fx.options.Controls {
				return
			}
			apiRaw(t, h, "/api/v1/status")
			apiRaw(t, h, "/api/v1/timeline")
			apiRaw(t, h, "/api/v1/tasks")
			_, tasks := apiInto[board.APITasks](t, h, "/api/v1/tasks")
			for _, g := range tasks.Groups {
				for _, row := range g.Tasks {
					apiRaw(t, h, "/api/v1/task/"+row.Ref)
					_, detail := apiInto[board.APITaskDetail](t, h, "/api/v1/task/"+row.Ref)
					checkFileRefs(t, "task "+row.Ref, detail.Delivered)
					for _, e := range detail.Events {
						scanScreenText(t, "/api/v1/task/"+row.Ref+" events", e.Detail)
						if apiScreenKinds[e.Kind] && e.Detail != "" {
							t.Fatalf("%s event carries screen text: %q", e.Kind, e.Detail)
						}
					}
					for _, a := range detail.Attempts {
						body := apiRaw(t, h, "/api/v1/attempt/"+a.Ref)
						if strings.Contains(body, `"worktree":`) || strings.Contains(body, `"launch":`) || strings.Contains(body, "/w/") {
							t.Fatalf("GET /api/v1/attempt/%s returns a host path:\n%s", a.Ref, body)
						}
						_, attempt := apiInto[board.APIAttemptDetail](t, h, "/api/v1/attempt/"+a.Ref)
						checkFileRefs(t, "attempt "+a.Ref, attempt.Delivered)
						for _, e := range attempt.Events {
							scanScreenText(t, "/api/v1/attempt/"+a.Ref+" events", e.Detail)
							if apiScreenKinds[e.Kind] && e.Detail != "" {
								t.Fatalf("%s event carries screen text: %q", e.Kind, e.Detail)
							}
						}
					}
				}
			}
		})
	}
}

func TestAPIComposedTextLosesHostPaths(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	ctx := context.Background()
	if _, _, err := fx.st.Deliver(ctx, 1, "/very/long/path/reports/t1/design.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.EndAttempt(ctx, 1, state.AttemptStopped, "launch did not finish at /very/long/path/worktrees/t1-a1"); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	_, detail := apiInto[board.APITaskDetail](t, h, "/api/v1/task/t1")
	var delivered bool
	for _, e := range detail.Events {
		if strings.Contains(e.Detail, "/very/long") {
			t.Fatalf("%s event names a host path: %q", e.Kind, e.Detail)
		}
		if e.Kind == "task.delivered" {
			delivered = true
			if e.Detail != "design.md" {
				t.Fatalf("delivered event detail = %q, want the file name", e.Detail)
			}
		}
	}
	if !delivered {
		t.Fatal("the fixture has no delivered event")
	}
	_, attempt := apiInto[board.APIAttemptDetail](t, h, "/api/v1/attempt/a1")
	if strings.Contains(attempt.Attempt.Reason, "/very/long") || !strings.Contains(attempt.Attempt.Reason, "t1-a1") {
		t.Fatalf("attempt reason = %q", attempt.Attempt.Reason)
	}
	if _, err := fx.st.EndSupervisor(ctx, 1, state.AttemptInterrupted, "launch failed at /very/long/path/supervisor.log"); err != nil {
		t.Fatal(err)
	}
	_, status := apiInto[board.APIStatus](t, h, "/api/v1/status")
	if status.Supervisor == nil {
		t.Fatal("the status has no supervisor")
	}
	if strings.Contains(status.Supervisor.Reason, "/very/long") || !strings.Contains(status.Supervisor.Reason, "supervisor.log") {
		t.Fatalf("supervisor reason = %q", status.Supervisor.Reason)
	}
	if body := apiRaw(t, h, "/api/v1/timeline"); strings.Contains(body, "/very/long") {
		t.Fatalf("the timeline names a supervisor end path:\n%s", body)
	}
}

func TestAPIThroughTheFleetPrefix(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	id := "f00000000e0aa"
	fx.options.Base = "/" + id
	h := fx.handler()
	home := t.TempDir()
	host := board.NewHost(board.HostOptions{
		Loopback: true,
		Resolve:  func(string) (string, error) { return home, nil },
		Open: func(string, string) (http.Handler, io.Closer, error) {
			return h, closer(func() error { return nil }), nil
		},
		List: func() ([]board.FleetLink, error) { return []board.FleetLink{{ID: id, Name: "golden"}}, nil },
	})
	req := httptest.NewRequest("GET", "/"+id+"/api/v1/status", nil)
	req.AddCookie(&http.Cookie{Name: "hand_board_" + id, Value: token})
	rec := httptest.NewRecorder()
	host.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /%s/api/v1/status = %d:\n%s", id, rec.Code, rec.Body.String())
	}
	var env board.APIEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Version != 1 || env.Fleet.ID != id || env.Cursor == "" {
		t.Fatalf("envelope = %+v, %v", env, err)
	}
	req = httptest.NewRequest("GET", "/"+id+"/api/v1/status", nil)
	rec = httptest.NewRecorder()
	host.ServeHTTP(rec, req)
	testAPIError(t, rec, http.StatusUnauthorized, "unauthorized")
}

func TestAPIThroughTheFleetPrefixReportsOpenFailuresAsJSON(t *testing.T) {
	id := "f00000000e0aa"
	home := t.TempDir()
	links := func() ([]board.FleetLink, error) { return []board.FleetLink{{ID: id, Name: "golden"}}, nil }
	host := board.NewHost(board.HostOptions{
		Loopback: true,
		Resolve:  func(string) (string, error) { return home, nil },
		Open: func(string, string) (http.Handler, io.Closer, error) {
			return nil, nil, errors.New("cannot open /very/long/path/hand.db")
		},
		List:  links,
		Valid: func(string, string) bool { return true },
	})
	anon := httptest.NewRequest("GET", "/"+id+"/api/v1/status", nil)
	rec := httptest.NewRecorder()
	host.ServeHTTP(rec, anon)
	testAPIError(t, rec, http.StatusUnauthorized, "unauthorized")
	req := httptest.NewRequest("GET", "/"+id+"/api/v1/status", nil)
	req.AddCookie(&http.Cookie{Name: "hand_board_" + id, Value: token})
	rec = httptest.NewRecorder()
	host.ServeHTTP(rec, req)
	body := testAPIError(t, rec, http.StatusInternalServerError, "internal")
	if strings.Contains(body.Message, "/very/long") {
		t.Fatalf("open failure names a path: %q", body.Message)
	}
	missing := board.NewHost(board.HostOptions{
		Loopback: true,
		Resolve:  func(string) (string, error) { return home, nil },
		Open: func(string, string) (http.Handler, io.Closer, error) {
			return nil, nil, state.ErrNotFound
		},
		List:  links,
		Valid: func(string, string) bool { return true },
	})
	req = httptest.NewRequest("GET", "/"+id+"/api/v1/status", nil)
	req.AddCookie(&http.Cookie{Name: "hand_board_" + id, Value: token})
	rec = httptest.NewRecorder()
	missing.ServeHTTP(rec, req)
	testAPIError(t, rec, http.StatusNotFound, "not_found")
}

func sceneHandler(t *testing.T, sc scene) (*fixture, http.Handler) {
	t.Helper()
	codex := t.TempDir()
	_ = os.WriteFile(filepath.Join(codex, "models_cache.json"), []byte(`{"models":[{"slug":"gpt-6-luna","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"}]},{"slug":"gpt-6","supported_reasoning_levels":[{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"}]}]}`), 0o644)
	policy, _ := json.Marshal(harness.StarterPolicy)
	fx := newFixture(t)
	fx.st = openNow(t, sc.id)
	fx.options.Harness.CodexHome = codex
	fx.options.Home = sceneHome(t, sc.id)
	fx.options.Base = ""
	fx.options.Tick = 2 * time.Second
	fx.options.Now = func() time.Time { return frozen }
	fx.options.Controls = sc.name != "network"
	_ = os.WriteFile(filepath.Join(fx.options.Home, harness.PolicyFile), policy, 0o644)
	sc.build(t, fx)
	if _, err := fx.st.RenameFleet(context.Background(), sc.name); err != nil {
		t.Fatal(err)
	}
	return fx, fx.handler()
}

func TestWebAndAPIAgree(t *testing.T) {
	for _, sc := range scenes() {
		t.Run(sc.name, func(t *testing.T) {
			fx, h := sceneHandler(t, sc)
			if !fx.options.Controls {
				testAPIError(t, request(h, "GET", "/api/v1/status", nil, true), http.StatusForbidden, "forbidden")
				return
			}
			agreeWebAndAPI(t, h)
		})
	}
	t.Run("two attempts", func(t *testing.T) {
		fx := apiFixture(t, func() time.Time { return frozen })
		ctx := context.Background()
		if _, err := fx.st.EndAttempt(ctx, 1, state.AttemptStopped, "superseded"); err != nil {
			t.Fatal(err)
		}
		extra, err := fx.st.AddAttempt(ctx, state.AttemptSpec{TaskID: 1, Harness: "codex", Model: "gpt-6-luna", Effort: "low", Argv: []string{"/bin/codex", "x"}}, filepath.Join(t.TempDir(), "worktrees"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fx.st.AttemptRunning(ctx, extra.ID, state.Terminal{ServerGeneration: "gen-2", TerminalID: "t2", PaneID: "4", PID: 2, StartMarker: "2"}); err != nil {
			t.Fatal(err)
		}
		if _, err := fx.st.EndAttempt(ctx, extra.ID, state.AttemptInterrupted, "launch failed at /very/long/path/worktrees"); err != nil {
			t.Fatal(err)
		}
		agreeWebAndAPI(t, fx.handler())
	})
	t.Run("history limits", func(t *testing.T) {
		fx := apiFixture(t, func() time.Time { return frozen })
		ctx := context.Background()
		unread, err := fx.st.Reports(ctx, state.ReportFilter{TaskID: 1, Unacked: true}, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range unread {
			if _, err := fx.st.AckReport(ctx, r.ID, "operator"); err != nil {
				t.Fatal(err)
			}
		}
		for i := range 51 {
			r, err := fx.st.AddReport(ctx, 1, state.ReportProgress, fmt.Sprintf("progress %d", i))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fx.st.AckReport(ctx, r.ID, "operator"); err != nil {
				t.Fatal(err)
			}
		}
		h := fx.handler()
		_, detail := apiInto[board.APITaskDetail](t, h, "/api/v1/task/t1")
		if !detail.MoreReports || !detail.MoreEvents {
			t.Fatalf("history limits = reports %v, events %v", detail.MoreReports, detail.MoreEvents)
		}
		_, attempt := apiInto[board.APIAttemptDetail](t, h, "/api/v1/attempt/a1")
		if !attempt.MoreReports || !attempt.MoreEvents {
			t.Fatalf("attempt history limits = reports %v, events %v", attempt.MoreReports, attempt.MoreEvents)
		}
		agreeWebAndAPI(t, h)
	})
}

func deliveredItems(page string) int {
	i := strings.Index(page, "<h3>Delivered</h3>")
	if i < 0 {
		return 0
	}
	j := strings.Index(page[i:], "</ul>")
	if j < 0 {
		return -1
	}
	return strings.Count(page[i:i+j], "<li>")
}

func transcriptItems(page string) int {
	i := strings.Index(page, "<h3>Transcript</h3>")
	if i < 0 {
		return 0
	}
	j := strings.Index(page[i:], "<h3>Timeline</h3>")
	if j < 0 {
		return -1
	}
	return strings.Count(page[i:i+j], `<li class="event">`)
}

func agreeWebAndAPI(t *testing.T, h http.Handler) {
	t.Helper()
	page := get(t, h, "/")
	_, status := apiInto[board.APIStatus](t, h, "/api/v1/status")
	m := regexp.MustCompile(`<div class="status-line" data-agent="([^"]*)" data-hint="([^"]*)"[^>]*>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("the status line is missing")
	}
	if html.UnescapeString(m[1]) != status.AgentState || html.UnescapeString(m[2]) != status.ComposerHint {
		t.Fatalf("status line = %q/%q, API = %q/%q", m[1], m[2], status.AgentState, status.ComposerHint)
	}
	waiting := regexp.MustCompile(`data-waiting="(\d+)"`).FindStringSubmatch(page)
	if waiting == nil || waiting[1] != strconv.Itoa(status.Needs.Waiting) {
		t.Fatalf("waiting = %v, API = %d", waiting, status.Needs.Waiting)
	}
	worst := regexp.MustCompile(`class="badge needs-count" href="#needs" data-worst="([^"]*)"`).FindStringSubmatch(page)
	if status.Needs.Worst == "" && worst != nil {
		t.Fatalf("HTML worst = %q, API has none", worst[1])
	}
	if status.Needs.Worst != "" && (worst == nil || worst[1] != status.Needs.Worst) {
		t.Fatalf("worst = %v, API = %q", worst, status.Needs.Worst)
	}
	blocks := strings.Split(page, `<details class="wait"`)[1:]
	if len(blocks) != len(status.Needs.Items) {
		t.Fatalf("%d waits in HTML, %d in API", len(blocks), len(status.Needs.Items))
	}
	for i, item := range status.Needs.Items {
		kind := regexp.MustCompile(`^ data-kind="([^"]*)"`).FindStringSubmatch(blocks[i])
		word := regexp.MustCompile(`class="badge code" data-tone="([^"]*)">([^<]+)<`).FindStringSubmatch(blocks[i])
		if kind == nil || kind[1] != item.Kind || word == nil || word[1] != item.Tone || word[2] != item.Word {
			t.Fatalf("wait %d = %v/%v, API = %q/%q/%q", i, kind, word, item.Kind, item.Tone, item.Word)
		}
	}

	_, tasks := apiInto[board.APITasks](t, h, "/api/v1/tasks")
	rows := 0
	for _, g := range tasks.Groups {
		rows += len(g.Tasks)
	}
	if rows != strings.Count(page, `class="row check task-row`) {
		t.Fatalf("%d task rows in HTML, %d in API", strings.Count(page, `class="row check task-row`), rows)
	}
	groups := regexp.MustCompile(`<h3 class="group">([^<]+) <span class="badge">(\d+)</span></h3>`).FindAllStringSubmatch(page, -1)
	if len(groups) != len(tasks.Groups) {
		t.Fatalf("%d task groups in HTML, %d in API", len(groups), len(tasks.Groups))
	}
	for i, g := range tasks.Groups {
		if groups[i][1] != g.Name || groups[i][2] != strconv.Itoa(g.Count) {
			t.Fatalf("group %d = %v, API = %q/%d", i, groups[i], g.Name, g.Count)
		}
	}
	var taskRows []board.APITaskRow
	var attemptCases []struct{ task, ref string }
	for _, g := range tasks.Groups {
		taskRows = append(taskRows, g.Tasks...)
		for _, row := range g.Tasks {
			if !strings.Contains(page, `data-state="`+row.State+`"`) || !strings.Contains(page, "/task/"+row.Ref) || !strings.Contains(page, html.EscapeString(row.Title)) {
				t.Fatalf("task %s is not in the page as the API says", row.Ref)
			}
		}
	}

	_, all := apiInto[board.APITasks](t, h, "/api/v1/tasks?all=1")
	for _, g := range all.Groups {
		for _, row := range g.Tasks {
			if (row.Status == state.StatusDone || row.Status == state.StatusAbandoned) && row.FinishedAt == "" {
				t.Fatalf("task %s is %s but its all=1 row has no finished_at", row.Ref, row.Status)
			}
		}
	}

	_, timeline := apiInto[board.APITimeline](t, h, "/api/v1/timeline")
	sup, you := 0, 0
	for _, e := range timeline.Entries {
		switch e.Role {
		case "supervisor":
			sup++
			if e.Ref != "" && !strings.Contains(page, ">"+html.EscapeString(e.Ref)+"<") {
				t.Fatalf("supervisor entry %s is not in the page", e.Ref)
			}
		case "hand", "wakes":
		default:
			you++
		}
	}
	if sup != strings.Count(page, `class="card from-sup"`) || you != strings.Count(page, `class="card from-you"`) {
		t.Fatalf("timeline HTML sup=%d you=%d, API sup=%d you=%d", strings.Count(page, `class="card from-sup"`), strings.Count(page, `class="card from-you"`), sup, you)
	}
	if (timeline.Working != nil) != strings.Contains(page, `class="working"`) {
		t.Fatalf("timeline working = %v, HTML = %v", timeline.Working != nil, strings.Contains(page, `class="working"`))
	}

	for _, row := range taskRows {
		if row.Status == state.StatusDone || row.Status == state.StatusAbandoned {
			if row.FinishedAt == "" {
				t.Fatalf("task %s is %s but its row has no finished_at", row.Ref, row.Status)
			}
		}
		detailPage := get(t, h, "/task/"+row.Ref)
		_, detail := apiInto[board.APITaskDetail](t, h, "/api/v1/task/"+row.Ref)
		if detail.Task.Ref != row.Ref || !strings.Contains(detailPage, html.EscapeString(detail.Task.Title)) || !strings.Contains(detailPage, detail.Task.Status) {
			t.Fatalf("task %s detail disagrees with its page", row.Ref)
		}
		if got, want := strings.Count(detailPage, `class="card report"`), len(detail.Reports); got != want {
			t.Fatalf("task %s has %d reports in HTML, %d in API", row.Ref, got, want)
		}
		if got, want := strings.Count(detailPage, `class="row wrap decision"`), len(detail.Decisions); got != want {
			t.Fatalf("task %s has %d decisions in HTML, %d in API", row.Ref, got, want)
		}
		if (detail.Plan != nil) != strings.Contains(detailPage, `data-role="plan"`) {
			t.Fatalf("task %s plan = %v, HTML = %v", row.Ref, detail.Plan != nil, strings.Contains(detailPage, `data-role="plan"`))
		}
		if (detail.Waits != nil) != strings.Contains(detailPage, "Waits until") {
			t.Fatalf("task %s waits = %v, HTML = %v", row.Ref, detail.Waits != nil, strings.Contains(detailPage, "Waits until"))
		}
		if detail.Waits != nil && detail.Waits.Note != "" && !strings.Contains(detailPage, html.EscapeString(detail.Waits.Note)) {
			t.Fatalf("task %s waits note is not in its page", row.Ref)
		}
		for _, d := range detail.Delivered {
			if !strings.Contains(detailPage, html.EscapeString(d.Ref)) {
				t.Fatalf("task %s deliverable %q is not in its page", row.Ref, d.Ref)
			}
		}
		if got := deliveredItems(detailPage); got != len(detail.Delivered) {
			t.Fatalf("task %s has %d deliverables in HTML, %d in API", row.Ref, got, len(detail.Delivered))
		}
		if got := strings.Count(detailPage, `data-tone="neutral">REPORT`); got != len(detail.Unread) {
			t.Fatalf("task %s has %d unread reports in HTML, %d in API", row.Ref, got, len(detail.Unread))
		}
		if detail.MoreReports != strings.Contains(detailPage, "Older reports are not shown") {
			t.Fatalf("task %s more_reports = %v", row.Ref, detail.MoreReports)
		}
		if detail.MoreEvents != strings.Contains(detailPage, "Only the latest 50 events are shown.") {
			t.Fatalf("task %s more_events = %v", row.Ref, detail.MoreEvents)
		}
		if got, want := strings.Count(detailPage, `class="row check sub"`), len(detail.Children); got != want {
			t.Fatalf("task %s has %d sub-tasks in HTML, %d in API", row.Ref, got, want)
		}
		if got, want := strings.Count(detailPage, `class="row check"`), len(detail.Attempts); got != want {
			t.Fatalf("task %s has %d attempts in HTML, %d in API", row.Ref, got, want)
		}
		if got, want := strings.Count(detailPage, `<li class="event">`), len(detail.Events); got != want {
			t.Fatalf("task %s has %d events in HTML, %d in API", row.Ref, got, want)
		}
		for _, e := range detail.Events {
			if !strings.Contains(detailPage, `datetime="`+e.At+`"`) || !strings.Contains(detailPage, `>`+strings.NewReplacer(".", " ", "_", " ").Replace(e.Kind)+`<`) {
				t.Fatalf("task %s event %d is not in its page", row.Ref, e.Seq)
			}
		}
		for _, a := range detail.Attempts {
			if a.Ref == row.Ref || !strings.Contains(detailPage, `/attempt/`+a.Ref) {
				t.Fatalf("task %s attempt row names %s", row.Ref, a.Ref)
			}
			attemptCases = append(attemptCases, struct{ task, ref string }{row.Ref, a.Ref})
		}
	}

	for _, row := range attemptCases {
		attemptPage := get(t, h, "/attempt/"+row.ref)
		_, attempt := apiInto[board.APIAttemptDetail](t, h, "/api/v1/attempt/"+row.ref)
		if attempt.Attempt.Ref != row.ref || attempt.Task.Ref != row.task || !strings.Contains(attemptPage, attempt.Attempt.Status) || !strings.Contains(attemptPage, html.EscapeString(attempt.Task.Title)) {
			t.Fatalf("attempt %s disagrees with its page", row.ref)
		}
		if got, want := strings.Count(attemptPage, `class="card report"`), len(attempt.Reports); got != want {
			t.Fatalf("attempt %s has %d reports in HTML, %d in API", row.ref, got, want)
		}
		if got := deliveredItems(attemptPage); got != len(attempt.Delivered) {
			t.Fatalf("attempt %s has %d deliverables in HTML, %d in API", row.ref, got, len(attempt.Delivered))
		}
		if attempt.MoreReports != strings.Contains(attemptPage, "Older reports are not shown") {
			t.Fatalf("attempt %s more_reports = %v", row.ref, attempt.MoreReports)
		}
		if attempt.MoreEvents != strings.Contains(attemptPage, "Only the latest 50 events are shown.") {
			t.Fatalf("attempt %s more_events = %v", row.ref, attempt.MoreEvents)
		}
		if attempt.Transcript.Shown != strings.Contains(attemptPage, "<h3>Transcript</h3>") {
			t.Fatalf("attempt %s transcript shown = %v", row.ref, attempt.Transcript.Shown)
		}
		if attempt.Transcript.More != strings.Contains(attemptPage, "Only the latest 50 entries are shown.") {
			t.Fatalf("attempt %s transcript more = %v", row.ref, attempt.Transcript.More)
		}
		if got := transcriptItems(attemptPage); got != len(attempt.Transcript.Turns) {
			t.Fatalf("attempt %s has %d transcript turns in HTML, %d in API", row.ref, got, len(attempt.Transcript.Turns))
		}
		for _, e := range attempt.Events {
			if !strings.Contains(attemptPage, `datetime="`+e.At+`"`) || !strings.Contains(attemptPage, `>`+strings.NewReplacer(".", " ", "_", " ").Replace(e.Kind)+`<`) {
				t.Fatalf("attempt %s event %d is not in its page", row.ref, e.Seq)
			}
		}
	}

	for _, item := range status.Needs.Items {
		if item.Decision == "" {
			continue
		}
		decisionPage := get(t, h, "/decision/"+item.Decision)
		_, decision := apiInto[board.APIDecisionDetail](t, h, "/api/v1/decision/"+item.Decision)
		if !strings.Contains(decisionPage, html.EscapeString(decision.Decision.Headline)) || !strings.Contains(decisionPage, decision.Decision.Status) || !strings.Contains(decisionPage, html.EscapeString(decision.Task.Title)) {
			t.Fatalf("decision %s disagrees with its page", item.Decision)
		}
	}
	for _, item := range status.Needs.Items {
		if item.Report == "" {
			continue
		}
		_, report := apiInto[board.APIReportDetail](t, h, "/api/v1/report/"+item.Report)
		reportPage := get(t, h, "/task/"+report.Report.Task)
		if !strings.Contains(reportPage, `id="`+item.Report+`"`) || !strings.Contains(reportPage, html.EscapeString(report.Task.Title)) {
			t.Fatalf("report %s disagrees with its task page", item.Report)
		}
	}
}

func TestAPIAttemptHasNoApproveOrAccess(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	body := apiRaw(t, fx.handler(), "/api/v1/attempt/a1")
	if strings.Contains(body, `"approve"`) || strings.Contains(body, `"access"`) {
		t.Fatalf("attempt JSON carries approve or access:\n%s", body)
	}
}

func TestACursorBelongsToItsPathAndQuery(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Tick = time.Hour
	h := fx.handler()
	env, _ := apiInto[json.RawMessage](t, h, "/api/v1/tasks")
	for _, path := range []string{"/api/v1/tasks?all=1", "/api/v1/timeline", "/api/v1/status"} {
		if rec := pollCursor(t, h, path, env.Cursor); rec.Code != http.StatusOK {
			t.Fatalf("a /tasks cursor answered %s with %d", path, rec.Code)
		}
	}
	if rec := pollCursor(t, h, "/api/v1/tasks", env.Cursor); rec.Code != http.StatusNotModified {
		t.Fatalf("the same read = %d", rec.Code)
	}
}

func TestACursorFollowsAFileADeliverableLinksTo(t *testing.T) {
	for _, gone := range []bool{false, true} {
		name := "created"
		if gone {
			name = "deleted"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			fx := apiFixture(t, func() time.Time { return frozen })
			file := filepath.Join(fx.options.Home, "reports", "out.md")
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if gone {
				if err := os.WriteFile(file, []byte("out"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			live, err := fx.st.LiveAttempts(ctx)
			if err != nil || len(live) == 0 {
				t.Fatalf("live attempts = %d, %v", len(live), err)
			}
			r, err := fx.st.AddReport(ctx, live[0].ID, state.ReportDone, "Done\nDelivered: "+file)
			if err != nil {
				t.Fatal(err)
			}
			h := fx.handler()
			for _, path := range []string{"/api/v1/report/" + state.ReportRef(r.ID), "/api/v1/task/" + state.TaskRef(r.TaskID), "/api/v1/tasks"} {
				apiInto[json.RawMessage](t, h, path)
				nextTick()
				env, _ := apiInto[json.RawMessage](t, h, path)
				if rec := pollCursor(t, h, path, env.Cursor); rec.Code != http.StatusNotModified {
					t.Fatalf("%s: unchanged poll = %d", path, rec.Code)
				}
				if gone {
					err = os.Remove(file)
				} else {
					err = os.WriteFile(file, []byte("out"), 0o644)
				}
				if err != nil {
					t.Fatal(err)
				}
				nextTick()
				rec := pollCursor(t, h, path, env.Cursor)
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: a poll after the file %s = %d", path, name, rec.Code)
				}
				if has := strings.Contains(rec.Body.String(), `"href"`); has == gone {
					t.Fatalf("%s: href present = %t after the file %s", path, has, name)
				}
				if gone {
					err = os.WriteFile(file, []byte("out"), 0o644)
				} else {
					err = os.Remove(file)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestACursorFollowsAnInboxImage(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	shot := filepath.Join(fx.options.Home, "inbox", "20260101-000000-1.png")
	if _, err := fx.st.AddSupervisorInput(context.Background(), "look\n[image: "+shot+"]"); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	apiInto[json.RawMessage](t, h, "/api/v1/timeline")
	nextTick()
	env, _ := apiInto[json.RawMessage](t, h, "/api/v1/timeline")
	if err := os.MkdirAll(filepath.Dir(shot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shot, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	nextTick()
	if rec := pollCursor(t, h, "/api/v1/timeline", env.Cursor); rec.Code != http.StatusOK {
		t.Fatalf("a poll after the image arrived = %d", rec.Code)
	}
}

func TestACursorFollowsTheOldFilesWarning(t *testing.T) {
	start := time.Now()
	var offset time.Duration
	fx := apiFixture(t, func() time.Time { return start.Add(offset) })
	home := fx.options.Home
	policy := `{"retention":{"warn_size":"off","warn_days":"30d"}}`
	if err := os.WriteFile(filepath.Join(home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(home, "shots", "t1", "a.png")
	h := fx.handler()
	env, _ := apiInto[json.RawMessage](t, h, "/api/v1/status")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	when := start.AddDate(0, 0, -31)
	if err := os.Chtimes(old, when, when); err != nil {
		t.Fatal(err)
	}
	offset = 6 * time.Minute
	nextTick()
	rec := pollCursor(t, h, "/api/v1/status", env.Cursor)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"kind":"artifacts"`) {
		t.Fatalf("the warning did not reach the poll (%d):\n%s", rec.Code, rec.Body.String())
	}
	env, _ = apiInto[json.RawMessage](t, h, "/api/v1/status")
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	offset = 12 * time.Minute
	nextTick()
	rec = pollCursor(t, h, "/api/v1/status", env.Cursor)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"kind":"artifacts"`) {
		t.Fatalf("the cleared warning did not reach the poll (%d):\n%s", rec.Code, rec.Body.String())
	}
}

func TestACursorFollowsAWorkerSignalTheLiveStateRetires(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	live, err := fx.st.LiveAttempts(context.Background())
	if err != nil || len(live) == 0 {
		t.Fatalf("live attempts = %d, %v", len(live), err)
	}
	if err := fx.st.NoteAttempt(context.Background(), live[0].ID, "idle", "turn ended"); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	nextTick()
	env, _ := apiInto[json.RawMessage](t, h, "/api/v1/status")
	fx.mu.Lock()
	fx.panes = map[string]string{"3": "idle"}
	fx.mu.Unlock()
	nextTick()
	if rec := pollCursor(t, h, "/api/v1/status", env.Cursor); rec.Code != http.StatusOK {
		t.Fatalf("a signal that the pane no longer retires left the cursor unchanged (%d)", rec.Code)
	}
}

func TestAReportWriteMovesAnotherReportsCursor(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	apiSeed(t, fx)
	fx.options.Tick = apiTick
	h := fx.handler()
	env, _ := apiInto[json.RawMessage](t, h, "/api/v1/report/r1")
	live, err := fx.st.LiveAttempts(ctx)
	if err != nil || len(live) == 0 {
		t.Fatalf("live attempts = %d, %v", len(live), err)
	}
	if _, err := fx.st.AddReport(ctx, live[len(live)-1].ID, state.ReportProgress, "Unrelated"); err != nil {
		t.Fatal(err)
	}
	nextTick()
	if rec := pollCursor(t, h, "/api/v1/report/r1", env.Cursor); rec.Code != http.StatusOK {
		t.Fatalf("a report write left another report's cursor unchanged (%d)", rec.Code)
	}
}

func TestAWriteDoesNotWaitForASlowVersion(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Tick = 300 * time.Millisecond
	fx.options.Control = func(ctx context.Context, call ...string) (string, error) {
		_, err := fx.st.AddSupervisorInput(ctx, call[3])
		return "", err
	}
	var block atomic.Bool
	entered, release := make(chan struct{}, 1), make(chan struct{})
	fx.srv.Handle("agent.explain", func(raw json.RawMessage) (any, error) {
		if block.Load() {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		}
		var p struct{ Pane string }
		_ = json.Unmarshal(raw, &p)
		return map[string]any{"pane": p.Pane, "agent": "claude", "status": "working"}, nil
	})
	h := fx.handler()
	env, _ := apiInto[json.RawMessage](t, h, "/api/v1/status")
	time.Sleep(fx.options.Tick + 50*time.Millisecond)
	block.Store(true)
	slow := make(chan int, 1)
	go func() { slow <- pollCursor(t, h, "/api/v1/status", env.Cursor).Code }()
	<-entered
	block.Store(false)
	posted := make(chan int, 1)
	go func() {
		posted <- apiPost(t, h, "/api/v1/message", map[string]string{"text": "hello"}, token, true).Code
	}()
	select {
	case code := <-posted:
		if code >= 300 {
			t.Fatalf("message = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a message waited for a slow version read")
	}
	close(release)
	<-slow
	if rec := pollCursor(t, h, "/api/v1/status", env.Cursor); rec.Code != http.StatusOK {
		t.Fatalf("poll after a message and a late version read = %d, want 200", rec.Code)
	}
}

func TestAnUnauthenticatedWriteKeepsTheSharedVersion(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Tick = time.Hour
	h := fx.handler()
	env, _ := apiInto[json.RawMessage](t, h, "/api/v1/status")
	before := fx.explained()
	for _, rec := range []*httptest.ResponseRecorder{
		apiPost(t, h, "/api/v1/message", map[string]string{"text": "hello"}, token, false),
		apiPost(t, h, "/api/v1/message", map[string]string{"text": "hello"}, "", true),
	} {
		if rec.Code < 400 {
			t.Fatalf("a request without a session or token = %d", rec.Code)
		}
	}
	if rec := pollCursor(t, h, "/api/v1/status", env.Cursor); rec.Code != http.StatusNotModified || fx.explained() != before {
		t.Fatalf("poll after refused writes = %d with %d pane reads", rec.Code, fx.explained()-before)
	}
}

func TestACursorDoesNotSurviveARestart(t *testing.T) {
	now := frozen
	fx := apiFixture(t, func() time.Time { return now })
	env, _ := apiInto[json.RawMessage](t, fx.handler(), "/api/v1/status")
	now = now.Add(time.Second)
	if rec := pollCursor(t, fx.handler(), "/api/v1/status", env.Cursor); rec.Code != http.StatusOK {
		t.Fatalf("a cursor from an earlier board = %d, want 200", rec.Code)
	}
}

func TestAFileOutsideTheFleetNeverMovesACursor(t *testing.T) {
	ctx := context.Background()
	fx := apiFixture(t, func() time.Time { return frozen })
	outside := filepath.Join(t.TempDir(), "build.log")
	inside := filepath.Join(fx.options.Home, "reports", "out.md")
	for _, f := range []string{outside, inside} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	live, err := fx.st.LiveAttempts(ctx)
	if err != nil || len(live) == 0 {
		t.Fatalf("live attempts = %d, %v", len(live), err)
	}
	r, err := fx.st.AddReport(ctx, live[0].ID, state.ReportDone, "Done\nDelivered: "+outside+"\nDelivered: "+inside)
	if err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	path := "/api/v1/report/" + state.ReportRef(r.ID)
	apiInto[json.RawMessage](t, h, path)
	nextTick()
	env, _ := apiInto[json.RawMessage](t, h, path)
	for _, f := range []string{outside, inside} {
		if err := os.WriteFile(f, []byte("a much longer file than before"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nextTick()
	if rec := pollCursor(t, h, path, env.Cursor); rec.Code != http.StatusNotModified {
		t.Fatalf("a delivered file that only grew moved the cursor (%d)", rec.Code)
	}
}
