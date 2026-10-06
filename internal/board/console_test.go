package board_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
)

func TestMain(m *testing.M) {
	fakebin.Main(map[string]func([]string) int{"agy": func(args []string) int {
		if len(args) > 0 && args[0] == "models" {
			fmt.Print(fakebin.Params()["models"])
		}
		return 0
	}})
	os.Exit(m.Run())
}

const agyModels = "gemini-3.8-flash-low\tGemini 3.8 Flash (Low)\n"

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
			contains(t, c.name, strip, `<span class="pill" data-state="`+c.lamp+`"`, `<span class="word">`+strings.ToLower(c.line)+`</span>`)
			if c.status == state.AttemptRunning {
				contains(t, c.name, strip, `<span class="who">s1 · sonnet low</span>`)
			}
		})
	}
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("hi"), usageRecord(508139)})
	contains(t, "gauge", region(get(t, fx.handler(), "/"), "status"), `<span class="ctx live" data-level=""`, `<span class="n">508K / 1M</span>`)
}

func TestTheConsoleCarriesTheControls(t *testing.T) {
	working := newFixture(t)
	working.supervisor(t, state.AttemptRunning, "gen-1")
	console := region(get(t, working.handler(), "/"), "console")
	contains(t, "working", console, `id="model-menu"`, `action="/supervisor/switch"`, `action="/supervisor/interrupt"`, `id="more-menu"`, `action="/supervisor/stop"`, "sonnet · low")
	idle := newFixture(t)
	idle.status = "idle"
	idle.supervisor(t, state.AttemptRunning, "gen-1")
	console = region(get(t, idle.handler(), "/"), "console")
	lacks(t, "idle", console, `action="/supervisor/interrupt"`)
	contains(t, "idle", console, `action="/supervisor/stop"`, "Switch now")
	stopped := newFixture(t)
	stopped.supervisor(t, state.AttemptStopped, "gen-1")
	contains(t, "stopped", region(get(t, stopped.handler(), "/"), "console"), `action="/supervisor/resume"`, `action="/supervisor/start"`, `href="/?pick=1#chat"`, `<a href="/">Fleets</a>`)
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
	contains(t, "pending", console, "next turn: opus · high", `name="cancel" value="1"`, "<button>Cancel switch</button>")
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
	console := regexp.MustCompile(`<button type="button" id="notify" hidden>[^<]*</button>`).ReplaceAllString(region(get(t, fx.handler(), "/"), "console"), "")
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
		if e.name == "status" && strings.Contains(e.data, "508K / 1M") {
			return
		}
	}
	t.Fatal("a context change sent no status event")
}

func TestTheStripNamesWhyTheSupervisorEnded(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptInterrupted, "gen-1")
	contains(t, "ended", region(get(t, fx.handler(), "/"), "status"), `<span class="pill" data-state="failing" title="test">`)
	oc := newFixture(t)
	ctx := context.Background()
	sup, err := oc.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "opencode", Argv: []string{"/bin/opencode"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oc.st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	contains(t, "opencode", get(t, oc.handler(), "/"), `<span class="who">s1 · opencode</span>`, `<span class="model">opencode</span>`)
}

func TestTheMastheadNamesStateAndContext(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("hi"), usageRecord(508139)})
	body := get(t, fx.handler(), "/")
	strip := region(body, "status")
	contains(t, "masthead", strip, `class="pill" data-state="working"`, `<span class="word">working</span>`, `<svg class="icon"`, `<span class="who">s1 · sonnet low</span>`, `class="ctx live"`, `<meter`, `508K / 1M`)
	lacks(t, "masthead", body, "data-clock-now", "CTX ")
}

func TestNotifyLivesInTheMenu(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	body := get(t, fx.handler(), "/")
	menu := body[strings.Index(body, `id="more-menu"`):]
	menu = menu[:strings.Index(menu, "</details>")]
	contains(t, "more menu", menu, `id="notify"`, `<a href="/">Fleets</a>`)
	if strings.Count(body, `id="notify"`) != 1 {
		t.Fatal("the page holds more than one notify button")
	}
}

func TestThePhoneRowNamesTheWorstItem(t *testing.T) {
	fx := newFixture(t)
	fx.status, fx.hint = "blocked", "Trust this folder?"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	contains(t, "phone row", region(get(t, fx.handler(), "/"), "status"), `class="needs-count" href="#needs" data-worst="blocked"`, `<span class="n">1</span> blocked`)
	calm := newFixture(t)
	calm.supervisor(t, state.AttemptRunning, "gen-1")
	lacks(t, "calm", region(get(t, calm.handler(), "/"), "status"), `class="needs-count"`)
}

func TestTheComposerFootRow(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	console := region(get(t, fx.handler(), "/"), "console")
	model, interrupt, stop := strings.Index(console, `id="model-menu"`), strings.Index(console, `<button class="key fail"`), strings.Index(console, `<button class="key stop">Stop</button>`)
	if model < 0 || interrupt < model || stop < interrupt {
		t.Fatalf("foot row order model %d, interrupt %d, stop %d:\n%s", model, interrupt, stop, console)
	}
	contains(t, "interrupt", console, `<span class="key-label">Interrupt</span>`)
	more := console[strings.Index(console, `id="more-menu"`):]
	lacks(t, "more menu", more[:strings.Index(more, "</details>")], "supervisor/stop")
	contains(t, "css", asset(t, "board.css"), `grid-template-areas:"text text text" "console hint send"`, "#more-menu:not(:has(.menu-body>:not([hidden])))")
}

func TestStopArmsWithJS(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	contains(t, "stop form", region(get(t, fx.handler(), "/"), "console"), `action="/supervisor/stop" data-fetch data-confirm="Press again to stop"`)
	contains(t, "app.js", asset(t, "app.js"), "form.dataset.confirm", "4000")
}

func TestProfilesShowWhatTheyResolveTo(t *testing.T) {
	fx := newFixture(t)
	policy := `{"profiles":{"deep":{"harness":"claude","model":"opus","effort":"xhigh"}}}`
	if err := os.WriteFile(filepath.Join(fx.options.Home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	contains(t, "start", region(get(t, fx.handler(), "/"), "console"), `<option value="deep">deep · claude opus xhigh</option>`)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	contains(t, "switch", region(get(t, fx.handler(), "/"), "console"), `<option value="deep">deep · claude opus xhigh</option>`)
}

func TestStartIsPrimaryAndFetches(t *testing.T) {
	fx := newFixture(t)
	contains(t, "start", region(get(t, fx.handler(), "/"), "console"), `<form method="post" class="start" action="/supervisor/start" data-fetch data-starting="starting…">`, `<button class="primary">Start</button>`)
	contains(t, "app.js", asset(t, "app.js"), "form.dataset.starting")
}

func TestTheSupervisorMenusOfferAgy(t *testing.T) {
	bin := t.TempDir()
	fakebin.Install(t, bin, "agy", "agy", map[string]string{"models": agyModels})
	policy := `{"profiles":{"gem":{"harness":"agy","model":"gemini-3.8-flash-low"}}}`
	none := newFixture(t)
	running := newFixture(t)
	for _, fx := range []*fixture{none, running} {
		fx.options.Harness.Path = bin
		if err := os.WriteFile(filepath.Join(fx.options.Home, "routing.json"), []byte(policy), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	contains(t, "start", region(get(t, none.handler(), "/"), "console"), `<button>Start agy</button>`, `<option value="gemini-3.8-flash-low">gemini-3.8-flash-low</option>`, `<option value="gem">`)
	running.status = "idle"
	running.supervisor(t, state.AttemptRunning, "gen-1")
	console := region(get(t, running.handler(), "/"), "console")
	contains(t, "other harness", console, `<input type="hidden" name="harness" value="agy">`, `<option value="gemini-3.8-flash-low">gemini-3.8-flash-low</option>`, `<button>Switch to agy</button>`)
	lacks(t, "other harness", console, `aria-label="agy effort"`)
}

func TestTheAgyModelMenuHasNoEffortSelect(t *testing.T) {
	bin := t.TempDir()
	fakebin.Install(t, bin, "agy", "agy", map[string]string{"models": agyModels})
	fx := newFixture(t)
	fx.options.Harness.Path = bin
	fx.status = "idle"
	sup, err := fx.st.AddSupervisor(context.Background(), state.SupervisorSpec{Harness: "agy", Model: "gemini-3.8-flash-low", Argv: []string{"/bin/agy", "-i", "x"}, Session: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.SupervisorRunning(context.Background(), sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	console := region(get(t, fx.handler(), "/"), "console")
	contains(t, "agy model menu", console, `<option value="gemini-3.8-flash-low">gemini-3.8-flash-low</option>`)
	lacks(t, "agy model menu", console, `<select name="effort"><option value="">keep`)
}

func TestStopKeepsItsArmAcrossARedraw(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js, "const arms = new Map()", "const until = Date.now() + armFor", "arms.set(action, until)", "if (arms.get(action) === until) disarm(action)", `if ("armed" in form.dataset && (arms.get(action) ?? 0) <= Date.now()) disarm(action);`, `for (const f of el.querySelectorAll("form[data-confirm]"))`, "(arms.get(f.getAttribute(\"action\")) ?? 0) > Date.now()", "disarm(action)")
	lacks(t, "app.js", js, "if (!(\"armed\" in form.dataset)) return;")
}

func startRow(t *testing.T, console, name string) string {
	t.Helper()
	marker := `<input type="hidden" name="harness" value="` + name + `">`
	i := strings.Index(console, marker)
	if i < 0 {
		t.Fatalf("no start row for %s:\n%s", name, console)
	}
	end := strings.Index(console[i:], "</form>")
	return console[i : i+end]
}

func TestStartFormListsClaudeModelsAndEfforts(t *testing.T) {
	fx := newFixture(t)
	console := region(get(t, fx.handler(), "/"), "console")
	row := startRow(t, console, "claude")
	contains(t, "claude row", row, `<option value="opus">opus</option>`, `<option value="xhigh">xhigh</option>`, `<button>Start claude</button>`)
	lacks(t, "claude row", row, `<input name="model"`)
	contains(t, "profile first", console, `name="profile"`)
	if strings.Index(console, `name="profile"`) > strings.Index(console, `name="harness"`) {
		t.Fatal("profile select does not come first")
	}
}

func TestStartFormCodexRowsComeFromTheModelsCache(t *testing.T) {
	fx := newFixture(t)
	fx.options.Harness.CodexHome = t.TempDir()
	if err := os.WriteFile(filepath.Join(fx.options.Harness.CodexHome, "models_cache.json"), []byte(`{"models":[{"slug":"gpt-6-luna","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	row := startRow(t, region(get(t, fx.handler(), "/"), "console"), "codex")
	contains(t, "codex row", row, `<option value="gpt-6-luna">gpt-6-luna</option>`, `<option value="medium">medium</option>`)
}

func TestStartFormFallsBackToTextBoxesWithoutModels(t *testing.T) {
	fx := newFixture(t)
	row := startRow(t, region(get(t, fx.handler(), "/"), "console"), "opencode")
	contains(t, "opencode row", row, `<input name="model"`, `<input name="effort"`)
	lacks(t, "opencode row", row, "<select")
}

func TestStartRowPostsHarnessModelAndEffort(t *testing.T) {
	fx := newFixture(t)
	row := startRow(t, region(get(t, fx.handler(), "/"), "console"), "claude")
	contains(t, "row", row, `<select name="model"`, `<select name="effort"`)
	post(fx.handler(), "/supervisor/start", url.Values{"harness": {"claude"}, "model": {"opus"}, "effort": {"xhigh"}})
	if got := strings.Join(fx.called()[0], " "); got != "supervisor start --harness claude --model opus --effort xhigh" {
		t.Fatalf("ran %q", got)
	}
}

func TestStartRowDefaultsPostNoModelOrEffort(t *testing.T) {
	fx := newFixture(t)
	row := startRow(t, region(get(t, fx.handler(), "/"), "console"), "claude")
	contains(t, "row", row, `<select name="model" aria-label="claude model"><option value="">default</option>`, `<select name="effort" aria-label="claude effort"><option value="">default</option>`)
	post(fx.handler(), "/supervisor/start", url.Values{"harness": {"claude"}, "model": {""}, "effort": {""}})
	if got := strings.Join(fx.called()[0], " "); got != "supervisor start --harness claude" {
		t.Fatalf("ran %q", got)
	}
}

func TestTheStartStripHasItsOwnRow(t *testing.T) {
	fresh := newFixture(t)
	policy := `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"},"deep":{"harness":"claude","model":"opus","effort":"xhigh"}}}`
	if err := os.WriteFile(filepath.Join(fresh.options.Home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	console := region(get(t, fresh.handler(), "/"), "console")
	contains(t, "first run", console, `<div class="starter">`, `<select name="profile" aria-label="Profile" required>`, `<option value="default" selected>`, `<details class="others" id="start-others">`)
	lacks(t, "first run", console, "or pick a harness")
	others := strings.Index(console, `id="start-others"`)
	for _, name := range []string{"claude", "codex", "opencode", "agy"} {
		if i := strings.Index(console, `name="harness" value="`+name+`"`); i < others {
			t.Errorf("the %s row is outside the other-harness details", name)
		}
	}
	contains(t, "no policy", region(get(t, newFixture(t).handler(), "/"), "console"), `<details class="others" id="start-others" open>`)
	ended := newFixture(t)
	ended.supervisor(t, state.AttemptInterrupted, "gen-1")
	picked := region(get(t, ended.handler(), "/?pick=1"), "console")
	contains(t, "after a supervisor", picked, `>same as s1 · sonnet low</option>`, `<button>Start new</button>`, `<button class="primary">Resume</button>`)
	lacks(t, "after a supervisor", picked, " required>", " selected>")
	contains(t, "board.css", asset(t, "board.css"), ".console:has(.starter){flex-wrap:wrap", ".starter{flex:1 1 100%", `.console-box:has(.console .starter,.console form[action$="/supervisor/resume"]) .send .primary{background:var(--card)`)
}

func TestTheFirstStartPostsAProfileInsteadOfNothing(t *testing.T) {
	fx := newFixture(t)
	policy := `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"},"deep":{"harness":"claude","model":"opus","effort":"xhigh"}}}`
	if err := os.WriteFile(filepath.Join(fx.options.Home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	picked := regexp.MustCompile(`<option value="([^"]+)" selected>`).FindStringSubmatch(region(get(t, fx.handler(), "/"), "console"))
	if picked == nil {
		t.Fatal("the profile select preselects nothing")
	}
	post(fx.handler(), "/supervisor/start", url.Values{"profile": {picked[1]}})
	if got := strings.Join(fx.called()[0], " "); got != "supervisor start --profile default" {
		t.Fatalf("ran %q", got)
	}
	noDefault := newFixture(t)
	if err := os.WriteFile(filepath.Join(noDefault.options.Home, "routing.json"), []byte(`{"profiles":{"deep":{"harness":"claude","model":"opus","effort":"xhigh"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	console := region(get(t, noDefault.handler(), "/"), "console")
	contains(t, "no default profile", console, `<select name="profile" aria-label="Profile" required><option value="">profile…</option>`)
	lacks(t, "no default profile", console, " selected>")
}
