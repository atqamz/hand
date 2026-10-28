package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopilotIsRefusedAsASupervisor(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	policy := `{"profiles":{"cp":{"harness":"copilot","model":"auto"},"default":{"harness":"claude","model":"sonnet","effort":"low"}}}`
	if err := os.WriteFile(filepath.Join(h.home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"supervisor", "start", "--harness", "copilot", "--model", "auto"},
		{"supervisor", "start", "--profile", "cp"},
	} {
		if _, errOut, code := h.run(args...); code != 2 || !strings.Contains(errOut, "copilot starts workers only; run the supervisor on claude, codex, opencode or agy") {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	startClaudeSupervisor(h)
	for _, args := range [][]string{
		{"supervisor", "switch", "--harness", "copilot", "--model", "auto"},
		{"supervisor", "switch", "--profile", "cp"},
	} {
		if _, errOut, code := h.run(args...); code != 2 || !strings.Contains(errOut, "copilot starts workers only") {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s1", "status: running")
	if events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.stopped"}, 10); err != nil || len(events) != 0 {
		t.Fatalf("a refused switch stopped the supervisor: %+v, %v", events, err)
	}
}
