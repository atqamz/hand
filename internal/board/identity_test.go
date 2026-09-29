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

func TestColoursMeetContrast(t *testing.T) {
	light, dark := themes(t)
	for name, block := range map[string]string{"light": light, "dark": dark} {
		root := block[strings.Index(block, ":root{"):]
		root = root[:strings.IndexByte(root, '}')]
		tokens := map[string]string{}
		for _, m := range hexToken.FindAllStringSubmatch(root, -1) {
			tokens[m[1]] = m[2]
		}
		for _, fg := range []string{"fail", "wait", "pass", "neutral", "fg", "muted", "pencil", "flash"} {
			for _, bg := range []string{"bg", "card"} {
				if tokens[fg] == "" || tokens[bg] == "" {
					t.Fatalf("%s theme lacks --%s or --%s", name, fg, bg)
				}
				if c := contrast(tokens[fg], tokens[bg]); c < 4.5 {
					t.Errorf("%s: --%s on --%s = %.2f", name, fg, bg, c)
				}
			}
		}
		tints := tintToken.FindAllStringSubmatch(block, -1)
		if len(tints) != 12 {
			t.Fatalf("%s theme defines %d tints", name, len(tints))
		}
		for _, m := range tints {
			if c := contrast(m[2], tokens["header"]); c < 3 {
				t.Errorf("%s: tint-%s on --header = %.2f", name, m[1], c)
			}
		}
	}
}

var iconNames = []string{"mark", "decision", "blocked", "failure", "report", "resume", "running", "passing", "idle", "chat", "bell"}

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
	order := []string{`data-region="status"`, `id="needs"`, `data-region="queue"`, `data-region="tasks"`, `id="chat"`, `data-region="timeline"`, `id="composer"`, `data-region="console"`}
	for i := 1; i < len(order); i++ {
		if at(order[i-1]) > at(order[i]) {
			t.Fatalf("%s comes after %s", order[i-1], order[i])
		}
	}
}

func TestPhoneTabsWorkWithoutJS(t *testing.T) {
	st := open(t)
	task := active(t, st, "Fix login")
	if _, err := st.Ask(context.Background(), task.ID, "Ship it?"); err != nil {
		t.Fatal(err)
	}
	body := get(t, board.New(st, token, board.Options{}), "/")
	contains(t, "tabs", body, `<nav class="tabs"`, `href="#needs"`, `href="#chat"`, `Needs you <span class="count n" data-waiting="1">1</span>`, `data-waiting="1"`)
	for _, id := range []string{"needs", "chat"} {
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
	if !strings.Contains(css, "@media (min-height:600px){") || !strings.Contains(css, "@media (max-height:599px){") || !strings.Contains(css, "[data-shell]") {
		t.Fatal("a short landscape phone gets neither the shell nor the flowing page, so the composer is cut off")
	}
	if strings.Contains(css, "min-width:900px") {
		t.Fatal("the two-column desktop grid is back; the tabs show at every width")
	}
}

func TestTabsShowAtEveryWidth(t *testing.T) {
	css := asset(t, "board.css")
	if strings.Contains(css, ".tabs{display:none}") || !strings.Contains(css, "#needs,#chat{") || !strings.Contains(css, "max-width:880px") {
		t.Fatal("the tabs must show at every width, over panels up to 880px wide")
	}
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
	alt := regexp.MustCompile(`data-icon-working="(/static/favicon-working\.[0-9a-f]{12}\.svg)" data-icon-attention="(/static/favicon-attention\.[0-9a-f]{12}\.svg)"`).FindStringSubmatch(body)
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

func TestEveryPillStateHasItsColour(t *testing.T) {
	css := asset(t, "board.css")
	for _, s := range []string{"running", "waiting", "failing", "passing", "ready", "working"} {
		if !strings.Contains(css, ".pill[data-state="+s+"]") {
			t.Errorf("board.css has no colour for the %s pill", s)
		}
	}
}

func TestRepliesStyleTheirBlocks(t *testing.T) {
	css := asset(t, "board.css")
	for _, sel := range []string{".md h3", ".md h4", ".md hr", ".md blockquote", ".md del", ".md .table{", ".md th.r", ".md td.r", ".md th:first-child", ".md table{"} {
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
	if i < 0 || !strings.Contains(css[i:], ".pill[data-state=working]::before{animation:none}") || !strings.Contains(css[i:], ".printhead{animation:none}") || !strings.Contains(css[i:], ".lamp{animation:none}") {
		t.Fatal("the working dot, the print head or the lamp keeps moving under prefers-reduced-motion")
	}
}

func TestTheFinishFixesHold(t *testing.T) {
	css := asset(t, "board.css")
	for _, rule := range []string{
		"body:not([data-shell]) .console-box{position:sticky;bottom:0;z-index:2;",
		"@media (max-height:599px){.console-box{position:sticky;bottom:0;z-index:2;",
		"body[data-shell] .timeline{max-height:none;overflow:visible}",
		"::placeholder{color:var(--muted)}",
		".md .table{width:fit-content;max-width:100%;",
		"scrollbar-gutter:stable both-edges",
		".tabs .dot{",
		".menu[open]>summary .icon{transform:rotate(180deg)}",
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
	body := get(t, fx.handler(), "/")
	contains(t, "fleet page", body, `href="/?all=1#needs"`, `href="/?pick=1#chat"`)
	contains(t, "all page", get(t, fx.handler(), "/?all=1"), `href="/#needs">Hide finished tasks`)
}

func TestTheWireDeskTokens(t *testing.T) {
	light, dark := themes(t)
	token := func(block, name string) string {
		root := block[strings.Index(block, ":root{"):]
		root = root[:strings.IndexByte(root, '}')]
		for _, m := range hexToken.FindAllStringSubmatch(root, -1) {
			if m[1] == name {
				return m[2]
			}
		}
		t.Fatalf("no --%s", name)
		return ""
	}
	bg := token(light, "bg")
	r, _ := strconv.ParseUint(bg[1:3], 16, 8)
	g, _ := strconv.ParseUint(bg[3:5], 16, 8)
	b, _ := strconv.ParseUint(bg[5:7], 16, 8)
	if r < 0xe0 || g < 0xe0 || b > 0xb0 {
		t.Errorf("the day desk ground %s is not canary copy", bg)
	}
	if l := luminance(token(dark, "bg")); l >= 0.02 {
		t.Errorf("the night desk ground is not carbon: luminance %.3f", l)
	}
	css := asset(t, "board.css")
	for _, sel := range []string{".slug{", ".strip{"} {
		i := strings.Index(css, sel)
		if i < 0 {
			t.Fatalf("board.css has no %s rule", sel)
		}
		rule := css[i : i+strings.IndexByte(css[i:], '}')]
		if !strings.Contains(rule, "font-family:var(--mono)") || !strings.Contains(rule, "text-transform:uppercase") {
			t.Errorf("%s is not mono caps: %s", sel, rule)
		}
	}
	if !strings.Contains(css, "--mono:ui-monospace") {
		t.Error("board.css has no --mono stack")
	}
	allowed := []string{".console-box", ".menu", "#toast", ".keys button", ".key", "kbd"}
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
