package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktopasset"
)

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
func (stub) Models(context.Context, string) (desktop.Models, error) {
	return desktop.Models{}, nil
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

func TestAutomationPluginOnlyInTheE2EBuild(t *testing.T) {
	const plugin = "github.com/egoist/mygo/plugins/automation"
	deps := func(tags string) string {
		out, err := exec.Command("go", "list", "-deps", "-tags", tags, ".").CombinedOutput()
		if err != nil {
			t.Fatalf("go list -tags %q: %v\n%s", tags, err, out)
		}
		return string(out)
	}
	if strings.Contains(deps(""), plugin) {
		t.Fatal("the default hand-desktop build links the automation plugin")
	}
	if strings.Contains(deps("mygo_noinspector"), plugin) {
		t.Fatal("the release hand-desktop build links the automation plugin")
	}
	if !strings.Contains(deps("e2e"), plugin) {
		t.Fatal("the e2e hand-desktop build lacks the automation plugin")
	}
}

func TestRefusalFollowsTheStampedChannel(t *testing.T) {
	channel, was := desktopasset.Channel, gated
	t.Cleanup(func() { desktopasset.Channel, gated = channel, was })
	gated = true
	desktopasset.Channel = "source"
	if refusal("windows", "amd64") == "" {
		t.Fatal("an unstamped build did not refuse on Windows")
	}
	desktopasset.Channel = "stable"
	if refusal("windows", "amd64") == "" {
		t.Fatal("a stable build did not refuse on Windows")
	}
	desktopasset.Channel = "edge"
	if msg := refusal("windows", "amd64"); msg != "" {
		t.Fatalf("an edge build refused on Windows: %q", msg)
	}
	for _, c := range []string{"source", "stable", "edge"} {
		desktopasset.Channel = c
		if msg := refusal("darwin", "arm64"); msg != "" {
			t.Fatalf("a %s build refused on macOS: %q", c, msg)
		}
		if msg := refusal("darwin", "amd64"); msg != "" {
			t.Fatalf("a %s build refused on Intel macOS: %q", c, msg)
		}
		if msg := refusal("freebsd", "amd64"); msg == "" {
			t.Fatalf("a %s build did not refuse on FreeBSD", c)
		}
		if msg := refusal("linux", "amd64"); msg != "" {
			t.Fatalf("a %s build refused on Linux: %q", c, msg)
		}
	}
	gated = false
	desktopasset.Channel = "stable"
	if msg := refusal("freebsd", "arm64"); msg != "" {
		t.Fatalf("the automation build refused: %q", msg)
	}
}

func TestRemoteHostsNeedAStateFolder(t *testing.T) {
	none := func(string) string { return "" }
	rd, closer := remotes(none, "linux", func() {})
	if rd.RemoteStore != nil || rd.RemoteClients != nil || rd.desk != nil || rd.off == "" {
		t.Fatalf("no HOME and no state folder gave %+v", rd)
	}
	closer()
	state := t.TempDir()
	rd, closer = remotes(func(k string) string {
		if k == "XDG_STATE_HOME" {
			return state
		}
		return ""
	}, "linux", func() {})
	defer closer()
	if rd.RemoteStore == nil || rd.RemoteClients == nil || rd.desk == nil || rd.off != "" {
		t.Fatal("a state folder gave no remote store")
	}
	if hosts, err := rd.Hosts(); err != nil || len(hosts) != 0 {
		t.Fatalf("hosts = %v, %v", hosts, err)
	}
}

func TestRemoteHostsAreOffOnWindowsWithTheReason(t *testing.T) {
	state := t.TempDir()
	rd, closer := remotes(func(k string) string {
		if k == "XDG_STATE_HOME" || k == "LOCALAPPDATA" {
			return state
		}
		return ""
	}, "windows", func() {})
	defer closer()
	if rd.desk != nil || rd.RemoteStore != nil || !strings.Contains(rd.off, "Windows") {
		t.Fatalf("windows gave %+v", rd)
	}
}
