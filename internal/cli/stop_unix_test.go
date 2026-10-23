//go:build unix

package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/update"
)

func fakeProcess(t *testing.T, script, pidFile string) (int, string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	marker, err := luvus.ProcStartMarker(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	writePID(t, pidFile, cmd.Process.Pid, marker, selfExe(t))
	return cmd.Process.Pid, marker
}

func running(pid int, marker string) bool {
	m, err := luvus.ProcStartMarker(pid)
	return err == nil && m == marker
}

func TestBoardStopStopsTheBoard(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.vars["SECONDHAND_HOME"], "board.pid")
	pid, marker := fakeProcess(t, "exec sleep 60", path)
	out := h.ok("board", "stop")
	if field(out, "board") != "stopped" || !strings.Contains(out, "hand watch stop") {
		t.Fatalf("out = %q", out)
	}
	if running(pid, marker) {
		t.Fatalf("process %d survived", pid)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("board.pid kept: %v", err)
	}
}

func TestBoardStopLeavesAProcessThatIgnoresTerm(t *testing.T) {
	old := *cli.UpdateGrace
	*cli.UpdateGrace = 300 * time.Millisecond
	t.Cleanup(func() { *cli.UpdateGrace = old })
	h := newHarness(t)
	path := filepath.Join(h.vars["SECONDHAND_HOME"], "board.pid")
	pid, marker := fakeProcess(t, "trap '' TERM; while :; do sleep 1; done", path)
	_, errOut, code := h.run("board", "stop")
	if code != 3 || !strings.Contains(errOut, "still alive") || !running(pid, marker) {
		t.Fatalf("board stop = %d %q alive=%v", code, errOut, running(pid, marker))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("board.pid: %v", err)
	}
}

func TestWatchStopStopsOnlyThisFleetsWatcher(t *testing.T) {
	h := newHarness(t)
	h.ok("init", h.home)
	path := filepath.Join(h.home, "watch.pid")
	pid, marker := fakeProcess(t, "exec sleep 60", path)
	out := h.ok("watch", "stop")
	if field(out, "watcher") != "stopped" || !strings.Contains(out, "Start it again with `hand watch`") || strings.Contains(out, "30 seconds") {
		t.Fatalf("out = %q", out)
	}
	if running(pid, marker) {
		t.Fatalf("process %d survived", pid)
	}
}

func TestWatchStopWarnsWhileTheBoardRuns(t *testing.T) {
	h := newHarness(t)
	h.ok("init", h.home)
	forget, err := update.WritePID(filepath.Join(h.vars["SECONDHAND_HOME"], "board.pid"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(forget)
	pid, marker := fakeProcess(t, "exec sleep 60", filepath.Join(h.home, "watch.pid"))
	out := h.ok("watch", "stop")
	if field(out, "watcher") != "stopped" || !strings.Contains(out, "within 30 seconds") || !strings.Contains(out, "`hand board stop` first, then `hand watch stop`") {
		t.Fatalf("out = %q", out)
	}
	if running(pid, marker) {
		t.Fatalf("process %d survived", pid)
	}
}
