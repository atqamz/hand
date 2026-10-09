//go:build unix

package luvus_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
)

func TestSocketPath(t *testing.T) {
	if got := luvus.SocketPath(vars(map[string]string{"HOME": "/home/me"}), "hand"); got != "/home/me/.luvus/sessions/hand/luvus.sock" {
		t.Fatalf("default = %s", got)
	}
	if got := luvus.SocketPath(vars(map[string]string{"HAND_LUVUS_SOCKET": "/x.sock", "HOME": "/home/me"}), "hand"); got != "/x.sock" {
		t.Fatalf("override = %s", got)
	}
}

func fakeTools(t *testing.T, names map[string]string) (string, string) {
	t.Helper()
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	for name, exit := range names {
		script := "#!/bin/sh\nprintf '%s' \"" + name + "\" >> " + calls + "\nfor a in \"$@\"; do printf ' [%s]' \"$a\" >> " + calls + "; done\necho >> " + calls + "\n" + exit + "\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin, calls
}

func userManager(t *testing.T) string {
	t.Helper()
	run := t.TempDir()
	if err := os.MkdirAll(filepath.Join(run, "systemd"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "systemd", "private"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestStartServerUsesAScopeWithAUserManager(t *testing.T) {
	envTool, err := exec.LookPath("env")
	if err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(t.TempDir(), "env")
	bin, calls := fakeTools(t, map[string]string{"systemd-run": envTool + " > " + envFile, "luvus": "exit 1"})
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t), "HOME=/h", "KEEP_ME=yes", "CLAUDECODE=1"}
	pinned := filepath.Join(bin, "luvus")
	if err := luvus.StartServer(context.Background(), pinned, "secondhand-f1", t.TempDir(), env); err != nil {
		t.Fatal(err)
	}
	want := "systemd-run [--user] [--scope] [--collect] [--quiet] [--description=Luvus server for secondhand-f1] [--] [" + pinned + "] [--session] [secondhand-f1] [server] [start]\n"
	if b, _ := os.ReadFile(calls); string(b) != want {
		t.Fatalf("calls = %q, want %q", b, want)
	}
	got, _ := os.ReadFile(envFile)
	for _, line := range []string{"HOME=/h", "KEEP_ME=yes", "XDG_RUNTIME_DIR=" + env[1][len("XDG_RUNTIME_DIR="):]} {
		if !strings.Contains("\n"+string(got), "\n"+line+"\n") {
			t.Fatalf("scope environment lacks %s:\n%s", line, got)
		}
	}
	if strings.Contains(string(got), "CLAUDECODE") {
		t.Fatalf("scope environment keeps CLAUDECODE:\n%s", got)
	}
}

func TestStartServerFallsBackWhenTheScopeFails(t *testing.T) {
	bin, calls := fakeTools(t, map[string]string{"systemd-run": "echo 'Failed to connect to user scope bus' >&2; exit 1", "luvus": "exit 0"})
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t)}
	pinned := filepath.Join(bin, "luvus")
	if err := luvus.StartServer(context.Background(), pinned, "secondhand-f1", t.TempDir(), env); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "systemd-run [--user] [--scope]") || lines[1] != "luvus [--session] [secondhand-f1] [server] [start]" {
		t.Fatalf("calls = %q", lines)
	}
}

func TestStartServerReportsTheDirectStartWhenBothFail(t *testing.T) {
	bin, _ := fakeTools(t, map[string]string{"systemd-run": "exit 1", "luvus": "echo 'no luvus here' >&2; exit 1"})
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t)}
	err := luvus.StartServer(context.Background(), filepath.Join(bin, "luvus"), "secondhand-f1", t.TempDir(), env)
	if err == nil || !strings.Contains(err.Error(), "no luvus here") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartServerStopsWhenItsContextEnds(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := fakeTools(t, map[string]string{"systemd-run": sleep + " 30", "luvus": sleep + " 30"})
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	if err := luvus.StartServer(ctx, filepath.Join(bin, "luvus"), "secondhand-f1", t.TempDir(), env); err == nil {
		t.Fatal("a cancelled start succeeded")
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("start ignored its context for %s", took)
	}
}
