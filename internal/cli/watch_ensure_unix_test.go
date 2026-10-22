//go:build unix

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureWatcherUsesTheUserManagerThenFallsBack(t *testing.T) {
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
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\necho 'systemd-run boom' >&2\nexit 1\n"
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
	want := "--user --unit=secondhand-watch-" + field(h.ok("init", h.home), "id") + " --collect -p Restart=on-failure -p RestartSec=5 -p RestartPreventExitStatus=3 --setenv=HAND_HOME=" + h.home + " --setenv=PATH=" + path +
		" --setenv=SECONDHAND_HOME=" + h.vars["SECONDHAND_HOME"] + " --setenv=HAND_LUVUS_SOCKET=" + h.vars["HAND_LUVUS_SOCKET"] + " --setenv=HOME=" + h.vars["HOME"] + " " + exe + " watch"
	if !strings.Contains(string(got), want) {
		t.Fatalf("systemd-run got %q, want %q", got, want)
	}
	eventually(t, func() bool { return watchSpawns(h.home) == 1 })
	if b, _ := os.ReadFile(filepath.Join(h.home, "watch.log")); !strings.Contains(string(b), "systemd-run boom") {
		t.Fatalf("watch.log = %q, want the systemd-run output", b)
	}
}
