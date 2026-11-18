package cli_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPlanSetFromFileAndShow(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	file := filepath.Join(t.TempDir(), "plan.md")
	body := "1. reproduce\n   - nested   step\n\treturn y\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := h.ok("plan", "set", "--body-file", file, "t1"); !strings.Contains(out, "plan: p1") {
		t.Fatalf("plan set = %q", out)
	}
	show := h.ok("plan", "show", "t1")
	if !strings.Contains(show, "plan: p1") || !strings.Contains(show, "body: "+strconv.Quote(body)) {
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

func TestPlanSetUnreadableBodyFileIsUsageError(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	if _, _, code := h.run("plan", "set", "--body-file", filepath.Join(t.TempDir(), "missing.md"), "t1"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestTaskAndPlanShowBoundTheirText(t *testing.T) {
	h := initWithProject(t)
	goal := strings.Repeat(strings.Repeat("g", 99)+"\n", 100)
	h.ok("task", "add", "--goal", goal, "hand", "Fix login")
	file := filepath.Join(t.TempDir(), "plan.md")
	plan := strings.Repeat(strings.Repeat("p", 99)+"\n", 200)
	if err := os.WriteFile(file, []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	h.ok("plan", "set", "--body-file", file, "t1")
	for _, c := range []struct {
		args []string
		name string
		full string
		max  int
	}{
		{[]string{"task", "show"}, "goal", goal, 4 << 10},
		{[]string{"plan", "show"}, "body", plan, 8 << 10},
	} {
		cut := field(h.ok(append(c.args, "t1")...), c.name)
		if !strings.Contains(cut, "bytes omitted") || !strings.Contains(cut, c.args[0]+" show --full t1") || len(cut) > c.max+200 {
			t.Fatalf("%v: %d bytes, %q", c.args, len(cut), cut[max(0, len(cut)-120):])
		}
		if got := field(h.ok(append(c.args, "--full", "t1")...), c.name); got != c.full {
			t.Fatalf("%v --full: %d bytes, want %d", c.args, len(got), len(c.full))
		}
		h.name = "hand-foo"
		if named := field(h.ok(append(c.args, "t1")...), c.name); !strings.Contains(named, "`hand-foo "+c.args[0]+" show --full t1`") {
			t.Fatalf("%v: note does not name the invoked binary: %q", c.args, named[max(0, len(named)-120):])
		}
		h.name = ""
	}
}
