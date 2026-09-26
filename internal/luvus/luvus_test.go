package luvus_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
