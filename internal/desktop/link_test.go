package desktop_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

func TestOpenLinkGrammarFollowsTheSharedVectors(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "companion-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Open struct {
			Valid []struct {
				Link   string
				Parsed struct {
					Fleet  string
					Page   string
					Report *string
				}
			}
			Invalid []struct{ Link string }
		}
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Open.Valid) < 6 || len(v.Open.Invalid) < 60 {
		t.Fatalf("vectors: %d valid, %d invalid", len(v.Open.Valid), len(v.Open.Invalid))
	}
	for _, c := range v.Open.Valid {
		want := desktop.OpenLink{Fleet: c.Parsed.Fleet}
		if _, ref, ok := strings.Cut(c.Parsed.Page, "/"); ok {
			want.Ref = ref
		}
		if c.Parsed.Report != nil {
			want.Ref = *c.Parsed.Report
		}
		got, err := desktop.ParseOpenLink(c.Link)
		if err != nil || got != want {
			t.Errorf("%q: got %+v, %v; want %+v", c.Link, got, err, want)
		}
	}
	for _, c := range v.Open.Invalid {
		if got, err := desktop.ParseOpenLink(c.Link); err == nil {
			t.Errorf("%q parsed as %+v", c.Link, got)
		}
	}
}

func TestOpenLinkRejectsHostileLinks(t *testing.T) {
	good := "handboard://open/" + fleetID + "/"
	for name, link := range map[string]string{
		"empty":              "",
		"pairing link":       "handboard://c#v=1&e=" + strings.Repeat("1", 64) + "&f=" + fleetID + "&c=K7QM3XRD2P",
		"http":               "http://127.0.0.1:7777/" + fleetID + "/task/t1",
		"file":               "file:///etc/passwd",
		"flag":               "--fleet=" + fleetID,
		"uppercase scheme":   "HANDBOARD://open/" + fleetID + "/",
		"uppercase fleet":    "handboard://open/F00000000E0AA/",
		"short fleet":        "handboard://open/f0000000e0aa/",
		"long fleet":         "handboard://open/" + fleetID + "0/",
		"fleet name":         "handboard://open/golden/",
		"no slash":           "handboard://open/" + fleetID,
		"trailing slash":     good + "task/t1/",
		"leading zero":       good + "task/t01",
		"zero":               good + "task/t0",
		"ten digits":         good + "task/t1234567890",
		"wrong letter":       good + "task/d1",
		"unknown page":       good + "doc/reports/t1/design.md",
		"traversal":          good + "../../etc/passwd",
		"query":              good + "task/t1?x=1",
		"userinfo":           "handboard://open/" + fleetID + "@evil.example/",
		"port":               "handboard://open:80/" + fleetID + "/",
		"percent":            good + "task/t%31",
		"fullwidth digit":    good + "task/t１",
		"space":              good + "task/t1 ",
		"leading space":      " " + good,
		"newline":            good + "task/t1\n",
		"nul":                good + "task/t1\x00",
		"report on attempt":  good + "attempt/a1#r1",
		"report on fleet":    good + "#r1",
		"report on decision": good + "decision/d1#r1",
		"bare fragment":      good + "task/t1#",
		"bad fragment":       good + "task/t1#x1",
		"two fragments":      good + "task/t1#r1#r2",
		"report zero":        good + "task/t1#r0",
		"huge":               good + "task/t" + strings.Repeat("9", 100000),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := desktop.ParseOpenLink(link); err == nil {
				t.Fatalf("parsed as %+v", got)
			}
		})
	}
}

func TestOpenLinkLandsOnTheRef(t *testing.T) {
	for name, tc := range map[string]struct {
		link   string
		screen desktop.Screen
		ref    string
	}{
		"task":     {"task/t1", desktop.TaskScreen, "t1"},
		"decision": {"decision/d1", desktop.DecisionScreen, "d1"},
		"attempt":  {"attempt/a1", desktop.AttemptScreen, "a1"},
		"report":   {"task/t1#r1", desktop.ReportScreen, "r1"},
		"fleet":    {"", desktop.BoardScreen, ""},
	} {
		t.Run(name, func(t *testing.T) {
			app := newApp(t, goldenFixture(t))
			app.Show(desktop.BoardScreen)
			if err := app.OpenLink("handboard://open/" + secondID + "/" + tc.link); err != nil {
				t.Fatal(err)
			}
			if app.Fleet.ID != secondID || app.Screen != tc.screen || app.Ref != tc.ref || app.Err != "" {
				t.Fatalf("fleet %s, screen %d, ref %q, err %q", app.Fleet.ID, app.Screen, app.Ref, app.Err)
			}
		})
	}
}

func TestOpenLinkInTheShownFleetKeepsItsFleet(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	app.Show(desktop.BoardScreen)
	if err := app.OpenLink("handboard://open/" + fleetID + "/decision/d1"); err != nil {
		t.Fatal(err)
	}
	if app.Fleet.ID != fleetID || app.Screen != desktop.DecisionScreen || app.Ref != "d1" {
		t.Fatalf("fleet %s, screen %d, ref %q", app.Fleet.ID, app.Screen, app.Ref)
	}
	app.Back()
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("back left on screen %d", app.Screen)
	}
}

func TestOpenLinkRefusesAnUnknownFleetAndStaysPut(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	app.Show(desktop.BoardScreen)
	err := app.OpenLink("handboard://open/f00000000dead/task/t1")
	if err == nil || !strings.Contains(err.Error(), "f00000000dead") || app.Err != err.Error() {
		t.Fatalf("err %v, shown %q", err, app.Err)
	}
	if app.Fleet.ID != fleetID || app.Screen != desktop.BoardScreen {
		t.Fatalf("moved to fleet %s, screen %d", app.Fleet.ID, app.Screen)
	}
}

func TestOpenLinkRefusesATextThatIsNotALinkAndStaysPut(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	app.Show(desktop.BoardScreen)
	if err := app.OpenLink("http://example.com/"); err == nil || app.Err != err.Error() {
		t.Fatalf("err %v, shown %q", err, app.Err)
	}
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("moved to screen %d", app.Screen)
	}
}

func TestOpenLinkWithoutABoardLeavesTheBoardError(t *testing.T) {
	app := desktop.New(func() (desktop.ReadModel, error) { return nil, desktop.ErrNoBoard })
	app.Boot()
	if err := app.OpenLink("handboard://open/" + fleetID + "/task/t1"); err != nil {
		t.Fatal(err)
	}
	if app.Err != desktop.ErrNoBoard.Error() {
		t.Fatalf("shown %q", app.Err)
	}
}

func TestAnOpenedRefRenders(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	if err := app.OpenLink("handboard://open/" + secondID + "/decision/d1"); err != nil {
		t.Fatal(err)
	}
	tt := newTester(app)
	mustText(t, tt, "second", "d1", "Options", "Yes, keep it", "No, rename it", "Open task")
}
