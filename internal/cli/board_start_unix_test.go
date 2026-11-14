//go:build unix

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func unitShim(t *testing.T, fx *boardStartFixture, loadState string) string {
	t.Helper()
	runtime, bin := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(runtime, "systemd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime, "systemd", "private"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "calls.log")
	spawn := "\"" + fx.bin + "\" >/dev/null 2>&1 &\n"
	tools := map[string]string{
		"systemd-run": "#!/bin/sh\nprintf 'systemd-run %s\\n' \"$*\" >> " + log + "\n" + spawn,
		"systemctl":   "#!/bin/sh\nprintf 'systemctl %s\\n' \"$*\" >> " + log + "\ncase \"$*\" in\n*show*) echo " + loadState + ";;\n*start*) " + spawn + ";;\nesac\n",
	}
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fx.h.vars["PATH"] += string(os.PathListSeparator) + bin
	fx.h.vars["XDG_RUNTIME_DIR"] = runtime
	return log
}

func TestBoardStartStartsTheInstalledUnit(t *testing.T) {
	fx := newBoardStartFixture(t)
	log := unitShim(t, fx, "loaded")
	out, errOut, code := fx.h.run("board", "start")
	if code != 0 || field(out, "board") != "running" {
		t.Fatalf("code=%d out=%q stderr=%q", code, out, errOut)
	}
	got, _ := os.ReadFile(log)
	want := "systemctl --user show -p LoadState --value secondhand-board.service\nsystemctl --user start secondhand-board.service\n"
	if string(got) != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if n := starts(t, fx.started); n != 1 {
		t.Fatalf("boards started = %d, want 1", n)
	}
}

func TestBoardStartStartsATransientUnitWithTheRecordedPath(t *testing.T) {
	fx := newBoardStartFixture(t)
	log := unitShim(t, fx, "not-found")
	root := fx.h.vars["SECONDHAND_HOME"]
	if err := os.WriteFile(filepath.Join(root, "board.path"), []byte("/opt/recorded/bin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := fx.h.run("board", "start")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	got, _ := os.ReadFile(log)
	if !strings.Contains(string(got), "--setenv=PATH=/opt/recorded/bin") || strings.Contains(string(got), "--user start") {
		t.Fatalf("calls = %q", got)
	}
	if n := starts(t, fx.started); n != 1 {
		t.Fatalf("boards started = %d, want 1", n)
	}
}
