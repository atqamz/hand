package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanSetFromFileAndShow(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	file := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(file, []byte("1. reproduce\n2. fix cookie\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := h.ok("plan", "set", "--body-file", file, "t1"); !strings.Contains(out, "plan: p1") {
		t.Fatalf("plan set = %q", out)
	}
	show := h.ok("plan", "show", "t1")
	if !strings.Contains(show, "plan: p1") || !strings.Contains(show, "  - 1. reproduce") {
		t.Fatalf("plan show = %q", show)
	}
	if task := h.ok("task", "show", "t1"); !strings.Contains(task, "plan: p1") {
		t.Fatalf("task show = %q", task)
	}
}

func TestPlanSetNeedsExactlyOneBodySource(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	if _, _, code := h.run("plan", "set", "t1"); code != 2 {
		t.Fatalf("no body code = %d, want 2", code)
	}
}
