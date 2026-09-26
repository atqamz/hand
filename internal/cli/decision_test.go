package cli_test

import (
	"strings"
	"testing"
)

func TestDecisionFlowThroughCLI(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	if out := h.ok("decision", "ask", "t1", "Keep the old cookie name?"); !strings.Contains(out, "decision: d1") {
		t.Fatalf("ask = %q", out)
	}
	list := h.ok("decision", "list")
	if !strings.Contains(list, "decisions[1]{id,task,question}:") || !strings.Contains(list, "d1,t1,Keep the old cookie name?") {
		t.Fatalf("list = %q", list)
	}
	if show := h.ok("task", "show", "t1"); !strings.Contains(show, "open_decisions[1]") {
		t.Fatalf("task show = %q", show)
	}
	if out := h.ok("decision", "answer", "d1", "yes"); !strings.Contains(out, "status: answered") {
		t.Fatalf("answer = %q", out)
	}
	_, errOut, code := h.run("decision", "answer", "d1", "no")
	if code != 3 || !strings.Contains(errOut, "decision d1 is answered") {
		t.Fatalf("repeat answer code=%d stderr=%q", code, errOut)
	}
}
