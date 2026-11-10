package cli_test

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
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

func TestTaskDoneRecordsDeliverables(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Build the brief")
	h.ok("task", "start", "t1")
	for _, bad := range []string{"notes/a.md", "./a.md", "~/a.md", "", " /tmp/a.pdf", "file:///tmp/a.pdf", "ftp://example.com/a", "mailto:a@b.c", "//host/share/a", "https://example.com/a b", "https://"} {
		for _, args := range [][]string{{"--deliverable", "/tmp/ok.pdf", "--deliverable", bad, "t1"}, {"--deliverable", "/tmp/ok.pdf", "t1", "--deliverable", bad}} {
			_, errOut, code := h.run(append([]string{"task", "done"}, args...)...)
			if code != 2 || !strings.Contains(errOut, "invalid value "+strconv.Quote(bad)+" for flag -deliverable") {
				t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
			}
		}
	}
	for _, args := range [][]string{{"t1", "t2"}, {"t1", "--deliverable", "/tmp/a", "t2"}, {"--deliverable", "/tmp/a"}} {
		if _, errOut, code := h.run(append([]string{"task", "done"}, args...)...); code != 2 || !strings.Contains(errOut, "task done: want 1 argument(s) after flags") {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	if show := h.ok("task", "show", "t1"); !strings.Contains(show, "status: active") || !strings.Contains(show, "delivered[0]{kind,ref}:") {
		t.Fatalf("a rejected value changed the task: %q", show)
	}
	out := h.ok("task", "done", "--deliverable", `C:\Users\me\out\brief.docx`, "--deliverable", `\\server\share\brief.md`, "t1", "--deliverable", "/tmp/brief.pdf", "--deliverable", "https://docs.example.com/brief")
	if !strings.Contains(out, "status: done") || !strings.Contains(out, "delivered[4]:\n") || !strings.Contains(out, "  - https://docs.example.com/brief\n") {
		t.Fatalf("task done = %q", out)
	}
	want := "delivered[4]{kind,ref}:\n" + `  link,"https://docs.example.com/brief"` + "\n  file,/tmp/brief.pdf\n" + `  file,\\server\share\brief.md` + "\n" + `  file,"C:\\Users\\me\\out\\brief.docx"` + "\n"
	if show := h.ok("task", "show", "t1"); !strings.Contains(show, want) {
		t.Fatalf("task show = %q, want %q", show, want)
	}
	_, errOut, code := h.run("task", "done", "--deliverable", "/tmp/late.pdf", "t1")
	if code != 3 || !strings.Contains(errOut, "task t1 is done and cannot become done; no deliverable was recorded") {
		t.Fatalf("done again: code=%d stderr=%q", code, errOut)
	}
	if show := h.ok("task", "show", "t1"); !strings.Contains(show, want) || strings.Contains(show, "late.pdf") {
		t.Fatalf("done again changed the deliverables: %q", show)
	}
	h.ok("task", "add", "hand", "Drop it")
	if _, errOut, code := h.run("task", "abandon", "--deliverable", "/tmp/x", "t2"); code != 2 || !strings.Contains(errOut, "-deliverable") {
		t.Fatalf("abandon --deliverable: code=%d stderr=%q", code, errOut)
	}
}

func TestDeliverablesAreNewestFirstAcrossReportsAndDone(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Mixed")
	h.ok("task", "start", "t1")
	st, err := state.Open(filepath.Join(h.home, "hand.db"), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: 1, Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}}, "/w")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "1"}); err != nil {
		t.Fatal(err)
	}
	r, err := st.AddReport(ctx, a.ID, state.ReportDone, "Done.\nDelivered: https://github.com/atqamz/hand/pull/7\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EndAttempt(ctx, a.ID, state.AttemptExited, "terminal exited"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AckReport(ctx, r.ID, "supervisor"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	h.ok("task", "done", "--deliverable", "/tmp/brief.pdf", "t1")
	if show := h.ok("task", "show", "t1"); !strings.Contains(show, "delivered[2]{kind,ref}:\n  file,/tmp/brief.pdf\n  PR,\"https://github.com/atqamz/hand/pull/7\"\n") {
		t.Fatalf("task show = %q", show)
	}
	if out := h.ok("open", "--print", "--delivered", "t1"); field(out, "path") != "/tmp/brief.pdf" {
		t.Fatalf("open --print --delivered = %q", out)
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

func TestTaskListQuerySearchesEveryStatus(t *testing.T) {
	h := initWithProject(t)
	for _, title := range []string{"Fix login", "Fix inbox", "Fix search", "Write docs"} {
		h.ok("task", "add", "hand", title)
	}
	h.ok("task", "start", "t1")
	h.ok("task", "start", "t3")
	h.ok("task", "done", "t3")
	h.ok("task", "abandon", "t4")
	out := h.ok("task", "list", "--q", "fix")
	for _, want := range []string{"tasks[3]{id,project,status,title,finished}:", "t3,hand,done,Fix search,20", "t2,hand,inbox,Fix inbox,", "t1,hand,active,Fix login,"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--q fix = %q, missing %q", out, want)
		}
	}
	if out := h.ok("task", "list", "--q", "fix", "--status", "inbox,active"); !strings.Contains(out, "tasks[2]") || strings.Contains(out, "t3,") {
		t.Fatalf("--q with --status = %q", out)
	}
	if out := h.ok("task", "list", "--q", "fix", "--since", "2999-01-01"); !strings.Contains(out, "tasks[0]") {
		t.Fatalf("--q with --since = %q", out)
	}
	if out := h.ok("task", "list", "--status", "all"); !strings.Contains(out, "tasks[4]") {
		t.Fatalf("--status all = %q", out)
	}
}

func TestTaskListStatusWords(t *testing.T) {
	h := initWithProject(t)
	for _, title := range []string{"Fix login", "Fix inbox", "Fix search"} {
		h.ok("task", "add", "hand", title)
	}
	h.ok("task", "start", "t1")
	h.ok("task", "done", "t1")
	h.ok("task", "abandon", "t3")
	if out := h.ok("task", "list", "--status", "finished"); !strings.Contains(out, "tasks[2]") || strings.Contains(out, "t2,") {
		t.Fatalf("--status finished = %q", out)
	}
	if out := h.ok("task", "list", "--status", "finished,inbox"); !strings.Contains(out, "tasks[3]") {
		t.Fatalf("--status finished,inbox = %q", out)
	}
	_, errOut, code := h.run("task", "list", "--status", "all,done")
	if code != 2 || !strings.Contains(errOut, "all stands alone") {
		t.Fatalf("--status all,done code=%d stderr=%q", code, errOut)
	}
}

func TestTaskDeliverRecordsOnActiveAndDoneTasks(t *testing.T) {
	h := initWithProject(t)
	for _, title := range []string{"Done", "Active", "Inbox", "Dropped"} {
		h.ok("task", "add", "hand", title)
	}
	h.ok("task", "start", "t1")
	h.ok("task", "done", "t1")
	h.ok("task", "start", "t2")
	h.ok("task", "start", "t4")
	h.ok("task", "abandon", "t4")
	before := h.ok("task", "list", "--status", "all")

	out := h.ok("task", "deliver", "t1", "/abs/out.pdf", "https://example.com/pr/1")
	for _, want := range []string{"task: t1\n", "status: done\n", "delivered[2]:\n  - /abs/out.pdf\n  - https://example.com/pr/1\n", "`hand task show t1`"} {
		if !strings.Contains(out, want) {
			t.Fatalf("task deliver = %q, missing %q", out, want)
		}
	}
	if strings.Contains(out, "already recorded") {
		t.Fatalf("task deliver = %q", out)
	}
	if after := h.ok("task", "list", "--status", "all"); after != before {
		t.Fatalf("task list changed:\n%s\n%s", before, after)
	}
	if show := h.ok("task", "show", "t1"); !strings.Contains(show, "status: done") || !strings.Contains(show, "delivered[2]{kind,ref}:\n  link,\"https://example.com/pr/1\"\n  file,/abs/out.pdf\n") {
		t.Fatalf("task show = %q", show)
	}

	out = h.ok("task", "deliver", "t2", `C:\Users\me\out\brief.docx`, `\\server\share\brief.md`)
	if !strings.Contains(out, "status: active\n") || !strings.Contains(out, "delivered[2]:\n") {
		t.Fatalf("task deliver on active = %q", out)
	}
	if show := h.ok("task", "show", "t2"); !strings.Contains(show, "status: active") || !strings.Contains(show, `file,\\server\share\brief.md`) || !strings.Contains(show, `file,"C:\\Users\\me\\out\\brief.docx"`) {
		t.Fatalf("task show = %q", show)
	}

	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"t3", "/abs/a"}, 3, "task t3 is inbox; only an active or done task takes a deliverable; no deliverable was recorded"},
		{[]string{"t4", "/abs/a"}, 3, "task t4 is abandoned; only an active or done task takes a deliverable; no deliverable was recorded"},
		{[]string{"t99", "/abs/a"}, 3, "task t99"},
		{[]string{"t1"}, 2, "task deliver: want a task and at least 1 PATH_OR_URL"},
		{[]string{}, 2, "task deliver: want a task and at least 1 PATH_OR_URL"},
		{[]string{"/abs/a", "t1"}, 2, "is not a t id"},
		{[]string{"t1", "/abs/a", "notes/a.md"}, 2, `invalid value "notes/a.md"`},
		{[]string{"t1", "notes/a.md", "/abs/a"}, 2, `invalid value "notes/a.md"`},
		{[]string{"t1", "/abs/ok", ""}, 2, `invalid value ""`},
		{[]string{"t1", "/abs/ok", " /abs/a"}, 2, `invalid value " /abs/a"`},
		{[]string{"t1", "/abs/ok", "ftp://example.com/a"}, 2, `invalid value "ftp://example.com/a"`},
		{[]string{"t1", "/abs/ok", "-x"}, 2, `invalid value "-x"`},
		{[]string{"-x", "t1", "/abs/ok"}, 2, "task deliver: flag provided but not defined: -x"},
	} {
		_, errOut, code := h.run(append([]string{"task", "deliver"}, c.args...)...)
		if code != c.code || !strings.Contains(errOut, c.want) {
			t.Fatalf("%q: code=%d stderr=%q, want %d and %q", c.args, code, errOut, c.code, c.want)
		}
	}
	if show := h.ok("task", "show", "t1"); !strings.Contains(show, "delivered[2]{kind,ref}:") || strings.Contains(show, "/abs/ok") || strings.Contains(show, "/abs/a\n") {
		t.Fatalf("a refused call recorded something: %q", show)
	}
	for _, id := range []string{"t3", "t4"} {
		if show := h.ok("task", "show", id); !strings.Contains(show, "delivered[0]{kind,ref}:") {
			t.Fatalf("task show %s = %q", id, show)
		}
	}
	if _, errOut, code := h.run("task", "done", "--deliverable", "/abs/late.pdf", "t1"); code != 3 || !strings.Contains(errOut, "task t1 is done and cannot become done; no deliverable was recorded") {
		t.Fatalf("done --deliverable on a done task: code=%d stderr=%q", code, errOut)
	}
}

func TestTaskDeliverIsIdempotent(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Backfill")
	h.ok("task", "start", "t1")
	h.ok("task", "done", "--deliverable", "/abs/first.pdf", "t1")
	events := func() int {
		st, err := state.Open(filepath.Join(h.home, "hand.db"), h.clock)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		e, err := st.DeliveredEvents(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		return len(e)
	}
	out := h.ok("task", "deliver", "t1", "/abs/first.pdf")
	if !strings.Contains(out, "delivered[0]:") || !strings.Contains(out, "already recorded[1]:\n  - /abs/first.pdf\n") || events() != 1 {
		t.Fatalf("a value done already recorded: %q, %d events", out, events())
	}
	out = h.ok("task", "deliver", "t1", "/abs/first.pdf", "https://example.com/pr/2", "/abs/first.pdf", "https://example.com/pr/2")
	if !strings.Contains(out, "delivered[1]:\n  - https://example.com/pr/2\n") || !strings.Contains(out, "already recorded[1]:\n  - /abs/first.pdf\n") || events() != 2 {
		t.Fatalf("mixed call: %q, %d events", out, events())
	}
	out = h.ok("task", "deliver", "t1", "https://example.com/pr/2", "/abs/first.pdf")
	if !strings.Contains(out, "delivered[0]:") || !strings.Contains(out, "already recorded[2]:\n  - https://example.com/pr/2\n  - /abs/first.pdf\n") || events() != 2 {
		t.Fatalf("repeat run: %q, %d events", out, events())
	}
	if show := h.ok("task", "show", "t1"); strings.Count(show, "first.pdf") != 1 {
		t.Fatalf("task show = %q", show)
	}
}

func TestTaskDeliverBackfillsNewestFirst(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Old")
	h.ok("task", "start", "t1")
	h.ok("task", "done", "--deliverable", "/abs/first.pdf", "t1")
	h.advance(time.Hour)
	h.ok("task", "deliver", "t1", "https://github.com/atqamz/hand/pull/9", "/abs/late.pdf")
	want := "delivered[3]{kind,ref}:\n  file,/abs/late.pdf\n  PR,\"https://github.com/atqamz/hand/pull/9\"\n  file,/abs/first.pdf\n"
	if show := h.ok("task", "show", "t1"); !strings.Contains(show, want) {
		t.Fatalf("task show = %q, want %q", show, want)
	}
	if out := h.ok("open", "--print", "--delivered", "t1"); field(out, "path") != "/abs/late.pdf" {
		t.Fatalf("open --print --delivered = %q", out)
	}
	st, err := state.Open(filepath.Join(h.home, "hand.db"), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	events, err := st.TaskEvents(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for _, e := range events {
		if e.Kind == "task.delivered" {
			details = append(details, e.Detail)
		}
	}
	if !slices.Equal(details, []string{"/abs/first.pdf", "https://github.com/atqamz/hand/pull/9", "/abs/late.pdf"}) {
		t.Fatalf("details = %q", details)
	}
}

func TestTaskAddParentAndParentCommand(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Epic")
	out := h.ok("task", "add", "--parent", "t1", "hand", "Part")
	if !strings.Contains(out, "task: t2") || !strings.Contains(out, "parent: t1") {
		t.Fatalf("task add --parent = %q", out)
	}
	if _, errOut, code := h.run("task", "add", "--parent", "t999", "hand", "Lost"); code != 3 || !strings.Contains(errOut, "t999") {
		t.Fatalf("missing parent: code=%d stderr=%q", code, errOut)
	}
	h.ok("task", "add", "hand", "Loose")
	if out := h.ok("task", "parent", "t3", "--parent", "t2"); !strings.Contains(out, "task: t3") || !strings.Contains(out, "parent: t2") {
		t.Fatalf("task parent = %q", out)
	}
	if out := h.ok("task", "parent", "t3", "--root"); !strings.Contains(out, "parent: none") {
		t.Fatalf("task parent --root = %q", out)
	}
	_, errOut, code := h.run("task", "parent", "t1", "--parent", "t2")
	if code != 3 || !strings.Contains(errOut, "task t1 cannot sit under t2: t2 is already below it (t2 > t1)") {
		t.Fatalf("cycle: code=%d stderr=%q", code, errOut)
	}
	for _, args := range [][]string{{"task", "parent", "t1"}, {"task", "parent", "t1", "--root", "--parent", "t2"}, {"task", "parent", "--root"}} {
		if _, errOut, code := h.run(args...); code != 2 {
			t.Fatalf("%v: code=%d stderr=%q", args, code, errOut)
		}
	}
}

func TestTaskListNestsChildrenUnderTheirParent(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "A")
	h.ok("task", "add", "hand", "B")
	h.ok("task", "add", "--parent", "t1", "hand", "A child")
	h.ok("task", "add", "--parent", "t3", "hand", "A grandchild")
	h.ok("task", "add", "--parent", "t2", "hand", "B child")
	h.ok("task", "add", "--parent", "t1", "hand", "A second child")
	tree := h.ok("task", "list")
	want := "tasks[6]{id,project,status,title,parent}:\n  t1,hand,inbox,A,\"\"\n  t3,hand,inbox,A child,t1\n  t4,hand,inbox,A grandchild,t3\n  t6,hand,inbox,A second child,t1\n  t2,hand,inbox,B,\"\"\n  t5,hand,inbox,B child,t2\n"
	if !strings.Contains(tree, want) {
		t.Fatalf("task list = %q, want it to contain %q", tree, want)
	}
	flat := h.ok("task", "list", "--flat")
	for _, bad := range []string{"parent", ",t1\n"} {
		if strings.Contains(flat, bad) {
			t.Fatalf("--flat has %q: %q", bad, flat)
		}
	}
	if !strings.Contains(flat, "tasks[6]{id,project,status,title}:\n  t6,hand,inbox,A second child\n  t5,") {
		t.Fatalf("--flat = %q", flat)
	}
}

func TestTaskListHasNoParentColumnWhenNothingIsNested(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "A")
	h.ok("task", "add", "hand", "B")
	if out := h.ok("task", "list"); !strings.Contains(out, "tasks[2]{id,project,status,title}:") || out != h.ok("task", "list", "--flat") {
		t.Fatalf("task list = %q", out)
	}
}

func TestTaskListShowsAnOrphanAsARootWithItsParentRef(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Parent")
	h.ok("task", "add", "--parent", "t1", "hand", "Child")
	h.ok("task", "add", "--parent", "t2", "hand", "Grandchild")
	h.ok("task", "start", "t1")
	h.ok("task", "done", "t1")
	out := h.ok("task", "list")
	if !strings.Contains(out, "tasks[2]{id,project,status,title,parent}:\n  t2,hand,inbox,Child,t1\n  t3,hand,inbox,Grandchild,t2\n") {
		t.Fatalf("task list = %q", out)
	}
	out = h.ok("task", "list", "--limit", "1")
	if !strings.Contains(out, "t3,hand,inbox,Grandchild,t2") {
		t.Fatalf("a --limit cut dropped the parent ref: %q", out)
	}
}

func TestTaskShowPrintsParentAndChildren(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Epic")
	h.ok("task", "add", "--parent", "t1", "hand", "Part")
	root := h.ok("task", "show", "t1")
	if !strings.Contains(root, "parent: none") || !strings.Contains(root, "children[1]{id,status,title}:\n  t2,inbox,Part\n") {
		t.Fatalf("root show = %q", root)
	}
	child := h.ok("task", "show", "t2")
	if !strings.Contains(child, "parent: t1") || strings.Contains(child, "children") {
		t.Fatalf("child show = %q", child)
	}
}

func TestTaskDoneWarnsAboutOpenSubTasksAndStillSucceeds(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Epic")
	h.ok("task", "add", "--parent", "t1", "hand", "Part")
	h.ok("task", "start", "t1")
	out := h.ok("task", "done", "t1")
	if !strings.Contains(out, "status: done") || !strings.Contains(out, "t1 has 1 open sub-task(s)") {
		t.Fatalf("task done = %q", out)
	}
	h.ok("task", "add", "hand", "Solo")
	h.ok("task", "start", "t3")
	if out := h.ok("task", "done", "t3"); strings.Contains(out, "warn") {
		t.Fatalf("task done without children warned: %q", out)
	}
}
