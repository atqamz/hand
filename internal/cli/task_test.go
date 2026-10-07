package cli_test

import (
	"strings"
	"testing"
)

func initWithProject(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "hand", handRepo)
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
	if !strings.Contains(show, "status: active") || !strings.Contains(show, "goal: users can log in") || !strings.Contains(show, "attempt: none") || !strings.Contains(show, "report: none") {
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

func TestBadListFiltersAreUsageErrors(t *testing.T) {
	h := initWithProject(t)
	for _, args := range [][]string{
		{"task", "list", "--status", "actve"},
		{"task", "list", "--limit", "0"},
		{"decision", "list", "--limit", "-1"},
	} {
		if _, _, code := h.run(args...); code != 2 {
			t.Fatalf("%q code = %d, want 2", args, code)
		}
	}
}

func TestTransitionUsageNamesTheCommand(t *testing.T) {
	h := initWithProject(t)
	if _, errOut, code := h.run("task", "start"); code != 2 || !strings.Contains(errOut, "task start:") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestTaskListSearchesFinishedTasks(t *testing.T) {
	h := initWithProject(t)
	for _, title := range []string{"Fix login", "Write docs", "Fix search"} {
		h.ok("task", "add", "hand", title)
	}
	h.ok("task", "start", "t1")
	h.ok("task", "done", "t1")
	h.ok("task", "abandon", "t2")
	list := h.ok("task", "list", "--status", "done,abandoned")
	if !strings.Contains(list, "tasks[2]{id,project,status,title,finished}:") || !strings.Contains(list, "t1,hand,done,Fix login,20") || !strings.Contains(list, "t2,hand,abandoned,Write docs,20") {
		t.Fatalf("finished list = %q", list)
	}
	if out := h.ok("task", "list", "--status", "done,abandoned", "--q", "FIX login"); !strings.Contains(out, "tasks[1]") || !strings.Contains(out, "t1,") {
		t.Fatalf("--q = %q", out)
	}
	if out := h.ok("task", "list", "--status", "done", "--since", "2999-01-01"); !strings.Contains(out, "tasks[0]") {
		t.Fatalf("--since = %q", out)
	}
	if out := h.ok("task", "list", "--status", "done", "--until", "2000-01-01"); !strings.Contains(out, "tasks[0]") {
		t.Fatalf("--until = %q", out)
	}
	if out := h.ok("task", "list"); !strings.Contains(out, "{id,project,status,title}:") || !strings.Contains(out, "t3,hand,inbox,Fix search") {
		t.Fatalf("default list = %q", out)
	}
	if _, _, code := h.run("task", "list", "--since", "yesterday"); code != 2 {
		t.Fatalf("bad --since code = %d, want 2", code)
	}
}
