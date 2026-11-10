package desktop_test

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
	wd, _ := os.Getwd()
	line := wd + "\n" + strings.Join(os.Args[1:], " ") + "\n"
	f, err := os.OpenFile(os.Getenv("FAKE_HAND_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(4)
	}
	f.WriteString(line)
	f.Close()
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
	if got[1] != strings.Join(argv, " ") {
		t.Fatalf("hand ran with %q, want %q", got[1], strings.Join(argv, " "))
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
		"/" + fleetID + "/":                 {"open"},
		"/" + fleetID:                       {"open"},
		"/" + fleetID + "/task/t12":         {"open", "t12"},
		"/" + fleetID + "/decision/d3#x":    {"open", "d3"},
		"/" + fleetID + "/report/r7":        {"open", "r7"},
		"/" + fleetID + "/attempt/a2?tab=1": {"open", "a2"},
	} {
		app.OpenBoardPath(dest)
		hand.want(t, argv...)
	}
	for _, dest := range []string{
		"/" + fleetID + "/doc/notes.md",
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
