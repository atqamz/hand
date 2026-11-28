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
		"report":   {"task/t1#r1", desktop.TaskScreen, "t1"},
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
		t.Fatalf("Back from the first page left on screen %d", app.Screen)
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
	app := newDesktop(func() (desktop.ReadModel, error) { return nil, desktop.ErrNoBoard })
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
	mustText(t, tt, "second", "d1", "Options", "Yes, keep it", "No, rename it", "for Fix login")
}

func TestViewLinkRoutesEveryPage(t *testing.T) {
	for name, tc := range map[string]struct {
		page   string
		screen desktop.Screen
		ref    string
	}{
		"fleet":     {"", desktop.BoardScreen, ""},
		"task":      {"task/t1", desktop.TaskScreen, "t1"},
		"decision":  {"decision/d1", desktop.DecisionScreen, "d1"},
		"attempt":   {"attempt/a1", desktop.AttemptScreen, "a1"},
		"report":    {"task/t1#r1", desktop.TaskScreen, "t1"},
		"doc":       {"doc/reports/t2/design.md", desktop.DocScreen, "reports/t2/design.md"},
		"doc space": {"doc/my%20notes/a%23b.md", desktop.DocScreen, "my notes/a#b.md"},
		"tasks":     {"tasks", desktop.TasksScreen, ""},
		"log":       {"log", desktop.HistoryScreen, ""},
		"companion": {"companion", desktop.PhoneScreen, ""},
		"alerts":    {"alerts", desktop.AlertsScreen, ""},
	} {
		t.Run(name, func(t *testing.T) {
			app := newApp(t, goldenFixture(t))
			app.Show(desktop.BoardScreen)
			if err := app.OpenLink("handboard://view/" + secondID + "/" + tc.page); err != nil {
				t.Fatal(err)
			}
			if app.Fleet.ID != secondID || app.Screen != tc.screen || app.Ref != tc.ref || app.Err != "" {
				t.Fatalf("fleet %s, screen %d, ref %q, err %q", app.Fleet.ID, app.Screen, app.Ref, app.Err)
			}
		})
	}
}

func TestViewLinkTasksCarriesTheQuery(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	app.Show(desktop.BoardScreen)
	if err := app.OpenLink("handboard://view/" + secondID + "/tasks?q=login+%26+more"); err != nil {
		t.Fatal(err)
	}
	if app.Screen != desktop.TasksScreen || app.SearchQ != "login & more" {
		t.Fatalf("screen %d, query %q", app.Screen, app.SearchQ)
	}
}

func TestViewLinkRefusesWhatIsNotAPageAndStaysPut(t *testing.T) {
	for _, link := range []string{
		"handboard://view/" + secondID + "/screen",
		"handboard://view/" + secondID + "/doc/../x.md",
		"handboard://view/" + secondID + "/doc/a%2F..%2Fb",
		"handboard://view/" + secondID + "/doc//etc/passwd",
		"handboard://view/" + secondID + "/log/x",
		"handboard://view/f00000000dead/doc",
		"handboard://view/",
	} {
		app := newApp(t, goldenFixture(t))
		app.Show(desktop.BoardScreen)
		if err := app.OpenLink(link); err == nil || app.Err != err.Error() {
			t.Fatalf("%q: err %v, shown %q", link, err, app.Err)
		}
		if app.Fleet.ID != fleetID || app.Screen != desktop.BoardScreen {
			t.Fatalf("%q moved to fleet %s, screen %d", link, app.Fleet.ID, app.Screen)
		}
	}
}

func TestViewLinkWithAnUnknownFleetStaysPut(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	app.Show(desktop.BoardScreen)
	err := app.OpenLink("handboard://view/f00000000dead/log")
	if err == nil || !strings.Contains(err.Error(), "f00000000dead") || app.Screen != desktop.BoardScreen {
		t.Fatalf("err %v, screen %d", err, app.Screen)
	}
}

func TestViewLinkBeforeTheBoardIsReadWaitsForIt(t *testing.T) {
	app := newDesktop(func() (desktop.ReadModel, error) { return nil, desktop.ErrNoBoard })
	app.Boot()
	if err := app.OpenLink("handboard://view/" + fleetID + "/log"); err != nil || strings.Contains(app.Err, "not a view link") {
		t.Fatalf("err %v, shown %q", err, app.Err)
	}
}

func TestViewLinkQueuedBeforeTheBoardOpensItsPageOnceItIsRead(t *testing.T) {
	f := goldenFixture(t)
	up := false
	app := newDesktop(func() (desktop.ReadModel, error) {
		if !up {
			return nil, desktop.ErrNoBoard
		}
		return f, nil
	})
	app.DraftDir = t.TempDir()
	app.Boot()
	if err := app.OpenLink("handboard://view/" + fleetID + "/log"); err != nil {
		t.Fatal(err)
	}
	if app.Screen == desktop.HistoryScreen {
		t.Fatal("opened the log before the Board was read")
	}
	up = true
	app.Refresh()
	if app.Screen != desktop.HistoryScreen {
		t.Fatalf("screen %d after the Board was read, want the log", app.Screen)
	}
}

func TestParseLinkTakesBothGrammarsOnly(t *testing.T) {
	for link, ok := range map[string]bool{
		"handboard://open/" + fleetID + "/task/t1": true,
		"handboard://view/" + fleetID + "/log":     true,
		"handboard://view/" + fleetID + "/doc/a?b": false,
		"handboard://view/" + fleetID + "/screen":  false,
		"handboard://c#v=1":                        false,
		"http://example.com/":                      false,
	} {
		if err := desktop.ParseLink(link); (err == nil) != ok {
			t.Errorf("ParseLink(%q) = %v, want ok=%v", link, err, ok)
		}
	}
}

func TestViewLinkRoutesAReportOfATask(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	app.Show(desktop.BoardScreen)
	if err := app.OpenLink("handboard://view/" + secondID + "/task/t1#r1"); err != nil {
		t.Fatal(err)
	}
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" || desktop.PageReport(app) != "r1" {
		t.Fatalf("screen %d, ref %q, report %q", app.Screen, app.Ref, desktop.PageReport(app))
	}
	if err := app.OpenLink("handboard://view/" + secondID + "/task/t1"); err != nil {
		t.Fatal(err)
	}
	if desktop.PageReport(app) != "" {
		t.Fatalf("a task link kept the report %q", desktop.PageReport(app))
	}
}
