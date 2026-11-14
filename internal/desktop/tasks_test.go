package desktop_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
	"github.com/egoist/mygo/ui"
)

func taskFixture(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.Projects = 2
	st.Needs = desktop.Needs{}
	f.status[fleetID] = st
	f.tasks[fleetID] = desktop.Tasks{
		All:           true,
		FinishedCount: 7,
		Groups: []desktop.TaskGroup{
			{Name: "Active", Count: 3, Tasks: []desktop.TaskRow{
				{
					Ref: "t1", Title: "Fix login", Project: "web", Status: "active", State: "waiting", Agent: "working",
					Attempt:   &desktop.Attempt{Ref: "a1", Task: "t1", Harness: "claude", Model: "sonnet", Status: "running"},
					Delivered: &desktop.Deliverable{Ref: "https://github.com/example/hand/pull/42", Kind: "PR"},
					Children:  1,
				},
				{
					Ref: "t2", Title: "Flaky test in the nightly pipeline on the arm64 macOS runner after the dependency bump",
					Project: "web", Status: "active", State: "failing", Parent: "t1",
					Attempt: &desktop.Attempt{Ref: "a2", Task: "t2", Harness: "codex", Model: "gpt-6-luna", Status: "failed"},
				},
				{
					Ref: "t3", Title: "Docs", Project: "api", Status: "active", State: "running",
					Attempt:   &desktop.Attempt{Ref: "a3", Task: "t3", Harness: "claude", Model: "sonnet", Status: "running"},
					Delivered: &desktop.Deliverable{Ref: "reports/t1/design.md", Kind: "file", Href: "/" + fleetID + "/doc/reports/t1/design.md"},
				},
			}},
			{Name: "Inbox", Count: 1, Tasks: []desktop.TaskRow{
				{
					Ref: "t4", Title: "Write docs", Project: "web", Status: "inbox", State: "idle", Parent: "t1",
					Waits: &desktop.Waits{Ref: "w1", Due: "2026-11-20T00:00:00Z", Note: "the API freeze", Status: "waiting", Waits: true},
				},
			}},
			{Name: "Done", Count: 7, Tasks: []desktop.TaskRow{
				{
					Ref: "t5", Title: "Ship the rename", Project: "api", Status: "done", State: "passing",
					FinishedAt: "2026-11-01T04:40:00Z",
					Delivered:  &desktop.Deliverable{Ref: `reports\t5\plan.md`, Kind: "file"},
				},
			}},
		},
	}
	return f
}

func TestScenesTasks(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "26-tasks-1100x760-light"
		if dark {
			name = "26-tasks-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			tt := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, dark)
			mustText(t, tt, "Tasks", "Active", "Inbox", "Done", "Fix login", "Write docs",
				"Waits until Nov 20: the API freeze", "1 sub-task", "under t1")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestTaskRowsAreFortyEightHigh(t *testing.T) {
	for _, dark := range []bool{false, true} {
		tt := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, dark)
		for _, ref := range []string{"task t1", "task t2", "task t4", "task t5"} {
			r, ok := tt.Find(ref)
			if !ok {
				t.Fatalf("dark=%v: no %q", dark, ref)
			}
			if r.H != 48 {
				t.Errorf("dark=%v: %s is %.0f high, want 48", dark, ref, r.H)
			}
		}
	}
}

func TestTheTaskIconFollowsItsState(t *testing.T) {
	tt := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, false)
	for _, want := range []string{"state waiting", "state failing", "state running", "state idle", "state passing"} {
		if _, ok := tt.Find(want); !ok {
			t.Errorf("no icon for %q in %q", want, tt.Texts())
		}
	}
}

func TestTheTaskMetaLineCarriesTheAttemptRefs(t *testing.T) {
	tt := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, false)
	mustText(t, tt, "a1 claude sonnet · working", "a2 codex gpt-6-luna · failed", "a3 claude sonnet · running")
	mustNotText(t, tt, "a1 claude sonnet · running")
}

func TestTheTaskSublineCarriesTheWaitsAndNestingMarks(t *testing.T) {
	tt := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, false)
	mustText(t, tt, "Waits until Nov 20: the API freeze", "1 sub-task", "under t1")
	mustNotText(t, tt, "1 sub-tasks")
}

func TestTheTaskRowShowsItsProjectAndDeliverableBadges(t *testing.T) {
	tt := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, false)
	row, ok := tt.Find("task t1")
	if !ok {
		t.Fatal("no task t1")
	}
	for _, want := range []string{"project web", "deliverable PR"} {
		b, ok := tt.Find(want)
		if !ok {
			t.Fatalf("no %q", want)
		}
		if b.X < row.X || b.X+b.W > row.X+row.W || b.Y < row.Y || b.Y+b.H > row.Y+row.H {
			t.Errorf("%q is at %v, outside its row %v", want, b, row)
		}
	}
	mustText(t, tt, "file: design.md", "file: plan.md")
}

func TestTheProjectBadgeIsCappedAtTwentyFourCharacters(t *testing.T) {
	f := taskFixture(t)
	ts := f.tasks[fleetID]
	long := strings.Repeat("project-", 10)
	ts.Groups[0].Tasks[0].Project = long
	f.tasks[fleetID] = ts
	tt := sized(t, newApp(t, f), windowWidth, windowHeight, false)
	row, ok := tt.Find("task t1")
	if !ok {
		t.Fatal("no task t1")
	}
	b, ok := tt.Find("project " + long)
	if !ok {
		t.Fatalf("no %q", "project "+long)
	}
	cut := string([]rune(long)[:23]) + "…"
	if _, ok := tt.Find(cut); !ok {
		t.Errorf("the badge draws %q, want the 24-character %q", tt.Texts(), cut)
	}
	if b.W > 200 {
		t.Errorf("the project badge is %.0f wide, want at most 200 for a %d-character name", b.W, len(long))
	}
	for _, want := range []string{"project " + long, "deliverable PR"} {
		b, ok := tt.Find(want)
		if !ok {
			t.Fatalf("no %q", want)
		}
		if b.X < row.X || b.X+b.W > row.X+row.W || b.Y < row.Y || b.Y+b.H > row.Y+row.H {
			t.Errorf("%q is at %v, outside its row %v", want, b, row)
		}
	}
}

func TestTheProjectBadgeOnlyShowsForSeveralProjects(t *testing.T) {
	multi := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, false)
	if _, ok := multi.Find("project web"); !ok {
		t.Error("a two-project fleet shows no project badge")
	}
	single := sized(t, newApp(t, goldenFixture(t)), windowWidth, windowHeight, false)
	if _, ok := single.Find("project hand"); ok {
		t.Error("a one-project fleet shows a project badge")
	}
}

func TestASubTaskIsIndentedUnderItsParent(t *testing.T) {
	tt := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, false)
	parent, ok := tt.Find("state waiting")
	if !ok {
		t.Fatal("no icon on the parent")
	}
	child, ok := tt.Find("state failing")
	if !ok {
		t.Fatal("no icon on the sub-task")
	}
	if got := child.X - parent.X; got != 20 {
		t.Errorf("the sub-task is indented %.0f, want 20", got)
	}
	if child.Y <= parent.Y {
		t.Errorf("the sub-task is at %.0f, not under its parent at %.0f", child.Y, parent.Y)
	}
}

func TestTheTaskGroupHeadsCarryTheBoardsWordsCountsAndOrder(t *testing.T) {
	tt := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, false)
	last := float32(-1)
	for _, tc := range []struct {
		group string
		count int
	}{{"Active", 3}, {"Inbox", 1}, {"Done", 7}} {
		head, ok := tt.Find("group " + tc.group)
		if !ok {
			t.Errorf("no %q group head", tc.group)
			continue
		}
		if head.Y <= last {
			t.Errorf("%q is at y=%.0f, not below the group before it at y=%.0f", tc.group, head.Y, last)
		}
		last = head.Y
		label := fmt.Sprintf("%s %d", tc.group, tc.count)
		if _, ok := tt.Find(label); !ok {
			t.Errorf("no count badge %q in %q", label, tt.Texts())
		}
	}
}

func TestTheTaskMoreLineCarriesTheBoardsFlags(t *testing.T) {
	f := taskFixture(t)
	ts := f.tasks[fleetID]
	ts.Hidden = 12
	f.tasks[fleetID] = ts
	tt := sized(t, newApp(t, f), windowWidth, windowHeight, false)
	mustText(t, tt, "12 more tasks are not shown; use hand task list --status inbox,active,done,abandoned --limit 1000.")
}

func TestADeepSubTaskSaysItsDepth(t *testing.T) {
	f := taskFixture(t)
	ts := f.tasks[fleetID]
	rows := make([]desktop.TaskRow, 0, 5)
	for i := range 5 {
		r := desktop.TaskRow{Ref: fmt.Sprintf("t1%d", i), Title: fmt.Sprintf("Deep %d", i), Project: "web", Status: "active", State: "idle"}
		if i > 0 {
			r.Parent = fmt.Sprintf("t1%d", i-1)
		}
		rows = append(rows, r)
	}
	ts.Groups[0].Tasks = append(ts.Groups[0].Tasks, rows...)
	ts.Groups[0].Count += len(rows)
	f.tasks[fleetID] = ts
	tt := sized(t, newApp(t, f), windowWidth, windowHeight, false)
	mustText(t, tt, "depth 4")
	mustNotText(t, tt, "depth 3")
	deep, ok := tt.Find("Deep 4")
	if !ok {
		t.Fatal("no deep sub-task")
	}
	root, ok := tt.Find("Deep 0")
	if !ok {
		t.Fatal("no root task")
	}
	if got := deep.X - root.X; got != 60 {
		t.Errorf("the fourth-level sub-task is indented %.0f, want 60 (three steps)", got)
	}
}

func TestTheTaskRowOpensItsTask(t *testing.T) {
	app := newApp(t, taskFixture(t))
	tt := sized(t, app, windowWidth, windowHeight, false)
	click(t, tt, "task t1")
	tt.Frame()
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("clicking the row went to screen %v ref %q", app.Screen, app.Ref)
	}
}

func TestAMissingSlotLeavesNoGap(t *testing.T) {
	f := taskFixture(t)
	ts := f.tasks[fleetID]
	ts.Groups = []desktop.TaskGroup{{Name: "Active", Count: 2, Tasks: []desktop.TaskRow{
		{Ref: "t22", Title: "A bare row", Project: "web", Status: "active", State: "idle"},
		{Ref: "t23", Title: "A row with a meta line", Project: "web", Status: "inbox", State: "waiting",
			Waits: &desktop.Waits{Ref: "w1", Due: "2026-11-20T00:00:00Z", Note: "the API freeze"}},
	}}}
	f.tasks[fleetID] = ts
	tt := sized(t, newApp(t, f), windowWidth, windowHeight, false)
	bare, ok := tt.Find("task t22")
	if !ok {
		t.Fatal("no bare row")
	}
	bareTitle, ok := tt.Find("A bare row")
	if !ok {
		t.Fatal("no bare title")
	}
	meta, ok := tt.Find("task t23")
	if !ok {
		t.Fatal("no row with a meta line")
	}
	metaTitle, ok := tt.Find("A row with a meta line")
	if !ok {
		t.Fatal("no title over a meta line")
	}
	if got, want := bareTitle.Y-bare.Y, metaTitle.Y-meta.Y; got <= want {
		t.Errorf("the bare title sits %.0f below its row, the title over a meta line %.0f: an empty meta line is reserved", got, want)
	}
}

func TestTheWaitsLineIsCutWithAnEllipsis(t *testing.T) {
	f := taskFixture(t)
	ts := f.tasks[fleetID]
	note := strings.Repeat("the vendor will answer ", 12)
	ts.Groups = []desktop.TaskGroup{{Name: "Inbox", Count: 1, Tasks: []desktop.TaskRow{{
		Ref: "t30", Title: "Wait on the vendor", Project: "web", Status: "inbox", State: "waiting",
		Waits: &desktop.Waits{Ref: "w1", Due: "2026-11-20T00:00:00Z", Note: note, Status: "waiting", Waits: true},
	}}}}
	f.tasks[fleetID] = ts
	tt := sized(t, newApp(t, f), windowWidth, windowHeight, false)
	row, ok := tt.Find("task t30")
	if !ok {
		t.Fatal("no waiting row")
	}
	line, ok := tt.Find("Waits until Nov 20: " + note)
	if !ok {
		t.Fatalf("no waits line in %q", tt.Texts())
	}
	if line.X+line.W > row.X+row.W {
		t.Errorf("the waits line ends at %.0f, past its row at %.0f", line.X+line.W, row.X+row.W)
	}
	if line.W >= row.W {
		t.Errorf("the waits line is %.0f wide in a %.0f row: nothing was cut", line.W, row.W)
	}
	if line.H > kit.Meta.Line {
		t.Errorf("the waits line is %.0f high, want one %.0f px line: it wrapped instead of being cut", line.H, kit.Meta.Line)
	}
}

func TestTheDeliverableEndsTheMetaLine(t *testing.T) {
	f := taskFixture(t)
	ts := f.tasks[fleetID]
	ts.Groups[0].Tasks[0].Waits = &desktop.Waits{Ref: "w1", Due: "2026-11-20T00:00:00Z", Note: "the API freeze"}
	f.tasks[fleetID] = ts
	tt := sized(t, newApp(t, f), windowWidth, windowHeight, false)
	title, ok := tt.Find("Fix login")
	if !ok {
		t.Fatal("no title")
	}
	attempt, ok := tt.Find("a1 claude sonnet · working")
	if !ok {
		t.Fatal("no attempt")
	}
	waits, ok := tt.Find("Waits until Nov 20: the API freeze")
	if !ok {
		t.Fatal("no waits line")
	}
	badge, ok := tt.Find("deliverable PR")
	if !ok {
		t.Fatal("no deliverable badge")
	}
	if !(attempt.X < waits.X && waits.X < badge.X) {
		t.Errorf("the meta line runs attempt %.0f, waits %.0f, deliverable %.0f: want that order", attempt.X, waits.X, badge.X)
	}
	for name, r := range map[string]ui.Rect{"attempt": attempt, "waits": waits, "deliverable": badge} {
		if r.Y < attempt.Y-2 || r.Y > attempt.Y+2 {
			t.Errorf("%s is at y=%.0f, off the meta line at y=%.0f", name, r.Y, attempt.Y)
		}
	}
	if title.Y+title.H > attempt.Y+1 {
		t.Errorf("the meta line at y=%.0f is not under the title ending at %.0f", attempt.Y, title.Y+title.H)
	}
}

func TestAnEmptyTaskGroupShowsNoHead(t *testing.T) {
	f := taskFixture(t)
	ts := f.tasks[fleetID]
	ts.Groups = append([]desktop.TaskGroup{{Name: "Empty", Count: 0}}, ts.Groups...)
	f.tasks[fleetID] = ts
	tt := sized(t, newApp(t, f), windowWidth, windowHeight, false)
	mustNotText(t, tt, "group Empty")
}

func TestALiveStateChangeRepaintsTheRow(t *testing.T) {
	f := taskFixture(t)
	ts := f.tasks[fleetID]
	ts.Groups = ts.Groups[:1]
	ts.Groups[0].Tasks = ts.Groups[0].Tasks[:1]
	ts.Groups[0].Count = 1
	f.tasks[fleetID] = ts
	app := newApp(t, f)
	app.Clock = newFakeClock()
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	app.Show(desktop.BoardScreen)
	settle(t, posted)
	tt := sized(t, app, windowWidth, windowHeight, false)
	mustText(t, tt, "state waiting")

	f.mu.Lock()
	ts = f.tasks[fleetID]
	ts.Groups[0].Tasks[0].State = "passing"
	ts.Groups[0].Tasks[0].Attempt = nil
	f.tasks[fleetID] = ts
	f.mu.Unlock()

	app.StartRefresh()
	app.Clock.(*fakeClock).Advance(desktop.FocusedRefresh)
	pumpUntil(t, posted, func() bool { return app.Tasks.Groups[0].Tasks[0].State == "passing" })
	tt.Frame()
	mustText(t, tt, "state passing")
	mustNotText(t, tt, "state waiting")
	app.Close()
}

func TestTheTaskSlotsRunInTheBoardsOrder(t *testing.T) {
	tt := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, false)
	icon, ok := tt.Find("state waiting")
	if !ok {
		t.Fatal("no state icon")
	}
	ref, ok := tt.Find("t1")
	if !ok {
		t.Fatal("no ref")
	}
	title, ok := tt.Find("Fix login")
	if !ok {
		t.Fatal("no title")
	}
	meta, ok := tt.Find("web")
	if !ok {
		t.Fatal("no project badge at the start of the meta line")
	}
	if !(icon.X < ref.X && ref.X < title.X) {
		t.Errorf("the slots run icon %.0f, ref %.0f, body %.0f: want icon, ref, body", icon.X, ref.X, title.X)
	}
	if meta.X < title.X || meta.X-title.X > 8 {
		t.Errorf("the meta line starts at %.0f, the title at %.0f: they share the body column", meta.X, title.X)
	}
	if title.Y >= meta.Y {
		t.Errorf("the title is at y=%.0f, the meta line at y=%.0f: the meta line goes under the title", title.Y, meta.Y)
	}
}

func TestTheFileChipNamesItsWholePathOnHover(t *testing.T) {
	tt := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, false)
	chip, ok := tt.Find("file: design.md")
	if !ok {
		t.Fatalf("no file chip in %q", tt.Texts())
	}
	tt.Move(chip.X+chip.W/2, chip.Y+chip.H/2)
	tt.Advance(time.Second)
	tt.Frame()
	if _, ok := tt.Find("reports/t1/design.md"); !ok {
		t.Errorf("resting on the chip shows no tooltip with the whole path, in %q", tt.Texts())
	}
}

func TestTheProjectBadgeNamesTheWholeProjectOnHover(t *testing.T) {
	f := taskFixture(t)
	ts := f.tasks[fleetID]
	long := strings.Repeat("project-", 10)
	ts.Groups[0].Tasks[0].Project = long
	f.tasks[fleetID] = ts
	tt := sized(t, newApp(t, f), windowWidth, windowHeight, false)
	badge, ok := tt.Find("project " + long)
	if !ok {
		t.Fatalf("no project badge in %q", tt.Texts())
	}
	tt.Move(badge.X+badge.W/2, badge.Y+badge.H/2)
	tt.Advance(time.Second)
	tt.Frame()
	if _, ok := tt.Find(long); !ok {
		t.Errorf("resting on the badge shows no tooltip with the whole name, in %q", tt.Texts())
	}
}

func TestScenesTasksProjectsAndLongRows(t *testing.T) {
	for _, dark := range []bool{false, true} {
		mode := map[bool]string{false: "light", true: "dark"}[dark]
		t.Run("one-project-"+mode, func(t *testing.T) {
			f := goldenFixture(t)
			st := f.status[fleetID]
			st.Needs = desktop.Needs{}
			f.status[fleetID] = st
			tt := sized(t, newApp(t, f), windowWidth, windowHeight, dark)
			mustText(t, tt, "Active", "Fix login")
			mustNotText(t, tt, "project hand")
			shotSized(t, tt, "34-tasks-one-project-1100x760-"+mode, windowWidth, windowHeight)
		})
		t.Run("long-"+mode, func(t *testing.T) {
			tt := sized(t, newApp(t, taskLongFixture(t)), windowWidth, windowHeight, dark)
			mustText(t, tt, "A wide task title", "Waits until Nov 20:")
			cut := string([]rune("platform-migration-and-the-very-long-project-name")[:23]) + "…"
			mustText(t, tt, cut)
			shotSized(t, tt, "35-tasks-long-1100x760-"+mode, windowWidth, windowHeight)
		})
	}
}

func taskLongFixture(t *testing.T) *fixture {
	t.Helper()
	f := taskFixture(t)
	ts := f.tasks[fleetID]
	ts.Groups = []desktop.TaskGroup{{Name: "Active", Count: 2, Tasks: []desktop.TaskRow{
		{
			Ref:     "t1",
			Title:   "A wide task title that runs past the width of the work pane and has to be cut with an ellipsis at its end",
			Project: "platform-migration-and-the-very-long-project-name",
			Status:  "active", State: "waiting",
			Attempt:   &desktop.Attempt{Ref: "a1", Task: "t1", Harness: "claude", Model: "sonnet", Status: "running"},
			Delivered: &desktop.Deliverable{Ref: "https://github.com/example/hand/pull/42", Kind: "PR"},
		},
		{
			Ref: "t2", Title: "A sub-task with a waiting vendor",
			Project: "platform-migration-and-the-very-long-project-name",
			Status:  "active", State: "failing", Parent: "t1",
			Waits: &desktop.Waits{Ref: "w1", Due: "2026-11-20T00:00:00Z",
				Note: "the vendor answers the questionnaire about the migration", Status: "waiting", Waits: true},
		},
	}}}
	f.tasks[fleetID] = ts
	return f
}
