package desktop_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

var refCases = []struct{ ref, title string }{
	{"t7", "Alpha short ref title"},
	{"t1234", "Bravo mid ref title"},
	{"a12345", "Charlie long ref title"},
}

func exactCount(tt *ui.Tester, want string) int {
	n := 0
	for _, s := range tt.Texts() {
		if s == want {
			n++
		}
	}
	return n
}

func mustAlignTitles(t *testing.T, tt *ui.Tester) float32 {
	t.Helper()
	var refX, titleX float32
	for i, c := range refCases {
		ref, ok := tt.Find(c.ref)
		if !ok {
			t.Fatalf("no %q in %q", c.ref, tt.Texts())
		}
		title, ok := tt.Find(c.title)
		if !ok {
			t.Fatalf("no %q in %q", c.title, tt.Texts())
		}
		if ref.X+ref.W > title.X {
			t.Errorf("%s ends at %v, past its title at %v", c.ref, ref.X+ref.W, title.X)
		}
		if title.X-ref.X < kit.RefWidth-1 {
			t.Errorf("%s: title starts %v after the ref, want at least %d", c.ref, title.X-ref.X, kit.RefWidth)
		}
		if i == 0 {
			refX, titleX = ref.X, title.X
			continue
		}
		if ref.X != refX {
			t.Errorf("%s starts at x %v, want %v", c.ref, ref.X, refX)
		}
		if title.X != titleX {
			t.Errorf("title of %s starts at x %v, want %v", c.ref, title.X, titleX)
		}
	}
	return titleX
}

func eachRefTheme(t *testing.T, app *desktop.App, check func(t *testing.T, tt *ui.Tester)) {
	t.Helper()
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) { check(t, darkTester(app, dark)) })
	}
}

func TestTaskListRefsFormOneColumn(t *testing.T) {
	f := goldenFixture(t)
	rows := make([]desktop.TaskRow, 0, len(refCases))
	for _, c := range refCases {
		rows = append(rows, desktop.TaskRow{Ref: c.ref, Title: c.title, Status: "active", State: "idle"})
	}
	f.tasks[fleetID] = desktop.Tasks{Groups: []desktop.TaskGroup{{Name: "Active", Count: len(rows), Tasks: rows}}}
	eachRefTheme(t, newApp(t, f), func(t *testing.T, tt *ui.Tester) { mustAlignTitles(t, tt) })
}

func TestTasksPageRefsFormOneColumn(t *testing.T) {
	f := goldenFixture(t)
	rows := make([]desktop.TaskSearchRow, 0, len(refCases))
	for _, c := range refCases {
		rows = append(rows, taskSearchRow(c.ref, c.title, "hand", "active", "idle"))
	}
	f.search[desktop.TaskSearchQuery{Page: 1}] = desktop.TaskSearch{
		Page: 1, PageSize: 50, Total: 3, Pages: 1,
		Groups: []desktop.TaskSearchGroup{{Name: "Sun 1 Nov UTC", Count: 3, Tasks: rows}},
	}
	app := newApp(t, f)
	app.Show(desktop.TasksScreen)
	eachRefTheme(t, app, func(t *testing.T, tt *ui.Tester) { mustAlignTitles(t, tt) })
}

func TestNeedsRefsFormOneColumn(t *testing.T) {
	f := needsWith(t, func(st *desktop.Status) {
		items := []desktop.Need{{Kind: "failure", Ref: "a9", Title: "Open first", Word: "FAILED", Tone: "fail", Task: "t9", Attempt: "a9"}}
		for _, c := range refCases {
			items = append(items, desktop.Need{Kind: "failure", Ref: c.ref, Headline: c.title, Word: "FAILED", Tone: "fail", Task: "t9", Attempt: c.ref})
		}
		items = append(items, desktop.Need{Kind: "quiet", Headline: "Delta without a ref", Word: "QUIET", Tone: "wait"})
		st.Needs.Items = items
	})
	eachRefTheme(t, newApp(t, f), func(t *testing.T, tt *ui.Tester) {
		want := mustAlignTitles(t, tt)
		r, ok := tt.Find("Delta without a ref")
		if !ok || r.X != want {
			t.Errorf("a need with no ref starts its title at %v (found %v), want %v", r.X, ok, want)
		}
	})
}

func TestSubtaskRefsFormOneColumn(t *testing.T) {
	f := goldenFixture(t)
	td := f.task[fleetID]
	td.Children = nil
	for _, c := range refCases {
		td.Children = append(td.Children, desktop.TaskRow{Ref: c.ref, Title: c.title, Status: "active", State: "idle"})
	}
	f.task[fleetID] = td
	app := newApp(t, f)
	app.OpenRef("t1")
	eachRefTheme(t, app, func(t *testing.T, tt *ui.Tester) { mustAlignTitles(t, tt) })
}

func TestTheViewerPrintsAnObjectsTitleOnce(t *testing.T) {
	f := goldenFixture(t)
	for name, tc := range map[string]struct {
		ref   string
		title func() string
	}{
		"task": {"t1", func() string { return f.task[fleetID].Task.Title }},
		"attempt": {"a1", func() string {
			return f.attempt[fleetID].Attempt.Harness + " " + f.attempt[fleetID].Attempt.Model + " " + f.attempt[fleetID].Attempt.Effort
		}},
		"decision": {"d1", func() string { return f.decision[fleetID].Decision.Headline }},
	} {
		t.Run(name, func(t *testing.T) {
			app := newApp(t, f)
			app.OpenRef(tc.ref)
			title := strings.TrimSpace(tc.title())
			if title == "" {
				t.Fatal("fixture has no title")
			}
			eachRefTheme(t, app, func(t *testing.T, tt *ui.Tester) {
				texts := tt.Texts()
				back := slices.Index(texts, "Back")
				closeAt := slices.Index(texts, "Close the viewer")
				if back < 0 || closeAt < back {
					t.Fatalf("no viewer strip in %q", texts)
				}
				strip, page := texts[back:closeAt], texts[closeAt:]
				if !slices.Contains(strip, tc.ref) {
					t.Errorf("the strip %q does not show %s", strip, tc.ref)
				}
				if slices.Contains(strip, title) {
					t.Errorf("the strip %q repeats the title %q", strip, title)
				}
				n := 0
				for _, s := range page {
					if s == title {
						n++
					}
				}
				if n != 1 {
					t.Errorf("%q is printed %d times in the page, want once", title, n)
				}
			})
		})
	}
}

func TestAFailedNeedShowsFAILEDOnce(t *testing.T) {
	f := needsWith(t, func(st *desktop.Status) {
		st.Needs.Items = []desktop.Need{{
			Kind: "failure", Ref: "a2", Title: "Failing run", Word: "FAILED", Tone: "fail",
			Task: "t2", Attempt: "a2", Reason: "boom",
			AttemptMeta: &desktop.NeedAttempt{Ref: "a2", Harness: "kilo", Model: "k9", Status: "failed"},
		}}
	})
	eachRefTheme(t, newApp(t, f), func(t *testing.T, tt *ui.Tester) {
		if n := exactCount(tt, "FAILED"); n != 1 {
			t.Errorf("FAILED is printed %d times", n)
		}
		if !tt.HasText("kilo k9") {
			t.Errorf("the attempt line is missing in %q", tt.Texts())
		}
		for _, s := range tt.Texts() {
			if strings.Contains(s, "kilo") && strings.Contains(strings.ToLower(s), "failed") {
				t.Errorf("the row repeats the word in %q", s)
			}
		}
	})
}
