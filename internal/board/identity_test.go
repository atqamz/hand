package board_test

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
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
		for _, fg := range []string{"fail", "wait", "pass", "neutral", "fg", "muted"} {
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
	order := []string{`id="needs"`, `data-region="queue"`, `data-region="tasks"`, `id="chat"`, `data-region="status"`, `data-region="timeline"`, `id="composer"`}
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
	contains(t, "tabs", body, `<nav class="tabs"`, `href="#needs"`, `href="#chat"`, `Needs you (<span class="n">1</span>)`, `data-waiting="1"`)
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
	if !strings.Contains(css, "@media (min-width:900px) and (min-height:600px){") || !strings.Contains(css, "@media (max-width:899px),(max-height:599px){") {
		t.Fatal("a short landscape phone gets neither the desktop shell nor the phone layout, so the composer is cut off")
	}
}
