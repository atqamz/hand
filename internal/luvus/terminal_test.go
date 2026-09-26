package luvus_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
)

func TestCreateSendsExactArgvUnfocusedWithoutEnv(t *testing.T) {
	srv := fakeuhp.Start(t, sock(t))
	srv.Handle("terminal.backend.create", func(json.RawMessage) (any, error) {
		return map[string]any{"type": "terminal_backend_created", "server_generation": "g", "terminal_id": "tid", "pane_id": "2", "cwd": "/w", "root_process": map[string]any{"pid": 7, "start_marker": "88"}}, nil
	})
	c := luvus.Client{Socket: srv.Socket}
	term, err := c.Create(context.Background(), "/w", "hand-a1", []string{"/bin/claude", "--model", "sonnet", "fix it"})
	want := luvus.Terminal{ServerGeneration: "g", TerminalID: "tid", PaneID: "2", CWD: "/w", Root: luvus.Root{PID: 7, StartMarker: "88"}}
	if err != nil || term != want {
		t.Fatalf("create = %+v, %v", term, err)
	}
	got := string(srv.Calls("terminal.backend.create")[0])
	if got != `{"command":["/bin/claude","--model","sonnet","fix it"],"cwd":"/w","focus":false,"label":"hand-a1","placement":{"kind":"workspace"}}` {
		t.Fatalf("create params = %s", got)
	}
}

func TestLocatorCallsCarryTheExpectedRoot(t *testing.T) {
	srv := fakeuhp.Start(t, sock(t))
	srv.Handle("terminal.backend.validate", func(json.RawMessage) (any, error) { return map[string]any{"state": "alive"}, nil })
	srv.Handle("terminal.backend.close", func(json.RawMessage) (any, error) {
		return nil, fakeuhp.Fail{Code: "stale_terminal", Message: "terminal is gone"}
	})
	c := luvus.Client{Socket: srv.Socket}
	term := luvus.Terminal{ServerGeneration: "g", TerminalID: "tid", PaneID: "2", Root: luvus.Root{PID: 7, StartMarker: "88"}}
	if state, err := c.Validate(context.Background(), term); err != nil || state != "alive" {
		t.Fatalf("validate = %s, %v", state, err)
	}
	if err := c.Close(context.Background(), term); luvus.Code(err) != "stale_terminal" {
		t.Fatalf("close err = %v", err)
	}
	want := `{"server_generation":"g","terminal_id":"tid","pane_id":"2","expected_root":{"pid":7,"start_marker":"88"}}`
	for _, m := range []string{"terminal.backend.validate", "terminal.backend.close"} {
		if got := string(srv.Calls(m)[0]); got != want {
			t.Fatalf("%s params = %s", m, got)
		}
	}
}

func TestInventoryStampsTheGenerationOnEveryTerminal(t *testing.T) {
	srv := fakeuhp.Start(t, sock(t))
	srv.Handle("terminal.backend.inventory", func(json.RawMessage) (any, error) {
		return map[string]any{"server_generation": "g", "terminals": []any{map[string]any{"terminal_id": "a", "pane_id": "1", "cwd": "/w/x", "root_process": map[string]any{"pid": 1, "start_marker": "2"}}}}, nil
	})
	terms, err := luvus.Client{Socket: srv.Socket}.Inventory(context.Background())
	if err != nil || len(terms) != 1 || terms[0].ServerGeneration != "g" || terms[0].CWD != "/w/x" {
		t.Fatalf("inventory = %+v, %v", terms, err)
	}
}

func TestAgentCalls(t *testing.T) {
	srv := fakeuhp.Start(t, sock(t))
	srv.Handle("agent.explain", func(json.RawMessage) (any, error) {
		return map[string]any{"pane": "5", "agent": "codex", "status": "blocked", "state_evidence": map[string]any{"blocked_hint": "Press enter to continue"}}, nil
	})
	srv.Handle("agent.prompt", func(json.RawMessage) (any, error) {
		return nil, fakeuhp.Fail{Code: "agent_not_ready", Message: "no prompt input was queued"}
	})
	srv.Handle("agent.read", func(json.RawMessage) (any, error) {
		return map[string]any{"text": "Trust this folder?", "content_revision": 7, "terminal_id": "tid"}, nil
	})
	srv.Handle("agent.keys", func(json.RawMessage) (any, error) { return map[string]any{"type": "ok"}, nil })
	c := luvus.Client{Socket: srv.Socket}
	ctx := context.Background()
	a, err := c.Explain(ctx, "5")
	if err != nil || a != (luvus.Agent{Pane: "5", Agent: "codex", Status: "blocked", Hint: "Press enter to continue"}) {
		t.Fatalf("explain = %+v, %v", a, err)
	}
	if err := c.Prompt(ctx, "5", "hello"); luvus.Code(err) != "agent_not_ready" {
		t.Fatalf("prompt err = %v", err)
	}
	s, err := c.Read(ctx, "5", 60)
	if err != nil || s.ContentRevision != 7 || s.TerminalID != "tid" || s.Text != "Trust this folder?" {
		t.Fatalf("read = %+v, %v", s, err)
	}
	if err := c.Keys(ctx, "5", []string{"enter"}, 7, "tid"); err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{
		"agent.explain": `{"pane":"5"}`,
		"agent.prompt":  `{"target":"5","text":"hello","wait":false}`,
		"agent.read":    `{"lines":60,"source":"visible","target":"5"}`,
		"agent.keys":    `{"if_content_revision":7,"keys":["enter"],"target":"5","terminal_id":"tid"}`,
	}
	for m, want := range checks {
		if got := string(srv.Calls(m)[0]); got != want {
			t.Fatalf("%s params = %s, want %s", m, got, want)
		}
	}
}

func TestProcStartMarkerReadsFieldTwentyTwo(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "x) y")
	if err := os.WriteFile(bin, src, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	got, err := luvus.ProcStartMarker(pid)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Fields(strings.Replace(string(raw), "(x) y)", "(c)", 1))[21]; got != want {
		t.Fatalf("marker = %q, want %q", got, want)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, err := luvus.ProcStartMarker(pid); err == nil {
		t.Fatal("marker for a reaped process")
	}
}
