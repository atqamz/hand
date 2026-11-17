package kit

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

func cssRule(t *testing.T, selector string) string {
	t.Helper()
	light, _ := boardCSS(t)
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(selector) + `\{([^}]*)\}`).FindStringSubmatch(light)
	if m == nil {
		t.Fatalf("board.css has no rule %q", selector)
	}
	return m[1]
}

func wantDecls(t *testing.T, selector string, decls ...string) {
	t.Helper()
	body := cssRule(t, selector)
	for _, d := range decls {
		if !strings.Contains(body, d) {
			t.Errorf("%s no longer says %q: %s", selector, d, body)
		}
	}
}

func host(draw func(c *ui.Context)) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(600).Label("host").Children(func() { draw(c) })
	}, 700, 400)
}

func rect(t *testing.T, tt *ui.Tester, label string) ui.Rect {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("no %q in %q", label, tt.Texts())
	}
	return ui.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}
}

func TestDocRowMatchesTheListing(t *testing.T) {
	wantDecls(t, ".listing a", "gap:var(--space-10)", "min-height:var(--tap)", "padding:0 var(--space-4)", "border-bottom:1px solid var(--line)")
	wantDecls(t, ".listing img,.listing .badge", "flex:none", "width:48px")
	tt := host(func(c *ui.Context) {
		DocRow(c, ColorsFor(false), "a", "MD", "design.md", "2 KiB", false)
		DocRow(c, ColorsFor(false), "b", "TXT", "notes.txt", "1 B", false)
	})
	if got := rect(t, tt, "host").H; got != 2*Tap {
		t.Errorf("two rows are %v tall, want %v", got, 2*Tap)
	}
	for _, kind := range []string{"MD", "TXT"} {
		if got := rect(t, tt, "kind "+kind).W; got != 48 {
			t.Errorf("the %s pill is %v wide, want 48", kind, got)
		}
	}
	if DocBadge != 48 {
		t.Errorf("DocBadge = %d", DocBadge)
	}
}

func TestDocNoticeMatchesTheBoardsNotice(t *testing.T) {
	wantDecls(t, ".doc-notice", "padding:var(--space-10) var(--space-12)", "border:1px solid var(--line)", "border-radius:var(--r-md)", "background:var(--wait-bg)")
	tt := host(func(c *ui.Context) { DocNotice(c, ColorsFor(false), "A PDF opens in the Board's viewer.", nil) })
	if got, want := rect(t, tt, "host").H, float32(Body.Line+2*Space10+2); got != want {
		t.Errorf("a one-line notice is %v tall, want %v", got, want)
	}
}

func TestDocLinesMatchTheBoardsLines(t *testing.T) {
	wantDecls(t, ".lines", "padding:var(--space-6) 0", "border:1px solid var(--line)", "border-radius:var(--r-md)", "background:var(--card)", "font-size:12px", "line-height:18px")
	wantDecls(t, ".chunk>.gut", "width:calc(8ch + 22px)", "padding:0 var(--space-10) 0 var(--space-12)")
	tt := host(func(c *ui.Context) { DocLines(c, ColorsFor(false), 1, []string{"a", "b", "c"}) })
	if got, want := rect(t, tt, "host").H, float32(3*Meta.Line+2*Space6+2); got != want {
		t.Errorf("three lines are %v tall, want %v", got, want)
	}
}

func TestDocStepMatchesTheBoardsNav(t *testing.T) {
	wantDecls(t, ".doc-nav a", "gap:var(--space-8)", "min-height:var(--control)")
	tt := host(func(c *ui.Context) { DocStep(c, ColorsFor(false), "notes.txt", true) })
	if got := rect(t, tt, "host").H; got != Control {
		t.Errorf("the step link is %v tall, want %v", got, Control)
	}
}

func cssDecl(t *testing.T, selector, prop string) string {
	t.Helper()
	m := regexp.MustCompile(`(?:^|;)` + regexp.QuoteMeta(prop) + `:([^;]+)`).FindStringSubmatch(cssRule(t, selector))
	if m == nil {
		t.Fatalf("%s has no %s", selector, prop)
	}
	return m[1]
}

func cssPx(t *testing.T, v string) float32 {
	t.Helper()
	v = strings.TrimSpace(v)
	if name, ok := strings.CutSuffix(strings.TrimPrefix(v, "var(--"), ")"); ok && strings.HasPrefix(v, "var(--") {
		if n, ok := lengths[name]; ok {
			return n
		}
	}
	if n, ok := strings.CutSuffix(v, "px"); ok {
		f, err := strconv.ParseFloat(n, 32)
		if err == nil {
			return float32(f)
		}
	}
	if v == "0" {
		return 0
	}
	t.Fatalf("cannot read %q as a length", v)
	return 0
}

func TestDocThemeMatchesTheBoardsDocumentHeadings(t *testing.T) {
	bottom := cssPx(t, strings.Fields(cssDecl(t, ".md h3,.md h4", "margin"))[2])
	weight := cssDecl(t, ".md h3,.md h4", "font-weight")
	levels := []struct {
		selector string
		heads    []string
		first    int
	}{{".doc-body h3", []string{"Hone", "Htwo"}, 0}, {".doc-body h4", []string{"Hthree", "Hfour"}, 2}}
	src := "alpha\n\n# Hone\n\nbeta\n\n## Htwo\n\ngamma\n\n### Hthree\n\ndelta\n\n#### Hfour\n\nepsilon\n"
	var th *markdown.Theme
	tt := ui.NewTester(func(c *ui.Context) {
		th = DocTheme(c)
		ui.Column(c).Width(600).Children(func() { markdown.View(c, src, &markdown.Options{Theme: th}) })
	}, 700, 900)
	order := []string{"alpha", "Hone", "beta", "Htwo", "gamma", "Hthree", "delta", "Hfour", "epsilon"}
	rects := map[string]ui.Rect{}
	for _, w := range order {
		rects[w] = rect(t, tt, w)
	}
	near := func(what string, got, want float32) {
		t.Helper()
		if got < want-.5 || got > want+.5 {
			t.Errorf("%s is %v, want %v", what, got, want)
		}
	}
	for _, lv := range levels {
		size := cssPx(t, cssDecl(t, lv.selector, "font-size"))
		top := cssPx(t, cssDecl(t, lv.selector, "margin-top"))
		for i, head := range lv.heads {
			style := th.Headings[lv.first+i]
			near(head+" font size", style.Size*th.FontSize, size)
			if strconv.Itoa(style.Weight) != strings.TrimSpace(weight) {
				t.Errorf("%s weight is %d, want %s", head, style.Weight, weight)
			}
			at := indexOf(order, head)
			above, below := rects[order[at-1]], rects[order[at+1]]
			r := rects[head]
			near(head+" gap above", r.Y-(above.Y+above.H), top)
			near(head+" gap below", below.Y-(r.Y+r.H), bottom)
		}
	}
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
