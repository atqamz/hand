package cli_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
)

func liveFleet(t *testing.T) (h *harness, session, luvusBin string) {
	t.Helper()
	if os.Getenv("HAND_LUVUS_IT") != "1" {
		t.Skip("set HAND_LUVUS_IT=1 to run against the installed luvus")
	}
	h = newHarness(t)
	h.inheritEnv()
	bin := t.TempDir()
	fakebin.Install(t, bin, "claude", "idle", nil)
	h.vars["PATH"] = bin + string(os.PathListSeparator) + os.Getenv("PATH")
	h.vars["HOME"] = t.TempDir()
	h.vars["LUVUS_HOME"] = t.TempDir()
	h.vars["HAND_TEST_AS_HAND"] = "1"
	*cli.TrustWait = time.Second
	t.Cleanup(func() { *cli.TrustWait = 15 * time.Second })
	luvusBin, err := exec.LookPath("luvus")
	if err != nil {
		t.Fatal(err)
	}
	session = fleet.Session(field(h.ok("init"), "id"))
	t.Cleanup(func() {
		stop := exec.Command(luvusBin, "--session", session, "server", "stop")
		stop.Env = h.env(nil, nil).Environ()
		_ = stop.Run()
	})
	h.ok("supervisor", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low")
	waitIdle(t, h, session, luvusBin)
	return h, session, luvusBin
}

func serverPID(t *testing.T, h *harness, session, bin string) string {
	t.Helper()
	_, dir, err := luvus.Address(context.Background(), bin, session, h.env(nil, nil).Environ())
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "server.pid"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func waitLive(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); !ok(); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not reached within 60s", what)
		}
	}
}

func waitIdle(t *testing.T, h *harness, session, bin string) {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		show := h.ok("supervisor", "show")
		if strings.Contains(show, "agent: idle") {
			return
		}
		if time.Now().After(deadline) {
			explain := exec.Command(bin, "--session", session, "agent", "explain", field(show, "pane"))
			explain.Env = h.env(nil, nil).Environ()
			out, err := explain.CombinedOutput()
			t.Fatalf("no idle supervisor within 60s\nshow: %s\nexplain: %v %s", show, err, out)
		}
	}
}

func TestLiveSwitchResumesTheSupervisor(t *testing.T) {
	h, session, bin := liveFleet(t)
	before := serverPID(t, h, session, bin)
	h.vars["HAND_INSTALL_BASE"] = handRelease(t, "0.9.0", "edge", "0123456789ab", luvus.Tested)
	channel, commit := cli.Channel, cli.Commit
	cli.Channel, cli.Commit = "edge", "0123456789ab"
	var waited time.Duration
	cli.UpdateHooks.Version = func(context.Context, fleet.Entry) (string, error) { return "0.0.1", nil }
	cli.UpdateHooks.Sleep = func(_ context.Context, d time.Duration) error { waited = d; return nil }
	t.Cleanup(func() {
		cli.Channel, cli.Commit = channel, commit
		cli.UpdateHooks.Version, cli.UpdateHooks.Sleep = nil, nil
	})
	h.advance(time.Hour)
	out := h.ok("update", "--channel", "edge")
	if !strings.Contains(out, "status: up to date\n") || !strings.Contains(out, ",\"skipped: current\",switched\n") {
		t.Fatalf("update = %s", out)
	}
	if waited != 30*time.Second {
		t.Fatalf("waited %v", waited)
	}
	if after := serverPID(t, h, session, bin); after == before {
		t.Fatalf("the server was not replaced: %s", after)
	}
	if show := h.ok("luvus", "show"); !strings.Contains(show, "match: yes\n") {
		t.Fatalf("luvus show = %s", show)
	}
	waitIdle(t, h, session, bin)
	if show := h.ok("supervisor", "show"); !strings.Contains(show, "supervisor: s2\n") || !strings.Contains(show, "status: running\n") {
		t.Fatalf("supervisor show = %s", show)
	}
	if orient := h.ok("orient"); !strings.Contains(orient, ",luvus.switched,") {
		t.Fatalf("orient = %s", orient)
	}
}

func TestLiveWatcherRestartsAKilledServer(t *testing.T) {
	h, session, bin := liveFleet(t)
	before := serverPID(t, h, session, bin)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.runCtx(ctx, "watch")
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitLive(t, "the watcher", func() bool { _, err := os.Stat(filepath.Join(h.home, "watch.pid")); return err == nil })
	pid, err := strconv.Atoi(strings.Fields(before)[0])
	if err != nil {
		t.Fatal(err)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	waitLive(t, "a new server", func() bool {
		b, err := os.ReadFile(filepath.Join(h.vars["LUVUS_HOME"], "sessions", session, "server.pid"))
		return err == nil && strings.TrimSpace(string(b)) != before && strings.Contains(h.ok("luvus", "show"), "match: yes\n")
	})
}
