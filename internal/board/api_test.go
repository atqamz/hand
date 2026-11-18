package board_test

import (
	"bytes"
	"context"
	"database/sql"
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
	"github.com/atqamz/hand/internal/luvus"
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
		Receipt     string          `json:"receipt"`
		Warn        string          `json:"warn"`
		Data        json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("GET %s is not the envelope: %v:\n%s", path, err, rec.Body.String())
	}
	var data T
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("GET %s data: %v:\n%s", path, err, env.Data)
	}
	return board.APIEnvelope{Version: env.Version, Fleet: env.Fleet, Cursor: env.Cursor, GeneratedAt: env.GeneratedAt, Receipt: env.Receipt, Warn: env.Warn, Data: data}, data
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
		Receipt     string          `json:"receipt"`
		Warn        string          `json:"warn"`
		Data        json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("POST is not the envelope: %v:\n%s", err, rec.Body.String())
	}
	var data T
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("POST data: %v:\n%s", err, env.Data)
	}
	return board.APIEnvelope{Version: env.Version, Fleet: env.Fleet, Cursor: env.Cursor, GeneratedAt: env.GeneratedAt, Receipt: env.Receipt, Warn: env.Warn, Data: data}, data
}

func TestAPIGolden(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	for _, c := range []struct{ name, path string }{
		{"status", "/api/v1/status"},
		{"models", "/api/v1/models"},
		{"timeline", "/api/v1/timeline"},
		{"tasks", "/api/v1/tasks"},
		{"tasks-search", "/api/v1/tasks/search?status=all"},
		{"projects", "/api/v1/projects"},
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

func apiSearchFixture(t *testing.T) (*fixture, *time.Time) {
	t.Helper()
	now := frozen.Add(-72 * time.Hour)
	path := filepath.Join(t.TempDir(), "hand.db")
	st, err := state.Open(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO fleet(only, id, name) VALUES (1, ?, 'golden')`, "f00000000e0aa"); err != nil {
		t.Fatal(err)
	}
	return &fixture{st: st, options: board.Options{Controls: true, Now: func() time.Time { return frozen }, Base: "", Tick: apiTick}}, &now
}

func TestAPITaskSearchDaysGolden(t *testing.T) {
	fx, now := apiSearchFixture(t)
	ctx := context.Background()
	for _, p := range []string{"hand", "docs"} {
		if _, err := fx.st.AddProject(ctx, p, filepath.Join(t.TempDir(), p), ""); err != nil {
			t.Fatal(err)
		}
	}
	open := func(project, title string, at time.Time) state.Task {
		*now = at
		task, err := fx.st.AddTask(ctx, project, title, "")
		if err != nil {
			t.Fatal(err)
		}
		*now = at
		if _, err := fx.st.Transition(ctx, task.ID, state.StatusActive); err != nil {
			t.Fatal(err)
		}
		return task
	}
	finish := func(task state.Task, to string, at time.Time) {
		*now = at
		if _, err := fx.st.Transition(ctx, task.ID, to); err != nil {
			t.Fatal(err)
		}
	}
	finish(open("hand", "Fix login", time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)), state.StatusDone, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	finish(open("docs", "Fix guide", time.Date(2026, 10, 2, 11, 0, 0, 0, time.UTC)), state.StatusAbandoned, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	finish(open("hand", "Chore", time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)), state.StatusDone, time.Date(2026, 10, 5, 11, 0, 0, 0, time.UTC))
	finish(open("hand", "Chore two", time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)), state.StatusAbandoned, time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC))
	*now = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	if _, err := fx.st.AddTask(ctx, "hand", "Fix inbox", ""); err != nil {
		t.Fatal(err)
	}
	open("hand", "Write docs", time.Date(2026, 10, 1, 7, 0, 0, 0, time.UTC))
	h := fx.handler()
	checkAPIGolden(t, h, "tasks-search-days", "/api/v1/tasks/search?status=all")
	_, got := apiInto[board.APITaskSearch](t, h, "/api/v1/tasks/search?status=all")
	if got.Page != 1 || got.PageSize != 50 || got.Total != 6 || got.Pages != 1 || got.Status != "all" {
		t.Fatalf("search = %+v", got)
	}
	want := []struct {
		name  string
		count int
	}{
		{"Mon 5 Oct UTC", 2}, {"Sun 4 Oct UTC", 1}, {"Sat 3 Oct UTC", 1}, {"Fri 2 Oct UTC", 1}, {"Thu 1 Oct UTC", 1},
	}
	if len(got.Groups) != len(want) {
		t.Fatalf("groups = %+v", got.Groups)
	}
	for i, g := range got.Groups {
		if g.Name != want[i].name || g.Count != want[i].count {
			t.Fatalf("group %d = %+v, want %+v", i, g, want[i])
		}
	}
	login := got.Groups[1].Tasks[0]
	if login.Ref != "t1" || login.Status != state.StatusDone || login.FinishedAt != "2026-10-04T09:00:00Z" || login.UpdatedAt != "2026-10-04T09:00:00Z" {
		t.Fatalf("t1 row = %+v", login)
	}
	inbox := got.Groups[3].Tasks[0]
	if inbox.Ref != "t5" || inbox.FinishedAt != "" || inbox.UpdatedAt != "2026-10-02T10:00:00Z" || inbox.Parent != "" {
		t.Fatalf("t5 row = %+v", inbox)
	}
}

var (
	pageFound  = regexp.MustCompile(`<p class="found" role="status">(\d+) tasks?</p>`)
	pageGroups = regexp.MustCompile(`<h3 class="group">([^<]+) <span class="badge">(\d+)</span></h3>`)
)

func searchMatchesThePage(t *testing.T, h http.Handler, q url.Values) board.APITaskSearch {
	t.Helper()
	body := get(t, h, "/tasks?"+q.Encode())
	_, got := apiInto[board.APITaskSearch](t, h, "/api/v1/tasks/search?"+q.Encode())
	var rows []string
	for _, g := range got.Groups {
		for _, row := range g.Tasks {
			rows = append(rows, row.Title)
		}
	}
	var want []string
	for _, title := range titles(body) {
		want = append(want, html.UnescapeString(title))
	}
	if !slices.Equal(rows, want) {
		t.Fatalf("%q rows = %q, page = %q", q.Encode(), rows, want)
	}
	total := pageFound.FindStringSubmatch(body)
	if total == nil || total[1] != strconv.Itoa(got.Total) {
		t.Fatalf("%q total = %v, API = %d", q.Encode(), total, got.Total)
	}
	headings := pageGroups.FindAllStringSubmatch(body, -1)
	if len(headings) != len(got.Groups) {
		t.Fatalf("%q groups = %v, API = %+v", q.Encode(), headings, got.Groups)
	}
	for i, g := range got.Groups {
		if headings[i][1] != g.Name || headings[i][2] != strconv.Itoa(g.Count) {
			t.Fatalf("%q group %d = %v, API = %q/%d", q.Encode(), i, headings[i], g.Name, g.Count)
		}
	}
	if got.Q != strings.TrimSpace(q.Get("q")) || got.Status != q.Get("status") || got.Project != q.Get("project") ||
		got.Since != q.Get("since") || got.Until != q.Get("until") {
		t.Fatalf("%q echo = %q/%q/%q/%q/%q", q.Encode(), got.Q, got.Status, got.Project, got.Since, got.Until)
	}
	return got
}

func TestAPITasksSearchMatchesThePage(t *testing.T) {
	fx, now := apiSearchFixture(t)
	ctx := context.Background()
	for _, p := range []string{"hand", "docs"} {
		if _, err := fx.st.AddProject(ctx, p, filepath.Join(t.TempDir(), p), ""); err != nil {
			t.Fatal(err)
		}
	}
	open := func(project, title string, at time.Time) state.Task {
		*now = at
		task, err := fx.st.AddTask(ctx, project, title, "")
		if err != nil {
			t.Fatal(err)
		}
		*now = at
		if _, err := fx.st.Transition(ctx, task.ID, state.StatusActive); err != nil {
			t.Fatal(err)
		}
		return task
	}
	finish := func(task state.Task, to string, at time.Time) {
		*now = at
		if _, err := fx.st.Transition(ctx, task.ID, to); err != nil {
			t.Fatal(err)
		}
	}
	finish(open("hand", "Fix login", time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)), state.StatusDone, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	finish(open("docs", "Fix guide", time.Date(2026, 10, 2, 11, 0, 0, 0, time.UTC)), state.StatusAbandoned, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	open("hand", "Fix search", time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC))
	finish(open("hand", "Fix 100% now", time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)), state.StatusDone, time.Date(2026, 10, 2, 13, 0, 0, 0, time.UTC))
	finish(open("docs", "Fix a_b", time.Date(2026, 10, 2, 13, 0, 0, 0, time.UTC)), state.StatusDone, time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC))
	open("hand", "Fix O'Brien", time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	finish(open("docs", "Fix R&D", time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)), state.StatusAbandoned, time.Date(2026, 10, 2, 16, 0, 0, 0, time.UTC))
	finish(open("hand", "Fix “curly” docs", time.Date(2026, 10, 2, 17, 0, 0, 0, time.UTC)), state.StatusDone, time.Date(2026, 10, 2, 18, 0, 0, 0, time.UTC))
	finish(open("hand", "Fix boundary late", time.Date(2026, 10, 5, 20, 0, 0, 0, time.UTC)), state.StatusDone, time.Date(2026, 10, 5, 23, 59, 0, 0, time.UTC))
	finish(open("docs", "Fix boundary early", time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)), state.StatusDone, time.Date(2026, 10, 6, 0, 1, 0, 0, time.UTC))
	*now = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	if _, err := fx.st.AddTask(ctx, "hand", "Fix inbox", ""); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	for _, q := range []url.Values{
		{},
		{"q": {"fix"}},
		{"q": {" fix "}},
		{"q": {"fix"}, "status": {"all"}},
		{"q": {"fix"}, "status": {"finished"}},
		{"q": {"fix"}, "status": {"done"}},
		{"q": {"fix"}, "status": {"abandoned"}},
		{"q": {"fix"}, "status": {"active"}},
		{"q": {"fix"}, "status": {"inbox"}},
		{"q": {"fix"}, "project": {"docs"}},
		{"q": {"login"}, "since": {"2026-10-04"}},
		{"q": {"login"}, "until": {"2026-10-03"}},
		{"q": {"t4"}},
		{"status": {"all"}},
		{"status": {"finished"}},
		{"q": {"fix"}, "status": {"finished"}, "project": {"hand"}, "since": {"2026-10-01"}, "until": {"2026-10-07"}},
		{"q": {"100%"}},
		{"q": {"a_b"}},
		{"q": {"O'Brien"}},
		{"q": {"R&D"}},
		{"q": {"“curly”"}},
		{"since": {"2026-10-05"}, "until": {"2026-10-01"}},
		{"q": {"boundary"}},
		{"q": {"boundary"}, "since": {"2026-10-06"}},
		{"q": {"boundary"}, "until": {"2026-10-05"}},
		{"q": {"boundary"}, "since": {"2026-10-05"}, "until": {"2026-10-06"}},
	} {
		searchMatchesThePage(t, h, q)
	}
}

func TestAPITasksSearchRowCarriesThePageBadges(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	ctx := context.Background()
	if _, err := fx.st.AddTaskUnder(ctx, 1, "hand", "Run it on Safari", ""); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	_, got := apiInto[board.APITaskSearch](t, h, "/api/v1/tasks/search?q=login&status=all")
	if len(got.Groups) != 1 || len(got.Groups[0].Tasks) != 1 {
		t.Fatalf("search = %+v", got)
	}
	row := got.Groups[0].Tasks[0]
	if row.Ref != "t1" || row.Title != "Fix login" || row.Project != "hand" || row.Status != state.StatusActive || row.State != "waiting" {
		t.Fatalf("row = %+v", row)
	}
	if row.Delivered == nil || row.OpenDecisions != 1 || row.Attempt == nil || row.Attempt.Ref != "a1" || row.UpdatedAt == "" || row.FinishedAt != "" || row.Children != 1 {
		t.Fatalf("row badges = %+v", row)
	}
}

func TestAPITasksSearchPages(t *testing.T) {
	fx, now := apiSearchFixture(t)
	ctx := context.Background()
	if _, err := fx.st.AddProject(ctx, "hand", filepath.Join(t.TempDir(), "hand"), ""); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := range 120 {
		*now = base.Add(time.Duration(i) * time.Minute)
		task, err := fx.st.AddTask(ctx, "hand", fmt.Sprintf("Chore %03d", i), "")
		if err != nil {
			t.Fatal(err)
		}
		*now = base
		if _, err := fx.st.Transition(ctx, task.ID, state.StatusActive); err != nil {
			t.Fatal(err)
		}
		*now = base.Add(time.Duration(i) * time.Minute)
		if _, err := fx.st.Transition(ctx, task.ID, state.StatusDone); err != nil {
			t.Fatal(err)
		}
	}
	*now = base.Add(200 * time.Minute)
	parent, err := fx.st.AddTask(ctx, "hand", "Parent chore", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Transition(ctx, parent.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Transition(ctx, parent.ID, state.StatusDone); err != nil {
		t.Fatal(err)
	}
	*now = base.Add(30 * time.Minute)
	child, err := fx.st.AddTaskUnder(ctx, parent.ID, "hand", "Child chore", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Transition(ctx, child.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.Transition(ctx, child.ID, state.StatusDone); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	seen := map[string]board.APITaskSearchRow{}
	for page, want := range map[int]struct {
		first string
		count int
	}{1: {"Parent chore", 50}, 2: {"Chore 070", 50}, 3: {"Chore 021", 22}} {
		got := searchMatchesThePage(t, h, url.Values{"page": {strconv.Itoa(page)}})
		var titles []string
		for _, g := range got.Groups {
			for _, row := range g.Tasks {
				titles = append(titles, row.Title)
				seen[row.Ref] = row
			}
		}
		if got.Page != page || got.PageSize != 50 || got.Total != 122 || got.Pages != 3 || len(titles) != want.count || titles[0] != want.first {
			t.Fatalf("page %d = %+v, first %q, %d rows", page, got, titles[0], len(titles))
		}
	}
	if row := seen[state.TaskRef(parent.ID)]; row.Title != "Parent chore" || row.Parent != "" || row.Children != 1 {
		t.Fatalf("parent row = %+v", row)
	}
	if row := seen[state.TaskRef(child.ID)]; row.Title != "Child chore" || row.Parent != state.TaskRef(parent.ID) || row.Children != 0 {
		t.Fatalf("child row = %+v", row)
	}
	_, past := apiInto[board.APITaskSearch](t, h, "/api/v1/tasks/search?page=4")
	if past.Page != 4 || past.Total != 122 || past.Pages != 3 || len(past.Groups) != 0 {
		t.Fatalf("page 4 = %+v", past)
	}
}

func TestAPITasksSearchRefusesBadParameters(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	for _, c := range []struct{ query, want string }{
		{"status=junk", `status "junk" must be one of finished, all, done, abandoned, active, inbox`},
		{"status=done%2Cabandoned", `status "done,abandoned" must be one of finished, all, done, abandoned, active, inbox`},
		{"since=2026-13-01", `since "2026-13-01" is not a date; want YYYY-MM-DD`},
		{"since=2026-1-1", `since "2026-1-1" is not a date; want YYYY-MM-DD`},
		{"until=October", `until "October" is not a date; want YYYY-MM-DD`},
		{"page=junk", "page must be a number from 1 to 1000000"},
		{"page=0", "page must be a number from 1 to 1000000"},
		{"page=-1", "page must be a number from 1 to 1000000"},
		{"page=1000001", "page must be a number from 1 to 1000000"},
	} {
		err := testAPIError(t, request(h, "GET", "/api/v1/tasks/search?"+c.query, nil, true), http.StatusBadRequest, "bad_request")
		if err.Message != c.want {
			t.Fatalf("%s = %q, want %q", c.query, err.Message, c.want)
		}
	}
}

func TestAPITasksSearchEmptyIsAnEmptyPage(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	_, got := apiInto[board.APITaskSearch](t, h, "/api/v1/tasks/search?q=nothing")
	if got.Page != 1 || got.PageSize != 50 || got.Total != 0 || got.Pages != 0 || got.Groups == nil || len(got.Groups) != 0 {
		t.Fatalf("empty search = %+v", got)
	}
}

func TestAPIProjectsFollowTheFilterOrder(t *testing.T) {
	fx, _ := apiSearchFixture(t)
	ctx := context.Background()
	for _, p := range []string{"zeta", "alpha", "hand"} {
		if _, err := fx.st.AddProject(ctx, p, filepath.Join(t.TempDir(), p), ""); err != nil {
			t.Fatal(err)
		}
	}
	h := fx.handler()
	env, got := apiInto[board.APIProjects](t, h, "/api/v1/projects")
	if want := []string{"alpha", "hand", "zeta"}; !slices.Equal(got.Projects, want) {
		t.Fatalf("projects = %q, want %q", got.Projects, want)
	}
	if rec := pollCursor(t, h, "/api/v1/projects", env.Cursor); rec.Code != http.StatusNotModified || rec.Header().Get("X-Hand-Cursor") != env.Cursor {
		t.Fatalf("unchanged projects = %d, cursor %q", rec.Code, rec.Header().Get("X-Hand-Cursor"))
	}
	page := get(t, h, "/tasks")
	selects := regexp.MustCompile(`(?s)<select name="project">(.*?)</select>`).FindStringSubmatch(page)
	if selects == nil {
		t.Fatal("the Tasks page has no project filter")
	}
	var pageNames []string
	for _, m := range regexp.MustCompile(`<option value="([^"]+)">`).FindAllStringSubmatch(selects[1], -1) {
		pageNames = append(pageNames, m[1])
	}
	if !slices.Equal(pageNames, got.Projects) {
		t.Fatalf("filter = %q, API = %q", pageNames, got.Projects)
	}
	empty := &fixture{st: openNow(t, "f00000000e0aa"), options: board.Options{Controls: true, Now: func() time.Time { return frozen }, Tick: apiTick}}
	_, none := apiInto[board.APIProjects](t, empty.handler(), "/api/v1/projects")
	if none.Projects == nil || len(none.Projects) != 0 {
		t.Fatalf("projects without a project = %+v", none.Projects)
	}
}

func TestThePhoneDecodesTheTasksSearchAnswerWithoutAChange(t *testing.T) {
	groupKeys := phoneRequired(t, "TaskGroup")
	rowKeys := phoneRequired(t, "TaskRow")
	for _, name := range []string{"tasks-search", "tasks-search-days"} {
		data := goldenData(t, name)
		for _, k := range []string{"page", "page_size", "total", "pages"} {
			if _, ok := data[k]; !ok {
				t.Fatalf("%s has no %s", name, k)
			}
		}
		var groups []map[string]json.RawMessage
		if err := json.Unmarshal(data["groups"], &groups); err != nil {
			t.Fatalf("%s: groups is not a list: %v", name, err)
		}
		for i, group := range groups {
			for _, k := range groupKeys {
				if _, ok := group[k]; !ok {
					t.Fatalf("%s group %d has no %s", name, i, k)
				}
			}
			var rows []map[string]json.RawMessage
			if err := json.Unmarshal(group["tasks"], &rows); err != nil {
				t.Fatalf("%s group %d: tasks is not a list: %v", name, i, err)
			}
			for j, row := range rows {
				for _, k := range rowKeys {
					if _, ok := row[k]; !ok {
						t.Fatalf("%s group %d row %d has no %s", name, i, j, k)
					}
				}
			}
		}
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

func TestAPIAllowsOnlyTheMethodsEachPathTakes(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	var paths []string
	takes := map[string][]string{}
	static := map[string]bool{}
	listed := map[string]bool{}
	for _, rt := range board.APIRoutes() {
		listed[rt.Method+" /api/v1"+rt.Path] = true
		if rt.Fallback {
			continue
		}
		path := "/api/v1" + strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(rt.Path, "{id}", "x1"), "{name}", "x1"), "{path...}", "reports/x1")
		if _, ok := takes[path]; !ok {
			paths = append(paths, path)
		}
		takes[path] = append(takes[path], rt.Method)
		if rt.Path != "" && !strings.Contains(rt.Path, "{id}") && !strings.Contains(rt.Path, "{name}") && !strings.Contains(rt.Path, "{path...}") {
			static[path] = true
		}
	}
	if len(paths) < 20 {
		t.Fatalf("the API lists %d paths: %q", len(paths), paths)
	}
	patterns, ok := h.(interface{ APIPatterns() []string })
	if !ok {
		t.Fatalf("the board does not report the API patterns it registers")
	}
	for _, pattern := range patterns.APIPatterns() {
		if !listed[pattern] {
			t.Fatalf("the board registers %q, which the API table lacks", pattern)
		}
	}
	slices.Sort(paths)
	for _, path := range paths {
		allow := "GET, HEAD"
		switch {
		case slices.Contains(takes[path], "POST") && slices.Contains(takes[path], "GET"):
			allow = "GET, HEAD, POST"
		case slices.Contains(takes[path], "POST"):
			allow = "POST"
		}
		for _, m := range []string{"GET", "HEAD", "POST", "PUT", "DELETE"} {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			req := httptest.NewRequest(m, path, nil).WithContext(ctx)
			req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			cancel()
			switch taken := slices.Contains(takes[path], m) || m == "HEAD" && slices.Contains(takes[path], "GET"); {
			case taken && rec.Code == http.StatusMethodNotAllowed:
				t.Fatalf("%s %s = 405: the router takes it, Allow = %q", m, path, rec.Header().Get("Allow"))
			case taken && static[path] && (m == "GET" || m == "HEAD") && rec.Code == http.StatusNotFound:
				t.Fatalf("%s %s = 404: the API table lists it, so the mux answers it", m, path)
			case taken:
			case m == "GET" || m == "HEAD":
				if rec.Code != http.StatusNotFound {
					t.Fatalf("%s %s = %d: the router answers it, but %q takes only %q", m, path, rec.Code, path, takes[path])
				}
			case rec.Code != http.StatusMethodNotAllowed:
				t.Fatalf("%s %s = %d: the router takes it, Allow = %q", m, path, rec.Code, allow)
			case rec.Header().Get("Allow") != allow:
				t.Fatalf("%s %s Allow = %q, want %q", m, path, rec.Header().Get("Allow"), allow)
			}
		}
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
		Warn    string          `json:"warn"`
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
	if !strings.HasPrefix(env.Warn, "the message was sent, but the conversation could not be read: ") || !strings.HasSuffix(env.Warn, "; refresh to see it") {
		t.Fatalf("warn = %q", env.Warn)
	}
	if header := apiUnescape(t, rec, "X-Hand-Warn"); header != env.Warn {
		t.Fatalf("X-Hand-Warn = %q, JSON warn = %q", header, env.Warn)
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
			if !strings.HasPrefix(env.Warn, "the action went through, but the status could not be read: ") || !strings.HasSuffix(env.Warn, "; refresh to see it") {
				t.Fatalf("warn = %q", env.Warn)
			}
			if env.Receipt == "" {
				t.Fatal("an applied control carries no receipt")
			}
			if header := apiUnescape(t, rec, "X-Hand-Warn"); header != env.Warn {
				t.Fatalf("X-Hand-Warn = %q, JSON warn = %q", header, env.Warn)
			}
		})
	}
}

func TestAControlsWarningStaysAheadOfTheUnreadCaveat(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	warn := "claude model acme-opus is not an alias (opus, sonnet, haiku, fable) or a claude-* name, and Claude Code lists no models; starting anyway"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fx.options.Control = func(context.Context, ...string) (string, error) {
		cancel()
		return warn, nil
	}
	req := httptest.NewRequest("POST", "/api/v1/supervisor/interrupt", strings.NewReader(`{"ref":"s1"}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hand-CSRF", token)
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec := httptest.NewRecorder()
	fx.handler().ServeHTTP(rec, req)
	env, got := apiBody[struct {
		Accepted bool `json:"accepted"`
	}](t, rec)
	if !got.Accepted {
		t.Fatalf("answer = %+v", got)
	}
	if want := warn + "; the action went through, but the status could not be read: "; !strings.HasPrefix(env.Warn, want) || !strings.HasSuffix(env.Warn, "; refresh to see it") {
		t.Fatalf("warn = %q, want %q…", env.Warn, want)
	}
	if header := apiUnescape(t, rec, "X-Hand-Warn"); header != env.Warn {
		t.Fatalf("X-Hand-Warn = %q, JSON warn = %q", header, env.Warn)
	}
}

func TestAResumeItemsSupervisorCarriesItsEndTime(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	ctx := context.Background()
	sup, ok, err := fx.st.LatestSupervisor(ctx)
	if err != nil || !ok {
		t.Fatalf("latest supervisor: %v", err)
	}
	ended, err := fx.st.EndSupervisor(ctx, sup.ID, state.AttemptInterrupted, "test")
	if err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	_, st := apiInto[board.APIStatus](t, h, "/api/v1/status")
	if st.Supervisor == nil || st.Supervisor.EndedAt != ended.EndedAt || st.Supervisor.EndedAt == "" {
		t.Fatalf("supervisor = %+v, want ended_at %q", st.Supervisor, ended.EndedAt)
	}
	var resume *board.APINeed
	for i := range st.Needs.Items {
		if st.Needs.Items[i].Kind == "resume" {
			resume = &st.Needs.Items[i]
		}
	}
	if resume == nil || resume.Ref != st.Supervisor.Ref {
		t.Fatalf("resume item = %+v, supervisor = %+v", resume, st.Supervisor)
	}
	var root struct {
		Data struct {
			Supervisor map[string]json.RawMessage `json:"supervisor"`
		} `json:"data"`
	}
	rec := request(h, "GET", "/api/v1/status", nil, true)
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root.Data.Supervisor["ended_at"]; !ok {
		t.Fatalf("a resume item's supervisor has no ended_at: %s", rec.Body.String())
	}
	running := apiFixture(t, func() time.Time { return frozen })
	rec = request(running.handler(), "GET", "/api/v1/status", nil, true)
	var plain struct {
		Data struct {
			Supervisor map[string]json.RawMessage `json:"supervisor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &plain); err != nil {
		t.Fatal(err)
	}
	if _, ok := plain.Data.Supervisor["ended_at"]; ok {
		t.Fatalf("a running supervisor carries ended_at: %s", rec.Body.String())
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

func needItem(t *testing.T, h http.Handler, kind string) (board.APIStatus, board.APINeed) {
	t.Helper()
	_, st := apiInto[board.APIStatus](t, h, "/api/v1/status")
	for _, n := range st.Needs.Items {
		if n.Kind == kind {
			return st, n
		}
	}
	t.Fatalf("no %s item in %+v", kind, st.Needs.Items)
	return st, board.APINeed{}
}

func TestAPIBlockedItemAlwaysCarriesTheBoardTitle(t *testing.T) {
	for _, c := range []struct{ name, screen, title string }{
		{"parsed", "Allow Bash?\n❯ 1. Yes\n  2. No", "Allow Bash?"},
		{"unparsed", "Something is wrong\nPress any key to continue", "The supervisor's screen needs a key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := apiFixture(t, func() time.Time { return frozen })
			fx.status, fx.screen = "blocked", c.screen
			_, item := needItem(t, fx.handler(), "blocked")
			if item.Title != c.title {
				t.Fatalf("title = %q, want %q", item.Title, c.title)
			}
			if item.Keys == nil {
				t.Fatal("a blocked item carries no keys")
			}
			if c.name == "unparsed" {
				if len(item.Options) != 0 {
					t.Fatalf("an unparsed screen carries options: %+v", item.Options)
				}
				if got := len(item.Keys.Options); got != len(state.SupervisorKeys) {
					t.Fatalf("the fixed toolbar has %d buttons, want %d", got, len(state.SupervisorKeys))
				}
			}
		})
	}
}

func TestAPIBlockedItemCarriesTheBoardKeys(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.status, fx.screen = "blocked", "Allow Bash?\n❯ 1. Yes\n  2. No"
	st, item := needItem(t, fx.handler(), "blocked")
	want := []board.APIAskOption{{Label: "2 No", Keys: []string{"2"}}, {Label: "Esc", Keys: []string{"esc"}}, {Label: "1 Yes", Keys: []string{"1"}}}
	if !reflect.DeepEqual(item.Options, want) {
		t.Fatalf("options = %+v, want %+v", item.Options, want)
	}
	if item.Keys == nil || !reflect.DeepEqual(item.Keys.Options, want) {
		t.Fatalf("keys = %+v", item.Keys)
	}
	guard := st.Supervisor.Keys
	if guard == nil || item.Keys.Revision != guard.Revision || item.Keys.Digest != guard.Digest || item.Keys.Pressed != guard.Pressed {
		t.Fatalf("item guard = %+v, supervisor guard = %+v", item.Keys, guard)
	}
}

func TestAPIWorkerItemCarriesItsScreenAndKeys(t *testing.T) {
	cases := []struct {
		name, screen, title string
		want                []board.APIAskOption
		toolbar             bool
	}{
		{"numbered", "Bash command\n  rm -f $r/$f\n Do you want to proceed?\n❯ 1. Yes\n  2. No", "Bash: rm -f $r/$f · Do you want to proceed?",
			[]board.APIAskOption{{Label: "2 No", Keys: []string{"2"}}, {Label: "Esc", Keys: []string{"esc"}}, {Label: "1 Yes", Keys: []string{"1"}}}, false},
		{"circle", "Which flavor?\n❯ ◉ Vanilla\n  ◯ Mint\n\n↑/↓ select · enter to confirm · esc cancel", "Which flavor?",
			[]board.APIAskOption{{Label: "Vanilla", Keys: []string{"enter"}}, {Label: "Mint", Keys: []string{"down", "enter"}}}, false},
		{"too many", "Pick one?\n❯ ◉ One\n  ◯ Two\n  ◯ Three\n  ◯ Four\n  ◯ Five\n  ◯ Six\n  ◯ Seven\n  ◯ Eight\n  ◯ Nine\n\n↑/↓ select · enter to confirm · esc cancel", "Pick one?", nil, true},
		{"unparsed", "Press any key to continue", "Esc to cancel · Tab to amend", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t)
			fx.screen = c.screen
			a := blockedWorker(t, fx, "Fix login")
			_, item := needItem(t, fx.handler(), "worker")
			if item.Ref != state.AttemptRef(a.ID) || item.Title != c.title || item.Excerpt != c.screen {
				t.Fatalf("item = %+v", item)
			}
			if !reflect.DeepEqual(item.Options, c.want) {
				t.Fatalf("options = %+v, want %+v", item.Options, c.want)
			}
			if item.Keys == nil {
				t.Fatal("a worker item carries no keys")
			}
			if c.toolbar {
				if got := len(item.Keys.Options); got != len(state.SupervisorKeys) {
					t.Fatalf("the fixed toolbar has %d buttons, want %d", got, len(state.SupervisorKeys))
				}
			} else if !reflect.DeepEqual(item.Keys.Options, c.want) {
				t.Fatalf("keys = %+v", item.Keys)
			}
			if item.Keys.Revision != 7 || item.Keys.Digest != luvus.ScreenDigest(c.screen) || item.Keys.Pressed != 0 {
				t.Fatalf("guard = %+v", item.Keys)
			}
		})
	}
}

func TestAPIAttemptKeys(t *testing.T) {
	fx := newFixture(t)
	fx.screen = "Bash command\n  rm -f $r/$f\n Do you want to proceed?\n❯ 1. Yes\n  2. No"
	a := blockedWorker(t, fx, "Fix login")
	var args []string
	fx.options.Control = func(ctx context.Context, call ...string) (string, error) {
		args = append([]string(nil), call...)
		return "", fx.st.NoteAttempt(ctx, a.ID, "keys", "enter")
	}
	h := fx.handler()
	_, item := needItem(t, h, "worker")
	if item.Keys == nil {
		t.Fatal("a worker item carries no keys")
	}
	body := map[string]any{"key": "enter", "revision": item.Keys.Revision, "after": item.Keys.Pressed, "screen": item.Keys.Digest}
	env, got := apiBody[board.APIAttemptDetail](t, apiPost(t, h, "/api/v1/attempt/a1/keys", body, token, true))
	if got.Attempt.Ref != "a1" || env.Cursor == "" {
		t.Fatalf("answer = %+v, envelope = %+v", got.Attempt, env)
	}
	want := []string{"attempt", "keys", "--revision", "7", "--screen", item.Keys.Digest, "--after", "0", "a1", "enter"}
	if !slices.Equal(args, want) {
		t.Fatalf("control called %q, want %q", args, want)
	}
	if pressed, err := fx.st.LastKeys(context.Background(), state.AttemptRef(a.ID)); err != nil || pressed <= item.Keys.Pressed {
		t.Fatalf("pressed = %d, err %v; the press did not advance", pressed, err)
	}
}

func TestAPIAttemptKeysRefusals(t *testing.T) {
	fx := newFixture(t)
	fx.screen = "Allow?\n❯ 1. Yes\n  2. No"
	a := blockedWorker(t, fx, "Fix login")
	fx.options.Control = func(context.Context, ...string) (string, error) {
		return "", fmt.Errorf("%w: the screen changed since revision 7; nothing was sent", state.ErrConflict)
	}
	h := fx.handler()
	body := map[string]any{"key": "1", "revision": 7, "after": 0, "screen": "0123456789abcdef0123456789abcdef"}
	conflict := testAPIError(t, apiPost(t, h, "/api/v1/attempt/a1/keys", body, token, true), http.StatusConflict, "conflict")
	if strings.Contains(conflict.Message, "/") {
		t.Fatalf("conflict names a path: %q", conflict.Message)
	}
	testAPIError(t, apiPost(t, h, "/api/v1/attempt/a99/keys", body, token, true), http.StatusNotFound, "not_found")
	for _, bad := range []map[string]any{
		{"revision": 7, "after": 0, "screen": "0123456789abcdef0123456789abcdef"},
		{"key": "", "revision": 7, "after": 0, "screen": "0123456789abcdef0123456789abcdef"},
		{"key": "space", "revision": 7, "after": 0, "screen": "0123456789abcdef0123456789abcdef"},
		{"key": "1", "after": 0, "screen": "0123456789abcdef0123456789abcdef"},
		{"key": "1", "revision": -1, "after": 0, "screen": "0123456789abcdef0123456789abcdef"},
		{"key": "1", "revision": 7, "screen": "0123456789abcdef0123456789abcdef"},
		{"key": "1", "revision": 7, "after": 0, "screen": "short"},
	} {
		testAPIError(t, apiPost(t, h, "/api/v1/attempt/a1/keys", bad, token, true), http.StatusBadRequest, "bad_request")
	}
	testAPIError(t, apiPost(t, h, "/api/v1/attempt/a1/keys", body, "", true), http.StatusForbidden, "csrf_required")
	testAPIError(t, apiPost(t, h, "/api/v1/attempt/a1/keys", body, "", false), http.StatusUnauthorized, "unauthorized")
	forwarded := httptest.NewRequest("POST", "/api/v1/attempt/a1/keys", strings.NewReader(`{}`))
	forwarded.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	forwarded.Header.Set("X-Hand-CSRF", token)
	forwarded.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, forwarded)
	testAPIError(t, rec, http.StatusForbidden, "forbidden")
	notLoopback := board.New(fx.st, token, board.Options{Now: func() time.Time { return frozen }})
	testAPIError(t, apiPost(t, notLoopback, "/api/v1/attempt/a1/keys", body, token, true), http.StatusForbidden, "forbidden")
	put := httptest.NewRequest("PUT", "/api/v1/attempt/a1/keys", nil)
	put.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, put)
	testAPIError(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
	if got := rec.Header().Get("Allow"); got != "POST" {
		t.Fatalf("PUT /api/v1/attempt/a1/keys Allow = %q", got)
	}
	if a.Status != state.AttemptRunning {
		t.Fatalf("the fixture worker is %s", a.Status)
	}
}

func TestANeedCarriesTheOpenItemsSlugData(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	_, st := apiInto[board.APIStatus](t, h, "/api/v1/status")
	byRef := map[string]board.APINeed{}
	for _, n := range st.Needs.Items {
		byRef[n.Ref] = n
	}
	failure, ok := byRef["a2"]
	if !ok {
		t.Fatalf("no failure item on a2 in %+v", st.Needs.Items)
	}
	meta := failure.AttemptMeta
	if meta == nil || meta.Ref != "a2" || meta.Harness != "codex" || meta.Model != "gpt-6-luna" || meta.Status != "failed" {
		t.Fatalf("attempt_meta = %+v", meta)
	}
	if at, err := time.Parse(time.RFC3339Nano, meta.EndedAt); err != nil || at.IsZero() {
		t.Fatalf("ended_at = %q (%v)", meta.EndedAt, err)
	}
	if want := "launch did not finish: the worktree already exists at a path that is not ours"; failure.Reason != want {
		t.Fatalf("reason = %q, want %q", failure.Reason, want)
	}
	if failure.Project != "hand" {
		t.Fatalf("project = %q", failure.Project)
	}
	if failure.Attempt != "a2" {
		t.Fatalf("the attempt ref changed to %q", failure.Attempt)
	}
	decision := byRef["d1"]
	if decision.AttemptMeta != nil || decision.Reason != "" || decision.Project != "hand" {
		t.Fatalf("decision item = %+v", decision)
	}
	report := byRef["r1"]
	if report.Attempt != "a1" || report.AttemptMeta != nil || report.Reason != "" || report.Project != "hand" || report.Status != "done" {
		t.Fatalf("report item = %+v", report)
	}
	rec := request(h, "GET", "/api/v1/status", nil, true)
	var root struct {
		Data struct {
			Needs struct {
				Items []map[string]json.RawMessage `json:"items"`
			} `json:"needs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	for _, item := range root.Data.Needs.Items {
		var ref string
		_ = json.Unmarshal(item["ref"], &ref)
		_, hasMeta := item["attempt_meta"]
		_, hasReason := item["reason"]
		_, hasProject := item["project"]
		if ref == "a2" {
			if !hasMeta || !hasReason || !hasProject {
				t.Fatalf("failure item = %v", item)
			}
			continue
		}
		if hasMeta || hasReason {
			t.Fatalf("%s carries slug data it has none of: %v", ref, item)
		}
		if ref == "d1" && !hasProject {
			t.Fatalf("decision item has no project: %v", item)
		}
	}
}

func apiUnescape(t *testing.T, rec *httptest.ResponseRecorder, name string) string {
	t.Helper()
	v := rec.Header().Get(name)
	if v == "" {
		return ""
	}
	got, err := url.PathUnescape(v)
	if err != nil {
		t.Fatalf("%s = %q: %v", name, v, err)
	}
	return got
}

func TestEveryActionAnswersWithTheBoardsReceipt(t *testing.T) {
	for _, c := range []struct {
		name, path string
		body       any
		want       string
	}{
		{"answer", "/api/v1/decision/d1/answer", map[string]string{"choice": "1. Yes, keep it", "note": "the docs keep the old name"}, "d1 answered · s1 reads it now"},
		{"ack", "/api/v1/report/r1/ack", nil, "r1 marked read"},
		{"start", "/api/v1/supervisor/start", nil, "Starting the supervisor…"},
		{"resume", "/api/v1/supervisor/resume", nil, "Starting the supervisor…"},
		{"stop", "/api/v1/supervisor/stop", map[string]string{"ref": "s1"}, "s1 stopping"},
		{"interrupt", "/api/v1/supervisor/interrupt", map[string]string{"ref": "s1"}, "Interrupt sent to s1"},
		{"force", "/api/v1/supervisor/force", map[string]string{"ref": "s1"}, "Typed into s1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := apiFixture(t, func() time.Time { return frozen })
			rec := apiPost(t, fx.handler(), c.path, c.body, token, true)
			env, _ := apiBody[any](t, rec)
			if env.Receipt != c.want {
				t.Fatalf("receipt = %q, want %q", env.Receipt, c.want)
			}
			if got := apiUnescape(t, rec, "X-Hand-Receipt"); got != env.Receipt {
				t.Fatalf("X-Hand-Receipt = %q, JSON receipt = %q", got, env.Receipt)
			}
			if env.Warn != "" || rec.Header().Get("X-Hand-Warn") != "" {
				t.Fatalf("warn = %q, header %q", env.Warn, rec.Header().Get("X-Hand-Warn"))
			}
		})
	}
}

func TestAKeysAnswerCarriesTheBoardsReceipt(t *testing.T) {
	t.Run("supervisor", func(t *testing.T) {
		fx := apiFixture(t, func() time.Time { return frozen })
		fx.status, fx.screen = "blocked", "Allow Bash?\n❯ 1. Yes\n  2. No"
		h := fx.handler()
		_, st := apiInto[board.APIStatus](t, h, "/api/v1/status")
		keys := st.Supervisor.Keys
		rec := apiPost(t, h, "/api/v1/supervisor/keys",
			map[string]any{"keys": []string{"down", "enter"}, "revision": keys.Revision, "digest": keys.Digest, "after": keys.Pressed, "ref": "s1"}, token, true)
		env, _ := apiBody[any](t, rec)
		if want := "Sent down enter to s1"; env.Receipt != want {
			t.Fatalf("receipt = %q, want %q", env.Receipt, want)
		}
		if got := apiUnescape(t, rec, "X-Hand-Receipt"); got != env.Receipt {
			t.Fatalf("X-Hand-Receipt = %q, JSON receipt = %q", got, env.Receipt)
		}
	})
	t.Run("worker", func(t *testing.T) {
		fx := newFixture(t)
		fx.screen = "Allow?\n❯ 1. Yes\n  2. No"
		blockedWorker(t, fx, "Fix login")
		h := fx.handler()
		_, item := needItem(t, h, "worker")
		rec := apiPost(t, h, "/api/v1/attempt/a1/keys",
			map[string]any{"key": "enter", "revision": item.Keys.Revision, "after": item.Keys.Pressed, "screen": item.Keys.Digest}, token, true)
		env, _ := apiBody[any](t, rec)
		if want := "Sent enter to a1"; env.Receipt != want {
			t.Fatalf("receipt = %q, want %q", env.Receipt, want)
		}
		if got := apiUnescape(t, rec, "X-Hand-Receipt"); got != env.Receipt {
			t.Fatalf("X-Hand-Receipt = %q, JSON receipt = %q", got, env.Receipt)
		}
	})
}

func TestAnActionsAnswerCarriesTheBoardsWarning(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.warn = "claude model acme-opus is not an alias (opus, sonnet, haiku, fable) or a claude-* name, and Claude Code lists no models; starting anyway"
	rec := apiPost(t, fx.handler(), "/api/v1/supervisor/start", nil, token, true)
	env, _ := apiBody[board.APIStatus](t, rec)
	if env.Warn != fx.warn {
		t.Fatalf("warn = %q, want %q", env.Warn, fx.warn)
	}
	if env.Receipt != "Starting the supervisor…" {
		t.Fatalf("receipt = %q", env.Receipt)
	}
	if got := apiUnescape(t, rec, "X-Hand-Warn"); got != env.Warn {
		t.Fatalf("X-Hand-Warn = %q, JSON warn = %q", got, env.Warn)
	}
}

func TestAFailedActionStillCarriesItsWarning(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.warn = "the screen moved under the press; nothing was sent"
	fx.fail = fmt.Errorf("%w: the screen changed since revision 7; nothing was sent", state.ErrConflict)
	rec := apiPost(t, fx.handler(), "/api/v1/supervisor/interrupt", map[string]string{"ref": "s1"}, token, true)
	got := testAPIError(t, rec, http.StatusConflict, "conflict")
	if got.Warn != fx.warn {
		t.Fatalf("error warn = %q, want %q", got.Warn, fx.warn)
	}
	if header := apiUnescape(t, rec, "X-Hand-Warn"); header != got.Warn {
		t.Fatalf("X-Hand-Warn = %q, JSON warn = %q", header, got.Warn)
	}
	if rec.Header().Get("X-Hand-Receipt") != "" {
		t.Fatalf("a failed action carries a receipt: %q", rec.Header().Get("X-Hand-Receipt"))
	}
}

func TestASentMessageCarriesNoReceipt(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Control = func(ctx context.Context, call ...string) (string, error) {
		_, err := fx.st.AddSupervisorInput(ctx, call[3])
		return "", err
	}
	rec := apiPost(t, fx.handler(), "/api/v1/message", map[string]string{"text": "hello"}, token, true)
	env, _ := apiBody[board.APITimeline](t, rec)
	if env.Receipt != "" || rec.Header().Get("X-Hand-Receipt") != "" {
		t.Fatalf("message receipt = %q, header %q", env.Receipt, rec.Header().Get("X-Hand-Receipt"))
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
		{"supervisor-started-with-choice", "/api/v1/supervisor/start", map[string]string{"harness": "claude", "model": "sonnet", "effort": "low"}},
		{"supervisor-switched", "/api/v1/supervisor/switch", map[string]string{"ref": "s1", "model": "opus", "effort": "high"}},
		{"models-refreshed", "/api/v1/models/refresh", map[string]string{"harness": "copilot"}},
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

func TestAPIDeliveredGolden(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	claudeLog(t, fx, []string{
		rec("user", ago(20), "status?", 0),
		rec("assistant", ago(19), "All good.", 0),
		rec("user", ago(20), "hello from the desktop", 0),
	})
	fx.options.Control = func(ctx context.Context, call ...string) (string, error) {
		if len(call) == 4 && call[2] == "--text" {
			_, err := fx.st.AddSupervisorInput(ctx, call[3])
			return "", err
		}
		return "", nil
	}
	h := fx.handler()
	rec := apiPost(t, h, "/api/v1/message", map[string]string{"text": "hello from the desktop"}, token, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/message = %d:\n%s", rec.Code, rec.Body.String())
	}
	if err := fx.st.DeliverSupervisorInput(context.Background(), 1, false); err != nil {
		t.Fatal(err)
	}
	checkAPIGolden(t, h, "message-delivered", "/api/v1/timeline")
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

func withoutScreens(t *testing.T, path, body string) string {
	t.Helper()
	if path != "/api/v1/status" {
		return body
	}
	var env struct{ Data board.APIStatus }
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	for i, n := range env.Data.Needs.Items {
		if n.Kind == "blocked" || n.Kind == "worker" {
			env.Data.Needs.Items[i].Excerpt, env.Data.Needs.Items[i].Title = "", ""
			if n.Keys != nil {
				env.Data.Needs.Items[i].Keys.Options = nil
			}
		}
	}
	out, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func apiRaw(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	rec := request(h, "GET", path, nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d:\n%s", path, rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	scanAPIBody(t, path, body)
	scanScreenText(t, "GET "+path, withoutScreens(t, path, body))
	return body
}

func TestAPIStatusShowsABlockedScreensLastLines(t *testing.T) {
	wantOptions := []board.APIAskOption{{Label: "2 No", Keys: []string{"2"}}, {Label: "Esc", Keys: []string{"esc"}}, {Label: "1 Yes", Keys: []string{"1"}}}
	wantTitle := "Bash: rm -f $r/$f · Do you want to proceed?"
	deny := frozen.Add(119 * time.Second).Format(time.RFC3339)
	long := strings.Repeat("noise\n", 30) + permission

	fx := apiFixture(t, func() time.Time { return frozen })
	fx.status, fx.screen = "blocked", long
	fx.panes = map[string]string{"3": "blocked"}
	fx.screens = map[string]string{"3": long}
	if err := fx.st.NoteAttempt(context.Background(), 1, "blocked", "Esc to cancel · Tab to amend"); err != nil {
		t.Fatal(err)
	}
	raw := apiRaw(t, fx.handler(), "/api/v1/status")
	var env struct{ Data board.APIStatus }
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatal(err)
	}
	seen := map[string]board.APINeed{}
	for _, n := range env.Data.Needs.Items {
		seen[n.Kind] = n
	}
	excerpt := strings.Repeat("noise\n", 12) + permission
	for _, kind := range []string{"blocked", "worker"} {
		n, ok := seen[kind]
		if !ok {
			t.Fatalf("no %s item in %+v", kind, env.Data.Needs.Items)
		}
		if n.Excerpt != excerpt || n.Title != wantTitle || n.DenyAt != deny || !reflect.DeepEqual(n.Options, wantOptions) {
			t.Fatalf("%s item = %+v", kind, n)
		}
	}
	if n := strings.Count(raw, "rm -f"); n != 4 {
		t.Fatalf("a screen line outside the two items' title and excerpt, %d matches:\n%s", n, raw)
	}
}

func TestAPIStatusHasNoExcerptWhenNothingIsBlocked(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.status, fx.screen = "idle", permission
	raw := apiRaw(t, fx.handler(), "/api/v1/status")
	var env struct{ Data board.APIStatus }
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatal(err)
	}
	for _, n := range env.Data.Needs.Items {
		if n.Kind == "blocked" || n.Kind == "worker" || n.Options != nil || n.DenyAt != "" || (n.Kind != "report" && n.Excerpt != "") {
			t.Fatalf("an unblocked fleet shows a screen on %+v", n)
		}
	}
}

func TestAPIRefusedPeerGetsNoBlockedScreen(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.status, fx.screen = "blocked", permission
	forwarded := httptest.NewRequest("GET", "/api/v1/status", nil)
	forwarded.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	forwarded.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()
	fx.handler().ServeHTTP(rec, forwarded)
	testAPIError(t, rec, http.StatusForbidden, "forbidden")
	if strings.Contains(rec.Body.String(), "rm -f") {
		t.Fatalf("a refused request got the screen:\n%s", rec.Body.String())
	}
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
		if _, _, err := fx.st.RecordQuiet(ctx, 1, state.Refused+"SECRET-SCREEN-TEXT"); err != nil {
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
			rec("user", ago(1), "[hand v1 wake]\nattempt.quiet a1: turn ended without a new report: will not clear by itself: SECRET-SCREEN-TEXT; leaked-quiet-tail", 0),
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
			scanScreenText(t, c, withoutScreens(t, c, rec.Body.String()))
		}
		_, status := apiInto[board.APIStatus](t, h, "/api/v1/status")
		blocked := 0
		for _, item := range status.Needs.Items {
			if item.Kind != "blocked" && item.Kind != "worker" && item.Kind != "limited" {
				continue
			}
			blocked++
			if item.Kind == "limited" && item.Title != "" {
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
		if raw := apiRaw(t, h, "/api/v1/timeline"); strings.Contains(raw, "leaked-quiet-tail") {
			t.Fatalf("the timeline kept a repeated refusal after a semicolon:\n%s", raw)
		}
		if raw := apiRaw(t, h, "/api/v1/timeline"); strings.Contains(raw, "leaked-after-semicolon") {
			t.Fatalf("the timeline kept a screen detail after a semicolon:\n%s", raw)
		}
	})
	t.Run("dialog without a question", func(t *testing.T) {
		fx := apiFixture(t, func() time.Time { return frozen })
		fx.status, fx.screen, fx.hint = "blocked", "rm -f id_rsa\n❯ 1. Yes\n  2. No", "Esc to cancel"
		raw := apiRaw(t, fx.handler(), "/api/v1/status")
		if strings.Contains(raw, `"ask"`) {
			t.Fatalf("status carries an ask:\n%s", raw)
		}
		_, st := apiInto[board.APIStatus](t, fx.handler(), "/api/v1/status")
		if st.Needs.Items[0].Title != "Esc to cancel" {
			t.Fatalf("a screen without a question has the title %q", st.Needs.Items[0].Title)
		}
		if !strings.Contains(raw, `"excerpt":"rm -f id_rsa`) {
			t.Fatalf("status lacks the screen's lines:\n%s", raw)
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

func TestAPIModelsServesTheBoardsOwnMenu(t *testing.T) {
	r := newModelRig(t)
	r.all(t)
	r.cache(t, "agy", func(m map[string]any) { m["at"] = frozen.Add(-72 * time.Hour).Format(time.RFC3339Nano) })
	r.cache(t, "codex", func(m map[string]any) {
		m["failed"] = map[string]any{"at": frozen.Format(time.RFC3339Nano), "reason": "codex exited 1"}
	})
	r.fx.options.Now = func() time.Time { return frozen }
	ctx := context.Background()
	sup, err := r.fx.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "opus", Effort: "high", Argv: []string{"/bin/claude", "x"}, Session: claudeSession})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.fx.st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	_, models := apiInto[board.APIModels](t, r.fx.handler(), "/api/v1/models")
	var names []string
	for _, h := range models.Harnesses {
		names = append(names, h.Name)
	}
	if want := []string{"claude", "codex", "opencode", "agy", "omp"}; !slices.Equal(names, want) {
		t.Fatalf("harnesses = %q, want %q", names, want)
	}
	if models.Current != "claude" || models.Model != "opus" || models.Effort != "high" || !models.Switchable || models.Resumable {
		t.Fatalf("models = %+v", models)
	}
	claude := models.Harnesses[0]
	if claude.Model != "opus" || claude.Effort != "high" || !slices.Equal(claude.Efforts, strings.Fields(claudeEfforts)) {
		t.Fatalf("claude = %+v", claude)
	}
	wantGroups := []struct {
		label  string
		models []board.APIModel
	}{
		{"built-in", []board.APIModel{
			{Name: "opus", Text: "Opus 5.5 · opus", Efforts: strings.Fields(claudeEfforts), Selected: true},
			{Name: "haiku", Text: "Haiku 5.5 · haiku"},
		}},
		{"custom, efforts unverified", []board.APIModel{
			{Name: "my-gw/claude-x", Text: "Gateway X · my-gw/claude-x", Efforts: []string{"low", "high"}},
			{Name: "acme-opus", Text: "Acme Opus · acme-opus", Efforts: strings.Fields(claudeEfforts), Unverified: true},
		}},
	}
	if len(claude.Groups) != len(wantGroups) {
		t.Fatalf("claude groups = %+v", claude.Groups)
	}
	for i, g := range wantGroups {
		if claude.Groups[i].Label != g.label || !reflect.DeepEqual(claude.Groups[i].Models, g.models) {
			t.Fatalf("claude group %d = %+v, want %q %+v", i, claude.Groups[i], g.label, g.models)
		}
	}
	codex := models.Harnesses[1]
	if codex.Note != "refresh failed: codex exited 1" {
		t.Fatalf("codex note = %q", codex.Note)
	}
	if len(codex.Groups) != 1 || len(codex.Groups[0].Models) != 3 || codex.Groups[0].Models[2].Efforts != nil {
		t.Fatalf("codex groups = %+v", codex.Groups)
	}
	agy := models.Harnesses[3]
	if agy.Note != "listed 3 d ago" {
		t.Fatalf("agy note = %q", agy.Note)
	}
	omp := models.Harnesses[4]
	if omp.Missing != "omp not found on the board's PATH; restart the board after installing it" || len(omp.Groups) != 0 || omp.Efforts != nil {
		t.Fatalf("omp = %+v", omp)
	}
	if n := strings.Count(string(apiRaw(t, r.fx.handler(), "/api/v1/models")), `"copilot"`); n != 0 {
		t.Fatalf("the menu offers copilot: %d", n)
	}
}

func TestAPIModelsCarriesTheNoteThePendingSwitchAndTheDrop(t *testing.T) {
	r := newModelRig(t)
	r.all(t)
	ctx := context.Background()
	sup, err := r.fx.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "opencode", Model: "local/m2", Effort: "deep", Argv: []string{"/bin/opencode", "x"}, Session: "ses_hand0123456789abcdef01234567"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.fx.st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := r.fx.st.SetSupervisorModel(ctx, sup.ID, "local/m9", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.fx.st.SetSupervisorSwitch(ctx, sup.ID, "local/m3", "fast"); err != nil {
		t.Fatal(err)
	}
	read := func() board.APIModels {
		t.Helper()
		_, models := apiInto[board.APIModels](t, r.fx.handler(), "/api/v1/models")
		return models
	}
	old := state.SupervisorRef(sup.ID)
	if models := read(); models.ModelNote != "Asked for local/m2 deep, running local/m9 default." || models.Switching != "local/m3 fast" || models.Drop != "" || models.DropRef != "" || !models.Switchable {
		t.Fatalf("live: note %q, switching %q, drop %q, drop ref %q, switchable %v", models.ModelNote, models.Switching, models.Drop, models.DropRef, models.Switchable)
	}
	if _, err := r.fx.st.EndSupervisor(ctx, sup.ID, state.AttemptStopped, "stopped from the Board"); err != nil {
		t.Fatal(err)
	}
	if models := read(); models.Drop != "local/m3 fast" || models.DropRef != old || models.ModelNote != "" || models.Switchable {
		t.Fatalf("ended: note %q, switching %q, drop %q, drop ref %q, switchable %v", models.ModelNote, models.Switching, models.Drop, models.DropRef, models.Switchable)
	}
	failed, err := r.fx.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "opus", Effort: "high", Argv: []string{"/bin/claude", "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.fx.st.EndSupervisor(ctx, failed.ID, state.AttemptFailed, "no pty"); err != nil {
		t.Fatal(err)
	}
	if models := read(); models.Drop != "local/m3 fast" || models.DropRef != old {
		t.Fatalf("failed replacement: drop %q, drop ref %q", models.Drop, models.DropRef)
	}
}

func TestAPIStartWithAHarnessModelAndEffort(t *testing.T) {
	r := newModelRig(t)
	r.all(t)
	var calls [][]string
	r.fx.options.Control = func(_ context.Context, call ...string) (string, error) {
		calls = append(calls, append([]string(nil), call...))
		return "", nil
	}
	h := r.fx.handler()
	env, st := apiBody[board.APIStatus](t, apiPost(t, h, "/api/v1/supervisor/start", map[string]string{"harness": "claude", "model": "opus", "effort": "high"}, token, true))
	if want := []string{"supervisor", "start", "--harness", "claude", "--model", "opus", "--effort", "high"}; !slices.Equal(calls[len(calls)-1], want) {
		t.Fatalf("start called %q, want %q", calls[len(calls)-1], want)
	}
	if env.Receipt != "Starting the supervisor…" || env.Warn != "" {
		t.Fatalf("receipt %q, warn %q", env.Receipt, env.Warn)
	}
	for _, c := range []struct {
		body map[string]string
		argv []string
	}{
		{map[string]string{"harness": "claude", "model": "opus"}, []string{"supervisor", "start", "--harness", "claude", "--model", "opus"}},
		{map[string]string{"harness": "codex", "model": "gpt-6-luna"}, []string{"supervisor", "start", "--harness", "codex", "--model", "gpt-6-luna"}},
		{map[string]string{"harness": "opencode"}, []string{"supervisor", "start", "--harness", "opencode"}},
	} {
		env, _ := apiBody[board.APIStatus](t, apiPost(t, h, "/api/v1/supervisor/start", c.body, token, true))
		if !slices.Equal(calls[len(calls)-1], c.argv) || env.Warn != "" {
			t.Fatalf("%v: start called %q, want %q; warn %q", c.body, calls[len(calls)-1], c.argv, env.Warn)
		}
	}
	_, again := apiInto[board.APIStatus](t, h, "/api/v1/status")
	a, _ := json.Marshal(st)
	b, _ := json.Marshal(again)
	if !bytes.Equal(a, b) {
		t.Fatalf("start and the GET disagree:\n%s\n%s", a, b)
	}

	refuse := func(body any, want string) {
		t.Helper()
		before := len(calls)
		err := testAPIError(t, apiPost(t, h, "/api/v1/supervisor/start", body, token, true), http.StatusBadRequest, "bad_request")
		if err.Message != want {
			t.Fatalf("message = %q, want %q", err.Message, want)
		}
		if len(calls) != before {
			t.Fatalf("a refused start ran %q", calls[before:])
		}
	}
	refuse(map[string]string{"model": "opus"}, "give a harness to start with a model or an effort")
	refuse(map[string]string{"harness": "gemini"}, `harness "gemini" must be one of claude, codex, opencode, agy, copilot, omp`)
	refuse(map[string]string{"harness": "copilot", "model": "gpt-6-luna"}, "copilot starts workers only; run the supervisor on claude, codex, opencode, agy or omp")
	refuse(map[string]string{"harness": "claude", "model": "nope", "effort": "high"}, "claude model nope is not an alias (opus, sonnet, haiku, fable), a claude-* name or in Claude Code's model list")
	refuse(map[string]string{"harness": "claude", "model": "opus", "effort": "ultra"}, "claude model opus: effort ultra is not one of low, medium, high, xhigh, max")
	refuse(map[string]string{"harness": "claude", "model": "opus", "effort": "Very High"}, `claude effort "Very High" must be 1 to 32 lowercase letters, digits, _ or -, starting with a letter`)
	refuse(map[string]string{"harness": "codex", "effort": "high"}, `codex model "" must be 1 to 200 characters of letters, digits and . _ : / @ [ ] # + -, starting with a letter or digit`)
	refuse(map[string]string{"harness": "claude"}, `claude model "" must be 1 to 200 characters of letters, digits and . _ : / @ [ ] # + -, starting with a letter or digit`)
	refuse(map[string]string{"harness": "claude", "model": "a b"}, `claude model "a b" must be 1 to 200 characters of letters, digits and . _ : / @ [ ] # + -, starting with a letter or digit`)
	testAPIError(t, apiPostRaw(t, h, "/api/v1/supervisor/start", `{"harness":"claude"} {}`, token, true), http.StatusBadRequest, "bad_request")
	testAPIError(t, apiPostRaw(t, h, "/api/v1/supervisor/start", `{"harness":1}`, token, true), http.StatusBadRequest, "bad_request")

	if err := os.Remove(filepath.Join(r.home, "models", "codex.json")); err != nil {
		t.Fatal(err)
	}
	env, _ = apiBody[board.APIStatus](t, apiPost(t, h, "/api/v1/supervisor/start", map[string]string{"harness": "codex", "model": "gpt-6-luna", "effort": "low"}, token, true))
	if want := "codex model gpt-6-luna could not be checked: no model list yet; run `hand models --refresh codex`; starting anyway"; env.Warn != want {
		t.Fatalf("warn = %q, want %q", env.Warn, want)
	}
	if want := []string{"supervisor", "start", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low"}; !slices.Equal(calls[len(calls)-1], want) {
		t.Fatalf("start called %q, want %q", calls[len(calls)-1], want)
	}

	if err := os.Remove(filepath.Join(r.home, "models", "claude.json")); err != nil {
		t.Fatal(err)
	}
	env, _ = apiBody[board.APIStatus](t, apiPost(t, h, "/api/v1/supervisor/start", map[string]string{"harness": "claude", "model": "gpt-6-luna"}, token, true))
	if want := "claude model gpt-6-luna is not an alias (opus, sonnet, haiku, fable), a claude-* name or in Claude Code's model list; starting anyway"; env.Warn != want {
		t.Fatalf("warn = %q, want %q", env.Warn, want)
	}
	if want := []string{"supervisor", "start", "--harness", "claude", "--model", "gpt-6-luna"}; !slices.Equal(calls[len(calls)-1], want) {
		t.Fatalf("start called %q, want %q", calls[len(calls)-1], want)
	}
}

func TestAPIStartWithoutABodyCopiesTheLastSupervisor(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	var calls [][]string
	fx.options.Control = func(_ context.Context, call ...string) (string, error) {
		calls = append(calls, append([]string(nil), call...))
		return "", nil
	}
	h := fx.handler()
	env, _ := apiBody[board.APIStatus](t, apiPost(t, h, "/api/v1/supervisor/start", nil, token, true))
	if want := []string{"supervisor", "start"}; !slices.Equal(calls[len(calls)-1], want) || env.Receipt != "Starting the supervisor…" {
		t.Fatalf("start called %q, receipt %q", calls[len(calls)-1], env.Receipt)
	}
	fresh := newFixture(t)
	err := testAPIError(t, apiPost(t, fresh.handler(), "/api/v1/supervisor/start", nil, token, true), http.StatusConflict, "conflict")
	if err.Message != "there is no earlier supervisor to copy; give a harness, model and effort" {
		t.Fatalf("conflict = %q", err.Message)
	}
}

func TestAPISwitchModelOrHarness(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	var calls [][]string
	fx.options.Control = func(_ context.Context, call ...string) (string, error) {
		calls = append(calls, append([]string(nil), call...))
		return fx.warn, fx.fail
	}
	h := fx.handler()
	post := func(body any, want []string, receipt string) board.APIEnvelope {
		t.Helper()
		rec := apiPost(t, h, "/api/v1/supervisor/switch", body, token, true)
		env, _ := apiBody[board.APIStatus](t, rec)
		if len(calls) == 0 || !slices.Equal(calls[len(calls)-1], want) {
			t.Fatalf("switch called %q, want %q", calls[len(calls)-1], want)
		}
		if env.Receipt != receipt {
			t.Fatalf("receipt = %q, want %q", env.Receipt, receipt)
		}
		if header := apiUnescape(t, rec, "X-Hand-Receipt"); header != receipt {
			t.Fatalf("X-Hand-Receipt = %q, receipt = %q", header, receipt)
		}
		return env
	}
	env := post(map[string]any{"ref": "s1", "cancel": true}, []string{"supervisor", "switch", "--expect", "s1", "--cancel"}, "Switch canceled")
	if _, st := apiInto[board.APIStatus](t, h, "/api/v1/status"); env.Cursor == "" || st.Supervisor == nil {
		t.Fatalf("a switch answered no status: %+v", env)
	}
	post(map[string]string{"ref": "s1", "model": "opus", "effort": "high"}, []string{"supervisor", "switch", "--expect", "s1", "--model", "opus", "--effort", "high"}, "Switch applied")
	post(map[string]string{"ref": "s1", "harness": "codex"}, []string{"supervisor", "switch", "--expect", "s1", "--harness", "codex"}, "Switching to codex…")

	ctx := context.Background()
	if _, err := fx.st.SetSupervisorSwitch(ctx, 1, "opus", "high"); err != nil {
		t.Fatal(err)
	}
	post(map[string]string{"ref": "s1", "model": "opus"}, []string{"supervisor", "switch", "--expect", "s1", "--model", "opus"}, "Switch set for after this turn")
	if _, err := fx.st.CancelSupervisorSwitch(ctx, 1); err != nil {
		t.Fatal(err)
	}
	fx.warn = state.NoteLost + ": is m available?"
	post(map[string]string{"ref": "s1", "effort": "high"}, []string{"supervisor", "switch", "--expect", "s1", "--effort", "high"}, "Switch applied, not answered")

	fx.fail = fmt.Errorf("%w: s1 is working; switch harness when its turn ends, or interrupt it first", state.ErrConflict)
	before := len(calls)
	err := testAPIError(t, apiPost(t, h, "/api/v1/supervisor/switch", map[string]string{"ref": "s1", "harness": "codex"}, token, true), http.StatusConflict, "conflict")
	if err.Message != "conflict: s1 is working; switch harness when its turn ends, or interrupt it first" || err.Warn != fx.warn {
		t.Fatalf("conflict = %+v", err)
	}
	if len(calls) != before+1 {
		t.Fatalf("a refused switch ran %q", calls[before:])
	}
	fx.fail = nil

	for _, c := range []struct {
		body any
		want string
	}{
		{map[string]any{}, "give the supervisor the status showed, as ref"},
		{map[string]string{"ref": "nope"}, "give the supervisor the status showed, as ref"},
		{map[string]string{"ref": "s1"}, "give a model or effort"},
		{map[string]string{"ref": "s1", "harness": "gemini"}, `harness "gemini" must be one of claude, codex, opencode, agy, copilot, omp`},
		{map[string]string{"ref": "s1", "harness": "copilot", "model": "gpt-6-luna"}, "copilot starts workers only; run the supervisor on claude, codex, opencode, agy or omp"},
	} {
		before := len(calls)
		got := testAPIError(t, apiPost(t, h, "/api/v1/supervisor/switch", c.body, token, true), http.StatusBadRequest, "bad_request")
		if got.Message != c.want {
			t.Fatalf("message = %q, want %q", got.Message, c.want)
		}
		if len(calls) != before {
			t.Fatalf("a refused switch ran %q", calls[before:])
		}
	}
	testAPIError(t, apiPostRaw(t, h, "/api/v1/supervisor/switch", `{"ref":"s1","model":"opus"} trailing`, token, true), http.StatusBadRequest, "bad_request")
	testAPIError(t, apiPost(t, h, "/api/v1/supervisor/switch", map[string]string{"ref": "s1", "model": "opus"}, "", true), http.StatusForbidden, "csrf_required")

	ctx2, cancel := context.WithCancel(context.Background())
	defer cancel()
	fx.options.Control = func(context.Context, ...string) (string, error) {
		cancel()
		return "", nil
	}
	req := httptest.NewRequest("POST", "/api/v1/supervisor/switch", strings.NewReader(`{"ref":"s1","model":"opus"}`))
	req = req.WithContext(ctx2)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hand-CSRF", token)
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec := httptest.NewRecorder()
	fx.handler().ServeHTTP(rec, req)
	env2, accepted := apiBody[struct {
		Accepted bool `json:"accepted"`
	}](t, rec)
	if !accepted.Accepted || !strings.HasPrefix(env2.Warn, "the action went through, but the status could not be read: ") || env2.Receipt != "Switch applied" {
		t.Fatalf("accepted = %+v, warn %q, receipt %q", accepted, env2.Warn, env2.Receipt)
	}
}

func TestAPIRefreshModels(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	env, models := apiBody[board.APIModels](t, apiPost(t, h, "/api/v1/models/refresh", map[string]string{"harness": "copilot"}, token, true))
	if env.Receipt != "Refreshing models…" || env.Warn != "" || len(models.Harnesses) != 5 {
		t.Fatalf("refresh = %+v, models = %+v", env, models)
	}
	deadline := time.Now().Add(10 * time.Second)
	var cooled board.APIError
	for {
		rec := apiPost(t, h, "/api/v1/models/refresh", map[string]string{"harness": "copilot"}, token, true)
		cooled = testAPIError(t, rec, http.StatusTooManyRequests, "rate_limited")
		if !strings.Contains(cooled.Message, "already refreshing") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a refused refresh never reached the cooldown")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if cooled.Message != "copilot models were refreshed 0 s ago; try again in a moment" {
		t.Fatalf("cooldown = %q", cooled.Message)
	}
	bad := testAPIError(t, apiPost(t, h, "/api/v1/models/refresh", map[string]string{"harness": "opencode"}, token, true), http.StatusBadRequest, "bad_request")
	if bad.Message != "refresh the models of claude, codex, agy, copilot or omp" {
		t.Fatalf("unknown harness = %q", bad.Message)
	}
	testAPIError(t, apiPostRaw(t, h, "/api/v1/models/refresh", `{"harness":"copilot"} x`, token, true), http.StatusBadRequest, "bad_request")
	testAPIError(t, apiPost(t, h, "/api/v1/models/refresh", nil, "", true), http.StatusForbidden, "csrf_required")
	testAPIError(t, request(h, "GET", "/api/v1/models/refresh", nil, true), http.StatusNotFound, "not_found")
}

func TestAPairedPhoneCannotRefreshModels(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	companion := r.mustPair(c, f)
	csrf := r.csrfOf(c, f, companion)
	rep := r.post(c, f, "models/refresh", `{"harness":"claude"}`, csrf, companion)
	if rep.Code != http.StatusForbidden || !strings.Contains(rep.Body, `"code":"forbidden"`) || !strings.Contains(rep.Body, "a paired phone cannot refresh model lists; use the host's Board") {
		t.Fatalf("a phone's refresh = %d:\n%s", rep.Code, rep.Body)
	}
}

func goldenData(t *testing.T, name string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "api", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Data == nil {
		t.Fatalf("%s is not an envelope: %v", name, err)
	}
	return env.Data
}

var (
	kotlinSerialName = regexp.MustCompile(`@SerialName\("([^"]+)"\)`)
	kotlinValName    = regexp.MustCompile(`\bval\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

func kotlinParams(t *testing.T, class string) []string {
	t.Helper()
	return kotlinParamsIn(t, "Status.kt", "data class "+class+"(")
}

func kotlinParamsIn(t *testing.T, file, head string) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "android", "src-tauri", "gen", "android", "app", "src", "main", "java", "io", "github", "atqamz", "handcompanion", file))
	if err != nil {
		t.Fatal(err)
	}
	at := strings.Index(string(src), head)
	if at < 0 {
		t.Fatalf("%s has no %s", file, head)
	}
	rest, depth, start := string(src)[at+len(head):], 1, 0
	var out []string
	for i, r := range rest {
		switch r {
		case '(', '<', '[':
			depth++
		case ')', '>', ']':
			depth--
			if depth == 0 {
				return append(out, rest[start:i])
			}
		case ',':
			if depth == 1 {
				out, start = append(out, rest[start:i]), i+1
			}
		}
	}
	t.Fatalf("%s's %s has no closing paren", file, head)
	return nil
}

func phoneRequired(t *testing.T, class string) []string {
	t.Helper()
	var out []string
	for _, param := range kotlinParams(t, class) {
		param = strings.TrimSpace(param)
		if param == "" || strings.Contains(param, " = ") {
			continue
		}
		if m := kotlinSerialName.FindStringSubmatch(param); m != nil {
			out = append(out, m[1])
		} else if m := kotlinValName.FindStringSubmatch(param); m != nil {
			out = append(out, m[1])
		} else {
			t.Fatalf("%s parameter %q names no field", class, param)
		}
	}
	return out
}

func TestThePhoneDecodesTheNewAnswersWithoutAChange(t *testing.T) {
	status := phoneRequired(t, "Status")
	if !slices.Contains(status, "agent_state") || !slices.Contains(status, "compactions") || slices.Contains(status, "supervisor") {
		t.Fatalf("Status.kt's required keys = %q", status)
	}
	supervisor := phoneRequired(t, "Supervisor")
	if !slices.Contains(supervisor, "ref") || slices.Contains(supervisor, "keys") {
		t.Fatalf("Status.kt's required supervisor keys = %q", supervisor)
	}
	for _, name := range []string{"supervisor-started-with-choice", "supervisor-switched"} {
		data := goldenData(t, name)
		for _, k := range status {
			if _, ok := data[k]; !ok {
				t.Fatalf("%s: the phone's Status has no %s", name, k)
			}
		}
		var sup map[string]json.RawMessage
		if err := json.Unmarshal(data["supervisor"], &sup); err != nil {
			t.Fatalf("%s: supervisor is not one object: %v", name, err)
		}
		for _, k := range supervisor {
			if _, ok := sup[k]; !ok {
				t.Fatalf("%s: the phone's Supervisor has no %s", name, k)
			}
		}
	}
	for _, name := range []string{"models", "models-refreshed"} {
		data := goldenData(t, name)
		if _, ok := data["harnesses"]; !ok {
			t.Fatalf("%s carries no harnesses", name)
		}
		for _, k := range append([]string{"supervisor"}, status...) {
			if _, ok := data[k]; ok {
				t.Fatalf("%s carries a Status field, %s, so a phone would read it as one", name, k)
			}
		}
	}
}
