package luvus_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
)

func TestMain(m *testing.M) {
	fakebin.Main(map[string]func([]string) int{"tool": func(args []string) int {
		p := fakebin.Params()
		line := p["name"]
		for _, a := range args {
			line += " [" + a + "]"
		}
		fakebin.Append(p["calls"], line)
		return 0
	}, "luvus": func(args []string) int {
		p := fakebin.Params()
		v := p["version"]
		if exe, _ := os.Executable(); p["copy"] != "" && strings.HasPrefix(filepath.Base(exe), ".luvus-") {
			v = p["copy"]
		}
		if v != "" && len(args) > 0 && args[0] == "--version" {
			fmt.Println("luvus " + v)
			return 0
		}
		if p["out"] != "" {
			fmt.Println(p["out"])
		}
		code, _ := strconv.Atoi(p["exit"])
		return code
	}})
	os.Exit(m.Run())
}

func sock(t *testing.T) string { return filepath.Join(t.TempDir(), "uhp.sock") }

func vars(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

const longLuvusHome = "/home/operator/.local/share/hand-tests/a-luvus-home-long-enough-to-pass-one-hundred-bytes"

func socketUnder(t *testing.T, root string) {
	t.Helper()
	want := fmt.Sprintf("%s/luvus-%d/c99ecb6d59337400-api.sock", root, os.Geteuid())
	if got := luvus.SocketPath(vars(map[string]string{"LUVUS_HOME": longLuvusHome}), "hand"); got != want {
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

func TestDialRelistsOnce(t *testing.T) {
	srv := fakeuhp.Start(t, sock(t))
	var stale []string
	c := luvus.Client{Socket: sock(t), Relist: func(_ context.Context, old string) (string, error) {
		stale = append(stale, old)
		return srv.Socket, nil
	}}
	if _, err := c.Check(context.Background()); err != nil || len(stale) != 1 || stale[0] != c.Socket {
		t.Fatalf("check = %v after relisting %q", err, stale)
	}
	stale = nil
	dead := sock(t)
	c.Relist = func(_ context.Context, old string) (string, error) {
		stale = append(stale, old)
		return dead, nil
	}
	if _, err := c.Check(context.Background()); !errors.Is(err, luvus.ErrUnreachable) || len(stale) != 1 {
		t.Fatalf("check = %v after relisting %q", err, stale)
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

func TestStartServerWithoutAUserManagerRunsLuvusDirectly(t *testing.T) {
	bin, calls := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	fakebin.Install(t, bin, "systemd-run", "tool", map[string]string{"name": "systemd-run", "calls": calls})
	exe := fakebin.Install(t, bin, "luvus", "tool", map[string]string{"name": "luvus", "calls": calls})
	env := []string{"PATH=" + bin, "HOME=/h"}
	if err := luvus.StartServer(context.Background(), exe, "secondhand-f1", "secondhand-luvus-f1", t.TempDir(), env); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(calls); string(b) != "luvus [--session] [secondhand-f1] [server] [start]\n" {
		t.Fatalf("calls = %q", b)
	}
}

func TestAttachArgv(t *testing.T) {
	if got := luvus.AttachArgv("/bin/luvus", "secondhand-f1", "7"); !slices.Equal(got, []string{"/bin/luvus", "--session", "secondhand-f1", "attach", "7"}) {
		t.Fatalf("pane argv = %q", got)
	}
	if got := luvus.AttachArgv("/bin/luvus", "secondhand-f1", ""); !slices.Equal(got, []string{"/bin/luvus", "session", "attach", "secondhand-f1"}) {
		t.Fatalf("session argv = %q", got)
	}
}
