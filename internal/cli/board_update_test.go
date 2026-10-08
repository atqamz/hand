package cli_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/luvus"
)

func realBoard() {
	*cli.UpdateMin = 0
	*cli.WatcherSweep = 30 * time.Millisecond
	cli.Channel = "edge"
	os.Exit(cli.Run(os.Args[1:], cli.Env{Stdout: os.Stdout, Stderr: os.Stderr, Getenv: cli.HostGetenv, Environ: os.Environ, Now: time.Now, Getwd: os.Getwd}))
}

func fakeUpdate(root string) {
	started, err := os.OpenFile(filepath.Join(root, "update.started"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fakeExit(1, err.Error())
	}
	fmt.Fprintf(started, "%d %s\n", os.Getpid(), strings.Join(os.Args[1:], " "))
	_ = started.Close()
	for {
		if _, err := os.Stat(filepath.Join(root, "update.release")); err == nil {
			os.Exit(0)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func updateRuns(root string) []string {
	b, _ := os.ReadFile(filepath.Join(root, "update.started"))
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func releaseUpdates(root string) {
	_ = os.WriteFile(filepath.Join(root, "update.release"), nil, 0o600)
}

func enableUpdate(t *testing.T, h *harness) string {
	t.Helper()
	root := h.vars["SECONDHAND_HOME"]
	h.vars["HAND_TEST_FAKE_UPDATE"] = "1"
	minimum, channel := *cli.UpdateMin, cli.Channel
	*cli.WatchExe, *cli.UpdateMin, cli.Channel = os.Executable, 0, "edge"
	t.Cleanup(func() {
		*cli.WatchExe = func() (string, error) { return "", fmt.Errorf("watcher start disabled in tests") }
		*cli.UpdateMin, cli.Channel = minimum, channel
		releaseUpdates(root)
		deadline := time.Now().Add(2 * time.Second)
		for _, line := range updateRuns(root) {
			pid, _ := strconv.Atoi(strings.Fields(line + " ")[0])
			for pid > 0 && time.Now().Before(deadline) {
				if _, err := luvus.ProcStartMarker(pid); err != nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	})
	return root
}

func TestBoardStartsOneUpdatePerIntervalAndNotWhileOneRuns(t *testing.T) {
	sweepFast(t)
	h := newHarness(t)
	h.ok("init")
	root := enableUpdate(t, h)
	every := 200 * time.Millisecond
	begin := time.Now()
	startBoard(t, h, "127.0.0.1", "--update-every", every.String())
	eventually(t, func() bool { return len(updateRuns(root)) == 1 })
	if time.Since(begin) < every {
		t.Fatal("the update started before its interval passed")
	}
	time.Sleep(5 * every)
	runs := updateRuns(root)
	if len(runs) != 1 || !strings.HasSuffix(runs[0], " update --channel edge --keep-luvus") {
		t.Fatalf("runs while the first lives = %q", runs)
	}
	releaseUpdates(root)
	eventually(t, func() bool { return len(updateRuns(root)) >= 2 })
}

func TestBoardRefusesAnUpdateIntervalBelowTheMinimum(t *testing.T) {
	h := newHarness(t)
	for _, every := range []string{"5m", "1ns", "-1h"} {
		_, errOut, code := h.run("board", "--addr", "127.0.0.1:0", "--update-every", every)
		if code != 2 || !strings.Contains(errOut, "--update-every must be 0 or at least 10m0s") {
			t.Fatalf("%s: code %d, stderr %q", every, code, errOut)
		}
	}
}

func TestBoardRefusesAutoUpdateOnASourceBuild(t *testing.T) {
	sweepFast(t)
	h := newHarness(t)
	h.ok("init")
	root := enableUpdate(t, h)
	cli.Channel = "source"
	_, stop := startBoard(t, h, "127.0.0.1", "--update-every", "100ms")
	settle()
	out := stop()
	if n := strings.Count(out, "this hand was built from source and has no update channel"); n != 1 {
		t.Fatalf("source-build notices = %d in %q", n, out)
	}
	if n := len(updateRuns(root)); n != 0 {
		t.Fatalf("updates started = %d", n)
	}
	if _, err := os.Stat(filepath.Join(root, "board.update-every")); err == nil {
		t.Fatal("a source build recorded an update interval")
	}
}

func TestBoardSkipsTheUpdateWhileTheLockIsHeld(t *testing.T) {
	sweepFast(t)
	h := newHarness(t)
	h.ok("init")
	root := enableUpdate(t, h)
	lock, err := os.OpenFile(filepath.Join(root, "update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if ok, err := flock.Lock(lock, false); !ok || err != nil {
		t.Fatalf("lock = %v, %v", ok, err)
	}
	_, stop := startBoard(t, h, "127.0.0.1", "--update-every", "100ms")
	settle()
	if n := len(updateRuns(root)); n != 0 {
		t.Fatalf("updates started while the lock was held = %d", n)
	}
	stop()
	_ = flock.Release(lock)
	_, stop = startBoard(t, h, "127.0.0.1", "--update-every", "100ms")
	eventually(t, func() bool { return len(updateRuns(root)) == 1 })
	stop()
}

func TestBoardRecordsItsUpdateIntervalForItsRestart(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	root := enableUpdate(t, h)
	path := filepath.Join(root, "board.update-every")
	_, stop := startBoard(t, h, "127.0.0.1", "--update-every", "1h")
	eventually(t, func() bool { b, _ := os.ReadFile(path); return string(b) == "1h0m0s\n" })
	stop()
	if b, _ := os.ReadFile(path); string(b) != "1h0m0s\n" {
		t.Fatalf("stopping the board forgot the interval: %q", b)
	}
	_, stop = startBoard(t, h, "127.0.0.1")
	stop()
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a board without --update-every kept the old interval")
	}
}

func TestTheUpdateSurvivesTheBoardBeingStopped(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	root := enableUpdate(t, h)
	h.vars["HAND_TEST_REAL_BOARD"] = "1"
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	board := exec.Command(exe, "board", "--addr", "127.0.0.1:0", "--update-every", "100ms")
	board.Env = os.Environ()
	for k, v := range h.vars {
		board.Env = append(board.Env, k+"="+v)
	}
	if err := board.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = board.Process.Kill(); _ = board.Wait() })
	eventually(t, func() bool { return len(updateRuns(root)) == 1 })
	pid, err := strconv.Atoi(strings.Fields(updateRuns(root)[0])[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := board.Process.Signal(syscall.SIGTERM); err != nil {
		if err := board.Process.Kill(); err != nil {
			t.Fatal(err)
		}
	}
	_ = board.Wait()
	time.Sleep(300 * time.Millisecond)
	if _, err := luvus.ProcStartMarker(pid); err != nil {
		t.Fatalf("the update (pid %d) died with the board: %v", pid, err)
	}
	if n := len(updateRuns(root)); n != 1 {
		t.Fatalf("updates started = %d, want 1", n)
	}
}
