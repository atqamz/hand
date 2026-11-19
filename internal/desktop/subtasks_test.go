package desktop_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func subtaskRows() []desktop.TaskRow {
	return []desktop.TaskRow{
		{Ref: "t10", Title: "Ship the umbrella", Project: "hand", Status: "active", State: "running", Children: 3},
		{Ref: "t11", Title: "Drop the old path", Project: "hand", Status: "abandoned", State: "idle", Parent: "t10",
			FinishedAt: "2026-11-01T11:40:00Z"},
		{Ref: "t12", Title: "Write the notes", Project: "hand", Status: "inbox", State: "idle", Parent: "t10"},
		{Ref: "t13", Title: "Cut the tag", Project: "hand", Status: "done", State: "idle", Parent: "t10",
			FinishedAt: "2026-11-01T11:20:00Z"},
		{Ref: "t14", Title: "A plain task", Project: "hand", Status: "active", State: "running"},
	}
}

func subtaskTasksFixture(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.Projects = 1
	st.Needs = desktop.Needs{}
	f.status[fleetID] = st
	rows := subtaskRows()
	f.tasks[fleetID] = desktop.Tasks{All: true, Groups: []desktop.TaskGroup{
		{Name: "Active", Count: 2, Tasks: []desktop.TaskRow{rows[0], rows[4]}},
		{Name: "Inbox", Count: 1, Tasks: []desktop.TaskRow{rows[2]}},
		{Name: "Done", Count: 2, Tasks: []desktop.TaskRow{rows[1], rows[3]}},
	}}
	return f
}

func subtaskSearchFixture(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.Projects = 1
	f.status[fleetID] = st
	f.projects[fleetID] = desktop.Projects{Projects: []string{"hand"}}
	var group []desktop.TaskSearchRow
	for _, r := range subtaskRows() {
		group = append(group, desktop.TaskSearchRow{TaskRow: r, UpdatedAt: "2026-11-01T11:40:00Z"})
	}
	group[0].OpenDecisions = 2
	group[2].OpenDecisions = 1
	f.search[desktop.TaskSearchQuery{Page: 1}] = desktop.TaskSearch{
		Page: 1, PageSize: 50, Total: 5, Pages: 1,
		Groups: []desktop.TaskSearchGroup{{Name: "Sun 1 Nov UTC", Count: 5, Tasks: group}},
	}
	return f
}

func subtaskViewFixture(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	td := f.task[fleetID]
	td.Task.Parent = "t9"
	td.Children = []desktop.TaskRow{
		{Ref: "t11", Title: "Drop the old path", Project: "hand", Status: "abandoned", State: "idle", Parent: "t1"},
		{Ref: "t12", Title: "Write the notes", Project: "hand", Status: "inbox", State: "idle", Parent: "t1"},
		{Ref: "t13", Title: "Cut the tag", Project: "hand", Status: "done", State: "idle", Parent: "t1"},
	}
	f.task[fleetID] = td
	return f
}

func count(tt *ui.Tester, s string) int {
	n := 0
	for _, x := range tt.Texts() {
		if x == s {
			n++
		}
	}
	return n
}

func noMarksOnThePlainRow(t *testing.T, tt *ui.Tester) {
	t.Helper()
	var under, subs int
	for _, x := range tt.Texts() {
		if strings.HasPrefix(x, "under ") {
			under++
		}
		if strings.Contains(x, "sub-task") {
			subs++
		}
	}
	if under != 6 || subs != 2 {
		t.Errorf("%d under marks and %d sub-task marks, want 6 and 2: only the three children and the parent carry them", under, subs)
	}
}

func mustOpen(t *testing.T, app *desktop.App, tt *ui.Tester, ref string) {
	t.Helper()
	tt.Frame()
	if app.Screen != desktop.TaskScreen || app.Ref != ref {
		t.Fatalf("screen %v ref %q, want the page of %s", app.Screen, app.Ref, ref)
	}
}

func eachTheme(t *testing.T, f func(t *testing.T, dark bool)) {
	for _, dark := range []bool{false, true} {
		t.Run(map[bool]string{false: "light", true: "dark"}[dark], func(t *testing.T) { f(t, dark) })
	}
}

func TestTheTasksListLinksUnderAndSubTasks(t *testing.T) {
	eachTheme(t, func(t *testing.T, dark bool) {
		app := newApp(t, subtaskTasksFixture(t))
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "3 sub-tasks", "under t10")
		if n := count(tt, "under t10"); n != 6 {
			t.Errorf("under t10 appears %d times, want 3 rows with a text and a label each", n)
		}
		if n := count(tt, "3 sub-tasks"); n != 2 {
			t.Errorf("3 sub-tasks appears %d times, want a text and a label", n)
		}
		noMarksOnThePlainRow(t, tt)
		click(t, tt, "3 sub-tasks")
		mustOpen(t, app, tt, "t10")
	})
}

func TestTheTasksListUnderLinkOpensTheParent(t *testing.T) {
	app := newApp(t, subtaskTasksFixture(t))
	tt := newTester(app)
	click(t, tt, "under t10")
	mustOpen(t, app, tt, "t10")
}

func TestAPlainTaskRowHasNoNestingMarks(t *testing.T) {
	app := newApp(t, subtaskTasksFixture(t))
	tt := newTester(app)
	row, ok := tt.Find("task t14")
	if !ok {
		t.Fatalf("no plain row in %q", tt.Texts())
	}
	if row.H != 48 {
		t.Errorf("plain row is %.0f high, want 48", row.H)
	}
	click(t, tt, "task t14")
	mustOpen(t, app, tt, "t14")
}

func TestTheTasksListDrawsTheStatusNotTheAttemptState(t *testing.T) {
	eachTheme(t, func(t *testing.T, dark bool) {
		app := newApp(t, subtaskTasksFixture(t))
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "state abandoned", "state inbox", "state done", "state running")
		mustNotText(t, tt, "state idle")
	})
}

func TestTheSearchRowsLinkUnderAndSubTasks(t *testing.T) {
	eachTheme(t, func(t *testing.T, dark bool) {
		app, _ := searchApp(t, subtaskSearchFixture(t))
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "3 sub-tasks", "under t10")
		if n := count(tt, "under t10"); n != 6 {
			t.Errorf("under t10 appears %d times, want 3 rows with a text and a label each", n)
		}
		if n := count(tt, "3 sub-tasks"); n != 2 {
			t.Errorf("3 sub-tasks appears %d times, want a text and a label", n)
		}
		noMarksOnThePlainRow(t, tt)
		click(t, tt, "under t10")
		mustOpen(t, app, tt, "t10")
	})
}

func TestTheSearchRowsDrawTheStatusAndTheDecisionBadge(t *testing.T) {
	eachTheme(t, func(t *testing.T, dark bool) {
		app, _ := searchApp(t, subtaskSearchFixture(t))
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "state abandoned", "state inbox", "state done", "state running",
			"2 decisions", "1 decision", "2 open decisions", "1 open decision")
		mustNotText(t, tt, "state idle")
		if n := count(tt, "1 decision"); n != 1 {
			t.Errorf("1 decision appears %d times, want 1", n)
		}
		mustNotText(t, tt, "0 decisions")
	})
}

func TestASearchRowWithoutDecisionsHasNoBadge(t *testing.T) {
	f := subtaskSearchFixture(t)
	s := f.search[desktop.TaskSearchQuery{Page: 1}]
	for i := range s.Groups[0].Tasks {
		s.Groups[0].Tasks[i].OpenDecisions = 0
	}
	f.search[desktop.TaskSearchQuery{Page: 1}] = s
	app, _ := searchApp(t, f)
	tt := sized(t, app, windowWidth, windowHeight, false)
	for _, w := range []string{"1 decision", "2 decisions"} {
		mustNotText(t, tt, w)
	}
}

func TestTheViewerParentIsALink(t *testing.T) {
	eachTheme(t, func(t *testing.T, dark bool) {
		app := newApp(t, subtaskViewFixture(t))
		app.OpenRef("t1")
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "Sub-task of t9")
		click(t, tt, "Sub-task of t9")
		mustOpen(t, app, tt, "t9")
	})
}

func TestTheViewerWithoutAParentHasNoLink(t *testing.T) {
	f := subtaskViewFixture(t)
	td := f.task[fleetID]
	td.Task.Parent = ""
	f.task[fleetID] = td
	app := newApp(t, f)
	app.OpenRef("t1")
	tt := newTester(app)
	tt.Frame()
	mustNotText(t, tt, "Sub-task of t9")
	if tt.HasText("Sub-task of") {
		t.Errorf("a root task shows a parent link in %q", tt.Texts())
	}
}

func TestTheViewerSubTaskRowsShowTheTaskStatus(t *testing.T) {
	eachTheme(t, func(t *testing.T, dark bool) {
		app := newApp(t, subtaskViewFixture(t))
		app.OpenRef("t1")
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "state abandoned", "state inbox", "state done", "abandoned", "inbox", "done")
		mustNotText(t, tt, "state idle")
		click(t, tt, "task t11")
		mustOpen(t, app, tt, "t11")
	})
}

func TestScenesTheTasksSearchWithSubTasks(t *testing.T) {
	eachTheme(t, func(t *testing.T, dark bool) {
		name := "79-tasks-search-subtasks-1100x760-light"
		if dark {
			name = "79-tasks-search-subtasks-1100x760-dark"
		}
		app, _ := searchApp(t, subtaskSearchFixture(t))
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "under t10", "3 sub-tasks", "state abandoned", "2 decisions")
		shotSized(t, tt, name, windowWidth, windowHeight)
	})
}

func TestScenesTheViewerWithAParentAndSubTasks(t *testing.T) {
	eachTheme(t, func(t *testing.T, dark bool) {
		name := "80-viewer-subtasks-1100x760-light"
		if dark {
			name = "80-viewer-subtasks-1100x760-dark"
		}
		app := newApp(t, subtaskViewFixture(t))
		app.OpenRef("t1")
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "Sub-task of t9", "state abandoned")
		shotSized(t, tt, name, windowWidth, windowHeight)
	})
}
