package desktop_test

import (
	"fmt"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

func taskFixture(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.Projects = 2
	st.Needs = desktop.Needs{}
	f.status[fleetID] = st
	f.tasks[fleetID] = desktop.Tasks{
		FinishedCount: 7,
		Groups: []desktop.TaskGroup{
			{Name: "Active", Count: 3, Tasks: []desktop.TaskRow{
				{
					Ref: "t1", Title: "Fix login", Project: "web", Status: "active", State: "waiting", Agent: "working",
					Attempt:   &desktop.Attempt{Ref: "a1", Task: "t1", Harness: "claude", Model: "sonnet", Status: "running"},
					Delivered: &desktop.Deliverable{Ref: "https://github.com/example/hand/pull/42", Kind: "PR"},
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
				{Ref: "t4", Title: "Write docs", Project: "web", Status: "inbox", State: "idle"},
			}},
			{Name: "Done", Count: 7, Tasks: []desktop.TaskRow{
				{Ref: "t5", Title: "Ship the rename", Project: "api", Status: "done", State: "passing", FinishedAt: "2026-11-01T04:40:00Z"},
			}},
		},
	}
	return f
}

func TestScenesTasks(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "23-tasks-light"
		if dark {
			name = "23-tasks-dark"
		}
		t.Run(name, func(t *testing.T) {
			tt := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, dark)
			mustText(t, tt, "Tasks", "Active", "Inbox", "Done", "Fix login", "Write docs",
				"Browse finished tasks (7)")
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
	for _, want := range []string{"state waiting", "state failure", "state running", "state idle", "state passing"} {
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
	if _, ok := tt.Find("deliverable file"); !ok {
		t.Error("no file deliverable")
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
	child, ok := tt.Find("state failure")
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

func TestTheTaskGroupHeadsCarryTheBoardsWordsAndCounts(t *testing.T) {
	tt := sized(t, newApp(t, taskFixture(t)), windowWidth, windowHeight, false)
	for _, tc := range []struct {
		group string
		count int
	}{{"Active", 3}, {"Inbox", 1}, {"Done", 7}} {
		if _, ok := tt.Find("group " + tc.group); !ok {
			t.Errorf("no %q group head", tc.group)
		}
		label := fmt.Sprintf("%s %d", tc.group, tc.count)
		if _, ok := tt.Find(label); !ok {
			t.Errorf("no count badge %q in %q", label, tt.Texts())
		}
	}
	mustText(t, tt, "Browse finished tasks (7)")
}
