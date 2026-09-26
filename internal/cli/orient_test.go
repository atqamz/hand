package cli_test

import (
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
