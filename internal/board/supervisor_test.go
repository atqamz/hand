package board_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	fx.srv.Handle("agent.explain", func(json.RawMessage) (any, error) {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		return map[string]any{"pane": "2", "agent": "claude", "status": fx.status, "state_evidence": map[string]any{"blocked_hint": fx.hint}}, nil
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
	contains(t, "ended", page, `action="/supervisor/resume"`, `href="/?pick=1"`)
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
		{"done", state.AttemptRunning, "done", []string{`action="/supervisor/interrupt"`, `action="/supervisor/stop"`}},
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
				contains(t, "blocked", body, "Trust this folder?", `name="revision" value="7"`, `value="enter"`, `value="esc"`, `value="up"`, `value="down"`, `value="1"`, `value="2"`, `value="3"`)
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
	contains(t, "stale", body, "luvus restarted")
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
		{"/supervisor/start", url.Values{"profile": {"default"}}, []string{"start", "--profile", "default"}, "/"},
		{"/supervisor/start", url.Values{"harness": {"opencode"}}, []string{"start", "--harness", "opencode"}, "/"},
		{"/supervisor/start", url.Values{"harness": {"claude"}, "model": {"sonnet"}, "effort": {"low"}}, []string{"start", "--harness", "claude", "--model", "sonnet", "--effort", "low"}, "/"},
		{"/supervisor/start", nil, []string{"start"}, "/"},
		{"/supervisor/resume", nil, []string{"resume"}, "/"},
		{"/supervisor/stop", nil, []string{"stop"}, "/"},
		{"/supervisor/interrupt", nil, []string{"interrupt"}, "/"},
		{"/supervisor/keys", url.Values{"revision": {"7"}, "key": {"enter"}}, []string{"keys", "--revision", "7", "enter"}, "/"},
		{"/supervisor/send", url.Values{"text": {"hi"}}, []string{"send", "--text", "hi"}, "/"},
		{"/supervisor/send", url.Values{"text": {"line one\r\nline two"}}, []string{"send", "--text", "line one\nline two"}, "/"},
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

var controlPaths = []string{"/supervisor/start", "/supervisor/resume", "/supervisor/stop", "/supervisor/interrupt", "/supervisor/keys", "/supervisor/send"}

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
	contains(t, "log", body, "hello &lt;b&gt;there&lt;/b&gt;", "Hi, operator.", "first queued", "second queued", ">Queued<", `class="timeline"`)
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
	contains(t, "last page", page, "m11", "m60", `href="/supervisor/log?before=10`)
	lacks(t, "last page", page, "m10<")
	older := get(t, fresh.handler(), "/supervisor/log?before=10")
	contains(t, "older page", older, "m01", "m10")
	lacks(t, "older page", older, "m11")
}
