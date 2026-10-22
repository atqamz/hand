package board_test

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

func tintOf(id string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return int(h.Sum32() % 12)
}

func TestTheFleetTintIsDeterministic(t *testing.T) {
	for id, want := range map[string]int{"f6b63e98d4af2": tintOf("f6b63e98d4af2"), "fda07bc0c8b0f": tintOf("fda07bc0c8b0f")} {
		if want < 0 || want > 11 || tintOf(id) != want {
			t.Fatalf("tint(%s) = %d", id, want)
		}
	}
	st := open(t)
	f, err := st.Fleet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body := get(t, board.New(st, token, board.Options{}), "/")
	contains(t, "fleet page", body, fmt.Sprintf(`<body class="tint-%d"`, tintOf(f.ID)))
	host := board.NewHost(board.HostOptions{Loopback: true, Resolve: func(string) (string, error) { return "", nil }, List: func() ([]board.FleetLink, error) { return nil, nil }})
	contains(t, "fleet list", fetch(host, "/").Body.String(), `<body class="tint-0"`)
}

var (
	hexToken  = regexp.MustCompile(`--([a-z-]+):(#[0-9a-f]{6})`)
	tintToken = regexp.MustCompile(`\.tint-([0-9]+)\{--tint:(#[0-9a-f]{6})\}`)
)

func luminance(hex string) float64 {
	var c [3]float64
	for i := range c {
		v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		x := float64(v) / 255
		if x <= 0.04045 {
			c[i] = x / 12.92
		} else {
			c[i] = math.Pow((x+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}

func contrast(a, b string) float64 {
	x, y := luminance(a), luminance(b)
	return (max(x, y) + 0.05) / (min(x, y) + 0.05)
}

func themes(t *testing.T) (light, dark string) {
	t.Helper()
	css := asset(t, "board.css")
	marker := "@media (prefers-color-scheme:dark){"
	start := strings.Index(css, marker)
	if start < 0 {
		t.Fatal("board.css has no dark block")
	}
	depth, end := 1, start+len(marker)
	for ; end < len(css) && depth > 0; end++ {
		switch css[end] {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return css[:start] + css[end:], css[start+len(marker) : end-1]
}

func rootTokens(t *testing.T, block string) map[string]string {
	t.Helper()
	root := block[strings.Index(block, ":root{"):]
	root = root[:strings.IndexByte(root, '}')]
	tokens := map[string]string{}
	for _, m := range hexToken.FindAllStringSubmatch(root, -1) {
		tokens[m[1]] = m[2]
	}
	return tokens
}

func TestColoursMeetContrast(t *testing.T) {
	light, dark := themes(t)
	pairs := [][2]string{{"fg", "bg"}, {"fg", "card"}, {"fg", "panel"}, {"muted", "bg"}, {"muted", "card"}, {"muted", "panel"}, {"accent", "bg"}, {"accent", "accent-bg"}, {"fail", "fail-bg"}, {"wait", "wait-bg"}, {"pass", "pass-bg"}, {"neutral", "neutral-bg"}, {"fail", "bg"}, {"wait", "bg"}, {"pass", "bg"}, {"fail", "card"}, {"pass", "card"}, {"fg", "accent-bg"}, {"muted", "fail-bg"}, {"muted", "wait-bg"}, {"muted", "pass-bg"}, {"muted", "neutral-bg"}, {"screen-fg", "screen"}, {"on-primary", "primary"}}
	for _, c := range []struct{ name, block string }{{"light", light}, {"dark", dark}} {
		tokens := rootTokens(t, c.block)
		for _, p := range pairs {
			if tokens[p[0]] == "" || tokens[p[1]] == "" {
				t.Fatalf("%s theme lacks --%s or --%s", c.name, p[0], p[1])
			}
			if r := contrast(tokens[p[0]], tokens[p[1]]); r < 4.5 {
				t.Errorf("%s: --%s on --%s = %.2f", c.name, p[0], p[1], r)
			}
		}
		for _, ground := range []string{"bg", "card", "panel"} {
			if r := contrast(tokens["line-strong"], tokens[ground]); r < 3 {
				t.Errorf("%s: --line-strong on --%s = %.2f", c.name, ground, r)
			}
		}
		tints := tintToken.FindAllStringSubmatch(c.block, -1)
		if len(tints) != 12 {
			t.Fatalf("%s theme defines %d tints", c.name, len(tints))
		}
		for _, m := range tints {
			if r := contrast(m[2], tokens["bg"]); r < 3 {
				t.Errorf("%s: tint-%s on --bg = %.2f", c.name, m[1], r)
			}
		}
	}
}

func TestTypeHasFiveSteps(t *testing.T) {
	css := asset(t, "board.css")
	for _, touch := range []string{"textarea,input,select{font-size:16px}", ".filters :is(input,select),.menu-body label select{font-size:16px}"} {
		contains(t, "touch inputs", css, touch)
		css = strings.Replace(css, touch, "", 1)
	}
	for _, m := range regexp.MustCompile(`font-size:([0-9.]+px)`).FindAllStringSubmatch(css, -1) {
		switch m[1] {
		case "22px", "18px", "15px", "14px", "12px":
		default:
			t.Errorf("board.css uses font-size %s", m[1])
		}
	}
	contains(t, "live figures", css, ".live{font-variant-numeric:tabular-nums")
}

var iconNames = []string{"mark", "failure", "running", "passing", "idle", "waiting", "chevron", "interrupt", "more", "search"}

func TestIconsAreOwnInlineSVG(t *testing.T) {
	src, err := os.ReadFile("templates/icons.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range iconNames {
		start := strings.Index(string(src), `{{define "icon-`+name+`"}}`)
		if start < 0 {
			t.Fatalf("icon-%s is not defined", name)
		}
		body := string(src[start:])
		body = body[:strings.Index(body, "{{end}}")]
		for _, want := range []string{"<svg", `viewBox="0 0 16 16"`, `aria-hidden="true"`} {
			if !strings.Contains(body, want) {
				t.Errorf("icon-%s lacks %s", name, want)
			}
		}
		if n := strings.Count(body, "<path"); n < 1 || n > 4 {
			t.Errorf("icon-%s has %d paths", name, n)
		}
		if strings.Contains(strings.ToLower(body), "octicon") {
			t.Errorf("icon-%s names octicon", name)
		}
	}
}

func TestFirstViewportOrder(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, "running", "gen-1")
	body := get(t, fx.handler(), "/")
	at := func(s string) int {
		i := strings.Index(body, s)
		if i < 0 {
			t.Fatalf("page lacks %q", s)
		}
		return i
	}
	order := []string{`data-region="status"`, `id="needs"`, `data-region="queue"`, `id="tasks"`, `data-region="tasks"`, `id="chat"`, `data-region="console"`, `data-region="timeline"`, `id="composer"`}
	for i := 1; i < len(order); i++ {
		if at(order[i-1]) > at(order[i]) {
			t.Fatalf("%s comes after %s", order[i-1], order[i])
		}
	}
	if n := strings.Count(body, ` data-region="`); n != 5 {
		t.Fatalf("the fleet page has %d regions, want status, console, timeline, queue and tasks", n)
	}
	for _, name := range regionNames {
		lacks(t, name, region(body, name), `id="composer"`)
	}
}

func TestPhoneTabsWorkWithoutJS(t *testing.T) {
	st := open(t)
	task := active(t, st, "Fix login")
	if _, err := st.Ask(context.Background(), task.ID, "Ship it?"); err != nil {
		t.Fatal(err)
	}
	body := get(t, board.New(st, token, board.Options{}), "/")
	contains(t, "tabs", body, `<nav class="tabs"`, `href="#needs"`, `href="#tasks"`, `href="#chat"`, `Needs <span class="badge n" data-waiting="1" data-worst="decision">1</span>`, `data-waiting="1"`)
	for _, id := range []string{"needs", "tasks", "chat"} {
		tag := regexp.MustCompile(`<[a-z]+[^>]*id="` + id + `"[^>]*>`).FindString(body)
		if tag == "" || strings.Contains(tag, "hidden") {
			t.Fatalf("section %s = %q", id, tag)
		}
	}
}

func TestNoWebFonts(t *testing.T) {
	css := asset(t, "board.css")
	if strings.Contains(css, "@font-face") || strings.Contains(css, "url(") {
		t.Fatal("board.css loads a font or an image")
	}
}

func TestHiddenSectionsStayHidden(t *testing.T) {
	if !strings.Contains(asset(t, "board.css"), "[hidden]{display:none!important}") {
		t.Fatal("board.css lets a display rule override the hidden attribute, so the phone tabs cannot hide a section")
	}
}

func TestTheAppShellNeedsHeight(t *testing.T) {
	css := asset(t, "board.css")
	if !strings.Contains(css, "@media (min-height:600px),(min-width:900px){") || !strings.Contains(css, "@media (max-height:599px){") || !strings.Contains(css, "[data-shell]") {
		t.Fatal("a short landscape phone gets neither the shell nor the flowing page, so the composer is cut off")
	}
}

func TestNeedsAndTasksSitBesideTheChatAtBoardWidth(t *testing.T) {
	css := asset(t, "board.css")
	if !strings.Contains(css, "@media (min-width:900px){") {
		t.Fatal("board.css has no board-width query")
	}
	contains(t, "board width", block(t, css, "@media (min-width:900px){"), "body[data-page=fleet] .board{display:grid;grid-template-columns:minmax(0,1fr) var(--pane)}", ".tabs{display:none}", ".sup{position:relative;border-left:1px solid var(--line);background:var(--panel)}", ".work>.panel{padding:24px 32px 0}")
	contains(t, "app.js", asset(t, "app.js"), `matchMedia("(min-width:900px)")`)
}

func TestTabsBelowBoardWidth(t *testing.T) {
	css := asset(t, "board.css")
	if strings.Contains(css[:strings.Index(css, "@media (min-width:900px){")], ".tabs{display:none}") {
		t.Fatal("the tabs are hidden below board width")
	}
	contains(t, "tabs", block(t, css, "@media (max-width:899px){"), "#tasks .panel-head h2{display:none}", ".work:not(:has(>.panel:not([hidden]))){display:none}")
	contains(t, "panels", css, ".work>.panel{width:100%;max-width:880px;margin:0 auto;padding:16px 16px 0}")
}

func TestTheComposerGrowsButStopsAt40dvh(t *testing.T) {
	css := asset(t, "board.css")
	if !strings.Contains(css, "@supports (field-sizing:content){") || !strings.Contains(css, "field-sizing:content") || !strings.Contains(css, "max-height:40dvh") || !strings.Contains(css, "max-height:30dvh") {
		t.Fatal("the composer must grow with its text and stop at 40dvh in the shell and 30dvh on the flowing page")
	}
}

func TestTheViewportLetsTheKeyboardResizeTheShell(t *testing.T) {
	body := get(t, newFixture(t).handler(), "/")
	contains(t, "viewport", body, `content="width=device-width, initial-scale=1, interactive-widget=resizes-content"`, `<kbd data-send-key>Ctrl Enter</kbd>`)
}

func TestTheFaviconsAreHashedSVG(t *testing.T) {
	body := get(t, newFixture(t).handler(), "/")
	link := regexp.MustCompile(`<link rel="icon" href="(/static/favicon\.[0-9a-f]{12}\.svg)">`).FindStringSubmatch(body)
	alt := regexp.MustCompile(`data-icon-working="(/static/favicon-working\.[0-9a-f]{12}\.svg)" data-icon-attention="(/static/favicon-attention\.[0-9a-f]{12}\.svg)" data-icon-blocked="(/static/favicon-blocked\.[0-9a-f]{12}\.svg)"`).FindStringSubmatch(body)
	if link == nil || alt == nil {
		t.Fatalf("no favicon links:\n%s", body)
	}
	for _, u := range append(link[1:], alt[1:]...) {
		rec := httptest.NewRecorder()
		board.ServeStatic(rec, httptest.NewRequest("GET", u, nil))
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(rec.Body.String(), "<svg") {
			t.Fatalf("%s = %d %q", u, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

func TestEverySlugStateHasItsColour(t *testing.T) {
	css := asset(t, "board.css")
	for _, s := range []string{"running", "waiting", "failing", "passing", "ready", "working"} {
		tone := false
		for _, line := range strings.Split(css, "\n") {
			if strings.HasPrefix(line, ".badge") && strings.Contains(line, "[data-state="+s+"]") {
				tone = true
			}
		}
		if !tone {
			t.Errorf("board.css has no badge tone for the %s state", s)
		}
	}
}

func TestRepliesStyleTheirBlocks(t *testing.T) {
	css := asset(t, "board.css")
	for _, sel := range []string{".md h3", ".md h4", ".md hr", ".md blockquote", ".md del", ".md .table{", ".md th.r", ".md td.r", ".md .table.pinned th:first-child", ".md table{"} {
		if !strings.Contains(css, sel) {
			t.Errorf("board.css has no rule for %s", sel)
		}
	}
	for _, rule := range []string{"overflow-x:auto", "position:sticky;left:0", "font-variant-numeric:tabular-nums"} {
		if !strings.Contains(css, rule) {
			t.Errorf("board.css lacks %s for reply tables", rule)
		}
	}
}

func TestMotionHonoursReducedMotion(t *testing.T) {
	css := asset(t, "board.css")
	i := strings.Index(css, "@media (prefers-reduced-motion:reduce){")
	if i < 0 || !strings.Contains(css[i:], ".pill .icon{animation:none}") || !strings.Contains(css[i:], ".timeline>[data-new]{animation:none}") {
		t.Fatal("the working pulse or the feed keeps moving under prefers-reduced-motion")
	}
	lacks(t, "motion", css, "printhead", "@keyframes carriage")
}

func TestTheFinishFixesHold(t *testing.T) {
	css := asset(t, "board.css")
	for _, rule := range []string{
		"body:not([data-shell]) .console-box{position:sticky;bottom:0;z-index:2",
		"@media (max-height:599px){.console-box{position:sticky;bottom:0;z-index:2",
		"body[data-shell] .timeline{max-height:none;overflow:visible}",
		"::placeholder{color:var(--muted)}",
		".md .table{width:fit-content;max-width:100%;",
		"scrollbar-gutter:stable both-edges",
		".tabs .dot{",
		".menu[open]>summary .icon{transform:rotate(180deg)}",
		".md table{width:max-content;",
		"max-height:calc(100dvh - 120px);overflow-y:auto;",
		"height:var(--control);padding:0 8px 0 10px;white-space:nowrap;",
	} {
		if !strings.Contains(css, rule) {
			t.Errorf("board.css lacks %q", rule)
		}
	}
	if strings.Contains(css, ".tabs .dot{content:\"\";flex:none;width:6px;height:6px;border-radius:50%;background:var(--accent);animation") || strings.Contains(css, ".md table{min-width:100%") {
		t.Error("the tab dot still pulses, or reply tables still stretch to the column")
	}
}

func TestFleetLinksKeepTheirTab(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptStopped, "gen-1")
	old := active(t, fx.st, "Old chore")
	if _, err := fx.st.Transition(context.Background(), old.ID, state.StatusDone); err != nil {
		t.Fatal(err)
	}
	body := get(t, fx.handler(), "/")
	contains(t, "fleet page", body, `href="/tasks">Browse finished tasks (1)`, `href="/?pick=1#chat"`)
	if strings.Contains(body, "Show finished tasks") {
		t.Fatal("the rail still offers Show finished tasks")
	}
	contains(t, "all page", get(t, fx.handler(), "/?all=1"), `href="/#tasks">Hide finished tasks`)
}

func TestTheOnCallPalette(t *testing.T) {
	light, dark := themes(t)
	want := map[string][2]string{
		"bg": {"#ffffff", "#111317"}, "panel": {"#f6f7f9", "#15181d"}, "card": {"#f1f3f5", "#1b1f25"}, "fg": {"#15171c", "#e8eaee"}, "muted": {"#5b626e", "#9ba2ad"},
		"line": {"#e3e6ea", "#262b32"}, "line-strong": {"#80889a", "#6b7482"}, "hover": {"#eef0f3", "#1e232a"}, "fail": {"#c42b32", "#ff6b6b"}, "wait": {"#8a5b00", "#e6ac45"},
		"pass": {"#1a7a3e", "#56c77a"}, "neutral": {"#5b626e", "#9ba2ad"}, "accent": {"#2457c5", "#7ea6ff"}, "fail-bg": {"#fdeeee", "#2c1719"},
		"wait-bg": {"#fcf3dc", "#2a2212"}, "pass-bg": {"#e4f4e9", "#13281b"}, "neutral-bg": {"#eef0f3", "#1e232a"}, "accent-bg": {"#ebf1fd", "#172238"},
		"screen": {"#111418", "#08090b"}, "screen-fg": {"#d8dde4", "#d8dde4"}, "primary": {"#15171c", "#e8eaee"}, "on-primary": {"#ffffff", "#111317"},
	}
	lt, dt := rootTokens(t, light), rootTokens(t, dark)
	for name, v := range want {
		if lt[name] != v[0] || dt[name] != v[1] {
			t.Errorf("--%s = %q / %q, want %s / %s", name, lt[name], dt[name], v[0], v[1])
		}
	}
	css := asset(t, "board.css")
	contains(t, "control edges", css,
		"textarea,input,select{font:inherit;font-size:14px;color:var(--fg);background:var(--bg);border:1px solid var(--line-strong)",
		"height:var(--control);padding:0 12px;border:1px solid var(--line-strong)",
		"padding:4px 4px 8px;border:1px solid var(--line-strong)",
		"width:var(--control);height:var(--control);border:1px solid var(--line-strong)")
	contains(t, "tokens", css, "--r-sm:4px;--r-md:6px;--r-lg:10px;--control:32px;--badge:20px;--pane:", "@media (pointer:coarse){:root{--control:44px}}")
	lacks(t, "deleted tokens", css, "--pencil", "--canary", "--carbon", "--h-", "--ink-band", "--flash", "--header", "--on-header")
	if !strings.Contains(css, "--mono:ui-monospace") {
		t.Error("board.css has no --mono stack")
	}
	allowed := []string{".console-box", ".menu", "#toast", "dialog.viewer", ".keys button", ".key", "kbd"}
	for rest := css; ; {
		i := strings.Index(rest, "box-shadow:")
		if i < 0 {
			break
		}
		start := strings.LastIndexAny(rest[:i], "}\n") + 1
		segment := rest[start:i]
		sel := segment[:max(0, strings.LastIndexByte(segment, '{'))]
		sel = strings.TrimSpace(sel[strings.LastIndexByte(sel, '{')+1:])
		ok := false
		for _, a := range allowed {
			if strings.Contains(sel, a) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s carries a shadow", sel)
		}
		rest = rest[i+len("box-shadow:"):]
	}
}

func TestTheSupervisorControlsRenderInThePaneHead(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	body := get(t, fx.handler(), "/")
	pane := body[strings.Index(body, `id="chat"`):]
	head, timeline, box := strings.Index(pane, `<section class="console" data-region="console">`), strings.Index(pane, `data-region="timeline"`), strings.Index(pane, `<div class="console-box">`)
	if head < 0 || timeline < head || box < timeline {
		t.Fatalf("pane order: console %d, timeline %d, composer box %d", head, timeline, box)
	}
	contains(t, "pane head", region(body, "console"), `<span class="sup-ref">s1</span>`, `id="model-menu"`, `action="/supervisor/stop"`, `id="more-menu"`)
	form := pane[box:]
	form = form[:strings.Index(form, "</form></div>")]
	lacks(t, "composer box", form, `data-region="console"`, "/supervisor/stop", `id="model-menu"`)
	css := asset(t, "board.css")
	for _, rule := range []string{".console{display:flex;flex-wrap:wrap;align-items:center;gap:8px;min-height:52px;padding:8px 16px;border-bottom:1px solid var(--line)}", ".menu-body{position:absolute;top:calc(100% + 6px);left:0;", "#more-menu .menu-body{left:auto;right:0;"} {
		if !strings.Contains(css, rule) {
			t.Errorf("board.css lacks %q", rule)
		}
	}
}

func TestTheTasksHeadCarriesTheSearch(t *testing.T) {
	body := get(t, newFixture(t).handler(), "/")
	head := body[strings.Index(body, `<div class="panel" id="tasks">`):]
	head = head[:strings.Index(head, `data-region="tasks"`)]
	contains(t, "tasks head", head, `<h2>Tasks</h2>`, `<form class="task-search" role="search" method="get" action="/tasks">`, `<input type="search" name="q" placeholder="Search tasks" aria-label="Search tasks"`)
	lacks(t, "tasks region", region(body, "tasks"), "task-search", "<h2>")
}

func TestTheChatPaneResizesInsideTheClamp(t *testing.T) {
	css, js := asset(t, "board.css"), asset(t, "app.js")
	contains(t, "board.css", css,
		"--pane:clamp(360px,var(--chat,clamp(400px,38vw,720px)),calc(100% - 440px))",
		".split{display:none}",
		".split{position:absolute;top:0;bottom:0;left:-1px;z-index:5;display:block;width:10px;cursor:col-resize;touch-action:none}",
		"body[data-drag]{cursor:col-resize;user-select:none}",
		"@media (min-width:900px) and (pointer:coarse){.split{left:-12px;width:24px}",
		".split::before,.split::after{transition:none}")
	if strings.Index(css, ".split{display:none}") > strings.Index(css, "@media (min-width:900px){") {
		t.Fatal("the separator is shown below board width")
	}
	contains(t, "app.js", js,
		`document.querySelector("body[data-page=fleet] .sup")`,
		`role: "separator"`, `"aria-orientation": "vertical"`, `"aria-controls": "chat"`, "aria-valuetext",
		`localStorage.getItem("hand.chat")`, `localStorage.setItem("hand.chat", String(px))`, `localStorage.removeItem("hand.chat")`,
		`root.setProperty("--chat"`, `root.removeProperty("--chat")`,
		`[360, Math.max(360, board.clientWidth - 440)]`,
		`ArrowLeft: width() + step, ArrowRight: width() - step, Home: lo, End: hi`,
		`e.key === "Enter"`, `split.addEventListener("dblclick", reset)`, "setPointerCapture")
	lacks(t, "app.js", js, "hand.chat${", "style.cssText", `setAttribute("style"`)
	body := get(t, newFixture(t).handler(), "/")
	lacks(t, "markup", body, `role="separator"`, `class="split"`, "--chat")
}

func TestTheCapTrimIsADefaultComponentsOverride(t *testing.T) {
	css := asset(t, "board.css")
	trim := "button:where(:not(:has(.icon))),.key.stop{"
	contains(t, "cap trim", css, trim)
	if strings.Index(css, trim) < strings.Index(css, "\nbutton{display:inline-flex") {
		t.Fatal("the cap-trim default comes before the base button rule, so the base rule wins")
	}
	lacks(t, "balance", css, "text-wrap:balance")
}

func TestTheDeskFinishFixes(t *testing.T) {
	css := asset(t, "board.css")
	for _, rule := range []string{
		".working-line{display:flex;",
		"@media (pointer:coarse){textarea,input,select{font-size:16px}",
		"[data-send-key]{display:none}\n.others>summary,.tabs a,.actions a,.finished a,.crumbs a:not(.ref),.back a,.need a:not(.ref),.decision a:not(.ref){min-height:var(--control)}",
		".keys{gap:12px}",
		".timeline .card .copy>:not(.table):not(pre):not(.code){max-width:72ch}",
		".from-you .copy{max-width:72ch;",
		".masthead{position:sticky;top:0;",
		"@keyframes feed{",
		".masthead .strip-note{flex-basis:100%;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}",
		"@media (max-height:599px) and (max-width:599px){.masthead .fleet-name,",
	} {
		if !strings.Contains(css, rule) {
			t.Errorf("board.css lacks %q", rule)
		}
	}
	for _, gone := range []string{"border-bottom:3px solid var(--tint)", ".slug>*+*::before", "\n.card .copy>:not(.table)", "[data-role=goal] .copy,.thread>.card[data-role=plan] .copy{max-width", ".slug>*:not(:last-child)", "a.chip{min-height:44px}"} {
		if strings.Contains(css, gone) {
			t.Errorf("board.css still has %q", gone)
		}
	}
}

func TestReceiptsDoNotLookLikeErrors(t *testing.T) {
	css, js := asset(t, "board.css"), asset(t, "app.js")
	if !strings.Contains(css, "#toast[data-kind=receipt]{border-color:var(--line)}") {
		t.Fatal("board.css gives receipts no neutral border")
	}
	if !strings.Contains(js, `toast.dataset.kind = kind`) || !strings.Contains(js, `show(receipt, 4000, "receipt")`) {
		t.Fatal("app.js does not mark receipts apart from errors")
	}
}

func TestTheSubmitterIsReadBeforeButtonsAreDisabled(t *testing.T) {
	js := asset(t, "app.js")
	js = js[strings.Index(js, `document.addEventListener("submit"`):]
	read, disable := strings.Index(js, "new FormData(form, e.submitter)"), strings.Index(js, "b.disabled = true")
	if read < 0 || disable < 0 || read > disable {
		t.Fatal("app.js disables the buttons before it reads the pressed key, so the key never reaches the server")
	}
}

func TestNotificationsReadTheFreshWorstItem(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js, "const heldWorst = (html) =>", "count(Number(m[1]), heldWorst(html))", "const notify = (n, text) =>")
}

func TestNoSidewaysScrollOnPhone(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "wrapping", css, ".copy,.wait-title,.report-title,.need-title,.hand-text{min-width:0;overflow-wrap:anywhere}", "text-overflow:ellipsis}")
	i := strings.Index(css, ".fleet-name{")
	if i < 0 || !strings.Contains(css[i:i+strings.IndexByte(css[i:], '}')], "text-overflow:ellipsis") {
		t.Fatal("the fleet name does not truncate first")
	}
	if regexp.MustCompile(`(?:^|\n)(?:html|body)\{[^}]*[{;]width:`).MatchString(css) {
		t.Fatal("html or body carries a fixed width")
	}
}

func TestControlsAndBadgesTakeTheirHeightFromTokens(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "controls", css,
		"button{display:inline-flex;align-items:center;justify-content:center;gap:6px;height:var(--control);",
		"input:not([type=file]):not([type=hidden]),select{height:var(--control);padding:0 10px}",
		".attach{position:relative;display:inline-flex;align-items:center;justify-content:center;width:var(--control);height:var(--control);",
		".key{display:inline-flex;align-items:center;justify-content:center;gap:6px;min-width:var(--control);",
		"button:where(:not(:has(.icon))),.key.stop{display:inline-block;height:auto;min-height:var(--control);")
	contains(t, "badges", css,
		".badge{display:inline-block;height:var(--size,var(--badge));min-width:var(--size,var(--badge));",
		".badge.pill{--size:calc(var(--badge) + 4px);",
		".needs-count{--size:calc(var(--badge) + 4px);display:none}",
		"kbd{display:inline-flex;align-items:center;height:var(--badge);",
		"@media (forced-colors:active){.badge{border-color:CanvasText}}")
	lacks(t, "fixed heights", css, "min-height:32px", "height:28px", "width:30px", "width:44px", "min-height:44px}\n")
	for _, gone := range []string{".chip{", ".chip.", ".count{", ".state{", "\n.pill{", ".status.pill", ".countdown{padding", ".tabs .n{", ".group .n{"} {
		if strings.Contains(css, gone) {
			t.Errorf("board.css still styles %q apart from .badge", gone)
		}
	}
}

func TestSelectingATabKeepsALinkedMessageInViewAndOpensWorkAtTheTop(t *testing.T) {
	contains(t, "app.js", asset(t, "app.js"),
		"const nested = linked && !tabs.some((t) => t.dataset.tab === linked.id);",
		"if (timeline && current === \"chat\" && !(nested && timeline.contains(linked))) pin(timeline);",
		`if (!nested) document.querySelector(".work")?.scrollTo(0, 0);`)
}

func TestATableKeepsItsFirstColumnUnpinnedUntilPinned(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		".md .table.pinned th:first-child,.md .table.pinned td:first-child{position:sticky;left:0;z-index:1;border-right:1px solid var(--line);background:var(--bg)}",
		".md .table.pinned thead th:first-child{background:var(--card)}",
		".md .table-pin{display:block;margin:0 0 4px}")
	lacks(t, "board.css", css, "\n.md th:first-child", "\n.md td:first-child", ".md thead th:first-child", ".doc-table tbody td:first-child", ".doc-table thead th:first-child")
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const pinKey = "hand.table.pin";`,
		`localStorage.getItem(pinKey) === "on"`, `localStorage.setItem(pinKey, "on")`, `localStorage.removeItem(pinKey)`,
		`new ResizeObserver(`, `box.scrollWidth > box.clientWidth`,
		`box.classList.toggle("pinned", over && pinFirst)`,
		`root.querySelectorAll(".md:not(.doc-table) .table")`,
		`pin.className = "table-pin"`, `pin.hidden = true`, `pin.textContent = "Pin first column"`, `pin.setAttribute("aria-pressed"`)
	lacks(t, "app.js", js, `.table-pin").style`, "hand.table.pin${")
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	body := get(t, fx.handler(), "/")
	lacks(t, "markup", body, `class="table-pin"`, `class="table pinned"`, "Pin first column")
}

func TestTheDocumentWidthToggleIsAButtonAndIsRemembered(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/t1/design.md": design, "reports/a.log": "x\n", "reports/p.csv": "a,b\n1,2\n", "reports/e.zip": "PK\x00\x01\x02"})
	const button = `<button type="button" class="doc-wide" aria-pressed="false" hidden>Wide</button>`
	for _, page := range []string{"/doc/reports/t1/design.md", "/doc/reports/a.log"} {
		body := get(t, h, page)
		contains(t, page, body, `class="doc-copy" data-copy="path"`, `Copy path</button>`+button)
		lacks(t, page, body, `data-wide`, ` style=`)
	}
	for _, page := range []string{"/doc/reports/p.csv", "/doc/reports/e.zip", "/doc/reports"} {
		lacks(t, page, get(t, h, page), `class="doc-wide"`)
	}
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		"[data-wide] .doc-body{max-width:none}",
		".thread.doc[data-wide]{max-width:none;padding-inline:24px}",
		"@media (min-width:600px){dialog.viewer[data-wide]{width:calc(100vw - 48px)}}",
		"@media (max-width:699px){.doc-wide{display:none}}")
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const wideKey = "hand.doc.wide";`,
		`localStorage.getItem(wideKey)`, `localStorage.setItem(wideKey, on ? "on" : "off")`,
		`root.querySelector(".doc-wide")`, `scope.toggleAttribute("data-wide", on)`,
		`scope.toggleAttribute("data-wide", wideSaved() === "on")`,
		`dlg.toggleAttribute("data-wide", wideSaved() !== "off")`)
	lacks(t, "app.js", js, `.doc-wide").style`, "hand.doc.wide${")
}
