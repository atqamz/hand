package orient

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/memory"
	"github.com/atqamz/hand/internal/state"
)

func setup(t *testing.T) (*state.Store, string) {
	t.Helper()
	home := t.TempDir()
	if err := memory.Init(home); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(home, "hand.db"), func() time.Time { return time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, home
}

func TestEmptyHomeRendersExactly(t *testing.T) {
	st, home := setup(t)
	doc, err := Build(context.Background(), st, home, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	want := "home: " + home + "\n" +
		"tasks: inbox=0 active=0 done=0 abandoned=0\n" +
		"cursor: 0\n" +
		"active[0]{id,project,title,plan,attempt,report,open_decisions}:\n" +
		"open_decisions[0]{id,task,question}:\n" +
		"unacked_reports[0]{id,attempt,status,summary}:\n" +
		"inbox[0]{id,project,title}:\n" +
		"recent[0]{seq,kind,task}:\n" +
		"operator_memory[2]:\n" +
		"  - # Operator memory\n" +
		"  - Write durable preferences and constraints for the supervisor here.\n" +
		"help[3]:\n" +
		"  - Capture every new request first: `hand task add --goal TEXT PROJECT TITLE`\n" +
		"  - Ask the operator only through `hand decision ask TASK QUESTION`\n" +
		"  - Wait for workers without polling: `hand wait --after CURSOR` (needs `hand watch` running)\n"
	if got := doc.String(); got != want {
		t.Fatalf("orient =\n%s\nwant\n%s", got, want)
	}
}

func TestOrientIsDeterministicAndShowsWork(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", "/home/me/hand")
	a, _ := st.AddTask(ctx, "hand", "Fix login", "")
	_, _ = st.Transition(ctx, a.ID, state.StatusActive)
	_, _ = st.SetPlan(ctx, a.ID, "fix cookie")
	_, _ = st.Ask(ctx, a.ID, "Keep cookie name?")
	_, _ = st.AddTask(ctx, "hand", "Write docs", "")
	first, err := Build(ctx, st, home, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := Build(ctx, st, home, DefaultBudget)
	if first.String() != second.String() {
		t.Fatal("orient is not deterministic")
	}
	out := first.String()
	for _, want := range []string{"t1,hand,Fix login,p1,none,none,1", "d1,t1,Keep cookie name?", "t2,hand,Write docs", "decision.asked"} {
		if !strings.Contains(out, want) {
			t.Fatalf("orient missing %q:\n%s", want, out)
		}
	}
}

func TestOrientStaysWithinBudgetForBigFleetAndHugeMemory(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", "/home/me/hand")
	for i := range 500 {
		task, err := st.AddTask(ctx, "hand", strings.Repeat("x", 150)+string(rune('a'+i%26)), "")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = st.Transition(ctx, task.ID, state.StatusActive)
		if i < 100 {
			_, _ = st.Ask(ctx, task.ID, strings.Repeat("why ", 400))
		}
	}
	big := strings.Repeat("remember this preference\n", 10000)
	if err := os.WriteFile(filepath.Join(home, "memory", memory.OperatorFile), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := Build(ctx, st, home, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	out := doc.String()
	if len(out) >= 6000 {
		t.Fatalf("orient is %d bytes, want < 6000", len(out))
	}
	if !utf8.ValidString(out) {
		t.Fatal("orient is not valid UTF-8")
	}
	for _, want := range []string{"active_more: 15 of 500 shown", "open_decisions_more: 15 of 100 shown", "operator_memory_truncated:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("orient missing %q", want)
		}
	}
}

func TestOrientStaysWithinBudgetForWorstCaseText(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	project := strings.Repeat("p", 64)
	if _, err := st.AddProject(ctx, project, "/home/me/p"); err != nil {
		t.Fatal(err)
	}
	titles := []string{strings.Repeat(`"`, 200), strings.Repeat("🙂", 200), strings.Repeat("漢,", 100)}
	for i := range 60 {
		task, err := st.AddTask(ctx, project, titles[i%len(titles)], "")
		if err != nil {
			t.Fatal(err)
		}
		if i < 40 {
			_, _ = st.Transition(ctx, task.ID, state.StatusActive)
			_, _ = st.Ask(ctx, task.ID, strings.Repeat("問", 2000))
		}
	}
	if err := os.WriteFile(filepath.Join(home, "memory", memory.OperatorFile), []byte(strings.Repeat("a\n", 100000)), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := Build(ctx, st, home, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	out := doc.String()
	if len(out) >= 6000 || !utf8.ValidString(out) {
		t.Fatalf("orient is %d bytes (valid UTF-8: %v), want < 6000:\n%s", len(out), utf8.ValidString(out), out)
	}
	for _, want := range []string{"active_more:", "operator_memory_truncated:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("orient missing %q:\n%s", want, out)
		}
	}
}

func TestOrientShowsTheLatestAttempt(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", "/home/me/hand")
	task, _ := st.AddTask(ctx, "hand", "Fix login", "")
	_, _ = st.Transition(ctx, task.ID, state.StatusActive)
	if _, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: "codex", Model: "gpt-6-luna", Effort: "low", Argv: []string{"/bin/codex", "x"}}, "/w"); err != nil {
		t.Fatal(err)
	}
	doc, err := Build(ctx, st, home, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	if out := doc.String(); !strings.Contains(out, "t1,hand,Fix login,none,a1 launching,none,0") {
		t.Fatalf("orient =\n%s", out)
	}
}

func TestOrientShowsReportsWithinBudget(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", "/home/me/hand")
	for i := range 40 {
		task, _ := st.AddTask(ctx, "hand", "Task", "")
		_, _ = st.Transition(ctx, task.ID, state.StatusActive)
		a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: "codex", Model: "m", Effort: "low", Argv: []string{"/bin/codex", "x"}}, "/w")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "1"}); err != nil {
			t.Fatal(err)
		}
		body := "Done with task " + strconv.Itoa(i) + "\n" + strings.Repeat("detail ", 9000)
		if _, err := st.AddReport(ctx, a.ID, state.ReportDone, body); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := Build(ctx, st, home, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	out := doc.String()
	if len(out) >= 6000 {
		t.Fatalf("orient is %d bytes", len(out))
	}
	for _, want := range []string{"unacked_reports[", "r40,a40,done,Done with task 39", "unacked_reports_more:", ",a1 running,r1 done,0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("orient missing %q:\n%s", want, out)
		}
	}
}

func TestOrientPairsTheReportWithTheLatestAttempt(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", "/home/me/hand")
	task, _ := st.AddTask(ctx, "hand", "Fix login", "")
	_, _ = st.Transition(ctx, task.ID, state.StatusActive)
	spec := state.AttemptSpec{TaskID: task.ID, Harness: "codex", Model: "m", Effort: "low", Argv: []string{"/bin/codex", "x"}}
	term := state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "1"}
	a1, _ := st.AddAttempt(ctx, spec, "/w")
	_, _ = st.AttemptRunning(ctx, a1.ID, term)
	_, _ = st.AddReport(ctx, a1.ID, state.ReportDone, "first try")
	_, _ = st.EndAttempt(ctx, a1.ID, state.AttemptStopped, "retry")
	a2, _ := st.AddAttempt(ctx, spec, "/w")
	_, _ = st.AttemptRunning(ctx, a2.ID, term)
	doc, err := Build(ctx, st, home, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	if out := doc.String(); !strings.Contains(out, "t1,hand,Fix login,none,a2 running,none,0") {
		t.Fatalf("orient =\n%s", out)
	}
}

func TestBuildAcceptsAZeroReportBudget(t *testing.T) {
	st, home := setup(t)
	b := DefaultBudget
	b.Reports = 0
	if _, err := Build(context.Background(), st, home, b); err != nil {
		t.Fatalf("zero report budget: %v", err)
	}
}
