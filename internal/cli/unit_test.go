//go:build unix

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoardUnitRunsTheBoard(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_HOME"] = ""
	exe, _ := os.Executable()
	board := h.ok("unit", "board")
	for _, want := range []string{"Description=Hand board\n", `ExecStart="` + exe + `" board --addr 127.0.0.1:7777` + "\n", "Restart=on-failure", "RestartPreventExitStatus=3\n", "After=graphical-session.target\n", "WantedBy=graphical-session.target\n"} {
		if !strings.Contains(board, want) {
			t.Fatalf("board unit missing %q:\n%s", want, board)
		}
	}
	if strings.Contains(board, "PartOf=") {
		t.Fatalf("board unit has PartOf=:\n%s", board)
	}
	if strings.Contains(board, `Environment="HAND_HOME=`) {
		t.Fatalf("board unit names a fleet home:\n%s", board)
	}
	if board := h.ok("unit", "--addr", "127.0.0.1:7778", "board"); !strings.Contains(board, `ExecStart="`+exe+`" board --addr 127.0.0.1:7778`) {
		t.Fatalf("board unit with --addr:\n%s", board)
	}
	if _, _, code := h.run("unit", "--addr", "127.0.0.1:7778 --x", "board"); code != 2 {
		t.Fatalf("spaced --addr code = %d, want 2", code)
	}
	if _, _, code := h.run("unit", "luvus"); code != 2 {
		t.Fatalf("unknown unit code = %d, want 2", code)
	}
}

func TestUnitWatchWasRemoved(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	out, errOut, code := h.run("unit", "watch")
	if code != 2 || out != "" || !strings.Contains(errOut, "unit watch was removed") || !strings.Contains(errOut, "Autostart") {
		t.Fatalf("unit watch: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestBoardUnitKeepsACustomSecondhandHome(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	root, err := filepath.EvalSymlinks(h.vars["SECONDHAND_HOME"])
	if err != nil {
		t.Fatal(err)
	}
	if out := h.ok("unit", "board"); !strings.Contains(out, `Environment="SECONDHAND_HOME=`+root+"\"\n") {
		t.Fatalf("board unit does not keep the root:\n%s", out)
	}
	delete(h.vars, "SECONDHAND_HOME")
	if out := h.ok("unit", "board"); strings.Contains(out, "SECONDHAND_HOME") {
		t.Fatalf("board unit names a root nobody set:\n%s", out)
	}
}

func TestBoardUnitKeepsTheCallersPath(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.vars["PATH"] = "/opt/tools/bin:/usr/bin"
	if out := h.ok("unit", "board"); !strings.Contains(out, `Environment="PATH=/opt/tools/bin:/usr/bin"`+"\n") {
		t.Fatalf("board unit does not keep PATH:\n%s", out)
	}
	h.vars["PATH"] = "/opt/100%/bin"
	if out := h.ok("unit", "board"); !strings.Contains(out, `Environment="PATH=/opt/100%%/bin"`+"\n") {
		t.Fatalf("board unit does not escape %% in PATH:\n%s", out)
	}
	h.vars["PATH"] = `/opt/a"b`
	if _, errOut, code := h.run("unit", "board"); code != 2 || !strings.Contains(errOut, "PATH holds a quote, backslash or control character") {
		t.Fatalf("quoted PATH: code=%d stderr=%q", code, errOut)
	}
}
