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
	"github.com/atqamz/hand/internal/toon"
)

var handRepo, _ = filepath.Abs("/home/me/hand")

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
	if _, err := st.CreateFleet(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	return st, home
}

func TestEmptyHomeRendersExactly(t *testing.T) {
	st, home := setup(t)
	f, err := st.Fleet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Build(context.Background(), st, home, "", true, true, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	want := "home: " + toon.Value(home) + "\n" +
		"fleet: test (" + f.ID + ")\n" +
		"supervisor: none\n" +
		"watch: running\n" +
		"tasks: inbox=0 active=0 done=0 abandoned=0\n" +
		"cursor: 1\n" +
		"active[0]{id,project,title,plan,attempt,report,open_decisions}:\n" +
		"open_decisions[0]{id,task,question}:\n" +
		"unacked_reports[0]{id,attempt,status,summary}:\n" +
		"inbox[0]{id,project,title}:\n" +
		"recent[1]{seq,kind,task}:\n" +
		"  1,fleet.created,\"\"\n" +
		"operator_memory[2]:\n" +
		"  - # Operator memory\n" +
		"  - Write durable preferences and constraints for the supervisor here.\n" +
		"help[3]:\n" +
		"  - Capture every new request first: `hand task add --goal TEXT PROJECT TITLE`\n" +
		"  - Ask the operator only through `hand decision ask TASK QUESTION`\n" +
		"  - Wait for workers without polling: `hand wait --after CURSOR` (needs `hand watch` running; the managed supervisor gets wakes as messages instead)\n"
	if got := doc.String(); got != want {
		t.Fatalf("orient =\n%s\nwant\n%s", got, want)
	}
}

func TestOrientIsDeterministicAndShowsWork(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	a, _ := st.AddTask(ctx, "hand", "Fix login", "")
	_, _ = st.Transition(ctx, a.ID, state.StatusActive)
	_, _ = st.SetPlan(ctx, a.ID, "fix cookie")
	_, _ = st.Ask(ctx, a.ID, "Keep cookie name?")
	_, _ = st.AddTask(ctx, "hand", "Write docs", "")
	first, err := Build(ctx, st, home, "", true, true, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := Build(ctx, st, home, "", true, true, DefaultBudget)
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
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
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
	doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
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
	for _, want := range []string{" of 500 shown; run `hand task list --status active", "operator_memory_truncated:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("orient missing %q", want)
		}
	}
}

func TestOrientStaysWithinBudgetForWorstCaseText(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	project := strings.Repeat("p", 64)
	if _, err := st.AddProject(ctx, project, handRepo, ""); err != nil {
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
	for i := range state.MaxTimers {
		if _, err := st.AddTimer(ctx, state.TimerSpec{After: time.Hour, Note: strings.Repeat(string([]rune(titles[i%len(titles)])[0]), state.MaxTimerNote)}); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	out := doc.String()
	if len(out) >= 6000 || !utf8.ValidString(out) {
		t.Fatalf("orient is %d bytes (valid UTF-8: %v), want < 6000:\n%s", len(out), utf8.ValidString(out), out)
	}
	for _, want := range []string{"active_more:", "operator_memory_truncated:", "timers[20]{id,due,note}:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("orient missing %q:\n%s", want, out)
		}
	}
}

func TestOrientShowsTheLatestAttempt(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	task, _ := st.AddTask(ctx, "hand", "Fix login", "")
	_, _ = st.Transition(ctx, task.ID, state.StatusActive)
	if _, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: "codex", Model: "gpt-6-luna", Effort: "low", Argv: []string{"/bin/codex", "x"}}, "/w"); err != nil {
		t.Fatal(err)
	}
	doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
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
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
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
	doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
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

func TestOrientCutsAReportSummaryAtAWordBoundary(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	task, _ := st.AddTask(ctx, "hand", "Task", "")
	_, _ = st.Transition(ctx, task.ID, state.StatusActive)
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: "codex", Model: "m", Effort: "low", Argv: []string{"/bin/codex", "x"}}, "/w")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "1"}); err != nil {
		t.Fatal(err)
	}
	for _, summary := range []string{"PR https://github.com/atqamz/hand/pull/801 opened and green", "PR number nine: https://github.com/atqamz/hand/pull/801 opened"} {
		if _, err := st.AddReport(ctx, a.ID, state.ReportDone, summary); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	out := doc.String()
	for _, want := range []string{`r1,a1,done,"PR https://github.com/atqamz/hand/pull/801…"`, `r2,a1,done,"PR number nine:…"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("orient missing %q:\n%s", want, out)
		}
	}
}

func TestOrientPairsTheReportWithTheLatestAttempt(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
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
	doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
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
	if _, err := Build(context.Background(), st, home, "", false, true, b); err != nil {
		t.Fatalf("zero report budget: %v", err)
	}
}

func TestOrientNamesTheSupervisor(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	orient := func() string {
		t.Helper()
		doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
		if err != nil {
			t.Fatal(err)
		}
		return doc.String()
	}
	if out := orient(); !strings.Contains(out, "\nsupervisor: none\n") || strings.Contains(out, "supervisor resume") {
		t.Fatalf("none = %q", out)
	}
	sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	if out := orient(); !strings.Contains(out, "\nsupervisor: s1 claude running\n") || strings.Contains(out, "supervisor resume") {
		t.Fatalf("running = %q", out)
	}
	if _, err := st.EndSupervisor(ctx, sup.ID, state.AttemptInterrupted, "luvus server restarted"); err != nil {
		t.Fatal(err)
	}
	if out := orient(); !strings.Contains(out, "\nsupervisor: s1 claude interrupted\n") || !strings.Contains(out, "  - Resume the supervisor: `hand supervisor resume`\n") {
		t.Fatalf("interrupted = %q", out)
	}
	if err := st.SetSupervisorSession(ctx, sup.ID, "S"); err != nil {
		t.Fatal(err)
	}
	resumed, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "--resume", "S"}, Session: "S"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, resumed.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t2", PaneID: "3", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	if out := orient(); !strings.Contains(out, "\nsupervisor: s2 claude running (resumes s1)\n") {
		t.Fatalf("resumed = %q, want the live ref and the ref the session's launch prompt named", out)
	}
}

func TestOperatorMemoryOutlastsTheLists(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	if _, err := st.AddProject(ctx, "hand", handRepo, ""); err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		task, err := st.AddTask(ctx, "hand", "task "+strconv.Itoa(i), "")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = st.Transition(ctx, task.ID, state.StatusActive)
		if i < 50 {
			_, _ = st.Ask(ctx, task.ID, strings.Repeat("why ", 20))
		}
	}
	var constraints []string
	for i := range 5 {
		constraints = append(constraints, "- constraint "+strconv.Itoa(i)+": "+strings.Repeat("keep this in mind ", 38))
	}
	body := "# Operator memory\n" + strings.Join(constraints, "\n") + "\n"
	if len(body) < 3200 || len(body) > 3600 {
		t.Fatalf("fixture is %d bytes", len(body))
	}
	if err := os.WriteFile(filepath.Join(home, "memory", memory.OperatorFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	out := doc.String()
	if len(out) >= 6000 {
		t.Fatalf("orient is %d bytes, want < 6000", len(out))
	}
	if strings.Contains(out, "operator_memory_truncated") || !strings.Contains(out, "constraint 4: keep this in mind") {
		t.Fatalf("a 3.4 KB operator memory is cut while list rows remain:\n%s", out)
	}
	for _, want := range []string{"active_more:", "open_decisions_more:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("orient missing %q:\n%s", want, out)
		}
	}
}

func TestOrientWatchLine(t *testing.T) {
	st, home := setup(t)
	running, err := Build(context.Background(), st, home, "", true, true, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := Build(context.Background(), st, home, "", false, true, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(running.String(), "supervisor: none\nwatch: running\ntasks:") || strings.Contains(running.String(), "Start the watcher") {
		t.Fatalf("running orient =\n%s", running)
	}
	if !strings.Contains(missing.String(), "supervisor: none\nwatch: missing\ntasks:") || !strings.Contains(missing.String(), "Start the watcher: `hand watch`") {
		t.Fatalf("missing orient =\n%s", missing)
	}
}

func TestOrientShowsWorkerSignal(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	spec := state.AttemptSpec{Harness: "codex", Model: "m", Effort: "low", Argv: []string{"/bin/codex", "x"}}
	for i, title := range []string{"Fix login", "Ship it", "Write docs"} {
		task, _ := st.AddTask(ctx, "hand", title, "")
		_, _ = st.Transition(ctx, task.ID, state.StatusActive)
		spec.TaskID = task.ID
		a, err := st.AddAttempt(ctx, spec, "/w")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t" + strconv.Itoa(i), PaneID: strconv.Itoa(i + 2), PID: 1, StartMarker: "1"}); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, _, err := st.RecordQuiet(ctx, 1, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.NoteAttempt(ctx, 2, "blocked", "Do you want to proceed? "+strings.Repeat("x", 100)); err != nil {
		t.Fatal(err)
	}
	doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	out := doc.String()
	for _, want := range []string{"t1,hand,Fix login,none,a1 running quiet,none,0", `t2,hand,Ship it,none,"a2 running blocked: Do you want to proceed?…",none,0`, "t3,hand,Write docs,none,a3 running,none,0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("orient missing %q:\n%s", want, out)
		}
	}
}

func TestOrientTellsASessionWhetherItIsTheSupervisor(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	you := func(pane string) string {
		t.Helper()
		doc, err := Build(ctx, st, home, pane, true, true, DefaultBudget)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(doc.String(), "\n") {
			if strings.HasPrefix(l, "you: ") {
				return l
			}
		}
		return ""
	}
	sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	for pane, want := range map[string]string{"": "", "2": "you: s1", "7": "you: not the supervisor"} {
		if got := you(pane); got != want {
			t.Fatalf("pane %q: %q, want %q", pane, got, want)
		}
	}
	if _, err := st.EndSupervisor(ctx, sup.ID, state.AttemptStopped, "x"); err != nil {
		t.Fatal(err)
	}
	if got := you("2"); got != "" {
		t.Fatalf("ended supervisor: %q", got)
	}
}

func TestOrientNamesUnwatchedTasks(t *testing.T) {
	home := t.TempDir()
	if err := memory.Init(home); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	st, err := state.Open(filepath.Join(home, "hand.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if _, err := st.CreateFleet(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	orient := func() string {
		t.Helper()
		doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
		if err != nil {
			t.Fatal(err)
		}
		return doc.String()
	}
	one, _ := st.AddTask(ctx, "hand", "Fix login", "")
	two, _ := st.AddTask(ctx, "hand", "Write docs", "")
	if out := orient(); strings.Contains(out, "unwatched") {
		t.Fatalf("orient names unwatched tasks with none active:\n%s", out)
	}
	_, _ = st.Transition(ctx, one.ID, state.StatusActive)
	_, _ = st.Transition(ctx, two.ID, state.StatusActive)
	now = now.Add(23 * time.Minute)
	if out := orient(); !strings.Contains(out, "cursor: 6\nunwatched[2]:\n  - t1\n  - t2\nunwatched_idle: 23m\nactive[2]") {
		t.Fatalf("orient =\n%s", out)
	}
	d, err := st.Ask(ctx, one.ID, "Keep the cookie?")
	if err != nil {
		t.Fatal(err)
	}
	if out := orient(); strings.Contains(out, "unwatched") {
		t.Fatalf("orient names unwatched tasks while a decision is open:\n%s", out)
	}
	if _, err := st.Withdraw(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: two.ID, Harness: "codex", Model: "gpt-6-luna", Effort: "low", Argv: []string{"/bin/codex", "x"}}, "/w"); err != nil {
		t.Fatal(err)
	}
	if out := orient(); strings.Contains(out, "unwatched") {
		t.Fatalf("orient names unwatched tasks while an attempt runs:\n%s", out)
	}
}

func TestOrientCapsTheUnwatchedList(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	for range 40 {
		task, _ := st.AddTask(ctx, "hand", "Fix login", "")
		_, _ = st.Transition(ctx, task.ID, state.StatusActive)
	}
	doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	out := doc.String()
	if !strings.Contains(out, "unwatched[15]:\n  - t1\n") || !strings.Contains(out, "  - t15\nunwatched_more: 15 of 40 shown; run `hand task list --status active --limit 500`\nunwatched_idle: <1m\n") || len(out) >= DefaultBudget.Bytes {
		t.Fatalf("orient = %d bytes\n%s", len(out), out)
	}
}

func TestOrientListsArmedTimers(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	task, _ := st.AddTask(ctx, "hand", "Fix login", "")
	_, _ = st.Transition(ctx, task.ID, state.StatusActive)
	orient := func() string {
		t.Helper()
		doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
		if err != nil {
			t.Fatal(err)
		}
		return doc.String()
	}
	if out := orient(); strings.Contains(out, "timers") || !strings.Contains(out, "unwatched[1]") {
		t.Fatalf("orient without timers =\n%s", out)
	}
	if _, err := st.AddTimer(ctx, state.TimerSpec{After: 30 * time.Minute, Note: "check PR 909 and its CodeRabbit review before you merge it"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTimer(ctx, state.TimerSpec{Every: 10 * time.Minute, TaskID: task.ID}); err != nil {
		t.Fatal(err)
	}
	out := orient()
	if !strings.Contains(out, "cursor: 6\ntimers[2]{id,due,note}:\n  w2,\"2026-09-26T00:10:00Z\",\"\"\n  w1,\"2026-09-26T00:30:00Z\",check PR 909 and its CodeRabbit review before…\nactive[1]") || strings.Contains(out, "unwatched") {
		t.Fatalf("orient =\n%s", out)
	}
}

func TestOrientMarksATimerBeyondTheHorizon(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	task, _ := st.AddTask(ctx, "hand", "Fix login", "")
	_, _ = st.Transition(ctx, task.ID, state.StatusActive)
	if _, err := st.AddTimer(ctx, state.TimerSpec{After: 7 * time.Hour, Note: "check PR 909"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTimer(ctx, state.TimerSpec{After: 8 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	out := doc.String()
	if !strings.Contains(out, "unwatched[1]") || !strings.Contains(out, "timers[2]{id,due,note}:\n  w1,\"2026-09-26T07:00:00Z\",(beyond the horizon) check PR 909\n  w2,\"2026-09-26T08:00:00Z\",(beyond the horizon)\n") {
		t.Fatalf("orient =\n%s", out)
	}
}

func TestOrientReadsTheClockOnceForUnwatchedAndTimers(t *testing.T) {
	home := t.TempDir()
	if err := memory.Init(home); err != nil {
		t.Fatal(err)
	}
	now, tick := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), time.Duration(0)
	st, err := state.Open(filepath.Join(home, "hand.db"), func() time.Time {
		at := now
		now = now.Add(tick)
		return at
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if _, err := st.CreateFleet(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	task, _ := st.AddTask(ctx, "hand", "Fix login", "")
	_, _ = st.Transition(ctx, task.ID, state.StatusActive)
	if _, err := st.AddTimer(ctx, state.TimerSpec{After: 6*time.Hour + 30*time.Second}); err != nil {
		t.Fatal(err)
	}
	tick = time.Minute
	doc, err := Build(ctx, st, home, "", true, true, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	if out := doc.String(); !strings.Contains(out, "unwatched[1]") || !strings.Contains(out, "(beyond the horizon)") {
		t.Fatalf("orient lists unwatched work with a timer that is not beyond the horizon:\n%s", out)
	}
}

func TestOrientMarksATimerNoManagedSupervisorFires(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	task, _ := st.AddTask(ctx, "hand", "Fix login", "")
	_, _ = st.Transition(ctx, task.ID, state.StatusActive)
	if _, err := st.AddTimer(ctx, state.TimerSpec{After: 30 * time.Minute, Note: "check PR 909"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTimer(ctx, state.TimerSpec{After: 8 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	doc, err := Build(ctx, st, home, "", true, false, DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	out := doc.String()
	if !strings.Contains(out, "unwatched[1]") || !strings.Contains(out, "timers[2]{id,due,note}:\n  w1,\"2026-09-26T00:30:00Z\",\"(no managed supervisor: not fired) check PR 909\"\n  w2,\"2026-09-26T08:00:00Z\",\"(no managed supervisor: not fired)\"\n") {
		t.Fatalf("orient =\n%s", out)
	}
}

func TestInboxFolderShowsOnlyWhenLarge(t *testing.T) {
	st, home := setup(t)
	dir := filepath.Join(home, "inbox")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	build := func() string {
		t.Helper()
		doc, err := Build(context.Background(), st, home, "", true, true, DefaultBudget)
		if err != nil {
			t.Fatal(err)
		}
		return doc.String()
	}
	for i := range 500 {
		if err := os.WriteFile(filepath.Join(dir, "20261019-120000-"+strconv.Itoa(i+1)+".png"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if out := build(); strings.Contains(out, "inbox_folder") || strings.Contains(out, "hand inbox size") {
		t.Fatalf("500 files shown:\n%s", out)
	}
	if err := os.WriteFile(filepath.Join(dir, "20261019-120000-501.png"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := build()
	if !strings.Contains(out, "\ninbox_folder: \"501 files, 0 MiB\"\n") || !strings.Contains(out, "  - See what fills the inbox folder: `hand inbox size`\n") {
		t.Fatalf("501 files not shown:\n%s", out)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	big, err := os.Create(filepath.Join(dir, "20261019-120000-1-video.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	defer big.Close()
	if err := big.Truncate(100 << 20); err != nil {
		t.Fatal(err)
	}
	if out := build(); strings.Contains(out, "inbox_folder") {
		t.Fatalf("100 MiB shown:\n%s", out)
	}
	if err := big.Truncate(100<<20 + 1); err != nil {
		t.Fatal(err)
	}
	if out := build(); !strings.Contains(out, "\ninbox_folder: \"1 file, 100 MiB\"\n") {
		t.Fatalf("over 100 MiB not shown:\n%s", out)
	}
}
