//go:build unix

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureWatcherUsesTheUserManager(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	enableWatcher(t, h.home)
	runtime, bin := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(runtime, "systemd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime, "systemd", "private"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "systemd-run.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(bin, "systemd-run"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	path := h.vars["PATH"] + string(os.PathListSeparator) + bin
	h.vars["PATH"], h.vars["XDG_RUNTIME_DIR"] = path, runtime
	startClaudeSupervisor(h)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(log)
	want := "--user --unit=secondhand-watch-" + field(h.ok("init", h.home), "id") + " --collect --setenv=HAND_HOME=" + h.home + " --setenv=PATH=" + path + " " + exe + " watch"
	if !strings.Contains(string(got), want) {
		t.Fatalf("systemd-run got %q, want %q", got, want)
	}
	if n := watchSpawns(h.home); n != 0 {
		t.Fatalf("a user manager must replace the detached start, got %d", n)
	}
}
