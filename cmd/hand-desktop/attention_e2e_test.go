//go:build e2e

package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type launcherBus struct {
	addr string
	mu   sync.Mutex
	out  strings.Builder
}

func (b *launcherBus) seen() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.out.String()
}

func (b *launcherBus) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.out.Write(p)
}

func startLauncherBus(t *testing.T, outDir string, env []string) *launcherBus {
	t.Helper()
	daemon := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1")
	daemon.Env = env
	pipe, err := daemon.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Start(); err != nil {
		t.Fatalf("dbus-daemon: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = daemon.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = daemon.Process.Kill()
		<-done
	})
	addr, err := bufio.NewReader(pipe).ReadString('\n')
	if err != nil {
		t.Fatalf("dbus-daemon printed no address: %v", err)
	}
	bus := &launcherBus{addr: strings.TrimSpace(addr)}
	log, err := os.Create(filepath.Join(outDir, "launcher.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	monitor := exec.Command("dbus-monitor", "--address", bus.addr, "type='signal',interface='com.canonical.Unity.LauncherEntry'")
	monitor.Env = env
	monitor.Stdout = bus
	monitor.Stderr = log
	if err := monitor.Start(); err != nil {
		t.Fatalf("dbus-monitor: %v", err)
	}
	mdone := make(chan struct{})
	go func() { _ = monitor.Wait(); close(mdone) }()
	t.Cleanup(func() {
		_ = monitor.Process.Kill()
		<-mdone
	})
	return bus
}

func untilTrue(t *testing.T, what string, limit time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("no %s after %s", what, limit)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func x11(t *testing.T, name string, args ...string) string {
	t.Helper()
	b, _ := exec.Command(name, args...).CombinedOutput()
	return string(b)
}

var launcherCount = regexp.MustCompile(`string "count"\s+variant\s+int64 (\d+)`)

func lastLauncherCount(seen string) string {
	all := launcherCount.FindAllStringSubmatch(seen, -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1][1]
}

func checkAttention(t *testing.T, run func(*exec.Cmd) *exec.Cmd, handBin, repo string, pid int, bus *launcherBus) {
	t.Helper()
	var xid string
	untilTrue(t, "X window for the process", 30*time.Second, func() bool {
		ids := strings.Fields(x11(t, "xdotool", "search", "--pid", strconv.Itoa(pid), "--name", "Hand"))
		if len(ids) == 0 {
			return false
		}
		xid = ids[len(ids)-1]
		return true
	})
	urgent := func() bool {
		return strings.Contains(x11(t, "xprop", "-id", xid, "WM_HINTS"), "The urgency hint bit is set")
	}
	title := func() string { return x11(t, "xprop", "-id", xid, "_NET_WM_NAME") }

	if !strings.Contains(title(), `"Hand · `+fleetName+`"`) || urgent() || lastLauncherCount(bus.seen()) != "" {
		t.Fatalf("the baseline read marked the window: title %q urgent %v launcher %q", title(), urgent(), bus.seen())
	}
	x11(t, "xdotool", "windowfocus", "0")
	untilTrue(t, "focus left the window", 30*time.Second, func() bool {
		return strings.TrimSpace(x11(t, "xdotool", "getwindowfocus")) != xid
	})
	for _, args := range [][]string{{"project", "add", "alpha", repo}, {"task", "add", "alpha", "Look at the window"}, {"decision", "ask", "t1", "Which one?"}} {
		if b, err := run(exec.Command(handBin, args...)).CombinedOutput(); err != nil {
			t.Fatalf("hand %s: %v\n%s", strings.Join(args, " "), err, b)
		}
	}
	want := "(1) Hand · " + fleetName
	untilTrue(t, "title "+want, 60*time.Second, func() bool { return strings.Contains(title(), want) })
	untilTrue(t, "urgency hint on the unfocused window", 30*time.Second, urgent)
	untilTrue(t, "launcher count 1", 30*time.Second, func() bool { return lastLauncherCount(bus.seen()) == "1" })

	x11(t, "xdotool", "windowfocus", xid)
	untilTrue(t, "urgency hint cleared by focus", 30*time.Second, func() bool { return !urgent() })
	if !strings.Contains(title(), want) || lastLauncherCount(bus.seen()) != "1" {
		t.Fatalf("focus changed the title %q or the launcher count %q", title(), lastLauncherCount(bus.seen()))
	}
}
