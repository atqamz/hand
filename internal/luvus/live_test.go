package luvus_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	return luvus.Client{Relist: func(ctx context.Context, _ string) (string, error) {
		addr, _, err := luvus.Address(ctx, bin, session, env)
		return addr, err
	}}
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

func startLive(t *testing.T, session string) (bin string, c luvus.Client, dir string) {
	t.Helper()
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
	c = liveClient(bin, session, env, getenv)
	t.Cleanup(func() {
		stop := exec.Command(bin, "--session", session, "server", "stop")
		stop.Env = env
		_ = stop.Run()
	})
	ctx := context.Background()
	if _, err := luvus.Ensure(ctx, c, func() error { return luvus.StartServer(ctx, bin, session, "", root, env) }); err != nil {
		t.Fatal(err)
	}
	if _, dir, err = luvus.Address(ctx, bin, session, env); err != nil || dir == "" {
		t.Fatalf("session dir %q, %v", dir, err)
	}
	return bin, c, dir
}

func serverPID(t *testing.T, dir string) (int, string) {
	t.Helper()
	var pid int
	var marker string
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		b, _ := os.ReadFile(filepath.Join(dir, "server.pid"))
		if n, _ := fmt.Sscanf(string(b), "%d %s", &pid, &marker); n == 2 {
			return pid, marker
		}
		if time.Now().After(deadline) {
			t.Fatalf("no server.pid in %s", dir)
		}
	}
}

func TestLiveServerPIDMarkerMatches(t *testing.T) {
	_, c, dir := startLive(t, "hand-it-pid")
	pid, marker := serverPID(t, dir)
	if got, err := luvus.ProcStartMarker(pid); err != nil || got != marker {
		t.Fatalf("marker = %q, %v; server.pid says %q", got, err, marker)
	}
	if v, err := c.Version(context.Background()); err != nil || v == "" {
		t.Fatalf("version = %q, %v", v, err)
	}
}

func TestLiveStopServer(t *testing.T) {
	_, c, dir := startLive(t, "hand-it-stop")
	ctx := context.Background()
	pid, marker := serverPID(t, dir)
	sleep := fakebin.Install(t, t.TempDir(), "sleep", "sleep", nil)
	term, err := c.Create(ctx, t.TempDir(), "hand-it", []string{sleep})
	if err != nil {
		t.Fatal(err)
	}
	forced := func(int, string) error { return errors.New("a graceful stop must not need force") }
	if err := c.StopServer(ctx, dir, forced); err != nil {
		t.Fatal(err)
	}
	if m, err := luvus.ProcStartMarker(pid); err == nil && m == marker {
		t.Fatalf("server %d is still alive", pid)
	}
	if _, err := os.Stat(filepath.Join(dir, "server.pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("server.pid err = %v", err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if m, err := luvus.ProcStartMarker(term.Root.PID); err != nil || m != term.Root.StartMarker {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane process %d outlived the server", term.Root.PID)
		}
	}
	if _, err := c.Version(ctx); !errors.Is(err, luvus.ErrUnreachable) {
		t.Fatalf("version after stop err = %v", err)
	}
}
