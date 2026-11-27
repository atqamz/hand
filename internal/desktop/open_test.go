package desktop_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
)

func TestMain(m *testing.M) {
	base := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if base != "hand" {
		time.Local = time.UTC
		os.Exit(m.Run())
	}
	if verb := os.Getenv("FAKE_HAND_FAIL_VERB"); verb == "" || slices.Contains(os.Args[1:], verb) {
		if msg := os.Getenv("FAKE_HAND_FAIL"); msg != "" {
			os.Stderr.WriteString(msg + "\n")
			os.Exit(3)
		}
	}
	if os.Getenv("FAKE_HAND_SLEEPER") != "" {
		stop := os.Getenv("FAKE_HAND_STOP")
		for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(stop); err == nil {
				break
			}
		}
		os.WriteFile(stop+".gone", nil, 0o600)
		os.Exit(0)
	}
	wd, _ := os.Getwd()
	line := wd + "\n" + strings.Join(os.Args[1:], " ") + "\n"
	f, err := os.OpenFile(os.Getenv("FAKE_HAND_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(4)
	}
	f.WriteString(line)
	f.Close()
	if argv := os.Getenv("FAKE_HAND_ARGV"); argv != "" {
		b, _ := json.Marshal(os.Args[1:])
		if af, err := os.OpenFile(argv, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			af.Write(append(b, '\n'))
			af.Close()
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "init" {
		os.MkdirAll(os.Args[len(os.Args)-1], 0o755)
	}
	os.Stdout.WriteString(os.Getenv("FAKE_HAND_OUT"))
	if os.Getenv("FAKE_HAND_SLEEP") != "" {
		time.Sleep(8 * time.Second)
	}
	if gate := os.Getenv("FAKE_HAND_GATE"); gate != "" {
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(gate); err == nil {
				break
			}
		}
	}
	if os.Getenv("FAKE_HAND_BG") != "" {
		c := exec.Command(os.Args[0])
		c.Env = append(os.Environ(), "FAKE_HAND_SLEEPER=1")
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		c.Start()
	}
}

type fakeHand struct {
	log  string
	home string
}

func installFakeHand(t *testing.T) fakeHand {
	t.Helper()
	bin := t.TempDir()
	name := "hand"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	src, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(filepath.Join(bin, name), os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv("FAKE_HAND_LOG", log)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return fakeHand{log: log, home: home}
}

func (h fakeHand) reset(t *testing.T) {
	t.Helper()
	if err := os.Remove(h.log); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func (h fakeHand) calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(h.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (h fakeHand) want(t *testing.T, argv ...string) {
	t.Helper()
	h.wantIn(t, h.home, argv...)
}

func (h fakeHand) wantIn(t *testing.T, dir string, argv ...string) {
	t.Helper()
	got := h.calls(t)
	if len(got) != 2 {
		t.Fatalf("hand ran %q, want one call", got)
	}
	wd, err := filepath.EvalSymlinks(got[0])
	if err != nil || wd != dir {
		t.Fatalf("hand ran in %q, want %q", got[0], dir)
	}
	want := strings.Join(append([]string{"--home", dir}, argv...), " ")
	if got[1] != want {
		t.Fatalf("hand ran with %q, want %q", got[1], want)
	}
	if err := os.Remove(h.log); err != nil {
		t.Fatal(err)
	}
}

func homedApp(t *testing.T, home string) *desktop.App {
	t.Helper()
	f := goldenFixture(t)
	f.fleets[0].Home = home
	return newApp(t, f)
}

func TestNoWindowEntryRunsHandOpen(t *testing.T) {
	hand := installFakeHand(t)
	app := homedApp(t, hand.home)
	tt := newTester(app)
	click(t, tt, "More")
	mustNotText(t, tt, "Open in browser")
	for _, ref := range []string{"t1", "a1", "d1", "r1"} {
		app.OpenRef(ref)
		tt.Frame()
		mustNotText(t, tt, "Open as a page")
		mustNotText(t, tt, "Open in browser")
	}
	for _, dest := range []string{
		"/" + fleetID + "/",
		"/" + fleetID + "/task/t12",
		"/" + fleetID + "/decision/d3#x",
		"/" + fleetID + "/report/r7",
		"/" + fleetID + "/attempt/a2?tab=1",
		"/" + fleetID + "/doc/reports/t1/x.md",
		"/" + fleetID + "/doc/",
		"/" + fleetID + "/task/d3",
		"/" + secondID + "/task/t1",
	} {
		app.OpenBoardLink(dest)
	}
	if got := hand.calls(t); len(got) != 0 {
		t.Fatalf("the window ran hand %q", got)
	}
}

func TestNoWindowSourceRunsHandOpen(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files: %v", err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var stack []ast.Node
		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, n)
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || lit.Value != `"open"` || len(stack) < 2 {
				return true
			}
			switch p := stack[len(stack)-2].(type) {
			case *ast.CompositeLit:
				t.Errorf("%s: a string list holds \"open\"", name)
			case *ast.CallExpr:
				switch fn := p.Fun.(type) {
				case *ast.SelectorExpr:
					if strings.HasPrefix(fn.Sel.Name, "Command") {
						t.Errorf("%s: a command is run with \"open\"", name)
					}
				case *ast.Ident:
					if fn.Name == "append" {
						t.Errorf("%s: \"open\" is appended to an argv", name)
					}
				}
			}
			return true
		})
	}
}

func deliveredApp(t *testing.T, home string, ds ...desktop.Deliverable) *desktop.App {
	t.Helper()
	f := goldenFixture(t)
	f.fleets[0].Home = home
	td := f.task[fleetID]
	td.Delivered = ds
	f.task[fleetID] = td
	at := f.attempt[fleetID]
	at.Delivered = ds
	f.attempt[fleetID] = at
	rd := f.report[fleetID]
	rd.Delivered = ds
	f.report[fleetID] = rd
	return newApp(t, f)
}

func TestADeliveredItemOpensWhereItBelongs(t *testing.T) {
	hand := installFakeHand(t)
	docHref := "/" + fleetID + "/doc/reports/t1/my%20design.md"
	ds := []desktop.Deliverable{
		{Ref: "https://github.com/atqamz/hand/pull/7", Kind: "PR"},
		{Ref: "https://docs.example.com/q3", Kind: "link"},
		{Ref: "my design.md", Kind: "file", Href: docHref},
		{Ref: "outside.pdf", Kind: "file"},
		{Ref: "javascript:alert(1)", Kind: "link"},
		{Ref: "file:///etc/passwd", Kind: "link"},
		{Ref: "evil.md", Kind: "file", Href: "/" + secondID + "/doc/evil.md"},
		{Ref: "up.md", Kind: "file", Href: "/" + fleetID + "/doc/../../etc/passwd"},
	}
	for _, screen := range []struct {
		name string
		open func(*desktop.App)
	}{
		{"task", func(a *desktop.App) { a.OpenRef("t1") }},
		{"attempt", func(a *desktop.App) { a.OpenRef("a1") }},
		{"report", func(a *desktop.App) { a.OpenRef("r1") }},
	} {
		t.Run(screen.name, func(t *testing.T) {
			app := deliveredApp(t, hand.home, ds...)
			tt := newTester(app)
			screen.open(app)
			tt.Frame()
			mustText(t, tt, "Delivered", "PR", "outside.pdf", "my design.md")

			click(t, tt, "open https://github.com/atqamz/hand/pull/7")
			click(t, tt, "open https://docs.example.com/q3")
			want := []string{"https://github.com/atqamz/hand/pull/7", "https://docs.example.com/q3"}
			if got := tt.OpenedURLs(); !slices.Equal(got, want) {
				t.Fatalf("opened %q, want %q", got, want)
			}
			if got := hand.calls(t); len(got) != 0 {
				t.Fatalf("hand ran for a link: %q", got)
			}

			click(t, tt, "open "+docHref)
			if app.Screen != desktop.DocScreen || app.Ref != "reports/t1/my design.md" {
				t.Fatalf("a fleet file left the viewer on %v %q", app.Screen, app.Ref)
			}
			if got := tt.OpenedURLs(); len(got) != len(want) {
				t.Fatalf("a fleet file reached the browser directly: %q", got)
			}
			if got := hand.calls(t); len(got) != 0 {
				t.Fatalf("hand ran for a fleet file: %q", got)
			}
			app.Back()
			tt.Frame()

			for _, plain := range []string{"outside.pdf", "javascript:alert(1)", "file:///etc/passwd", "evil.md", "up.md"} {
				if err := tt.Click(plain); err == nil {
					tt.Frame()
				}
			}
			if got := tt.OpenedURLs(); len(got) != len(want) {
				t.Fatalf("an item that should not open reached the browser: %q", got)
			}
			if got := hand.calls(t); len(got) != 0 {
				t.Fatalf("an item that should not open ran hand: %q", got)
			}
		})
	}
}

func TestSameNamedDeliveredFilesOpenTheirOwnDocument(t *testing.T) {
	hand := installFakeHand(t)
	first := "/" + fleetID + "/doc/reports/t1/design.md"
	second := "/" + fleetID + "/doc/reports/t2/design.md"
	ds := []desktop.Deliverable{
		{Ref: "design.md", Kind: "file", Href: first},
		{Ref: "design.md", Kind: "file", Href: second},
	}
	app := deliveredApp(t, hand.home, ds...)
	tt := newTester(app)
	app.OpenRef("t1")
	tt.Frame()
	click(t, tt, "open "+first)
	if app.Screen != desktop.DocScreen || app.Ref != "reports/t1/design.md" {
		t.Fatalf("the first file opened %v %q", app.Screen, app.Ref)
	}
	app.Back()
	tt.Frame()
	click(t, tt, "open "+second)
	if app.Screen != desktop.DocScreen || app.Ref != "reports/t2/design.md" {
		t.Fatalf("the second file opened %v %q", app.Screen, app.Ref)
	}
}
