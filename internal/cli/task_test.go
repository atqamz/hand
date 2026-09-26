package cli_test

import (
	"strings"
	"testing"
)

func initWithProject(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "hand", "/home/me/hand")
	return h
}

func TestTaskLifecycleThroughCLI(t *testing.T) {
	h := initWithProject(t)
	out := h.ok("task", "add", "--goal", "users can log in", "hand", "Fix login")
	if !strings.Contains(out, "task: t1") || !strings.Contains(out, "status: inbox") {
		t.Fatalf("task add = %q", out)
	}
	h.ok("task", "start", "t1")
	show := h.ok("task", "show", "1")
	if !strings.Contains(show, "status: active") || !strings.Contains(show, "goal: users can log in") {
		t.Fatalf("task show = %q", show)
	}
	list := h.ok("task", "list")
	if !strings.Contains(list, "tasks[1]{id,project,status,title}:") || !strings.Contains(list, "t1,hand,active,Fix login") {
		t.Fatalf("task list = %q", list)
	}
}

func TestRepeatedTransitionIsRefusedWithExit3(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	h.ok("task", "start", "t1")
	h.ok("task", "done", "t1")
	_, errOut, code := h.run("task", "done", "t1")
	if code != 3 || !strings.Contains(errOut, "task t1 is done and cannot become done") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestCommandsOutsideAHomeAreRefused(t *testing.T) {
	h := newHarness(t)
	_, errOut, code := h.run("task", "list")
	if code != 3 || !strings.Contains(errOut, "run `hand init`") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestBadIDIsUsageError(t *testing.T) {
	h := initWithProject(t)
	if _, _, code := h.run("task", "show", "tx"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
