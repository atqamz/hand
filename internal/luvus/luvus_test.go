package luvus_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
)

func sock(t *testing.T) string { return filepath.Join(t.TempDir(), "uhp.sock") }

func vars(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestSocketPath(t *testing.T) {
	if got := luvus.SocketPath(vars(map[string]string{"HOME": "/home/me"}), "hand"); got != "/home/me/.luvus/sessions/hand/luvus.sock" {
		t.Fatalf("default = %s", got)
	}
	if got := luvus.SocketPath(vars(map[string]string{"HAND_LUVUS_SOCKET": "/x.sock", "HOME": "/home/me"}), "hand"); got != "/x.sock" {
		t.Fatalf("override = %s", got)
	}
	long := "/tmp/claude-1000/-home-atqa-secondhand-dev/f6218d56-7092-4541-a758-a96c0a0d8ec3/scratchpad/luvus-ref/home"
	want := fmt.Sprintf("/tmp/luvus-%d/ad6a6b2b37a55ba4-api.sock", os.Geteuid())
	if got := luvus.SocketPath(vars(map[string]string{"LUVUS_HOME": long}), "hand"); got != want {
		t.Fatalf("long = %s, want %s", got, want)
	}
}

func TestCallSendsOneEnvelopeAndDecodesReplies(t *testing.T) {
	srv := fakeuhp.Start(t, sock(t))
	srv.Handle("ping", func(json.RawMessage) (any, error) {
		return map[string]any{"type": "pong", "version": "0.14.2"}, nil
	})
	srv.Handle("boom", func(json.RawMessage) (any, error) {
		return nil, fakeuhp.Fail{Code: "not_found", Message: "pane not found"}
	})
	c := luvus.Client{Socket: srv.Socket}
	var pong struct {
		Version string `json:"version"`
	}
	if err := c.Call(context.Background(), "ping", nil, &pong); err != nil || pong.Version != "0.14.2" {
		t.Fatalf("ping = %+v, %v", pong, err)
	}
	err := c.Call(context.Background(), "boom", map[string]string{"pane": "9"}, nil)
	if luvus.Code(err) != "not_found" || !strings.Contains(err.Error(), "pane not found") {
		t.Fatalf("boom err = %v", err)
	}
	if got := string(srv.Calls("boom")[0]); got != `{"pane":"9"}` {
		t.Fatalf("params = %s", got)
	}
	if got := string(srv.Calls("ping")[0]); got != `{}` {
		t.Fatalf("nil params = %s", got)
	}
	if err := (luvus.Client{Socket: sock(t)}).Call(context.Background(), "ping", nil, nil); !errors.Is(err, luvus.ErrUnreachable) {
		t.Fatalf("no server err = %v", err)
	}
}

func TestCheckPinsProtocolAndMethods(t *testing.T) {
	srv := fakeuhp.Start(t, sock(t))
	c := luvus.Client{Socket: srv.Socket}
	caps, err := c.Check(context.Background())
	if err != nil || caps.ServerGeneration != "gen-1" {
		t.Fatalf("check = %+v, %v", caps, err)
	}
	srv.Handle("uhp.capabilities", func(json.RawMessage) (any, error) {
		return map[string]any{"protocol": map[string]any{"name": "luvus-uhp", "major": 2}, "methods": luvus.Required}, nil
	})
	if _, err := c.Check(context.Background()); !errors.Is(err, luvus.ErrIncompatible) {
		t.Fatalf("major 2 err = %v", err)
	}
	srv.Handle("uhp.capabilities", func(json.RawMessage) (any, error) {
		return map[string]any{"protocol": map[string]any{"name": "luvus-uhp", "major": 1}, "methods": luvus.Required[1:]}, nil
	})
	if _, err := c.Check(context.Background()); !errors.Is(err, luvus.ErrIncompatible) {
		t.Fatalf("missing method err = %v", err)
	}
}

func TestEnsureStartsTheServerOnlyWhenUnreachable(t *testing.T) {
	path := sock(t)
	c := luvus.Client{Socket: path}
	starts := 0
	start := func() error {
		starts++
		fakeuhp.Start(t, path)
		return nil
	}
	if _, err := luvus.Ensure(context.Background(), c, start); err != nil || starts != 1 {
		t.Fatalf("first ensure: starts=%d err=%v", starts, err)
	}
	if _, err := luvus.Ensure(context.Background(), c, start); err != nil || starts != 1 {
		t.Fatalf("second ensure: starts=%d err=%v", starts, err)
	}
}

func TestScrubDropsAgentAndPaneVariables(t *testing.T) {
	got := luvus.Scrub([]string{"HOME=/h", "CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CODEX_SANDBOX=seatbelt", "LUVUS_PANE_ID=3", "LUVUS_SOCKET_PATH=/s", "LUVUS_HOME=/l", "PATH=/bin", "CLAUDECODEX=keep", "CODEX_HOME=/c", "HAND_HOME=/fleet"})
	want := []string{"HOME=/h", "LUVUS_HOME=/l", "PATH=/bin", "CLAUDECODEX=keep", "CODEX_HOME=/c"}
	if !slices.Equal(got, want) {
		t.Fatalf("scrub = %q", got)
	}
}

func TestCallGivesUpOnASilentServer(t *testing.T) {
	srv := fakeuhp.Start(t, sock(t))
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv.Handle("hang", func(json.RawMessage) (any, error) {
		<-release
		return nil, nil
	})
	start := time.Now()
	err := luvus.Client{Socket: srv.Socket, Timeout: 200 * time.Millisecond}.Call(context.Background(), "hang", nil, nil)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("call = %v after %s", err, time.Since(start))
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
	bin, calls := fakeTools(t, map[string]string{"systemd-run": "exit 0", "systemctl": "exit 1", "luvus": "exit 0"})
	dir := t.TempDir()
	env := []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + userManager(t), "HOME=/h", "CLAUDECODE=1", "INVOCATION_ID=abc", "JOURNAL_STREAM=8:9"}
	if err := luvus.StartServer(context.Background(), filepath.Join(bin, "luvus"), "secondhand-f1", "secondhand-luvus-f1", dir, env); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || lines[0] != "systemctl [--user] [reset-failed] [secondhand-luvus-f1.service]" {
		t.Fatalf("calls = %q", lines)
	}
	run := lines[1]
	for _, want := range []string{"systemd-run [--user] [--unit=secondhand-luvus-f1]", "[--working-directory=" + dir + "]", "[-p] [Type=forking]", "[-p] [Restart=on-failure]", "[-E] [HOME]", "[-E] [PATH]",
		"[" + filepath.Join(bin, "luvus") + "] [--session] [secondhand-f1] [server] [start]"} {
		if !strings.Contains(run, want) {
			t.Fatalf("systemd-run call missing %q:\n%s", want, run)
		}
	}
	for _, not := range []string{"CLAUDECODE", "INVOCATION_ID", "JOURNAL_STREAM", "=/h", "PATH="} {
		if strings.Contains(run, not) {
			t.Fatalf("systemd-run call passes %s:\n%s", not, run)
		}
	}
	if !strings.HasSuffix(run, "[server] [start]") {
		t.Fatalf("luvus must be the command systemd-run starts:\n%s", run)
	}
}

func TestStartServerWithoutAUserManagerRunsLuvusDirectly(t *testing.T) {
	bin, calls := fakeTools(t, map[string]string{"systemd-run": "exit 0", "luvus": "exit 0"})
	env := []string{"PATH=" + bin, "HOME=/h"}
	if err := luvus.StartServer(context.Background(), filepath.Join(bin, "luvus"), "secondhand-f1", "secondhand-luvus-f1", t.TempDir(), env); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(calls); string(b) != "luvus [--session] [secondhand-f1] [server] [start]\n" {
		t.Fatalf("calls = %q", b)
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

func TestStartServerFallsBackWhenTheManagerIsGone(t *testing.T) {
	bin, calls := fakeTools(t, map[string]string{"systemd-run": "echo 'Failed to connect to user scope bus via local transport: No such file or directory' >&2; exit 1", "systemctl": "exit 1", "luvus": "exit 0"})
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
