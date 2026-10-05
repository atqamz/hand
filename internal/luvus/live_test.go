package luvus_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/luvus"
)

func liveClient(bin, session string, env []string, getenv func(string) string) luvus.Client {
	if runtime.GOOS != "windows" {
		return luvus.Client{Socket: luvus.SocketPath(getenv, session)}
	}
	return luvus.Client{Relist: func(ctx context.Context, _ string) (string, error) { return luvus.Address(ctx, bin, session, env) }}
}

func TestLiveLuvusRoundTrip(t *testing.T) {
	if os.Getenv("HAND_LUVUS_IT") != "1" {
		t.Skip("set HAND_LUVUS_IT=1 to run against the installed luvus")
	}
	bin, err := exec.LookPath("luvus")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	env := append(luvus.Scrub(os.Environ()), "LUVUS_HOME="+root)
	getenv := func(k string) string {
		switch k {
		case "LUVUS_HOME":
			return root
		case "HOME":
			return os.Getenv("HOME")
		}
		return ""
	}
	const session = "hand-it"
	c := liveClient(bin, session, env, getenv)
	t.Cleanup(func() {
		stop := exec.Command(bin, "--session", session, "server", "stop")
		stop.Env = env
		_ = stop.Run()
	})
	ctx := context.Background()
	caps, err := luvus.Ensure(ctx, c, func() error { return luvus.StartServer(ctx, bin, session, "", root, env) })
	if err != nil || caps.ServerGeneration == "" {
		t.Fatalf("ensure = %+v, %v", caps, err)
	}
	streamCtx, cancelStream := context.WithTimeout(ctx, 20*time.Second)
	defer cancelStream()
	stream, err := c.Subscribe(streamCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	sleep := fakebin.Install(t, t.TempDir(), "sleep", "sleep", nil)
	term, err := c.Create(ctx, t.TempDir(), "hand-it", []string{sleep})
	if err != nil {
		t.Fatal(err)
	}
	if m, err := luvus.ProcStartMarker(term.Root.PID); err != nil || m != term.Root.StartMarker {
		t.Fatalf("marker = %q, %v; luvus said %q", m, err, term.Root.StartMarker)
	}
	if state, err := c.Validate(ctx, term); err != nil || state != "alive" {
		t.Fatalf("validate = %s, %v", state, err)
	}
	terms, err := c.Inventory(ctx)
	if err != nil || !slices.ContainsFunc(terms, func(x luvus.Terminal) bool { return x.TerminalID == term.TerminalID }) {
		t.Fatalf("inventory = %+v, %v", terms, err)
	}
	if err := c.Close(ctx, term); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(ctx, term); luvus.Code(err) != "stale_terminal" {
		t.Fatalf("second close err = %v", err)
	}
	seen := map[string]bool{}
	deadline := time.Now().Add(5 * time.Second)
	for !(seen["pane.created"] && seen["pane.closed"]) {
		if time.Now().After(deadline) {
			t.Fatalf("events seen = %v", seen)
		}
		ev, err := stream.Next()
		if err != nil {
			t.Fatal(err)
		}
		var d struct {
			Pane string `json:"pane"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		if d.Pane == term.PaneID {
			seen[ev.Event] = true
		}
	}
}

func TestLiveAttachOpensThePane(t *testing.T) {
	if os.Getenv("HAND_LUVUS_IT") != "1" {
		t.Skip("set HAND_LUVUS_IT=1 to run against the installed luvus")
	}
	bin, err := exec.LookPath("luvus")
	if err != nil {
		t.Fatal(err)
	}
	script, err := exec.LookPath("script")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("attach needs a terminal; install util-linux script to run this")
	}
	root := t.TempDir()
	env := append(luvus.Scrub(os.Environ()), "LUVUS_HOME="+root)
	getenv := func(k string) string {
		switch k {
		case "LUVUS_HOME":
			return root
		case "HOME":
			return os.Getenv("HOME")
		}
		return ""
	}
	const session = "hand-it-attach"
	c := liveClient(bin, session, env, getenv)
	t.Cleanup(func() {
		stop := exec.Command(bin, "--session", session, "server", "stop")
		stop.Env = env
		_ = stop.Run()
	})
	ctx := context.Background()
	if _, err := luvus.Ensure(ctx, c, func() error { return luvus.StartServer(ctx, bin, session, "", root, env) }); err != nil {
		t.Fatal(err)
	}
	sleep := fakebin.Install(t, t.TempDir(), "sleep", "sleep", nil)
	term, err := c.Create(ctx, t.TempDir(), "hand-it", []string{sleep})
	if err != nil {
		t.Fatal(err)
	}
	for _, pane := range []string{term.PaneID, ""} {
		run, cancel := context.WithTimeout(ctx, 3*time.Second)
		var line []string
		for _, a := range luvus.AttachArgv(bin, session, pane) {
			line = append(line, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
		}
		cmd := exec.CommandContext(run, script, "-qec", strings.Join(line, " "), "/dev/null")
		cmd.Env = env
		out, _ := cmd.CombinedOutput()
		cancel()
		if !strings.Contains(string(out), "luvus · "+session) {
			t.Fatalf("attach %q did not open the session TUI: %q", pane, out)
		}
		if state, err := c.Validate(ctx, term); err != nil || state != "alive" {
			t.Fatalf("after attach %q: validate = %s, %v", pane, state, err)
		}
	}
}
