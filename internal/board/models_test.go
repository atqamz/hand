package board_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
)

const (
	claudeEfforts = "low medium high xhigh max"
	codexBundled  = `{"models":[{"slug":"gpt-6-luna","display_name":"GPT-6 Luna","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"}]},{"slug":"gpt-6-sol","display_name":"GPT-6 Sol","supported_reasoning_levels":[{"effort":"low"},{"effort":"ultra"}]},{"slug":"gpt-6-mini","display_name":"GPT-6 Mini"}]}`
)

func claudeReply(models ...map[string]any) string {
	b, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": "hand-models", "response": map[string]any{"models": models}}})
	return string(b) + "\n"
}

func claudeModel(value, label string, efforts ...string) map[string]any {
	return map[string]any{"value": value, "displayName": label, "supportsEffort": len(efforts) > 0, "supportedEffortLevels": efforts}
}

var claudeFixture = claudeReply(
	claudeModel("opus", "Opus 5.5", strings.Fields(claudeEfforts)...),
	claudeModel("haiku", "Haiku 5.5"),
	claudeModel("my-gw/claude-x", "Gateway X", "low", "high"),
	claudeModel("acme-opus", "Acme Opus", strings.Fields(claudeEfforts)...),
)

type modelRig struct {
	fx     *fixture
	bin    string
	claude string
	home   string
	env    harness.Env
}

func newModelRig(t *testing.T) *modelRig {
	t.Helper()
	r := &modelRig{fx: newFixture(t), bin: t.TempDir(), home: t.TempDir()}
	vars := map[string]string{"PATH": r.bin, "HOME": t.TempDir(), "SECONDHAND_HOME": r.home, "CODEX_HOME": t.TempDir()}
	r.env = harness.EnvOf(func(k string) string { return vars[k] })
	r.fx.options.Harness = r.env
	r.fx.options.Models = harness.Options{Environ: []string{"PATH=" + r.bin}}
	return r
}

func (r *modelRig) install(t *testing.T, name, behavior string, params map[string]string) string {
	t.Helper()
	return fakebin.Install(t, r.bin, name, behavior, params)
}

func (r *modelRig) refresh(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if l, err := harness.Refresh(context.Background(), name, r.env, harness.Options{Environ: []string{"PATH=" + r.bin}}); err != nil || l.Failed != nil {
			t.Fatalf("listing %s: %+v, %v", name, l, err)
		}
	}
}

func (r *modelRig) all(t *testing.T) {
	t.Helper()
	r.claude = r.install(t, "claude", "printer", map[string]string{"out": claudeFixture})
	r.install(t, "codex", "printer", map[string]string{"out": codexBundled})
	r.install(t, "agy", "agy", map[string]string{"models": agyModels})
	r.install(t, "opencode", "agy", map[string]string{"models": "acme/acme-coder-1\nacme/acme-coder-2\n"})
	r.refresh(t, "claude", "codex", "agy", "opencode")
}

func (r *modelRig) cache(t *testing.T, name string, edit func(map[string]any)) {
	t.Helper()
	path := filepath.Join(r.home, "models", name+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	if b, err = json.Marshal(m); err == nil {
		err = os.WriteFile(path, b, 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (r *modelRig) console(t *testing.T) string {
	t.Helper()
	return region(get(t, r.fx.handler(), "/"), "console")
}

func TestTheStartFormGroupsClaudeModelsBySource(t *testing.T) {
	r := newModelRig(t)
	r.all(t)
	row := startRow(t, r.console(t), "claude")
	want := []string{
		`<optgroup label="built-in">`,
		`<option value="opus" data-efforts="low medium high xhigh max">Opus 5.5 · opus</option>`,
		`<option value="haiku" data-efforts="">Haiku 5.5 · haiku</option>`,
		`<optgroup label="custom, efforts unverified">`,
		`<option value="my-gw/claude-x" data-efforts="low high">Gateway X · my-gw/claude-x</option>`,
		`<option value="acme-opus" data-efforts="low medium high xhigh max" data-unverified>Acme Opus · acme-opus</option>`,
		`<select name="effort" aria-label="claude effort"><option value="">default</option>`,
		`<span class="unverified" hidden>efforts unverified</span>`,
	}
	prev := -1
	for _, w := range want {
		at := strings.Index(row, w)
		if at < 0 || at < prev {
			t.Fatalf("claude row lacks %q in order:\n%s", w, row)
		}
		prev = at
	}
	lacks(t, "claude row", row, `<optgroup label="discovered">`, `<input name="model"`)
	if strings.Index(row, `name="other"`) < strings.Index(row, "</select>") || strings.Index(row, `name="other"`) > strings.Index(row, `name="effort"`) {
		t.Fatalf("the other-model box is not between the model select and the effort:\n%s", row)
	}
}

func TestTheStartFormListsCodexAndAgyFromTheirLists(t *testing.T) {
	r := newModelRig(t)
	r.all(t)
	console := r.console(t)
	codex := startRow(t, console, "codex")
	contains(t, "codex row", codex,
		`<optgroup label="built-in">`,
		`<option value="gpt-6-luna" data-efforts="low medium high">GPT-6 Luna · gpt-6-luna</option>`,
		`<option value="gpt-6-sol" data-efforts="low ultra">GPT-6 Sol · gpt-6-sol</option>`,
		`<option value="gpt-6-mini" data-efforts="">GPT-6 Mini · gpt-6-mini</option>`,
		`<option value="ultra">ultra</option>`)
	lacks(t, "codex row", codex, `data-unverified`, `custom`)
	agy := startRow(t, console, "agy")
	contains(t, "agy row", agy, `<optgroup label="discovered">`, `<option value="gemini-3.8-flash-low" data-efforts="">Gemini 3.8 Flash (Low) · gemini-3.8-flash-low</option>`, `name="other"`)
	lacks(t, "agy row", agy, `name="effort"`)
}

func TestOpencodeShowsNoModelControlEvenWithAList(t *testing.T) {
	r := newModelRig(t)
	r.all(t)
	if models, err := harness.Detected("opencode", r.env); err != nil || len(models) != 2 {
		t.Fatalf("opencode list: %v, %v", models, err)
	}
	for _, console := range []string{r.console(t), func() string { r.fx.supervisor(t, state.AttemptRunning, "gen-1"); return r.console(t) }()} {
		row := startRow(t, console, "opencode")
		contains(t, "opencode row", row, `<button>`)
		lacks(t, "opencode row", row, "<select", "<input name=", "acme/acme-coder-1", "model-note")
	}
}

func TestAnEmptyOrUnavailableListKeepsTheTextInputs(t *testing.T) {
	r := newModelRig(t)
	r.install(t, "claude", "printer", map[string]string{"out": claudeFixture})
	r.refresh(t, "claude")
	r.install(t, "agy", "printer", map[string]string{"out": ""})
	if l, err := harness.Refresh(context.Background(), "agy", r.env, harness.Options{Environ: []string{"PATH=" + r.bin}}); err != nil || l.Failed == nil {
		t.Fatalf("agy listing: %+v, %v", l, err)
	}
	console := r.console(t)
	for _, name := range []string{"codex", "agy"} {
		row := startRow(t, console, name)
		contains(t, name+" row", row, `<input name="model" placeholder="model"`)
		lacks(t, name+" row", row, "<select", "optgroup", `name="other"`)
	}
	contains(t, "agy note", startRow(t, console, "agy"), `<p class="model-note">refresh failed: it listed no models</p>`)
	lacks(t, "codex note", startRow(t, console, "codex"), "model-note")
}

func TestTheNoteAppearsOnlyForAStaleListOrAFailure(t *testing.T) {
	r := newModelRig(t)
	r.all(t)
	r.fx.options.Now = func() time.Time { return time.Now().Add(30 * time.Minute) }
	lacks(t, "every row after 30 min", r.console(t), "model-note")
	r.fx.options.Now = func() time.Time { return time.Now().Add(3*time.Hour + 5*time.Minute) }
	console := r.console(t)
	contains(t, "claude row after 3 h", startRow(t, console, "claude"), `<p class="model-note">listed 3 h ago</p>`)
	lacks(t, "codex row after 3 h", startRow(t, console, "codex"), "model-note")
	r.fx.options.Now = func() time.Time { return time.Now().Add(7 * time.Hour) }
	console = r.console(t)
	contains(t, "codex row after 7 h", startRow(t, console, "codex"), `<p class="model-note">listed 7 h ago</p>`)
	contains(t, "claude row after 7 h", startRow(t, console, "claude"), `<p class="model-note">listed 7 h ago</p>`)
	r.fx.options.Now = nil
	lacks(t, "fresh", r.console(t), "model-note")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(r.claude, later, later); err != nil {
		t.Fatal(err)
	}
	if st := harness.StatusOf("claude", r.env, time.Now()); !st.Stale || st.Old {
		t.Fatalf("a changed binary: %+v", st)
	}
	lacks(t, "changed binary", r.console(t), "model-note")
	r.cache(t, "codex", func(m map[string]any) {
		m["failed"] = map[string]any{"at": time.Now().Format(time.RFC3339), "reason": "codex debug models --bundled timed out after 5s"}
	})
	contains(t, "failed", startRow(t, r.console(t), "codex"), `<p class="model-note">refresh failed: codex debug models --bundled timed out after 5s</p>`)
	lacks(t, "claude stays quiet", startRow(t, r.console(t), "claude"), "model-note")
}

func TestTheSwitchMenuHasTheGroupedSelectAndTheEffortFollowsIt(t *testing.T) {
	r := newModelRig(t)
	r.all(t)
	r.fx.status = "idle"
	r.fx.supervisor(t, state.AttemptRunning, "gen-1")
	console := r.console(t)
	menu := console[strings.Index(console, `id="model-menu"`):]
	menu = menu[:strings.Index(menu, "Switch the supervisor to another harness")]
	contains(t, "switch menu", menu,
		`<label>Model <select name="model"><option value="">keep sonnet</option><optgroup label="built-in">`,
		`<optgroup label="custom, efforts unverified">`,
		`data-unverified>Acme Opus · acme-opus</option>`,
		`<label class="other">Other model <input name="other" size="10" autocomplete="off"></label>`,
		`<label>Effort <select name="effort"><option value="">keep low</option>`,
		`<span class="unverified" hidden>efforts unverified</span>`)
	if strings.Index(menu, `name="model"`) > strings.Index(menu, `name="other"`) || strings.Index(menu, `name="other"`) > strings.Index(menu, `name="effort"`) {
		t.Fatalf("switch menu order:\n%s", menu)
	}
	others := console[strings.Index(console, "Switch the supervisor to another harness"):]
	contains(t, "other harnesses", others, `<optgroup label="built-in">`, `<p class="harness-name">codex</p>`, `<label>Model <select name="model" aria-label="codex model">`, `<label>Effort <select name="effort" aria-label="codex effort">`, `<option value="gpt-6-sol" data-efforts="low ultra">`, `<option value="gemini-3.8-flash-low" data-efforts="">`)
	lacks(t, "opencode switch row", startRow(t, console, "opencode"), "<select", "name=\"other\"")
}

func TestTheEffortRowsOfEveryModelCase(t *testing.T) {
	r := newModelRig(t)
	r.all(t)
	row := startRow(t, r.console(t), "claude")
	for _, tc := range []struct {
		model, efforts string
		unverified     bool
	}{
		{"opus", claudeEfforts, false},
		{"haiku", "", false},
		{"my-gw/claude-x", "low high", false},
		{"acme-opus", claudeEfforts, true},
	} {
		want := `<option value="` + tc.model + `" data-efforts="` + tc.efforts + `"`
		if tc.unverified {
			want += " data-unverified"
		}
		want += ">"
		contains(t, tc.model, row, want)
	}
	for _, e := range strings.Fields(claudeEfforts) {
		contains(t, "effort options", row, `<option value="`+e+`">`+e+`</option>`)
	}
}

func TestTheEffortHandlerNarrowsToTheSelectedModel(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const has = pick?.dataset.efforts;`,
		`const allowed = has === undefined ? null : has.split(" ").filter(Boolean);`,
		`o.disabled = o.hidden = allowed !== null && !allowed.includes(o.value);`,
		`const wrap = effort.closest("label") ?? effort;`,
		`wrap.hidden = effort.disabled = allowed?.length === 0;`,
		`const typed = !!form.querySelector("input[name=other]")?.value.trim();`,
		`const pick = typed ? null : model.selectedOptions[0];`,
		`const mark = form.querySelector(".unverified");`,
		`if (mark) mark.hidden = !(typed || pick?.hasAttribute("data-unverified"));`,
		`if (!effort.selectedOptions[0] || effort.selectedOptions[0].disabled) effort.selectedIndex = [...effort.options].findIndex((o) => !o.disabled);`,
		`if (!form || !e.target.matches("select[name=model], input[name=other]")) return;`,
		`if (other) other.value = "";`,
		`narrowAll(document);`)
}

func TestALiveUpdateKeepsTheSelectionTheTypedTextAndTheNarrowedEfforts(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const fields = "textarea, select, input:not([type=hidden]):not([type=radio])";`,
		`const drafts = new Map([...el.querySelectorAll(fields)].filter((f) => f.value).map((f) => [key(f), f.value]));`,
		`f.value = drafts.get(key(f));`,
		`if (f instanceof HTMLSelectElement && (f.selectedIndex < 0 || f.selectedOptions[0].disabled)) f.selectedIndex = Math.max(0, [...f.options].findIndex((o) => o.defaultSelected));`,
		"narrowAll(el);\n\t\trestore(el);",
		`const typing = (el) => el.contains(document.activeElement) && document.activeElement.matches("textarea, select, input:not([type=hidden])");`,
		`if (typing(el)) {`)
	if strings.Count(js, "const narrow = ") != 1 || strings.Count(js, "const narrowAll = ") != 1 {
		t.Fatal("app.js declares narrow or narrowAll twice")
	}
}

func TestANewCacheReachesTheNextRender(t *testing.T) {
	r := newModelRig(t)
	r.install(t, "claude", "printer", map[string]string{"out": claudeFixture})
	r.refresh(t, "claude")
	h := r.fx.handler()
	contains(t, "before", startRow(t, region(get(t, h, "/"), "console"), "claude"), `value="acme-opus"`)
	r.install(t, "claude", "printer", map[string]string{"out": claudeReply(claudeModel("opus", "Opus 5.5", "low"))})
	r.refresh(t, "claude")
	row := startRow(t, region(get(t, h, "/"), "console"), "claude")
	lacks(t, "after", row, `value="acme-opus"`)
	contains(t, "after", row, `<option value="opus" data-efforts="low">Opus 5.5 · opus</option>`)
}

func TestAProfileWhoseModelLeftAFreshListIsUnavailable(t *testing.T) {
	r := newModelRig(t)
	r.install(t, "agy", "agy", map[string]string{"models": "gem-1\tGem 1\ngem-2\tGem 2\n"})
	r.refresh(t, "agy")
	r.install(t, "agy", "agy", map[string]string{"models": "gem-2\tGem 2\n"})
	r.refresh(t, "agy")
	r.install(t, "claude", "printer", map[string]string{"out": claudeFixture})
	r.refresh(t, "claude")
	policy := `{"profiles":{"default":{"harness":"agy","model":"gem-1"},"live":{"harness":"agy","model":"gem-2"},"odd":{"harness":"claude","model":"acme-gw","effort":"high"},"plain":{"harness":"claude","model":"opus","effort":"high"}}}`
	if err := os.WriteFile(filepath.Join(r.fx.options.Home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	console := r.console(t)
	contains(t, "start strip", console,
		`<option value="default" disabled>default · agy gem-1 · model gem-1 is no longer listed by agy</option>`,
		`<option value="live" selected>live · agy gem-2</option>`,
		`<option value="plain">plain · claude opus high</option>`)
	if !strings.Contains(console, `<option value="odd">odd · claude acme-gw high · claude model acme-gw is not an alias (opus, sonnet, haiku, fable), a claude-* name or in Claude Code&#39;s model list`) {
		t.Fatalf("the unverified profile shows no warning:\n%s", console)
	}
	lacks(t, "start strip", console, `<option value="odd" disabled`, `<option value="plain" disabled`, "restart the board")
	post(r.fx.handler(), "/supervisor/start", url.Values{"profile": {"live"}})
	if got := strings.Join(r.fx.called()[0], " "); got != "supervisor start --profile live" {
		t.Fatalf("ran %q", got)
	}
	r.fx.status = "idle"
	r.fx.supervisor(t, state.AttemptRunning, "gen-1")
	menu := r.console(t)
	contains(t, "switch menu", menu[strings.Index(menu, `id="model-menu"`):], `<option value="odd">odd · claude acme-gw high · claude model acme-gw`)
}

func TestAProfileOnAClaudeListThatLostItsModelStaysStartable(t *testing.T) {
	r := newModelRig(t)
	r.install(t, "claude", "printer", map[string]string{"out": claudeFixture})
	r.refresh(t, "claude")
	r.install(t, "claude", "printer", map[string]string{"out": claudeReply(claudeModel("opus", "Opus 5.5", "low"))})
	r.refresh(t, "claude")
	policy := `{"profiles":{"default":{"harness":"claude","model":"acme-opus","effort":"high"}}}`
	if err := os.WriteFile(filepath.Join(r.fx.options.Home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	console := r.console(t)
	contains(t, "start strip", console, `<option value="default" selected>default · claude acme-opus high · model acme-opus is no longer listed by claude`)
	lacks(t, "start strip", console, `<option value="default" disabled`)
}

func TestNoProfileCanStartWhenEveryModelLeft(t *testing.T) {
	r := newModelRig(t)
	r.install(t, "agy", "agy", map[string]string{"models": "gem-1\tGem 1\n"})
	r.refresh(t, "agy")
	r.install(t, "agy", "agy", map[string]string{"models": "gem-2\tGem 2\n"})
	r.refresh(t, "agy")
	policy := `{"profiles":{"default":{"harness":"agy","model":"gem-1"}}}`
	if err := os.WriteFile(filepath.Join(r.fx.options.Home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	console := r.console(t)
	contains(t, "start strip", console, `<button class="primary" disabled>Start</button>`, `<p class="gone">model gem-1 is no longer listed by agy.</p>`)
	lacks(t, "start strip", console, " selected>")
}

func TestTheBoardNeverRunsAHarnessToReadTheModelLists(t *testing.T) {
	r := newModelRig(t)
	r.all(t)
	log := filepath.Join(t.TempDir(), "log")
	for _, name := range []string{"claude", "codex", "opencode", "agy"} {
		r.install(t, name, "forbidden", map[string]string{"log": log})
	}
	r.cache(t, "agy", func(m map[string]any) { m["gone"] = []string{"gone-1"} })
	policy := `{"profiles":{"default":{"harness":"agy","model":"gone-1"},"odd":{"harness":"claude","model":"acme-gw","effort":"high"}}}`
	if err := os.WriteFile(filepath.Join(r.fx.options.Home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	r.fx.status = "idle"
	r.fx.supervisor(t, state.AttemptRunning, "gen-1")
	h := r.fx.handler()
	for _, path := range []string{"/", "/?pick=1", "/supervisor/log"} {
		get(t, h, path)
	}
	req := httptest.NewRequest("GET", "/models/refresh", nil)
	req.AddCookie(laptop)
	if rec := httptest.NewRecorder(); func() int { h.ServeHTTP(rec, req); return rec.Code }() != http.StatusMethodNotAllowed {
		t.Fatal("GET /models/refresh does not answer 405")
	}
	contains(t, "listed from the cache", region(get(t, h, "/?pick=1"), "console"), `value="acme-opus"`, `value="gpt-6-sol"`, `value="gemini-3.8-flash-low"`)
	if b, err := os.ReadFile(log); err == nil {
		t.Fatalf("the board ran a harness: %q", b)
	}
}

func TestTheRefreshRowIsInTheOverflowMenuForTheLaptopOnly(t *testing.T) {
	p := newPhoneRig(t)
	row := `<form method="post" action="/models/refresh" data-fetch><input type="hidden" name="csrf" value="` + token + `"><button>Refresh models</button></form>`
	idle := region(p.do("GET", "/", nil, laptop, nil).Body.String(), "console")
	more := idle[strings.Index(idle, `id="more-menu"`):]
	contains(t, "no supervisor", more[:strings.Index(more, "</details>")], row)
	p.fx.supervisor(t, state.AttemptRunning, "gen-1")
	running := region(p.do("GET", "/", nil, laptop, nil).Body.String(), "console")
	more = running[strings.Index(running, `id="more-menu"`):]
	contains(t, "running", more[:strings.Index(more, "</details>")], row)
	phone := p.pair()
	own := csrfRe.FindStringSubmatch(p.do("GET", "/connect", nil, phone, viaServe).Body.String())[1]
	lacks(t, "phone", region(p.do("GET", "/", nil, phone, viaServe).Body.String(), "console"), "/models/refresh", "Refresh models")
	rec := p.do("POST", "/models/refresh", url.Values{"csrf": {own}, "harness": {"agy"}}, phone, map[string]string{"X-Hand-Fetch": "1", "X-Forwarded-For": "100.64.0.7", "X-Forwarded-Proto": "https", "Tailscale-User-Login": "operator@example.com"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a phone's refresh = %d", rec.Code)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestTheRefreshRunsInTheBackgroundOncePerHarnessAndInterval(t *testing.T) {
	p := newPhoneRig(t)
	bin, home, gate, log := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "gate"), filepath.Join(t.TempDir(), "log")
	fakebin.Install(t, bin, "agy", "gated", map[string]string{"out": agyModels, "gate": gate, "log": log})
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0o600) })
	vars := map[string]string{"PATH": bin, "HOME": t.TempDir(), "SECONDHAND_HOME": home}
	env := harness.EnvOf(func(k string) string { return vars[k] })
	p.fx.options.Harness = env
	p.fx.options.Models = harness.Options{Environ: []string{"PATH=" + bin}}
	p.h = p.fx.handler()
	fetch := map[string]string{"X-Hand-Fetch": "1"}
	refresh := func(form url.Values) (int, string, string) {
		form.Set("csrf", token)
		rec := p.do("POST", "/models/refresh", form, laptop, fetch)
		msg, _ := url.PathUnescape(rec.Header().Get("X-Hand-Error"))
		receipt, _ := url.PathUnescape(rec.Header().Get("X-Hand-Receipt"))
		return rec.Code, receipt, msg
	}
	began := time.Now()
	code, receipt, _ := refresh(url.Values{"harness": {"agy"}})
	if code != http.StatusNoContent || receipt != "Refreshing models…" || time.Since(began) > 5*time.Second {
		t.Fatalf("first refresh = %d %q after %v", code, receipt, time.Since(began))
	}
	waitFor(t, "the listing to start", func() bool { _, err := os.Stat(log); return err == nil })
	if code, _, msg := refresh(url.Values{"harness": {"agy"}}); code != http.StatusTooManyRequests || msg != "agy models are already refreshing" {
		t.Fatalf("a second refresh while one runs = %d %q", code, msg)
	}
	if got := harness.StatusOf("agy", env, time.Now()); got.Found {
		t.Fatal("the cache was written before the listing finished")
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the cache", func() bool { return harness.StatusOf("agy", env, time.Now()).Found })
	waitFor(t, "the refresh to end", func() bool {
		code, _, msg := refresh(url.Values{"harness": {"agy"}})
		return code == http.StatusTooManyRequests && strings.Contains(msg, "refreshed") && strings.Contains(msg, "s ago; try again in a moment")
	})
	if models, err := harness.Detected("agy", env); err != nil || len(models) != 1 {
		t.Fatalf("detected %v, %v", models, err)
	}
	p.advance(31 * time.Second)
	if code, _, msg := refresh(url.Values{"harness": {"agy"}}); code != http.StatusNoContent {
		t.Fatalf("a refresh after the interval = %d %q", code, msg)
	}
	waitFor(t, "the third run", func() bool { b, _ := os.ReadFile(log); return strings.Count(string(b), "models") == 2 })
	if code, _, _ := refresh(url.Values{"harness": {"opencode"}}); code != http.StatusBadRequest {
		t.Fatalf("refreshing opencode = %d", code)
	}
	if rec := p.do("GET", "/models/refresh", nil, laptop, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET refresh = %d", rec.Code)
	}
	if b, _ := os.ReadFile(log); strings.Count(string(b), "models") != 2 {
		t.Fatalf("listings run: %q", b)
	}
}

func TestTheRefreshActionListsCopilot(t *testing.T) {
	r := newModelRig(t)
	r.install(t, "copilot", "printer", map[string]string{"out": "  `model`: AI model\n    - \"gpt-6-luna\"\n\n  `other`: x\n"})
	if rec := fetchPost(r.fx.handler(), "/models/refresh", url.Values{"harness": {"copilot"}}); rec.Code != http.StatusNoContent {
		t.Fatalf("refresh copilot = %d", rec.Code)
	}
	waitFor(t, "the copilot list", func() bool { return harness.StatusOf("copilot", r.env, time.Now()).Found })
	if models, err := harness.Detected("copilot", r.env); err != nil || len(models) != 2 || models[0].Name != "auto" || models[1].Name != "gpt-6-luna" {
		t.Fatalf("detected %v, %v", models, err)
	}
}

func TestARefreshOfEveryHarnessAnswersAtOnceAndAReadOnlyBoardRefuses(t *testing.T) {
	r := newModelRig(t)
	gate := filepath.Join(t.TempDir(), "gate")
	r.install(t, "agy", "gated", map[string]string{"out": agyModels, "gate": gate, "log": filepath.Join(t.TempDir(), "log")})
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0o600) })
	r.fx.options.Controls = false
	if rec := fetchPost(r.fx.handler(), "/models/refresh", url.Values{}); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only refresh = %d", rec.Code)
	}
	r.fx.options.Controls = true
	began := time.Now()
	rec := fetchPost(r.fx.handler(), "/models/refresh", url.Values{})
	if rec.Code != http.StatusNoContent || time.Since(began) > 5*time.Second {
		t.Fatalf("refresh of every harness = %d after %v", rec.Code, time.Since(began))
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agy cache", func() bool { return harness.StatusOf("agy", r.env, time.Now()).Found })
}

func TestTheModelPickerRules(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "optgroup", css, "optgroup{background:var(--bg);color:var(--fg)}")
	contains(t, "unverified mark", css, ".unverified{grid-column:1/-1;color:var(--wait);font-size:12px;font-weight:600}")
	contains(t, "row columns", css, ".others-body form.start{display:grid;grid-template-columns:minmax(0,3fr) minmax(0,2fr) minmax(0,1.6fr) 10em}")
	contains(t, "menu width", css, "#model-menu .menu-body{width:min(300px,calc(100vw - 24px))}")
	contains(t, "menu selects", css, "#model-menu .menu-body form select{width:0;min-width:100%}")
	contains(t, "note", css, ".model-note{grid-column:1/-1;margin:0;color:var(--wait);font-size:12px}")
	contains(t, "container stack", css, "@container (max-width:519px){.others-body form.start>select[name=model],.others-body form.start>input[name=other],.others-body form.start>select[name=effort]{grid-column:1/-1}}")
	contains(t, "phone stack", block(t, css, "@media (max-width:599px){"), ".others-body form.start>select[name=model],.others-body form.start>input[name=other],.others-body form.start>select[name=effort]{grid-column:1/-1}")
}
