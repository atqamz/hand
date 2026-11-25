package orient

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func finishedTask(t *testing.T, st *state.Store, title string) state.Task {
	t.Helper()
	ctx := context.Background()
	task, err := st.AddTask(ctx, "hand", title, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Transition(ctx, task.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	a := runningAttempt(t, st, task.ID)
	r, err := st.AddReport(ctx, a.ID, state.ReportDone, "Fixed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AckReport(ctx, r.ID, "supervisor"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EndAttempt(ctx, a.ID, state.AttemptExited, "done"); err != nil {
		t.Fatal(err)
	}
	return task
}

func hasClosable(out string) bool {
	return strings.Contains(out, "\nclosable[") || strings.Contains(out, "\nclosable_more")
}

func bothOutputs(t *testing.T, st *state.Store, home string, since int64, b Budget) (long, short string) {
	t.Helper()
	ctx := context.Background()
	l, err := Build(ctx, st, home, "", true, true, Turn{}, b)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Short(ctx, st, since, "", true, true, Turn{}, b)
	if err != nil {
		t.Fatal(err)
	}
	return l.String(), s.String()
}

func TestClosableBlockIsAbsentWhenNothingIsClosable(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	task, _ := st.AddTask(ctx, "hand", "Fix login", "")
	_, _ = st.Transition(ctx, task.ID, state.StatusActive)
	runningAttempt(t, st, task.ID)
	long, short := bothOutputs(t, st, home, 0, DefaultBudget)
	for _, out := range []string{long, short} {
		if hasClosable(out) {
			t.Fatalf("output names closable:\n%s", out)
		}
	}
}

func TestClosableBlockListsOneRowAfterTheUnackedReports(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	finishedTask(t, st, "Fix login")
	long, short := bothOutputs(t, st, home, 0, DefaultBudget)
	block := "closable[1]{id,title}:\n  t1,Fix login\n"
	if !strings.Contains(long, "unacked_reports[0]{id,attempt,status,summary}:\n"+block+"inbox[0]") {
		t.Fatalf("orient =\n%s", long)
	}
	if !strings.Contains(short, block+"changed[1]") || strings.Contains(short, "closable_more") {
		t.Fatalf("short =\n%s", short)
	}
	if _, err := st.Ask(ctx, 1, "Ship it?"); err != nil {
		t.Fatal(err)
	}
	long, short = bothOutputs(t, st, home, 0, DefaultBudget)
	if hasClosable(long) || hasClosable(short) {
		t.Fatalf("a task with an open decision is closable:\n%s\n%s", long, short)
	}
}

func TestClosableBlockCapsRowsAndClipsTitles(t *testing.T) {
	st, home := setup(t)
	_, _ = st.AddProject(context.Background(), "hand", handRepo, "")
	longTitle := strings.Repeat("x", 120)
	finishedTask(t, st, longTitle)
	for i := 1; i < 7; i++ {
		finishedTask(t, st, "Task "+strconv.Itoa(i))
	}
	long, short := bothOutputs(t, st, home, 0, DefaultBudget)
	more := "closable_more: 5 of 7 shown; run `hand task list --status active --limit 500`\n"
	for _, out := range []string{long, short} {
		if !strings.Contains(out, "closable[5]{id,title}:\n  t1,"+Clip(longTitle, DefaultBudget.TitleBytes)+"\n  t2,Task 1\n") || !strings.Contains(out, "  t5,Task 4\n"+more) {
			t.Fatalf("output =\n%s", out)
		}
		if strings.Contains(out, longTitle) {
			t.Fatalf("title not clipped:\n%s", out)
		}
	}
}

func TestShortClosableListsTheWholeFleetNotOnlyWhatChanged(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	finishedTask(t, st, "Old finished")
	cursor, err := st.LastEventSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, short := bothOutputs(t, st, home, cursor, DefaultBudget)
	if !strings.Contains(short, "closable[1]{id,title}:\n  t1,Old finished\n") || strings.Contains(short, "changed") {
		t.Fatalf("short =\n%s", short)
	}
}

func TestClosableBlockKeepsTheByteBudgetsAndDropsAfterTheRowsBelowIt(t *testing.T) {
	st, home := setup(t)
	ctx := context.Background()
	_, _ = st.AddProject(ctx, "hand", handRepo, "")
	for i := 0; i < 5; i++ {
		finishedTask(t, st, strings.Repeat("y", 80)+strconv.Itoa(i))
	}
	for i := 0; i < 30; i++ {
		task, _ := st.AddTask(ctx, "hand", strings.Repeat("z", 80)+strconv.Itoa(i), "")
		if i%2 == 0 {
			_, _ = st.Transition(ctx, task.ID, state.StatusActive)
		}
	}
	asked, _ := st.AddTask(ctx, "hand", "Asks something", "")
	_, _ = st.Transition(ctx, asked.ID, state.StatusActive)
	if _, err := st.Ask(ctx, asked.ID, "Question?"); err != nil {
		t.Fatal(err)
	}
	reporting, _ := st.AddTask(ctx, "hand", "Reports something", "")
	_, _ = st.Transition(ctx, reporting.ID, state.StatusActive)
	if _, err := st.AddReport(ctx, runningAttempt(t, st, reporting.ID).ID, state.ReportProgress, "Halfway"); err != nil {
		t.Fatal(err)
	}
	long, short := bothOutputs(t, st, home, 0, DefaultBudget)
	if len(long) >= DefaultBudget.Bytes || len(short) >= DefaultBudget.ShortBytes {
		t.Fatalf("orient %d bytes, short %d bytes", len(long), len(short))
	}
	for _, out := range []string{long, short} {
		if !strings.Contains(out, "closable[5]{id,title}:") {
			t.Fatalf("closable cut too early:\n%s", out)
		}
	}
	b := DefaultBudget
	b.Bytes = len(long) - 1
	b.ShortBytes = len(short) - 1
	cutLong, cutShort := bothOutputs(t, st, home, 0, b)
	for _, out := range []string{cutLong, cutShort} {
		if !strings.Contains(out, "closable[5]{id,title}:") {
			t.Fatalf("closable dropped before the rows below it:\n%s", out)
		}
	}
	if len(cutLong) >= len(long) || len(cutShort) >= len(short) {
		t.Fatalf("a smaller budget cut nothing: %d %d", len(cutLong), len(cutShort))
	}
	if !strings.Contains(cutShort, "events[19]") || !strings.Contains(cutShort, "changed[15]") {
		t.Fatalf("a one byte cut did not take a plain event first:\n%s", cutShort)
	}
	rows := func(out, name string) int {
		m := regexp.MustCompile(`(?m)^` + name + `\[(\d+)\]`).FindStringSubmatch(out)
		if m == nil {
			return 0
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	for limit := len(short); limit > 1; limit -= 13 {
		b.ShortBytes = limit
		_, s := bothOutputs(t, st, home, 0, b)
		if rows(s, "closable") < 5 && rows(s, "changed") > 0 {
			t.Fatalf("short at %d bytes cut closable before changed:\n%s", limit, s)
		}
		if rows(s, "closable") > 0 && (rows(s, "open_decisions") == 0 || rows(s, "unacked_reports") == 0) {
			t.Fatalf("short at %d bytes cut decisions or reports before closable:\n%s", limit, s)
		}
	}
	for limit := len(long); limit > 1; limit -= 13 {
		b.Bytes = limit
		l, _ := bothOutputs(t, st, home, 0, b)
		if rows(l, "closable") < 5 && (rows(l, "inbox") > 0 || rows(l, "recent") > 0) {
			t.Fatalf("orient at %d bytes cut closable before inbox and recent:\n%s", limit, l)
		}
		if rows(l, "closable") > 0 && (rows(l, "open_decisions") == 0 || rows(l, "unacked_reports") == 0) {
			t.Fatalf("orient at %d bytes cut decisions or reports before closable:\n%s", limit, l)
		}
	}
	b.Bytes, b.ShortBytes = 1, 1
	tiny, tinyShort := bothOutputs(t, st, home, 0, b)
	for _, out := range []string{tiny, tinyShort} {
		if strings.Contains(out, "closable[") || !strings.Contains(out, "closable_more: 0 of 5 shown") {
			t.Fatalf("closable rows survive a 1 byte budget:\n%s", out)
		}
	}
}
