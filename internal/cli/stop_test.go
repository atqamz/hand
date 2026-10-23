package cli_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/update"
)

func writePID(t *testing.T, path string, pid int, marker, exe string) {
	t.Helper()
	line := strconv.Itoa(pid) + " " + marker + " " + exe + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
}

func selfExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err == nil {
		exe, err = update.Target(exe)
	}
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func selfMarker(t *testing.T) string {
	t.Helper()
	m, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBoardStopWithNoBoard(t *testing.T) {
	h := newHarness(t)
	if out := h.ok("board", "stop"); field(out, "board") != "not running" || strings.Contains(out, "Removed") {
		t.Fatalf("out = %q", out)
	}
}

func TestStopRemovesAStalePIDFileWithoutSignalling(t *testing.T) {
	h := newHarness(t)
	h.ok("init", h.home)
	board := filepath.Join(h.vars["SECONDHAND_HOME"], "board.pid")
	watch := filepath.Join(h.home, "watch.pid")
	for _, c := range []struct{ name, path string }{{"board", board}, {"watch", watch}} {
		writePID(t, c.path, os.Getpid(), "another marker", selfExe(t))
		out := h.ok(c.name, "stop")
		if !strings.Contains(out, "not running") || !strings.Contains(out, "Removed "+filepath.Base(c.path)) {
			t.Fatalf("%s stop = %q", c.name, out)
		}
		if _, err := os.Stat(c.path); !os.IsNotExist(err) {
			t.Fatalf("%s kept: %v", c.path, err)
		}
	}
}

func TestStopRefusesAProcessOfAnotherBinary(t *testing.T) {
	h := newHarness(t)
	board := filepath.Join(h.vars["SECONDHAND_HOME"], "board.pid")
	other := filepath.Join(string(filepath.Separator), "elsewhere", "hand")
	writePID(t, board, os.Getpid(), selfMarker(t), other)
	_, errOut, code := h.run("board", "stop")
	if code != 3 || !strings.Contains(errOut, "runs "+other+", not this hand") || !strings.Contains(errOut, "left running") {
		t.Fatalf("board stop = %d %q", code, errOut)
	}
	if _, err := os.Stat(board); err != nil {
		t.Fatalf("board.pid: %v", err)
	}
}

func TestWatchStopOutsideAFleet(t *testing.T) {
	h := newHarness(t)
	_, errOut, code := h.run("watch", "stop")
	if code != 3 || !strings.Contains(errOut, "hand init") {
		t.Fatalf("watch stop = %d %q", code, errOut)
	}
}

func TestStopTakesNoArguments(t *testing.T) {
	h := newHarness(t)
	h.ok("init", h.home)
	for _, c := range []string{"board", "watch"} {
		if _, errOut, code := h.run(c, "stop", "now"); code != 2 || !strings.Contains(errOut, c+" stop: want 0 argument(s)") {
			t.Fatalf("%s stop now = %d %q", c, code, errOut)
		}
	}
}

func TestStopRemovesOnlyTheRecordItStopped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.pid")
	old, fresh := []byte("1 old /bin/hand\n"), []byte("2 new /bin/hand\n")
	for _, c := range []struct {
		name string
		disk []byte
		kept bool
	}{{"same record", old, false}, {"replacement", fresh, true}, {"already gone", nil, false}} {
		if c.disk != nil {
			if err := os.WriteFile(path, c.disk, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := cli.RemoveIfSame(path, old); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		_, err := os.Stat(path)
		if kept := err == nil; kept != c.kept {
			t.Fatalf("%s: kept = %v, want %v", c.name, kept, c.kept)
		}
		_ = os.Remove(path)
	}
}
