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

func TestDecisionShowReadsTheAnswer(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	h.ok("decision", "ask", "t1", "Keep the old cookie name?")
	h.ok("decision", "answer", "--by", "atqa", "d1", "yes, until v2")
	out := h.ok("decision", "show", "d1")
	for _, want := range []string{"decision: d1", "task: t1", "status: answered", "question: Keep the old cookie name?", `answer: "yes, until v2"`, "answered_by: atqa"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show missing %q in %q", want, out)
		}
	}
	if _, errOut, code := h.run("decision", "show", "d9"); code != 3 || !strings.Contains(errOut, "decision d9") {
		t.Fatalf("missing decision code=%d stderr=%q", code, errOut)
	}
}
