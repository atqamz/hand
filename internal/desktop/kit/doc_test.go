package kit

import (
	"regexp"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
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
