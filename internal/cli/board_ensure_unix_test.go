//go:build unix

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func userManagerBoard(t *testing.T, loadState string) (*openFixture, string) {
	t.Helper()
	fx := openWithoutBoard(t)
	runtime, bin := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(runtime, "systemd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime, "systemd", "private"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "calls.log")
	spawn := "\"" + exe + "\" board >/dev/null 2>&1 &\n"
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
	return fx, log
}

func TestOpenStartsTheInstalledBoardUnit(t *testing.T) {
	fx, log := userManagerBoard(t, "loaded")
	out := fx.h.ok("open", "--print")
	if !strings.Contains(out, "url:") {
		t.Fatalf("open --print = %q", out)
	}
	got, _ := os.ReadFile(log)
	want := "systemctl --user show -p LoadState --value secondhand-board.service\nsystemctl --user start secondhand-board.service\n"
	if string(got) != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if n := boardSpawns(fx.h); n != 1 {
		t.Fatalf("boards started = %d, want 1", n)
	}
}

func TestOpenStartsATransientBoardUnit(t *testing.T) {
	fx, log := userManagerBoard(t, "not-found")
	out := fx.h.ok("open", "--print")
	if !strings.Contains(out, "url:") {
		t.Fatalf("open --print = %q", out)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(log)
	want := "systemd-run --user --unit=secondhand-board --collect -p Restart=on-failure -p RestartSec=5 -p RestartPreventExitStatus=3 --setenv=PATH=" + fx.h.vars["PATH"] +
		" --setenv=SECONDHAND_HOME=" + fx.h.vars["SECONDHAND_HOME"] + " " + exe + " board\n"
	if !strings.Contains(string(got), want) || strings.Contains(string(got), "--user start") {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if n := boardSpawns(fx.h); n != 1 {
		t.Fatalf("boards started = %d, want 1", n)
	}
}
