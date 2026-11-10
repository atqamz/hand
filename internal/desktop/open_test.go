package desktop_test

import (
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
		os.Exit(m.Run())
	}
	if msg := os.Getenv("FAKE_HAND_FAIL"); msg != "" {
		os.Stderr.WriteString(msg + "\n")
		os.Exit(3)
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
	got := h.calls(t)
	if len(got) != 2 {
		t.Fatalf("hand ran %q, want one call", got)
	}
	wd, err := filepath.EvalSymlinks(got[0])
	if err != nil || wd != h.home {
		t.Fatalf("hand ran in %q, want %q", got[0], h.home)
	}
	want := strings.Join(append([]string{"--home", h.home}, argv...), " ")
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

func TestEveryScreenOpensItsObjectThroughHandOpen(t *testing.T) {
	hand := installFakeHand(t)
	app := homedApp(t, hand.home)
	tt := newTester(app)
	for _, tc := range []struct {
		screen string
		open   func()
		argv   []string
	}{
		{"Status", func() {}, []string{"open"}},
		{"Needs", func() { click(t, tt, "Needs") }, []string{"open"}},
		{"Timeline", func() { click(t, tt, "Timeline") }, []string{"open"}},
		{"Tasks", func() { click(t, tt, "Tasks") }, []string{"open"}},
		{"task", func() { app.OpenRef("t1") }, []string{"open", "t1"}},
		{"attempt", func() { app.OpenRef("a1") }, []string{"open", "a1"}},
		{"decision", func() { app.OpenRef("d1") }, []string{"open", "d1"}},
		{"report", func() { app.OpenRef("r1") }, []string{"open", "r1"}},
	} {
		t.Run(tc.screen, func(t *testing.T) {
			tc.open()
			click(t, tt, "Open in browser")
			hand.want(t, tc.argv...)
			if app.OpenErr != "" {
				t.Fatalf("OpenErr = %q", app.OpenErr)
			}
		})
	}
}

func TestAFailingHandShowsItsLastLine(t *testing.T) {
	hand := installFakeHand(t)
	t.Setenv("FAKE_HAND_FAIL", "the board did not start")
	app := homedApp(t, hand.home)
	tt := newTester(app)
	click(t, tt, "Open in browser")
	mustText(t, tt, "The browser did not open", "the board did not start")
	click(t, tt, "Dismiss")
	mustNotText(t, tt, "The browser did not open")
}

func TestAMissingHandShowsAnError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	app := homedApp(t, t.TempDir())
	tt := newTester(app)
	click(t, tt, "Open in browser")
	mustText(t, tt, "The browser did not open", "`hand` was not found")
}

func TestAFleetWithoutAFolderShowsAnError(t *testing.T) {
	installFakeHand(t)
	app := homedApp(t, "")
	tt := newTester(app)
	click(t, tt, "Open in browser")
	mustText(t, tt, "the fleet's folder is not known")
}

func TestABoardLinkGoesThroughHandOpen(t *testing.T) {
	hand := installFakeHand(t)
	app := homedApp(t, hand.home)
	for dest, argv := range map[string][]string{
		"/" + fleetID + "/":                    {"open"},
		"/" + fleetID:                          {"open"},
		"/" + fleetID + "/task/t12":            {"open", "t12"},
		"/" + fleetID + "/decision/d3#x":       {"open", "d3"},
		"/" + fleetID + "/report/r7":           {"open", "r7"},
		"/" + fleetID + "/attempt/a2?tab=1":    {"open", "a2"},
		"/" + fleetID + "/doc/reports/t1/x.md": {"open", "--doc", "reports/t1/x.md"},
	} {
		app.OpenBoardPath(dest)
		hand.want(t, argv...)
	}
	for _, dest := range []string{
		"/" + fleetID + "/doc/",
		"/" + fleetID + "/docs/a.md",
		"/" + fleetID + "/task/d3",
		"/" + fleetID + "/task/t1/x",
		"/" + fleetID + "x/task/t1",
		"/" + secondID + "/task/t1",
	} {
		app.OpenBoardPath(dest)
		if app.OpenErr == "" || len(hand.calls(t)) != 0 {
			t.Fatalf("%s: err %q calls %q", dest, app.OpenErr, hand.calls(t))
		}
	}
}

func TestAHandThatLeavesADescendantOnThePipeStillReturns(t *testing.T) {
	hand := installFakeHand(t)
	t.Setenv("FAKE_HAND_BG", "1")
	stop := filepath.Join(t.TempDir(), "stop")
	t.Setenv("FAKE_HAND_STOP", stop)
	defer func() {
		os.WriteFile(stop, nil, 0o600)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(stop + ".gone"); err == nil {
				time.Sleep(200 * time.Millisecond)
				return
			}
		}
	}()
	app := homedApp(t, hand.home)
	start := time.Now()
	app.OpenInBrowser()
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("hand open held the window for %v", d)
	}
}

func TestAnAsyncOpenIsBoundedAndShowsItsErrorOnlyForItsFleet(t *testing.T) {
	for _, switched := range []bool{false, true} {
		hand := installFakeHand(t)
		t.Setenv("FAKE_HAND_SLEEP", "1")
		restore := desktop.SetOpenTimeout(300 * time.Millisecond)
		app := homedApp(t, hand.home)
		tt := newTester(app)
		posted := make(chan func(), 8)
		app.Post = func(fn func()) { posted <- fn }
		start := time.Now()
		click(t, tt, "Open in browser")
		if switched {
			app.SelectFleet(secondID)
		}
		waitPost(t, posted)()
		for !switched && app.OpenBusy {
			waitPost(t, posted)()
		}
		restore()
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("switched=%v: the run took %v", switched, d)
		}
		if got := app.OpenErr != ""; got == switched {
			t.Fatalf("switched=%v: OpenErr = %q", switched, app.OpenErr)
		}
		if app.OpenBusy {
			t.Fatalf("switched=%v: still busy", switched)
		}
	}
}

func TestOpenInBrowserIsDisabledWhileARunIsInFlight(t *testing.T) {
	hand := installFakeHand(t)
	gate := filepath.Join(t.TempDir(), "gate")
	t.Setenv("FAKE_HAND_GATE", gate)
	app := homedApp(t, hand.home)
	tt := newTester(app)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	click(t, tt, "Open in browser")
	for deadline := time.Now().Add(30 * time.Second); len(hand.calls(t)) != 2; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("hand never started")
		}
	}
	tt.Frame()
	tt.Click("Open in browser")
	if !app.OpenBusy {
		t.Fatal("the window is not busy while hand runs")
	}
	if got := hand.calls(t); len(got) != 2 {
		t.Fatalf("hand ran %q, want one call while busy", got)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for app.OpenBusy {
		waitPost(t, posted)()
	}
	if got := hand.calls(t); len(got) != 2 {
		t.Fatalf("hand ran %q, want one call", got)
	}
	tt.Frame()
	if err := tt.Click("Open in browser"); err != nil {
		t.Fatal(err)
	}
	for app.OpenBusy {
		waitPost(t, posted)()
	}
	if got := hand.calls(t); len(got) != 4 {
		t.Fatalf("hand ran %q, want a second call once idle", got)
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

			click(t, tt, "https://github.com/atqamz/hand/pull/7")
			click(t, tt, "https://docs.example.com/q3")
			want := []string{"https://github.com/atqamz/hand/pull/7", "https://docs.example.com/q3"}
			if got := tt.OpenedURLs(); !slices.Equal(got, want) {
				t.Fatalf("opened %q, want %q", got, want)
			}
			if got := hand.calls(t); len(got) != 0 {
				t.Fatalf("hand ran for a link: %q", got)
			}

			click(t, tt, "my design.md")
			hand.want(t, "open", "--doc", "reports/t1/my design.md")
			if got := tt.OpenedURLs(); len(got) != len(want) {
				t.Fatalf("a fleet file reached the browser directly: %q", got)
			}

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
	ds := []desktop.Deliverable{
		{Ref: "design.md", Kind: "file", Href: "/" + fleetID + "/doc/reports/t1/design.md"},
		{Ref: "design.md", Kind: "file", Href: "/" + fleetID + "/doc/reports/t2/design.md"},
	}
	app := deliveredApp(t, hand.home, ds...)
	tt := newTester(app)
	app.OpenRef("t1")
	tt.Frame()
	first, ok := tt.Find("design.md")
	if !ok {
		t.Fatal("design.md not on screen")
	}
	tt.ClickAt(first.X+1, first.Y+first.H/2)
	hand.want(t, "open", "--doc", "reports/t1/design.md")
	for dy := first.H; dy < first.H*4 && len(hand.calls(t)) == 0; dy++ {
		tt.ClickAt(first.X+1, first.Y+first.H/2+dy)
		time.Sleep(20 * time.Millisecond)
	}
	hand.want(t, "open", "--doc", "reports/t2/design.md")
}
