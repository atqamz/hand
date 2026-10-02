package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnitFilesRunTheWatcherAndTheBoard(t *testing.T) {
	h := newHarness(t)
	h.home = filepath.Join(t.TempDir(), "my fleet")
	if err := os.MkdirAll(h.home, 0o755); err != nil {
		t.Fatal(err)
	}
	h.ok("init")
	exe, _ := os.Executable()
	watch := h.ok("unit", "watch")
	for _, want := range []string{"[Service]", `Environment="HAND_HOME=` + h.home + `"`, `ExecStart="` + exe + `" watch`, "Restart=on-failure", "WantedBy=default.target"} {
		if !strings.Contains(watch, want) {
			t.Fatalf("watch unit missing %q:\n%s", want, watch)
		}
	}
	if !strings.Contains(watch, "Description=Hand watcher for my fleet ("+h.home+")\n") {
		t.Fatalf("watch unit does not name the fleet:\n%s", watch)
	}
	if board := h.ok("unit", "board"); !strings.Contains(board, `ExecStart="`+exe+`" board --addr 127.0.0.1:7777`) {
		t.Fatalf("board unit:\n%s", board)
	}
	if board := h.ok("unit", "--addr", "127.0.0.1:7778", "board"); !strings.Contains(board, `ExecStart="`+exe+`" board --addr 127.0.0.1:7778`) {
		t.Fatalf("board unit with --addr:\n%s", board)
	}
	for _, args := range [][]string{{"unit", "--addr", "127.0.0.1:7778", "watch"}, {"unit", "--addr", "127.0.0.1:7778 --x", "board"}} {
		if _, _, code := h.run(args...); code != 2 {
			t.Fatalf("%q code = %d, want 2", args, code)
		}
	}
	h.ok("init", "--name", `say "hi"`)
	if out, errOut, code := h.run("unit", "watch"); code != 2 || out != "" || !strings.Contains(errOut, "systemd unit") {
		t.Fatalf("quoted fleet name: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if _, _, code := h.run("unit", "luvus"); code != 2 {
		t.Fatalf("unknown unit code = %d, want 2", code)
	}
}

func TestUnitFilesEscapeSpecifiersAndRefuseUnquotablePaths(t *testing.T) {
	h := newHarness(t)
	h.home = filepath.Join(t.TempDir(), "100%h fleet")
	if err := os.MkdirAll(h.home, 0o755); err != nil {
		t.Fatal(err)
	}
	h.ok("init")
	escaped := strings.ReplaceAll(h.home, "%", "%%")
	watch := h.ok("unit", "watch")
	for _, want := range []string{"Description=Hand watcher for 100%%h fleet (" + escaped + ")\n", `Environment="HAND_HOME=` + escaped + `"`} {
		if !strings.Contains(watch, want) {
			t.Fatalf("watch unit missing %q:\n%s", want, watch)
		}
	}
	for _, name := range []string{`say "hi"`, `back\slash`, "line\nExecStartPre=/bin/false"} {
		h.home = filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(h.home, 0o755); err != nil {
			t.Fatal(err)
		}
		h.ok("init")
		if out, errOut, code := h.run("unit", "watch"); code != 2 || out != "" || !strings.Contains(errOut, "systemd unit") {
			t.Fatalf("home %q: code=%d stdout=%q stderr=%q", name, code, out, errOut)
		}
	}
}

func TestBoardUnitIsGlobal(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_HOME"] = ""
	exe, _ := os.Executable()
	board := h.ok("unit", "board")
	for _, want := range []string{"Description=Hand board\n", `ExecStart="` + exe + `" board --addr 127.0.0.1:7777` + "\n", "Restart=on-failure", "WantedBy=default.target"} {
		if !strings.Contains(board, want) {
			t.Fatalf("board unit missing %q:\n%s", want, board)
		}
	}
	if strings.Contains(board, `Environment="HAND_HOME=`) {
		t.Fatalf("board unit names a fleet home:\n%s", board)
	}
	if _, errOut, code := h.run("unit", "watch"); code != 3 || !strings.Contains(errOut, "not inside a fleet home") {
		t.Fatalf("watch unit outside a fleet: code=%d stderr=%q", code, errOut)
	}
}

func TestUnitFilesKeepACustomSecondhandHome(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	root, err := filepath.EvalSymlinks(h.vars["SECONDHAND_HOME"])
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range []string{"watch", "board"} {
		if out := h.ok("unit", unit); !strings.Contains(out, `Environment="SECONDHAND_HOME=`+root+"\"\n") {
			t.Fatalf("%s unit does not keep the root:\n%s", unit, out)
		}
	}
	delete(h.vars, "SECONDHAND_HOME")
	if out := h.ok("unit", "board"); strings.Contains(out, "SECONDHAND_HOME") {
		t.Fatalf("board unit names a root nobody set:\n%s", out)
	}
}

func TestUnitFilesKeepTheCallersPath(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.vars["PATH"] = "/opt/tools/bin:/usr/bin"
	for _, unit := range []string{"watch", "board"} {
		if out := h.ok("unit", unit); !strings.Contains(out, `Environment="PATH=/opt/tools/bin:/usr/bin"`+"\n") {
			t.Fatalf("%s unit does not keep PATH:\n%s", unit, out)
		}
	}
	h.vars["PATH"] = `/opt/a"b`
	if _, errOut, code := h.run("unit", "board"); code != 2 || !strings.Contains(errOut, "systemd unit") {
		t.Fatalf("quoted PATH: code=%d stderr=%q", code, errOut)
	}
}
