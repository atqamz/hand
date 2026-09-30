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
	if show := h.ok("task", "show", "t1"); !strings.Contains(show, "decisions[1]{id,status,question}:") {
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

func TestTaskShowListsEveryDecision(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	h.ok("decision", "ask", "t1", "Keep the old cookie name?")
	h.ok("decision", "ask", "t1", "Ship behind a flag?")
	h.ok("decision", "answer", "d1", "yes")
	show := h.ok("task", "show", "t1")
	for _, want := range []string{"decisions[2]{id,status,question}:", "d1,answered,Keep the old cookie name?", "d2,open,Ship behind a flag?", "hand decision show dN"} {
		if !strings.Contains(show, want) {
			t.Fatalf("task show missing %q in %q", want, show)
		}
	}
}

func TestDecisionListAllShowsAnsweredOnes(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	h.ok("decision", "ask", "t1", "Keep the old cookie name?")
	h.ok("decision", "ask", "t1", "Ship behind a flag?")
	h.ok("decision", "answer", "d1", "yes")
	open := h.ok("decision", "list")
	if !strings.Contains(open, "decisions[1]{id,task,question}:") || strings.Contains(open, "d1,") || !strings.Contains(open, "hand decision list --all") {
		t.Fatalf("list = %q", open)
	}
	all := h.ok("decision", "list", "--all")
	for _, want := range []string{"decisions[2]{id,task,status,question}:", "d1,t1,answered,Keep the old cookie name?", "d2,t1,open,Ship behind a flag?", "hand decision show dN"} {
		if !strings.Contains(all, want) {
			t.Fatalf("list --all missing %q in %q", want, all)
		}
	}
}

func TestDecisionAskReadsAFileOrStdin(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	h.ok("task", "start", "t1")
	h.in = strings.NewReader("Pick one\n\n1. A\n2. B\n")
	h.ok("decision", "ask", "--file", "-", "t1")
	if out := h.ok("decision", "show", "d1"); !strings.Contains(out, `Pick one\n\n1. A\n2. B`) {
		t.Fatalf("show = %q", out)
	}
	if _, errOut, code := h.run("decision", "ask", "--file", "-", "t1", "also this"); code != 2 || !strings.Contains(errOut, "exactly one") {
		t.Fatalf("both a file and a question: code=%d stderr=%q", code, errOut)
	}
}
