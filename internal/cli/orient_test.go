package cli_test

import (
	"regexp"
	"strings"
	"testing"
)

func TestOrientCommand(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	out := h.ok("orient")
	if !strings.Contains(out, "tasks: inbox=1 active=0 done=0 abandoned=0") || !strings.Contains(out, "inbox[1]{id,project,title}:") {
		t.Fatalf("orient = %q", out)
	}
}

func TestOrientSinceListsOnlyWhatChanged(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	cursor := regexp.MustCompile(`(?m)^cursor: (\d+)$`).FindStringSubmatch(h.ok("orient"))[1]
	h.ok("task", "add", "hand", "Write docs")
	out := h.ok("orient", "--since", cursor)
	if !strings.Contains(out, "since: "+cursor+"\n") || !strings.Contains(out, "events[1]{seq,kind,task}:") || !strings.Contains(out, "task.added,t2") || strings.Contains(out, "Fix login") || strings.Contains(out, "help[") {
		t.Fatalf("orient --since = %q", out)
	}
	if _, errOut, code := h.run("orient", "--since", "-5"); code == 0 || !strings.Contains(errOut, "run `hand orient`") {
		t.Fatalf("orient --since -5 = %q, exit %d", errOut, code)
	}
	if _, errOut, code := h.run("orient", "--since", "99999"); code == 0 || !strings.Contains(errOut, "run `hand orient`") {
		t.Fatalf("orient --since 99999 = %q, exit %d", errOut, code)
	}
}
