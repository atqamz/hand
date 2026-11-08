package board_test

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
		a, err := fx.st.AddAttempt(ctx, state.AttemptSpec{TaskID: taskID, Harness: harness, Model: model, Effort: effort, Argv: []string{"/bin/" + harness, "x"}}, "")
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

func apiFixture(t *testing.T, now func() time.Time) *fixture {
	t.Helper()
	fx := newFixture(t)
	fx.st = openNow(t, "f00000000e0aa")
	fx.options.Now = now
	fx.options.Controls = true
	fx.options.Base = ""
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
		t.Run(c.name, func(t *testing.T) {
			rec := request(h, "GET", c.path, nil, true)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d:\n%s", c.path, rec.Code, rec.Body.String())
			}
			var out bytes.Buffer
			if err := json.Indent(&out, rec.Body.Bytes(), "", "  "); err != nil {
				t.Fatalf("GET %s: %v", c.path, err)
			}
			out.WriteByte('\n')
			file := filepath.Join("testdata", "api", c.name+".json")
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
				t.Fatalf("GET %s does not match %s:\n%s", c.path, file, out.String())
			}
		})
	}
}

func TestAPICursorFollowsTheEventSequence(t *testing.T) {
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
	rec = request(h, "GET", "/api/v1/status?since="+url.QueryEscape(env.Cursor), nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("old cursor after an event = %d:\n%s", rec.Code, rec.Body.String())
	}
	var next board.APIEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if next.Cursor == env.Cursor {
		t.Fatal("the cursor did not move with the event sequence")
	}
	body := testAPIError(t, request(h, "GET", "/api/v1/status?since=not-a-cursor", nil, true), http.StatusBadRequest, "bad_cursor")
	if strings.Contains(body.Message, "/") {
		t.Fatalf("cursor error names a path: %q", body.Message)
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
	testAPIError(t, request(h, "POST", "/api/v1/status", url.Values{"csrf": {token}}, true), http.StatusMethodNotAllowed, "method_not_allowed")
	testAPIError(t, request(h, "GET", "/api/v1/nope", nil, true), http.StatusNotFound, "not_found")

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
}

func TestAPINeverReturnsScreenText(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.panes = map[string]string{"2": "blocked"}
	fx.hint = "Esc to cancel · Tab to amend"
	fx.screen = "SECRET-SCREEN-TEXT\nDo you want to proceed?\n❯ 1. Yes\n  2. No"
	h := fx.handler()
	for _, c := range []string{
		"/api/v1/status", "/api/v1/timeline", "/api/v1/tasks",
		"/api/v1/task/t1", "/api/v1/attempt/a1", "/api/v1/decision/d1", "/api/v1/report/r1",
	} {
		rec := request(h, "GET", c, nil, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d:\n%s", c, rec.Code, rec.Body.String())
		}
		for _, secret := range []string{"last_screen", "SECRET-SCREEN-TEXT", "Do you want to proceed?", "Trust this folder?", token} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatalf("GET %s returns %q:\n%s", c, secret, rec.Body.String())
			}
		}
	}
	_, status := apiInto[board.APIStatus](t, h, "/api/v1/status")
	blocked := 0
	for _, item := range status.Needs.Items {
		if item.Kind != "blocked" && item.Kind != "worker" && item.Kind != "limited" {
			continue
		}
		blocked++
		if item.Title != "" || item.Note != "" {
			t.Fatalf("%s item carries screen text: %+v", item.Kind, item)
		}
	}
	if blocked == 0 {
		t.Fatal("the fixture has no blocked item")
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

func TestWebAndAPIAgree(t *testing.T) {
	codex := t.TempDir()
	_ = os.WriteFile(filepath.Join(codex, "models_cache.json"), []byte(`{"models":[{"slug":"gpt-6-luna","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"}]},{"slug":"gpt-6","supported_reasoning_levels":[{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"}]}]}`), 0o644)
	policy, _ := json.Marshal(harness.StarterPolicy)
	for _, sc := range scenes() {
		t.Run(sc.name, func(t *testing.T) {
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
			h := fx.handler()
			if !fx.options.Controls {
				testAPIError(t, request(h, "GET", "/api/v1/status", nil, true), http.StatusForbidden, "forbidden")
				return
			}
			page := get(t, h, "/")
			_, status := apiInto[board.APIStatus](t, h, "/api/v1/status")
			m := regexp.MustCompile(`<div class="status-line" data-agent="([^"]*)" data-hint="([^"]*)">`).FindStringSubmatch(page)
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
			if rows != strings.Count(page, `class="row check task-row"`) {
				t.Fatalf("%d task rows in HTML, %d in API", strings.Count(page, `class="row check task-row"`), rows)
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
			for _, g := range tasks.Groups {
				taskRows = append(taskRows, g.Tasks...)
				for _, row := range g.Tasks {
					if !strings.Contains(page, `data-state="`+row.State+`"`) || !strings.Contains(page, "/task/"+row.Ref) || !strings.Contains(page, html.EscapeString(row.Title)) {
						t.Fatalf("task %s is not in the page as the API says", row.Ref)
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

			for i, row := range taskRows {
				if i == 2 {
					break
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
				if got, want := strings.Count(detailPage, `class="row check"`), len(detail.Attempts); got != want {
					t.Fatalf("task %s has %d attempts in HTML, %d in API", row.Ref, got, want)
				}
			}

			for _, row := range taskRows {
				if row.Attempt == nil {
					continue
				}
				attemptPage := get(t, h, "/attempt/"+row.Attempt.Ref)
				_, attempt := apiInto[board.APIAttemptDetail](t, h, "/api/v1/attempt/"+row.Attempt.Ref)
				if attempt.Attempt.Ref != row.Attempt.Ref || attempt.Task.Ref != row.Ref || !strings.Contains(attemptPage, attempt.Attempt.Status) || !strings.Contains(attemptPage, html.EscapeString(attempt.Task.Title)) {
					t.Fatalf("attempt %s disagrees with its page", row.Attempt.Ref)
				}
				if got, want := strings.Count(attemptPage, `class="card report"`), len(attempt.Reports); got != want {
					t.Fatalf("attempt %s has %d reports in HTML, %d in API", row.Attempt.Ref, got, want)
				}
				break
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
				break
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
				break
			}
		})
	}
}
