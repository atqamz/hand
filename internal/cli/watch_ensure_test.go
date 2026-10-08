package cli_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/flock"
)

func TestMain(m *testing.M) {
	fakebin.Main(fakeBehaviors())
	if len(os.Args) > 2 && os.Args[1] == "board" && os.Getenv("HAND_TEST_REAL_BOARD") != "" {
		realBoard()
	}
	if len(os.Args) > 1 && os.Args[1] == "update" && os.Getenv("HAND_TEST_FAKE_UPDATE") != "" {
		fakeUpdate(os.Getenv("SECONDHAND_HOME"))
	}
	if len(os.Args) > 1 && os.Args[1] == "watch" && os.Getenv("HAND_HOME") != "" {
		fakeWatch(os.Getenv("HAND_HOME"))
	}
	if len(os.Args) > 1 && os.Args[1] == "board" && os.Getenv("SECONDHAND_HOME") != "" {
		fakeBoard(os.Getenv("SECONDHAND_HOME"))
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
		fakeExit(1, err.Error())
	}
	_, _ = started.WriteString("started\n")
	_ = started.Close()
	lock, err := os.OpenFile(filepath.Join(home, "watch.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		fakeExit(1, err.Error())
	}
	for try := 1; ; try++ {
		ok, err := flock.Lock(lock, false)
		if err != nil {
			fakeExit(1, err.Error())
		}
		if ok {
			break
		}
		if try == 5 {
			fakeExit(1, "watch.lock is held")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for {
		runtime.KeepAlive(lock)
		if _, err := os.Stat(filepath.Join(home, "watch.started")); err != nil {
			fakeExit(0, err.Error())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func fakeExit(code int, why string) {
	fmt.Fprintln(os.Stderr, "fake watcher exits:", why)
	os.Exit(code)
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
		log, _ := os.ReadFile(filepath.Join(h.home, "watch.log"))
		t.Fatalf("watchers started = %d, want 1; watch.log:\n%s", n, log)
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

func TestOrientDoesNotCreateWatchLock(t *testing.T) {
	h := initWithProject(t)
	if !strings.Contains(h.ok("orient"), "watch: missing") {
		t.Fatal("orient must report a missing watcher")
	}
	if _, err := os.Stat(filepath.Join(h.home, "watch.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orient left watch.lock behind: %v", err)
	}
}
