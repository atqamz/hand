package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/flock"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "watch" && os.Getenv("HAND_HOME") != "" {
		fakeWatch(os.Getenv("HAND_HOME"))
	}
	*cli.WatchExe = func() (string, error) { return "", errors.New("watcher start disabled in tests") }
	os.Exit(m.Run())
}

func enableWatcher(t *testing.T, home string) {
	t.Helper()
	*cli.WatchExe = os.Executable
	t.Cleanup(func() {
		*cli.WatchExe = func() (string, error) { return "", errors.New("watcher start disabled in tests") }
		_ = os.Remove(filepath.Join(home, "watch.started"))
		time.Sleep(300 * time.Millisecond)
	})
}

func fakeWatch(home string) {
	started, err := os.OpenFile(filepath.Join(home, "watch.started"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(1)
	}
	_, _ = started.WriteString("started\n")
	lock, err := os.OpenFile(filepath.Join(home, "watch.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		os.Exit(1)
	}
	if ok, err := flock.Lock(lock, false); !ok || err != nil {
		os.Exit(1)
	}
	for {
		if _, err := os.Stat(filepath.Join(home, "watch.started")); err != nil {
			os.Exit(0)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func holdWatchLock(t *testing.T, home string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(home, "watch.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if ok, err := flock.Lock(f, false); !ok || err != nil {
		t.Fatalf("lock = %v, %v", ok, err)
	}
}

func watchSpawns(home string) int {
	b, _ := os.ReadFile(filepath.Join(home, "watch.started"))
	return strings.Count(string(b), "\n")
}

func TestEnsureWatcherStartsOnce(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	enableWatcher(t, h.home)
	if !strings.Contains(h.ok("orient"), "watch: missing") {
		t.Fatal("orient must report a missing watcher before the supervisor starts")
	}
	startClaudeSupervisor(h)
	eventually(t, func() bool { return strings.Contains(h.ok("orient"), "watch: running") })
	h.ok("supervisor", "stop")
	has(t, "restart", h.ok("supervisor", "start"), "status: running")
	if n := watchSpawns(h.home); n != 1 {
		t.Fatalf("watchers started = %d, want 1", n)
	}
}

func TestEnsureWatcherSkipsHeldLock(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	enableWatcher(t, h.home)
	holdWatchLock(t, h.home)
	has(t, "start", startClaudeSupervisor(h), "status: running")
	time.Sleep(300 * time.Millisecond)
	if n := watchSpawns(h.home); n != 0 {
		t.Fatalf("watchers started = %d, want 0", n)
	}
	if !strings.Contains(h.ok("orient"), "watch: running") {
		t.Fatal("orient must report the held lock as a running watcher")
	}
}
