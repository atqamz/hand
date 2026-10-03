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

func TestStartServerRunsLuvusInItsOwnUserUnit(t *testing.T) {
	bin, calls := fakeTools(t, map[string]string{"systemd-run": "exit 0", "systemctl": managerEnv, "luvus": "exit 0"})
	dir := t.TempDir()
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t), "HOME=/h", "CLAUDECODE=1", "INVOCATION_ID=abc", "JOURNAL_STREAM=8:9"}
	if err := luvus.StartServer(context.Background(), filepath.Join(bin, "luvus"), "secondhand-f1", "secondhand-luvus-f1", dir, env); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || lines[0] != "systemctl [--user] [reset-failed] [secondhand-luvus-f1.service]" || lines[1] != "systemctl [--user] [show-environment]" {
		t.Fatalf("calls = %q", lines)
	}
	run := lines[2]
	for _, want := range []string{"systemd-run [--user] [--unit=secondhand-luvus-f1]", "[-p] [UnsetEnvironment=CLAUDECODE]", "[-p] [UnsetEnvironment=LUVUS_PANE_ID]", "[--working-directory=" + dir + "]", "[-p] [Type=forking]", "[-p] [Restart=on-failure]", "[-E] [HOME]", "[-E] [PATH]",
		"[" + filepath.Join(bin, "luvus") + "] [--session] [secondhand-f1] [server] [start]"} {
		if !strings.Contains(run, want) {
			t.Fatalf("systemd-run call missing %q:\n%s", want, run)
		}
	}
	for _, not := range []string{"[-E] [CLAUDECODE]", "INVOCATION_ID", "JOURNAL_STREAM", "=/h", "PATH=", "UnsetEnvironment=HOME", "UnsetEnvironment=LUVUS_HOME"} {
		if strings.Contains(run, not) {
			t.Fatalf("systemd-run call passes %s:\n%s", not, run)
		}
	}
	if !strings.HasSuffix(run, "[server] [start]") {
		t.Fatalf("luvus must be the command systemd-run starts:\n%s", run)
	}
}

const managerEnv = `if [ "$2" = show-environment ]; then printf 'HOME=/h\nCLAUDECODE=1\nLUVUS_PANE_ID=3\nLUVUS_HOME=/l\n'; fi; exit 0`

func TestStartServerRefusesAnUnreadableManagerEnvironment(t *testing.T) {
	bin, calls := fakeTools(t, map[string]string{"systemd-run": "exit 0", "systemctl": `if [ "$2" = show-environment ]; then echo 'Access denied' >&2; exit 1; fi; exit 0`, "luvus": "exit 0"})
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t)}
	if err := luvus.StartServer(context.Background(), filepath.Join(bin, "luvus"), "secondhand-f1", "secondhand-luvus-f1", t.TempDir(), env); err == nil || !strings.Contains(err.Error(), "Access denied") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(calls); strings.Contains(string(b), "systemd-run") || strings.Contains(string(b), "luvus [--session]") {
		t.Fatalf("started without knowing what the manager would pass: %q", b)
	}
}

func TestStartServerJoinsAStartAlreadyUnderWay(t *testing.T) {
	bin, calls := fakeTools(t, map[string]string{"systemd-run": "echo 'Unit secondhand-luvus-f1.service was already loaded or has a fragment file.' >&2; exit 1", "systemctl": "exit 0", "luvus": "exit 1"})
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t)}
	if err := luvus.StartServer(context.Background(), filepath.Join(bin, "luvus"), "secondhand-f1", "secondhand-luvus-f1", t.TempDir(), env); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(calls); !strings.HasSuffix(string(b), "systemctl [--user] [start] [secondhand-luvus-f1.service]\n") || strings.Contains(string(b), "luvus [--session]") {
		t.Fatalf("calls = %q", b)
	}
}

func TestStartServerJoinsALoadedUnitOfTheSameBinary(t *testing.T) {
	bin, calls := fakeTools(t, map[string]string{"systemd-run": "echo 'Unit secondhand-luvus-f1.service was already loaded or has a fragment file.' >&2; exit 1", "luvus": "exit 1"})
	exec := filepath.Join(bin, "luvus")
	systemctl := "#!/bin/sh\necho \"systemctl $*\" >> " + calls + "\ncase \"$*\" in *ExecStart*) echo '{ path=" + exec + " ; argv[]=" + exec + " --session secondhand-f1 server start ; }';; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(systemctl), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t)}
	if err := luvus.StartServer(context.Background(), exec, "secondhand-f1", "secondhand-luvus-f1", t.TempDir(), env); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(calls); !strings.HasSuffix(string(b), "systemctl --user start secondhand-luvus-f1.service\n") || strings.Contains(string(b), "--user stop") {
		t.Fatalf("calls = %q", b)
	}
}

func TestStartServerReplacesAUnitThatRunsAnotherBinary(t *testing.T) {
	state := filepath.Join(t.TempDir(), "loaded")
	bin, calls := fakeTools(t, map[string]string{"systemd-run": "if [ -e " + state + " ]; then exit 0; fi\n: > " + state + "\necho 'Unit secondhand-luvus-f1.service was already loaded or has a fragment file.' >&2; exit 1", "luvus": "exit 1"})
	systemctl := "#!/bin/sh\necho \"systemctl $*\" >> " + calls + "\ncase \"$*\" in *ExecStart*) echo '{ path=/usr/bin/luvus ; argv[]=/usr/bin/luvus --session secondhand-f1 server start ; }';; *LoadState*) echo not-found;; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(systemctl), 0o755); err != nil {
		t.Fatal(err)
	}
	pinned := filepath.Join(bin, "luvus")
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t)}
	if err := luvus.StartServer(context.Background(), pinned, "secondhand-f1", "secondhand-luvus-f1", t.TempDir(), env); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	got := string(b)
	if strings.Contains(got, "--user start secondhand-luvus-f1.service") || !strings.Contains(got, "systemctl --user stop secondhand-luvus-f1.service") || strings.Count(got, "systemd-run") != 2 || !strings.Contains(got, "["+pinned+"] [--session] [secondhand-f1] [server] [start]") {
		t.Fatalf("calls = %q", got)
	}
}

func TestStartServerFallsBackWhenTheManagerIsGone(t *testing.T) {
	bin, calls := fakeTools(t, map[string]string{"systemd-run": "exit 0", "systemctl": "echo 'Failed to connect to user scope bus via local transport: No such file or directory' >&2; exit 1", "luvus": "exit 0"})
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t)}
	if err := luvus.StartServer(context.Background(), filepath.Join(bin, "luvus"), "secondhand-f1", "secondhand-luvus-f1", t.TempDir(), env); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(calls); !strings.HasSuffix(string(b), "luvus [--session] [secondhand-f1] [server] [start]\n") {
		t.Fatalf("calls = %q", b)
	}
}

func TestStartServerWithoutAUnitNameRunsLuvusDirectly(t *testing.T) {
	bin, calls := fakeTools(t, map[string]string{"systemd-run": "exit 0", "systemctl": "exit 0", "luvus": "exit 0"})
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t)}
	if err := luvus.StartServer(context.Background(), filepath.Join(bin, "luvus"), "secondhand-f1", "", t.TempDir(), env); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(calls); string(b) != "luvus [--session] [secondhand-f1] [server] [start]\n" {
		t.Fatalf("calls = %q", b)
	}
}

func TestStartServerStopsWhenItsContextEnds(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := fakeTools(t, map[string]string{"systemd-run": sleep + " 30", "systemctl": "exit 0", "luvus": "exit 0"})
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	if err := luvus.StartServer(ctx, filepath.Join(bin, "luvus"), "secondhand-f1", "secondhand-luvus-f1", t.TempDir(), env); err == nil || !strings.Contains(err.Error(), "secondhand-luvus-f1") {
		t.Fatalf("a cancelled start = %v", err)
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("start ignored its context for %s", took)
	}
}

func TestStartServerReportsAFailedUserUnit(t *testing.T) {
	bin, calls := fakeTools(t, map[string]string{"systemd-run": "echo 'Job for secondhand-luvus-f1.service failed.' >&2; exit 1", "systemctl": "exit 0", "luvus": "exit 0"})
	defer func() {
		if b, _ := os.ReadFile(calls); strings.Contains(string(b), "[start]") && strings.Contains(string(b), "systemctl [--user] [start]") {
			t.Fatalf("started a loaded unit after a failure that was not a start under way: %q", b)
		}
	}()
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t)}
	err := luvus.StartServer(context.Background(), filepath.Join(bin, "luvus"), "secondhand-f1", "secondhand-luvus-f1", t.TempDir(), env)
	if err == nil || !strings.Contains(err.Error(), "secondhand-luvus-f1") || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartServerReportsASystemctlStartFailure(t *testing.T) {
	bin, _ := fakeTools(t, map[string]string{"systemd-run": "echo 'Unit secondhand-luvus-f1.service was already loaded or has a fragment file.' >&2; exit 1", "systemctl": `if [ "$2" = start ]; then echo 'unit is masked' >&2; exit 1; fi; exit 0`, "luvus": "exit 1"})
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t)}
	err := luvus.StartServer(context.Background(), filepath.Join(bin, "luvus"), "secondhand-f1", "secondhand-luvus-f1", t.TempDir(), env)
	if err == nil || !strings.Contains(err.Error(), "unit is masked") || strings.Contains(err.Error(), "systemd-run") {
		t.Fatalf("err = %v", err)
	}
}
