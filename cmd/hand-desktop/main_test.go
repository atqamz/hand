package main

import (
	"context"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

func TestFleetTitle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fleet desktop.Fleet
		want  string
	}{
		{name: "a named fleet", fleet: desktop.Fleet{ID: "f1", Name: "alpha"}, want: "Hand · alpha"},
		{name: "an id without a name", fleet: desktop.Fleet{ID: "f1"}, want: "Hand"},
		{name: "a name without an id", fleet: desktop.Fleet{Name: "alpha"}, want: "Hand"},
		{name: "no fleet", want: "Hand"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var titles []string
			setFleetTitle(func(title string) { titles = append(titles, title) }, tc.fleet)
			if len(titles) != 1 || titles[0] != tc.want {
				t.Fatalf("titles = %q, want one %q", titles, tc.want)
			}
		})
	}
}

const fleetID = "f00000000e0aa"

type stub struct{ desktop.ReadModel }

func (stub) Fleets() []desktop.Fleet { return []desktop.Fleet{{ID: fleetID, Name: "alpha"}} }
func (stub) Name(context.Context, string) (string, error) {
	return "alpha", nil
}
func (stub) Status(context.Context, string) (desktop.Status, error) {
	return desktop.Status{}, nil
}
func (stub) Timeline(context.Context, string, int) (desktop.Timeline, error) {
	return desktop.Timeline{}, nil
}
func (stub) Tasks(context.Context, string, bool) (desktop.Tasks, error) {
	return desktop.Tasks{}, nil
}
func (stub) Decision(context.Context, string, string) (desktop.DecisionDetail, error) {
	return desktop.DecisionDetail{}, nil
}

func booted() *desktop.App {
	app := desktop.New(func() (desktop.ReadModel, error) { return stub{}, nil })
	app.Boot()
	return app
}

func TestParseArgs(t *testing.T) {
	link := "handboard://open/" + fleetID + "/decision/d3"
	got, err := parseArgs([]string{"--open", link, "--fleet", "alpha"})
	if err != nil || got.open != link || got.fleet != "alpha" || got.version {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got, err := parseArgs(nil); err != nil || got != (options{}) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got, err := parseArgs([]string{"--version"}); err != nil || !got.version {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestParseArgsRefusesWhatIsNotAnOpenLink(t *testing.T) {
	for name, args := range map[string][]string{
		"http":         {"--open", "http://127.0.0.1:7777/" + fleetID + "/task/t1"},
		"fleet name":   {"--open", "handboard://open/alpha/"},
		"pairing link": {"--open", "handboard://c#v=1&f=" + fleetID},
		"traversal":    {"--open", "handboard://open/" + fleetID + "/../../etc/passwd"},
		"newline":      {"--open", "handboard://open/" + fleetID + "/task/t1\n--fleet=x"},
		"no value":     {"--open"},
		"positional":   {"handboard://open/" + fleetID + "/"},
		"unknown flag": {"--exec", "sh"},
		"escape flag":  {"--x\x1b]0;pwn\x07"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := parseArgs(args); err == nil {
				t.Fatalf("parsed as %+v", got)
			}
		})
	}
}

func TestParseArgsClipsAHugeLinkInItsMessage(t *testing.T) {
	_, err := parseArgs([]string{"--open", "handboard://open/" + strings.Repeat("x", 100000)})
	if err == nil || len(err.Error()) > 400 {
		t.Fatalf("err %v", err)
	}
}

func TestSecondInstanceHandsItsLinkToTheRunningWindow(t *testing.T) {
	app := booted()
	handoff(app, []string{"--open", "handboard://open/" + fleetID + "/decision/d3"})
	if app.Fleet.ID != fleetID || app.Screen != desktop.DecisionScreen || app.Ref != "d3" || app.Err != "" {
		t.Fatalf("fleet %s, screen %d, ref %q, err %q", app.Fleet.ID, app.Screen, app.Ref, app.Err)
	}
}

func TestSecondInstanceWithoutALinkChangesNothing(t *testing.T) {
	app := booted()
	app.Show(desktop.BoardScreen)
	handoff(app, nil)
	if app.Screen != desktop.BoardScreen || app.Err != "" {
		t.Fatalf("screen %d, err %q", app.Screen, app.Err)
	}
}

func TestSecondInstanceWithAHostileLinkIsRefusedOnScreen(t *testing.T) {
	for _, args := range [][]string{
		{"--open", "file:///etc/passwd"},
		{"--open", "handboard://open/f00000000dead/task/t1"},
		{"--bogus"},
		{"--bogus\x1b]0;pwn\x07"},
		{"--bogus" + strings.Repeat("x", 100000)},
	} {
		app := booted()
		app.Show(desktop.BoardScreen)
		handoff(app, args)
		if app.Err == "" || app.Screen != desktop.BoardScreen || len(app.Err) > 400 || strings.ContainsAny(app.Err, "\x1b\x07") {
			t.Fatalf("%.100q: screen %d, err %.200q", args, app.Screen, app.Err)
		}
	}
}
