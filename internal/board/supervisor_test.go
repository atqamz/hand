package board_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

const claudeSession = "0f8fad5b-d9cb-469f-a165-70867728950e"

type fixture struct {
	st       *state.Store
	srv      *fakeuhp.Server
	mu       sync.Mutex
	calls    [][]string
	fail     error
	status   string
	panes    map[string]string
	hint     string
	revision int
	screen   string
	claude   string
	options  board.Options
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fx := &fixture{st: open(t), status: "working", revision: 7, screen: "Trust this folder?\n❯ 1. Yes", claude: t.TempDir()}
	fx.srv = fakeuhp.Start(t, filepath.Join(t.TempDir(), "uhp.sock"))
	fx.srv.Handle("agent.explain", func(raw json.RawMessage) (any, error) {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		var p struct{ Pane string }
		_ = json.Unmarshal(raw, &p)
		status, ok := fx.panes[p.Pane]
		if !ok {
			status = fx.status
		}
		return map[string]any{"pane": p.Pane, "agent": "claude", "status": status, "state_evidence": map[string]any{"blocked_hint": fx.hint}}, nil
	})
	fx.srv.Handle("agent.read", func(json.RawMessage) (any, error) {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		return map[string]any{"text": fx.screen, "content_revision": fx.revision, "terminal_id": "t1"}, nil
	})
	fx.options = board.Options{
		Controls:   true,
		Control:    fx.control,
		Luvus:      luvus.Client{Socket: fx.srv.Socket},
		Transcript: &transcript.Reader{Paths: transcript.Paths{Claude: fx.claude}},
		Home:       t.TempDir(),
	}
	return fx
}

func (fx *fixture) control(_ context.Context, args ...string) error {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	fx.calls = append(fx.calls, args)
	return fx.fail
}

func (fx *fixture) called() [][]string {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return slices.Clone(fx.calls)
}

func (fx *fixture) handler() http.Handler { return board.New(fx.st, token, fx.options) }

func (fx *fixture) supervisor(t *testing.T, status, generation string) {
	t.Helper()
	ctx := context.Background()
	sup, err := fx.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}, Session: claudeSession})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: generation, TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	if status != state.AttemptRunning {
		if _, err := fx.st.EndSupervisor(ctx, sup.ID, status, "test"); err != nil {
			t.Fatal(err)
		}
	}
}

func get(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	rec := request(h, "GET", path, nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d:\n%s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func contains(t *testing.T, what, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Fatalf("%s missing %q:\n%s", what, w, body)
		}
	}
}

func lacks(t *testing.T, what, body string, nots ...string) {
	t.Helper()
	for _, n := range nots {
		if strings.Contains(body, n) {
			t.Fatalf("%s should not hold %q:\n%s", what, n, body)
		}
	}
}

func TestTheStartFormAndThePickPage(t *testing.T) {
	fresh := newFixture(t)
	contains(t, "fleet page before the first start", get(t, fresh.handler(), "/"), `action="/supervisor/send"`, `name="harness"`)
	ended := newFixture(t)
	ended.supervisor(t, state.AttemptInterrupted, "gen-1")
	page := get(t, ended.handler(), "/")
	contains(t, "ended", page, `action="/supervisor/resume"`, `href="/?pick=1#chat"`)
	lacks(t, "ended", page, `name="harness"`)
	contains(t, "picking a harness", get(t, ended.handler(), "/?pick=1"), `name="harness"`, `name="profile"`)
}

func TestEveryPanelState(t *testing.T) {
	forms := []string{`action="/supervisor/start"`, `action="/supervisor/resume"`, `action="/supervisor/stop"`, `action="/supervisor/interrupt"`, `action="/supervisor/keys"`}
	cases := []struct {
		name, status, agent string
		want                []string
	}{
		{"none", "", "", []string{`action="/supervisor/start"`}},
		{"interrupted", state.AttemptInterrupted, "", []string{`action="/supervisor/start"`, `action="/supervisor/resume"`}},
		{"blocked", state.AttemptRunning, "blocked", []string{`action="/supervisor/keys"`, `action="/supervisor/stop"`}},
		{"working", state.AttemptRunning, "working", []string{`action="/supervisor/interrupt"`, `action="/supervisor/stop"`}},
		{"done", state.AttemptRunning, "done", []string{`action="/supervisor/stop"`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t)
			fx.status, fx.hint = c.agent, "Trust this folder?"
			if c.status != "" {
				fx.supervisor(t, c.status, "gen-1")
			}
			body := get(t, fx.handler(), "/")
			for _, f := range forms {
				if slices.Contains(c.want, f) != strings.Contains(body, f) {
					t.Fatalf("%s: form %s present=%v:\n%s", c.name, f, strings.Contains(body, f), body)
				}
			}
			if c.agent == "blocked" {
				contains(t, "blocked", body, "Trust this folder?", `name="revision" value="7"`, `value="esc"`, `value="1"`)
			}
			if c.status == "" {
				contains(t, "start", body, `value="claude"`, `value="codex"`, `value="opencode"`)
			}
		})
	}
	fx := newFixture(t)
	ctx := context.Background()
	sup, err := fx.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "codex", Model: "gpt-6-luna", Effort: "low", Argv: []string{"/bin/codex", "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.EndSupervisor(ctx, sup.ID, state.AttemptFailed, "launch did not finish"); err != nil {
		t.Fatal(err)
	}
	body := get(t, fx.handler(), "/")
	lacks(t, "failed without a session", body, `action="/supervisor/resume"`)
	contains(t, "failed without a session", body, `action="/supervisor/start"`, "New session")
}

func TestAStaleGenerationShowsNoKeys(t *testing.T) {
	fx := newFixture(t)
	fx.status = "blocked"
	fx.supervisor(t, state.AttemptRunning, "gen-0")
	body := get(t, fx.handler(), "/")
	contains(t, "stale", body, "Luvus restarted")
	lacks(t, "stale", body, `action="/supervisor/keys"`, "Trust this folder?")
	if n := len(fx.srv.Calls("agent.read")) + len(fx.srv.Calls("agent.explain")); n != 0 {
		t.Fatalf("read the pane of an earlier server %d times", n)
	}
}

func post(h http.Handler, path string, form url.Values) *http.Response {
	if form == nil {
		form = url.Values{}
	}
	if !form.Has("csrf") {
		form.Set("csrf", token)
	}
	return request(h, "POST", path, form, true).Result()
}

func TestControlsCallTheCLI(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	h := fx.handler()
	cases := []struct {
		path string
		form url.Values
		want []string
		back string
	}{
		{"/supervisor/start", url.Values{"profile": {"default"}}, []string{"supervisor", "start", "--profile", "default"}, "/"},
		{"/supervisor/start", url.Values{"harness": {"opencode"}}, []string{"supervisor", "start", "--harness", "opencode"}, "/"},
		{"/supervisor/start", url.Values{"harness": {"claude"}, "model": {"sonnet"}, "effort": {"low"}}, []string{"supervisor", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low"}, "/"},
		{"/supervisor/start", nil, []string{"supervisor", "start"}, "/"},
		{"/supervisor/resume", nil, []string{"supervisor", "resume"}, "/"},
		{"/supervisor/stop", nil, []string{"supervisor", "stop"}, "/"},
		{"/supervisor/interrupt", nil, []string{"supervisor", "interrupt"}, "/"},
		{"/supervisor/switch", url.Values{"harness": {"codex"}, "model": {"gpt-6-luna"}, "effort": {"low"}}, []string{"supervisor", "switch", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low"}, "/"},
		{"/supervisor/switch", url.Values{"harness": {"opencode"}}, []string{"supervisor", "switch", "--harness", "opencode"}, "/"},
		{"/attempt/a1/keys", url.Values{"revision": {"7"}, "screen": {"0123456789abcdef0123456789abcdef"}, "key": {"2"}}, []string{"attempt", "keys", "--revision", "7", "--screen", "0123456789abcdef0123456789abcdef", "a1", "2"}, "/"},
		{"/supervisor/force", nil, []string{"supervisor", "force"}, "/"},
		{"/supervisor/keys", url.Values{"revision": {"7"}, "key": {"enter"}}, []string{"supervisor", "keys", "--revision", "7", "enter"}, "/"},
		{"/supervisor/keys", url.Values{"revision": {"7"}, "after": {"4"}, "key": {"enter"}}, []string{"supervisor", "keys", "--revision", "7", "--after", "4", "enter"}, "/"},
		{"/supervisor/keys", url.Values{"revision": {"7"}, "after": {"-1"}, "key": {"enter"}}, []string{"supervisor", "keys", "--revision", "7", "enter"}, "/"},
		{"/supervisor/keys", url.Values{"revision": {"7"}, "screen": {"0123456789abcdef0123456789abcdef"}, "key": {"enter"}}, []string{"supervisor", "keys", "--revision", "7", "--screen", "0123456789abcdef0123456789abcdef", "enter"}, "/"},
		{"/supervisor/keys", url.Values{"revision": {"7"}, "screen": {"not-a-digest"}, "key": {"enter"}}, []string{"supervisor", "keys", "--revision", "7", "enter"}, "/"},
		{"/supervisor/send", url.Values{"text": {"hi"}}, []string{"supervisor", "send", "--text", "hi"}, "/"},
		{"/supervisor/send", url.Values{"text": {"line one\r\nline two"}}, []string{"supervisor", "send", "--text", "line one\nline two"}, "/"},
		{"/supervisor/switch", url.Values{"profile": {"claude-deep"}}, []string{"supervisor", "switch", "--profile", "claude-deep"}, "/"},
		{"/supervisor/switch", url.Values{"model": {"opus"}, "effort": {"high"}}, []string{"supervisor", "switch", "--model", "opus", "--effort", "high"}, "/"},
		{"/supervisor/switch", url.Values{"effort": {"max"}}, []string{"supervisor", "switch", "--effort", "max"}, "/"},
		{"/supervisor/switch", url.Values{"cancel": {"1"}}, []string{"supervisor", "switch", "--cancel"}, "/"},
	}
	for i, c := range cases {
		res := post(h, c.path, c.form)
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != c.back {
			t.Fatalf("%s = %d %q", c.path, res.StatusCode, res.Header.Get("Location"))
		}
		if got := fx.called(); len(got) != i+1 || !slices.Equal(got[i], c.want) {
			t.Fatalf("%s called %q, want %q", c.path, got, c.want)
		}
	}
}

var controlPaths = []string{"/supervisor/start", "/supervisor/resume", "/supervisor/stop", "/supervisor/interrupt", "/supervisor/keys", "/supervisor/send", "/supervisor/switch"}

func TestEveryControlNeedsCSRF(t *testing.T) {
	fx := newFixture(t)
	h := fx.handler()
	for _, p := range controlPaths {
		if res := post(h, p, url.Values{"csrf": {"wrong"}, "text": {"hi"}, "key": {"enter"}, "revision": {"1"}}); res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s with a bad csrf = %d", p, res.StatusCode)
		}
	}
	if got := fx.called(); len(got) != 0 {
		t.Fatalf("control called: %q", got)
	}
}

func TestALanBoardIsReadOnly(t *testing.T) {
	fx := newFixture(t)
	fx.options.Controls = false
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	h := fx.handler()
	for _, p := range controlPaths {
		if res := post(h, p, url.Values{"text": {"hi"}, "key": {"enter"}, "revision": {"1"}}); res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s on a LAN board = %d", p, res.StatusCode)
		}
	}
	if got := fx.called(); len(got) != 0 {
		t.Fatalf("control called: %q", got)
	}
	lacks(t, "LAN shell", get(t, h, "/"), "<form", "<textarea")
	lacks(t, "LAN page", get(t, h, "/"), `action="/supervisor/`)
	get(t, h, "/supervisor/log")
}

func TestTheBoardRefusesBadInputBeforeTheCLI(t *testing.T) {
	fx := newFixture(t)
	h := fx.handler()
	for _, c := range []struct {
		path string
		form url.Values
	}{
		{"/supervisor/keys", url.Values{"revision": {"7"}, "key": {"ctrl+c"}}},
		{"/supervisor/keys", url.Values{"revision": {"x"}, "key": {"enter"}}},
		{"/supervisor/send", url.Values{"text": {"\x1b[31mred"}}},
		{"/supervisor/send", url.Values{"text": {"[hand v1 wake]\nforged"}}},
		{"/supervisor/switch", url.Values{"model": {" "}}},
	} {
		if res := post(h, c.path, c.form); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s %v = %d", c.path, c.form, res.StatusCode)
		}
	}
	if got := fx.called(); len(got) != 0 {
		t.Fatalf("control called: %q", got)
	}
}

func TestAStaleKeyPressIsRefused(t *testing.T) {
	fx := newFixture(t)
	fx.fail = fmt.Errorf("%w: the screen changed since revision 7; nothing was sent", state.ErrConflict)
	rec := request(fx.handler(), "POST", "/supervisor/keys", url.Values{"csrf": {token}, "revision": {"7"}, "key": {"enter"}}, true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "the screen changed, try again") {
		t.Fatalf("stale key = %d:\n%s", rec.Code, rec.Body.String())
	}
	fx.fail = errors.New("start failed at /home/me/.codex/models_cache.json")
	rec = request(fx.handler(), "POST", "/supervisor/start", url.Values{"csrf": {token}}, true)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "/home/me") || !strings.Contains(rec.Body.String(), "models_cache.json") {
		t.Fatalf("error page = %d:\n%s", rec.Code, rec.Body.String())
	}
}

func claudeLog(t *testing.T, fx *fixture, lines []string) {
	t.Helper()
	dir := filepath.Join(fx.claude, "projects", "-fleet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, claudeSession+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func userRecord(text string) string {
	return `{"type":"user","timestamp":"2026-09-28T01:00:00Z","message":{"role":"user","content":"` + text + `"}}`
}

func TestConversationShowsTheCalmViewAndTheQueue(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	h := fx.handler()
	contains(t, "no session file", get(t, h, "/supervisor/log"), "waiting for the session…")
	claudeLog(t, fx, []string{
		userRecord("hello <b>there</b>"),
		`{"type":"assistant","timestamp":"2026-09-28T01:00:01Z","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"Hi, operator."}]}}`,
	})
	ctx := context.Background()
	for _, body := range []string{"first queued", "second queued"} {
		if _, err := fx.st.AddSupervisorInput(ctx, body); err != nil {
			t.Fatal(err)
		}
	}
	body := get(t, h, "/supervisor/log")
	contains(t, "log", body, "hello &lt;b&gt;there&lt;/b&gt;", "Hi, operator.", "first queued", "second queued", ">queued<", `class="timeline"`)
	lacks(t, "log", body, "#end")
	if !(strings.Index(body, "second queued") < strings.Index(body, "first queued") && strings.Index(body, "first queued") < strings.Index(body, "Hi, operator.")) {
		t.Fatalf("entries must be newest first in the page, so the reversed column shows the newest at the bottom:\n%s", body)
	}
	fresh := newFixture(t)
	fresh.supervisor(t, state.AttemptRunning, "gen-1")
	var lines []string
	for i := 1; i <= 60; i++ {
		lines = append(lines, userRecord(fmt.Sprintf("m%02d", i)))
	}
	claudeLog(t, fresh, lines)
	page := get(t, fresh.handler(), "/supervisor/log")
	contains(t, "last page", page, "m11", "m60", `href="/supervisor/log?before=11`)
	lacks(t, "last page", page, "m10<")
	older := get(t, fresh.handler(), "/supervisor/log?before=11")
	contains(t, "older page", older, "m01", "m10")
	lacks(t, "older page", older, "m11")
}

func TestTheSwitchControlOffersOnlyTheRunningHarness(t *testing.T) {
	policy := `{"profiles":{"claude-deep":{"harness":"claude","model":"opus","effort":"high"},"luna":{"harness":"codex","model":"gpt-6-luna","effort":"low"}}}`
	fixture := func(t *testing.T, agent string) *fixture {
		fx := newFixture(t)
		if err := os.WriteFile(filepath.Join(fx.options.Home, "routing.json"), []byte(policy), 0o644); err != nil {
			t.Fatal(err)
		}
		fx.status = agent
		return fx
	}
	working := fixture(t, "working")
	working.supervisor(t, state.AttemptRunning, "gen-1")
	status := region(get(t, working.handler(), "/"), "console")
	contains(t, "working", status, `id="model-menu"`, `value="claude-deep"`, "Switch after this turn", `action="/supervisor/switch"`)
	lacks(t, "working", status, `value="luna"`, "Switch now")
	idle := fixture(t, "idle")
	idle.supervisor(t, state.AttemptRunning, "gen-1")
	contains(t, "idle", region(get(t, idle.handler(), "/"), "console"), "Switch now")
	blocked := fixture(t, "blocked")
	blocked.supervisor(t, state.AttemptRunning, "gen-1")
	contains(t, "blocked", region(get(t, blocked.handler(), "/"), "console"), "Switch after this turn")
	stopped := fixture(t, "idle")
	stopped.supervisor(t, state.AttemptStopped, "gen-1")
	lacks(t, "stopped", get(t, stopped.handler(), "/"), `id="model-menu"`)
	lan := fixture(t, "idle")
	lan.options.Controls = false
	lan.supervisor(t, state.AttemptRunning, "gen-1")
	lacks(t, "lan", get(t, lan.handler(), "/"), `id="model-menu"`)
	oc := fixture(t, "idle")
	ctx := context.Background()
	sup, err := oc.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "opencode", Argv: []string{"/bin/opencode"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oc.st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	ocPage := get(t, oc.handler(), "/")
	contains(t, "opencode", ocPage, `id="model-menu"`, "Switch to claude", "Switch to codex")
	lacks(t, "opencode", ocPage, "Switch now", "Switch after this turn")
	pending := fixture(t, "working")
	pending.supervisor(t, state.AttemptRunning, "gen-1")
	if _, err := pending.st.SetSupervisorSwitch(ctx, 1, "opus", "high"); err != nil {
		t.Fatal(err)
	}
	status = region(get(t, pending.handler(), "/"), "console")
	contains(t, "pending", status, "next turn: opus · high", `name="cancel" value="1"`)
	lacks(t, "pending", status, `id="model-menu"`)
}

func fetchPost(h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf", token)
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Hand-Fetch", "1")
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func receipt(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if got, _ := url.PathUnescape(rec.Header().Get("X-Hand-Receipt")); rec.Code != http.StatusNoContent || got != want {
		t.Fatalf("status %d, receipt %q, want 204 and %q:\n%s", rec.Code, got, want, rec.Body.String())
	}
}

func TestActionsReturnReceipts(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	task := active(t, fx.st, "Fix login")
	if _, err := fx.st.Ask(ctx, task.ID, "Keep it?"); err != nil {
		t.Fatal(err)
	}
	receipt(t, fetchPost(fx.handler(), "/decision/d1/answer", url.Values{"answer": {"yes"}}), "d1 answered · no supervisor is running; it waits")
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	if _, err := fx.st.Ask(ctx, task.ID, "Ship it?"); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	receipt(t, fetchPost(h, "/decision/d2/answer", url.Values{"answer": {"no"}}), "d2 answered · s1 reads it now")
	a := workerAttempt(t, fx.st, task.ID)
	r, err := fx.st.AddReport(ctx, a.ID, state.ReportDone, "Fixed")
	if err != nil {
		t.Fatal(err)
	}
	receipt(t, fetchPost(h, "/report/"+state.ReportRef(r.ID)+"/ack", nil), "r1 marked read")
	receipt(t, fetchPost(h, "/supervisor/keys", url.Values{"revision": {"7"}, "key": {"2"}}), "Sent 2 to s1")
	receipt(t, fetchPost(h, "/attempt/a1/keys", url.Values{"revision": {"7"}, "key": {"esc"}}), "Sent esc to a1")
	receipt(t, fetchPost(h, "/supervisor/stop", nil), "s1 stopping")
	receipt(t, fetchPost(h, "/supervisor/interrupt", nil), "Interrupt sent to s1")
	if res := post(h, "/supervisor/stop", nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("a form post without JavaScript = %d, want a redirect", res.StatusCode)
	}
}

func TestKeysAndLifecycleShowInChat(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	if err := fx.st.NoteSupervisor(ctx, 1, "keys", "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.EndSupervisor(ctx, 1, state.AttemptStopped, "stopped by operator"); err != nil {
		t.Fatal(err)
	}
	body := get(t, fx.handler(), "/supervisor/log")
	contains(t, "log", body, "s1 started · claude sonnet low", "You pressed 2 on s1&#39;s screen", "s1 stopped by operator")
	if !(strings.Index(body, "s1 stopped") < strings.Index(body, "You pressed 2") && strings.Index(body, "You pressed 2") < strings.Index(body, "s1 started")) {
		t.Fatalf("lifecycle lines out of order (newest first):\n%s", body)
	}
}

func TestDeliveredMessagesShowTheirTime(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	in, err := fx.st.AddSupervisorInput(ctx, "ship it")
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.st.DeliverSupervisorInput(ctx, in.ID, false); err != nil {
		t.Fatal(err)
	}
	claudeLog(t, fx, []string{userRecord("ship it")})
	contains(t, "log", get(t, fx.handler(), "/supervisor/log"), "delivered <time")
}

func TestChatKeepsAnEarlierSession(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	if _, err := fx.st.EndSupervisor(ctx, 1, state.AttemptStopped, "stopped by operator"); err != nil {
		t.Fatal(err)
	}
	claudeLog(t, fx, []string{
		`{"type":"user","timestamp":"2026-09-25T01:00:00Z","message":{"role":"user","content":"old question"}}`,
		`{"type":"assistant","timestamp":"2026-09-25T01:00:01Z","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"old answer"}]}}`,
	})
	const second = "1b4e28ba-2fa1-11d2-883f-0016d3cca427"
	sup, err := fx.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "haiku", Effort: "low", Argv: []string{"/bin/claude", "x"}, Session: second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(fx.claude, "projects", "-fleet")
	if err := os.WriteFile(filepath.Join(dir, second+".jsonl"), []byte(`{"type":"user","timestamp":"2026-09-27T01:00:00Z","message":{"role":"user","content":"new question"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := get(t, fx.handler(), "/supervisor/log")
	contains(t, "log", body, "old question", "old answer", "s2 started · claude haiku low", "new question")
	if !(strings.Index(body, "new question") < strings.Index(body, "s2 started") && strings.Index(body, "s2 started") < strings.Index(body, "old answer")) {
		t.Fatalf("sessions out of order (newest first):\n%s", body)
	}
	older := get(t, fx.handler(), "/supervisor/log?before=2")
	contains(t, "older page", older, "old question", "old answer")
	lacks(t, "older page", older, "new question")
}

func TestRefChipsCarryTheirTitle(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	claudeLog(t, fx, []string{userRecord(`[hand v1 wake]\nattempt.quiet a1: turn ended`), reply("t1 is done")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "timeline", tl, `href="/ref/t1" title="Fix login"`, `href="/ref/a1" title="on t1 &#34;Fix login&#34; · claude sonnet"`)
}

func TestModelMenuListsModels(t *testing.T) {
	fx := newFixture(t)
	fx.options.Harness.CodexHome = t.TempDir()
	if err := os.WriteFile(filepath.Join(fx.options.Harness.CodexHome, "models_cache.json"), []byte(`{"models":[{"slug":"gpt-6-luna","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.status = "idle"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	console := region(get(t, fx.handler(), "/"), "console")
	contains(t, "console", console, `<option value="opus">opus</option>`, `<option value="xhigh">xhigh</option>`, `name="harness" value="codex"`, `<option value="gpt-6-luna">gpt-6-luna</option>`, `name="harness" value="opencode"`, "Switch to codex", "Switch to opencode")
}

func TestAnInterruptedSupervisorOffersResumeInTheComposer(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptInterrupted, "gen-1")
	page := get(t, fx.handler(), "/")
	contains(t, "console", region(page, "console"), `action="/supervisor/resume"`)
	contains(t, "composer hint", page, "s1 stopped: ", "Resume continues its session")
	contains(t, "needs", region(page, "queue"), `data-kind="resume"`)
}

func TestHeadersSurviveNonASCIIText(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.st.Ask(context.Background(), active(t, fx.st, "Fix login").ID, "Keep it?"); err != nil {
		t.Fatal(err)
	}
	rec := fetchPost(fx.handler(), "/decision/d1/answer", url.Values{"answer": {"ya · lanjut"}})
	raw := rec.Header().Get("X-Hand-Receipt")
	for i := range len(raw) {
		if raw[i] >= 0x80 {
			t.Fatalf("receipt header %q is not ASCII, so a browser reads it as Latin-1", raw)
		}
	}
	if got, err := url.PathUnescape(raw); err != nil || got != "d1 answered · no supervisor is running; it waits" {
		t.Fatalf("receipt decodes to %q, %v", got, err)
	}
	if !strings.Contains(asset(t, "app.js"), "decodeURIComponent") {
		t.Fatal("app.js does not decode the headers")
	}
}
