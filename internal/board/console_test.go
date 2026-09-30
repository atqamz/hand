package board_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func usageRecord(cacheRead int) string {
	return `{"type":"assistant","timestamp":"2026-09-28T01:00:05Z","message":{"model":"claude-opus-5-5","stop_reason":"end_turn","usage":{"input_tokens":2,"cache_creation_input_tokens":170,"cache_read_input_tokens":` + strconv.Itoa(cacheRead) + `},"content":[{"type":"text","text":"Done."}]}}`
}

func TestTheLiveStripNamesTheLine(t *testing.T) {
	cases := []struct {
		name, status, agent, gen, lamp, line string
		launching                            bool
	}{
		{name: "working", status: state.AttemptRunning, agent: "working", gen: "gen-1", lamp: "working", line: "WORKING"},
		{name: "ready", status: state.AttemptRunning, agent: "idle", gen: "gen-1", lamp: "ready", line: "READY"},
		{name: "blocked", status: state.AttemptRunning, agent: "blocked", gen: "gen-1", lamp: "failing", line: "BLOCKED"},
		{name: "unreachable", status: state.AttemptRunning, agent: "working", gen: "gen-0", lamp: "failing", line: "UNREACHABLE"},
		{name: "stopped", status: state.AttemptStopped, gen: "gen-1", lamp: "neutral", line: "STOPPED"},
		{name: "interrupted", status: state.AttemptInterrupted, gen: "gen-1", lamp: "failing", line: "INTERRUPTED"},
		{name: "starting", launching: true, lamp: "neutral", line: "STARTING"},
		{name: "none", lamp: "neutral", line: "NO SUPERVISOR"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t)
			fx.status = c.agent
			if c.status != "" {
				fx.supervisor(t, c.status, c.gen)
			}
			if c.launching {
				if _, err := fx.st.AddSupervisor(context.Background(), state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude"}}); err != nil {
					t.Fatal(err)
				}
			}
			strip := region(get(t, fx.handler(), "/"), "status")
			contains(t, c.name, strip, `<span class="lamp" data-state="`+c.lamp+`"></span><span class="line">`+c.line+`</span>`)
			if c.status == state.AttemptRunning {
				contains(t, c.name, strip, "S1 SONNET · LOW")
			}
		})
	}
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("hi"), usageRecord(508139)})
	contains(t, "gauge", region(get(t, fx.handler(), "/"), "status"), `<span class="gauge" data-level="">CTX 508K</span>`)
}

func TestTheConsoleCarriesTheControls(t *testing.T) {
	working := newFixture(t)
	working.supervisor(t, state.AttemptRunning, "gen-1")
	console := region(get(t, working.handler(), "/"), "console")
	contains(t, "working", console, `id="model-menu"`, `action="/supervisor/switch"`, `action="/supervisor/interrupt"`, `id="more-menu"`, `action="/supervisor/stop"`, "SONNET · LOW")
	idle := newFixture(t)
	idle.status = "idle"
	idle.supervisor(t, state.AttemptRunning, "gen-1")
	console = region(get(t, idle.handler(), "/"), "console")
	lacks(t, "idle", console, `action="/supervisor/interrupt"`)
	contains(t, "idle", console, `action="/supervisor/stop"`, "Switch now")
	stopped := newFixture(t)
	stopped.supervisor(t, state.AttemptStopped, "gen-1")
	contains(t, "stopped", region(get(t, stopped.handler(), "/"), "console"), `action="/supervisor/resume"`, `action="/supervisor/start"`, `href="/?pick=1#chat"`)
	interrupted := newFixture(t)
	interrupted.supervisor(t, state.AttemptInterrupted, "gen-1")
	body := get(t, interrupted.handler(), "/")
	contains(t, "interrupted console", region(body, "console"), `action="/supervisor/resume"`)
	contains(t, "interrupted needs", region(body, "queue"), `action="/supervisor/resume"`)
	none := newFixture(t)
	contains(t, "none", region(get(t, none.handler(), "/"), "console"), `name="harness"`, `name="profile"`)
	pending := newFixture(t)
	pending.supervisor(t, state.AttemptRunning, "gen-1")
	if _, err := pending.st.SetSupervisorSwitch(context.Background(), 1, "opus", "high"); err != nil {
		t.Fatal(err)
	}
	console = region(get(t, pending.handler(), "/"), "console")
	contains(t, "pending", console, "→ OPUS · HIGH", `name="cancel" value="1"`)
	lacks(t, "pending", console, `id="model-menu"`)
	lan := newFixture(t)
	lan.options.Controls = false
	lan.supervisor(t, state.AttemptRunning, "gen-1")
	lacks(t, "lan", get(t, lan.handler(), "/"), `data-region="console"`)
}

func TestConsoleMenusKeepIdsAndFields(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	console := region(get(t, fx.handler(), "/"), "console")
	for _, want := range []string{`<details class="menu" id="model-menu">`, `<details class="menu" id="more-menu">`, `name="model"`, `name="effort"`} {
		if strings.Count(console, want) != 1 {
			t.Fatalf("console holds %q %d times:\n%s", want, strings.Count(console, want), console)
		}
	}
}

func TestTheConsoleWorksWithoutJS(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	console := region(get(t, fx.handler(), "/"), "console")
	lacks(t, "console", console, `type="button"`)
	if forms, buttons := strings.Count(console, `<form `), strings.Count(console, "<button"); forms == 0 || buttons != forms {
		t.Fatalf("console has %d forms and %d buttons:\n%s", forms, buttons, console)
	}
	for _, f := range strings.Split(console, "<form ")[1:] {
		if !strings.HasPrefix(f, `method="post"`) {
			t.Fatalf("a console form is not a POST: %.80s", f)
		}
	}
}

func TestEventsFollowTheContext(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("hi"), usageRecord(1000)})
	base, _ := serve(t, quick(fx.st, fx.options))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	f, err := os.OpenFile(filepath.Join(fx.claude, "projects", "-fleet", claudeSession+".jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(usageRecord(508139) + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	for _, e := range collect(ch, 2*time.Second) {
		if e.name == "status" && strings.Contains(e.data, "CTX 508K") {
			return
		}
	}
	t.Fatal("a context change sent no status event")
}

func TestTheStripNamesWhyTheSupervisorEnded(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptInterrupted, "gen-1")
	contains(t, "ended", region(get(t, fx.handler(), "/"), "status"), `<p class="strip-note">test</p>`)
	oc := newFixture(t)
	ctx := context.Background()
	sup, err := oc.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "opencode", Argv: []string{"/bin/opencode"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oc.st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	contains(t, "opencode", get(t, oc.handler(), "/"), `<span class="who">S1 OPENCODE</span>`, `<span class="model">OPENCODE</span>`)
}
