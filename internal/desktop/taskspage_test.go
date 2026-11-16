package desktop_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func taskSearchRow(ref, title, project, status, state string) desktop.TaskSearchRow {
	return desktop.TaskSearchRow{TaskRow: desktop.TaskRow{
		Ref: ref, Title: title, Project: project, Status: status, State: state,
	}}
}

func daySearch() desktop.TaskSearch {
	first := taskSearchRow("t23", "Move the rail to 1024 px", "hand", "done", "passing")
	first.FinishedAt = "2026-11-01T11:40:00Z"
	first.UpdatedAt = first.FinishedAt
	first.Attempt = &desktop.Attempt{Ref: "a11", Task: "t23", Harness: "codex", Model: "gpt-6-luna",
		Status: "exited", CreatedAt: "2026-11-01T09:00:00Z"}
	first.Delivered = &desktop.Deliverable{Ref: "https://github.com/example/hand/pull/42", Kind: "PR"}
	second := taskSearchRow("t20", "Check the Windows pipe address", "hand", "active", "idle")
	second.UpdatedAt = "2026-11-01T10:20:00Z"
	third := taskSearchRow("t1", "Fix login", "hand", "done", "passing")
	third.FinishedAt = "2026-11-01T11:20:00Z"
	third.UpdatedAt = third.FinishedAt
	fourth := taskSearchRow("t9", "Add a render check for the board", "hand", "done", "passing")
	fourth.FinishedAt = "2026-10-31T16:10:00Z"
	fourth.UpdatedAt = fourth.FinishedAt
	fourth.Attempt = &desktop.Attempt{Ref: "a6", Task: "t9", Harness: "codex", Model: "gpt-6-luna",
		Status: "running", CreatedAt: "2026-10-31T15:00:00Z"}
	fifth := taskSearchRow("t12", "Tidy the CLI help for hand task", "hand", "active", "waiting")
	fifth.UpdatedAt = "2026-10-31T14:05:00Z"
	fifth.Attempt = &desktop.Attempt{Ref: "a9", Task: "t12", Harness: "codex", Model: "gpt-6-luna",
		Status: "running", CreatedAt: "2026-10-31T13:00:00Z"}
	return desktop.TaskSearch{
		Page: 1, PageSize: 50, Total: 5, Pages: 1,
		Groups: []desktop.TaskSearchGroup{
			{Name: "Sun 1 Nov UTC", Count: 3, Tasks: []desktop.TaskSearchRow{first, second, third}},
			{Name: "Sat 31 Oct UTC", Count: 2, Tasks: []desktop.TaskSearchRow{fourth, fifth}},
		},
	}
}

func searchFixture(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.Projects = 1
	f.status[fleetID] = st
	f.projects[fleetID] = desktop.Projects{Projects: []string{"hand"}}
	f.search[desktop.TaskSearchQuery{Page: 1}] = daySearch()
	return f
}

func pageOneSearch() desktop.TaskSearch {
	s := daySearch()
	s.Total, s.Pages, s.PageSize = 60, 2, 50
	s.Groups = s.Groups[:1]
	s.Groups[0].Count = 3
	return s
}

func pageTwoSearch() desktop.TaskSearch {
	row := taskSearchRow("t20", "Check the Windows pipe address", "hand", "active", "idle")
	row.UpdatedAt = "2026-10-31T10:20:00Z"
	return desktop.TaskSearch{
		Page: 2, PageSize: 50, Total: 60, Pages: 2,
		Groups: []desktop.TaskSearchGroup{
			{Name: "Sat 31 Oct UTC", Count: 1, Tasks: []desktop.TaskSearchRow{row}},
		},
	}
}

func searchApp(t *testing.T, f *fixture) (*desktop.App, *ui.Tester) {
	t.Helper()
	app := newApp(t, f)
	app.Show(desktop.TasksScreen)
	return app, newTester(app)
}

func TestScenesTheTasksSearch(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "43-tasks-search-1100x760-light"
		if dark {
			name = "43-tasks-search-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			app, _ := searchApp(t, searchFixture(t))
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "Fleets / golden / tasks", "Tasks", "Search", "Default",
				"Sun 1 Nov UTC", "Sat 31 Oct UTC", "Move the rail to 1024 px",
				"a11 codex gpt-6-luna · exited", "5 tasks", "Check the Windows pipe address")
			mustNotText(t, tt, "No tasks match.")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestScenesTheTasksSearchOnTheSecondPage(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "44-tasks-search-page-2-1100x760-light"
		if dark {
			name = "44-tasks-search-page-2-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			f := searchFixture(t)
			f.search[desktop.TaskSearchQuery{Page: 1}] = pageOneSearch()
			f.search[desktop.TaskSearchQuery{Page: 2}] = pageTwoSearch()
			app, _ := searchApp(t, f)
			app.SearchPage(2)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "Page 2 of 2", "Previous", "Sat 31 Oct UTC", "Check the Windows pipe address")
			if _, ok := tt.Find("Next"); ok {
				t.Error("the last page offers Next")
			}
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestScenesTheTasksSearchWithEveryFilter(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "45-tasks-search-filters-1100x760-light"
		if dark {
			name = "45-tasks-search-filters-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			f := searchFixture(t)
			st := f.status[fleetID]
			st.Projects = 2
			f.status[fleetID] = st
			f.projects[fleetID] = desktop.Projects{Projects: []string{"api", "web"}}
			q := desktop.TaskSearchQuery{Q: "login", Status: "active", Project: "web",
				Since: "2026-10-01", Until: "2026-10-31", Page: 1}
			row := taskSearchRow("t1", "Fix login", "web", "active", "running")
			row.UpdatedAt = "2026-10-31T09:00:00Z"
			row.Attempt = &desktop.Attempt{Ref: "a4", Task: "t1", Harness: "codex",
				Model: "gpt-6-luna", Status: "running", CreatedAt: "2026-10-31T08:00:00Z"}
			f.search[q] = desktop.TaskSearch{Q: q.Q, Status: q.Status, Project: q.Project,
				Since: q.Since, Until: q.Until, Page: 1, PageSize: 50, Total: 1, Pages: 1,
				Groups: []desktop.TaskSearchGroup{{Name: "Fri 30 Oct UTC", Count: 1, Tasks: []desktop.TaskSearchRow{row}}}}
			app, _ := searchApp(t, f)
			app.SearchQ, app.SearchStatus, app.SearchProject = q.Q, q.Status, q.Project
			app.SearchSince, app.SearchUntil = q.Since, q.Until
			app.SearchNow()
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, `Search “login”`, "Status", "Active", "Project web", "Since 2026-10-01",
				"Until 2026-10-31", "Clear", "1 task", "project web", "Fri 30 Oct UTC")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestScenesTheTasksSearchFindsNothing(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "46-tasks-search-empty-1100x760-light"
		if dark {
			name = "46-tasks-search-empty-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			f := searchFixture(t)
			f.search[desktop.TaskSearchQuery{Q: "nothing", Page: 1}] = desktop.TaskSearch{
				Q: "nothing", Page: 1, PageSize: 50,
			}
			app, _ := searchApp(t, f)
			app.SearchQ = "nothing"
			app.SearchNow()
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "No tasks match.", "0 tasks", `Search “nothing”`)
			mustNotText(t, tt, "Sun 1 Nov UTC")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestScenesTheTasksSearchPastTheLastPage(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "47-tasks-search-past-1100x760-light"
		if dark {
			name = "47-tasks-search-past-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			f := searchFixture(t)
			f.search[desktop.TaskSearchQuery{Page: 1}] = pageOneSearch()
			f.search[desktop.TaskSearchQuery{Page: 3}] = desktop.TaskSearch{Page: 3, PageSize: 50, Total: 60, Pages: 2}
			app, _ := searchApp(t, f)
			app.SearchPage(3)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "No tasks match.", "60 tasks", "Previous")
			mustNotText(t, tt, "Page 3 of 2")
			mustNotText(t, tt, "Next")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestScenesTheTasksSearchRefusesAParameter(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "48-tasks-search-refused-1100x760-light"
		if dark {
			name = "48-tasks-search-refused-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			f := searchFixture(t)
			f.searchErr = &desktop.APIError{Status: 400, Code: "bad_request",
				Message: `status "nope" must be one of finished, all, done, abandoned, active, inbox`}
			app, _ := searchApp(t, f)
			app.SearchStatus = "nope"
			app.SearchNow()
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "The tasks could not be searched", "Retry",
				`status "nope" must be one of finished, all, done, abandoned, active, inbox`)
			mustNotText(t, tt, "No tasks match.")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestScenesTheTasksSearchInANarrowWindow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "49-tasks-search-880x760-light"
		if dark {
			name = "49-tasks-search-880x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			app, _ := searchApp(t, searchFixture(t))
			tt := sized(t, app, 880, 760, dark)
			mustText(t, tt, "Fleets / golden / tasks", "Sun 1 Nov UTC", "Move the rail to 1024 px", "Since", "Until")
			mustNotText(t, tt, "Chat")
			shotSized(t, tt, name, 880, 760)
		})
	}
}

func TestScenesTheTasksSearchWithAnOvertakenAnswer(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "50-tasks-search-overtaken-1100x760-light"
		if dark {
			name = "50-tasks-search-overtaken-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			f := searchFixture(t)
			slow := taskSearchRow("t30", "Slow answer", "hand", "done", "passing")
			slow.FinishedAt = "2026-11-01T09:00:00Z"
			fast := taskSearchRow("t31", "Fast answer", "hand", "done", "passing")
			fast.FinishedAt = "2026-11-01T10:00:00Z"
			f.search[desktop.TaskSearchQuery{Q: "slow", Page: 1}] = desktop.TaskSearch{Q: "slow",
				Page: 1, PageSize: 50, Total: 1, Pages: 1,
				Groups: []desktop.TaskSearchGroup{{Name: "Sun 1 Nov UTC", Count: 1, Tasks: []desktop.TaskSearchRow{slow}}}}
			f.search[desktop.TaskSearchQuery{Q: "fast", Page: 1}] = desktop.TaskSearch{Q: "fast",
				Page: 1, PageSize: 50, Total: 1, Pages: 1,
				Groups: []desktop.TaskSearchGroup{{Name: "Sun 1 Nov UTC", Count: 1, Tasks: []desktop.TaskSearchRow{fast}}}}
			model := &slowSearch{ReadModel: f, gate: "slow",
				entered: make(chan struct{}, 1), release: make(chan struct{})}
			app := desktop.New(func() (desktop.ReadModel, error) { return model, nil })
			app.DraftDir = t.TempDir()
			app.Boot()
			app.Show(desktop.TasksScreen)
			posted := make(chan func(), 8)
			app.Post = func(fn func()) { posted <- fn }
			app.SearchQ = "slow"
			app.SearchNow()
			select {
			case <-model.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("the slow search never started")
			}
			app.SearchQ = "fast"
			app.SearchNow()
			waitPost(t, posted)()
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "Fast answer", `Search “fast”`)
			close(model.release)
			waitPost(t, posted)()
			tt.Frame()
			mustText(t, tt, "Fast answer", `Search “fast”`)
			mustNotText(t, tt, "Slow answer")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

type slowSearch struct {
	desktop.ReadModel
	gate     string
	mu       sync.Mutex
	entered  chan struct{}
	release  chan struct{}
	seenGate bool
}

func (s *slowSearch) TaskSearch(ctx context.Context, fleet string, q desktop.TaskSearchQuery) (desktop.TaskSearch, error) {
	s.mu.Lock()
	hold := q.Q == s.gate && !s.seenGate
	if hold {
		s.seenGate = true
	}
	s.mu.Unlock()
	if hold {
		s.entered <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return desktop.TaskSearch{}, ctx.Err()
		}
	}
	return s.ReadModel.TaskSearch(ctx, fleet, q)
}

func TestTheRailOpensTheTasksPage(t *testing.T) {
	app := newApp(t, searchFixture(t))
	tt := newTester(app)
	click(t, tt, "Tasks")
	tt.Frame()
	if app.Screen != desktop.TasksScreen {
		t.Fatalf("screen = %v, want the tasks page", app.Screen)
	}
	mustText(t, tt, "Fleets / golden / tasks", "Sun 1 Nov UTC", "Move the rail to 1024 px", "Search")
	mustNotText(t, tt, "Message the supervisor")
}

func TestTheFleetPagesSearchFieldOpensTheTasksPageWithTheQuery(t *testing.T) {
	f := searchFixture(t)
	row := taskSearchRow("t1", "Fix login", "hand", "active", "waiting")
	row.UpdatedAt = "2026-11-01T09:00:00Z"
	f.search[desktop.TaskSearchQuery{Q: "login", Page: 1}] = desktop.TaskSearch{Q: "login",
		Page: 1, PageSize: 50, Total: 1, Pages: 1,
		Groups: []desktop.TaskSearchGroup{{Name: "Sun 1 Nov UTC", Count: 1, Tasks: []desktop.TaskSearchRow{row}}}}
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Search tasks")
	tt.Type("login")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if app.Screen != desktop.TasksScreen {
		t.Fatalf("screen = %v, want the tasks page", app.Screen)
	}
	mustText(t, tt, `Search “login”`, "Fix login")
}

func TestTheStatusFilterAppliesWithTheBoardsWords(t *testing.T) {
	f := searchFixture(t)
	row := taskSearchRow("t3", "Docs", "hand", "active", "running")
	row.UpdatedAt = "2026-11-01T09:00:00Z"
	f.search[desktop.TaskSearchQuery{Status: "active", Page: 1}] = desktop.TaskSearch{Status: "active",
		Page: 1, PageSize: 50, Total: 1, Pages: 1,
		Groups: []desktop.TaskSearchGroup{{Name: "Sun 1 Nov UTC", Count: 1, Tasks: []desktop.TaskSearchRow{row}}}}
	_, tt := searchApp(t, f)
	click(t, tt, "Default")
	tt.Frame()
	click(t, tt, "Active")
	click(t, tt, "Search")
	tt.Frame()
	mustText(t, tt, "Status", "Active", "Docs")
	mustNotText(t, tt, "Move the rail to 1024 px")
}

func TestTheProjectFilterListsTheProjectsAndApplies(t *testing.T) {
	f := searchFixture(t)
	st := f.status[fleetID]
	st.Projects = 2
	f.status[fleetID] = st
	f.projects[fleetID] = desktop.Projects{Projects: []string{"api", "web"}}
	row := taskSearchRow("t1", "Fix login", "web", "active", "waiting")
	row.UpdatedAt = "2026-11-01T09:00:00Z"
	f.search[desktop.TaskSearchQuery{Project: "web", Page: 1}] = desktop.TaskSearch{Project: "web",
		Page: 1, PageSize: 50, Total: 1, Pages: 1,
		Groups: []desktop.TaskSearchGroup{{Name: "Sun 1 Nov UTC", Count: 1, Tasks: []desktop.TaskSearchRow{row}}}}
	_, tt := searchApp(t, f)
	if _, ok := tt.Find("All projects"); !ok {
		t.Fatalf("no project filter in %q", tt.Texts())
	}
	click(t, tt, "All projects")
	tt.Frame()
	click(t, tt, "web")
	click(t, tt, "Search")
	tt.Frame()
	mustText(t, tt, "Project web", "Fix login")
}

func TestTheDateFiltersApplyAndAChipRemovesThem(t *testing.T) {
	f := searchFixture(t)
	f.search[desktop.TaskSearchQuery{Since: "2026-10-01", Page: 1}] = desktop.TaskSearch{Since: "2026-10-01",
		Page: 1, PageSize: 50, Total: 1, Pages: 1,
		Groups: []desktop.TaskSearchGroup{{Name: "Sun 1 Nov UTC", Count: 1, Tasks: []desktop.TaskSearchRow{taskSearchRow("t1", "Fix login", "hand", "done", "passing")}}}}
	_, tt := searchApp(t, f)
	click(t, tt, "Since")
	tt.Type("2026-10-01")
	click(t, tt, "Search")
	tt.Frame()
	mustText(t, tt, "Since 2026-10-01", "1 task")
	click(t, tt, "Since 2026-10-01")
	tt.Frame()
	mustText(t, tt, "5 tasks")
	mustNotText(t, tt, "Since 2026-10-01")
}

func TestThePagerMovesBetweenPagesAndShowsTheTotal(t *testing.T) {
	f := searchFixture(t)
	f.search[desktop.TaskSearchQuery{Page: 1}] = pageOneSearch()
	f.search[desktop.TaskSearchQuery{Page: 2}] = pageTwoSearch()
	_, tt := searchApp(t, f)
	mustText(t, tt, "60 tasks", "Page 1 of 2", "Next")
	if _, ok := tt.Find("Previous"); ok {
		t.Error("the first page offers Previous")
	}
	click(t, tt, "Next")
	tt.Frame()
	mustText(t, tt, "Page 2 of 2", "Previous", "Check the Windows pipe address")
	mustNotText(t, tt, "Move the rail to 1024 px")
	click(t, tt, "Previous")
	tt.Frame()
	mustText(t, tt, "Page 1 of 2", "Move the rail to 1024 px")
}

func TestATasksSearchRowOpensTheTaskAndClosingReturnsToTheSearch(t *testing.T) {
	app, tt := searchApp(t, searchFixture(t))
	click(t, tt, "task t1")
	tt.Frame()
	if app.Screen != desktop.TaskScreen {
		t.Fatalf("screen = %v, want the task page", app.Screen)
	}
	mustText(t, tt, "Fix login")
	click(t, tt, "Back")
	if app.Screen != desktop.TaskScreen {
		t.Error("the first page from the search went Back")
	}
	app.CloseViewer()
	tt.Frame()
	if app.Screen != desktop.TasksScreen {
		t.Fatalf("closing the task left the screen on %v, want the tasks page", app.Screen)
	}
	mustText(t, tt, "Fleets / golden / tasks", "Move the rail to 1024 px")
}

func TestTheTasksPageShowsTheBoardsRefusal(t *testing.T) {
	f := searchFixture(t)
	f.searchErr = &desktop.APIError{Status: 400, Code: "bad_request",
		Message: `page must be a number from 1 to 1000000`}
	_, tt := searchApp(t, f)
	mustText(t, tt, "The tasks could not be searched", "page must be a number from 1 to 1000000")
	f.searchErr = nil
	click(t, tt, "Retry")
	tt.Frame()
	mustText(t, tt, "Sun 1 Nov UTC", "Move the rail to 1024 px")
}

func TestTheNarrowTasksPageIsOneColumn(t *testing.T) {
	_, tt := searchApp(t, searchFixture(t))
	tt.SetSize(880, 760)
	tt.Frame()
	status, ok := tt.Find("Default")
	if !ok {
		t.Fatalf("no status filter in %q", tt.Texts())
	}
	since, ok := tt.Find("Since")
	if !ok {
		t.Fatalf("no since field in %q", tt.Texts())
	}
	if since.Y <= status.Y+status.H {
		t.Errorf("the date filter is beside the status filter at %v and %v", since, status)
	}
	row, ok := tt.Find("task t23")
	if !ok {
		t.Fatalf("no task row in %q", tt.Texts())
	}
	if row.X+row.W > 880 {
		t.Errorf("the row ends at %v, past the window", row.X+row.W)
	}
}

func TestAPollRefreshesTheSearchAndKeepsItOnScreen(t *testing.T) {
	f := searchFixture(t)
	app, tt := searchApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	before := f.count("search")
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	if got := f.count("search"); got <= before {
		t.Fatalf("the poll searched %d times, want more than %d", got, before)
	}
	mustText(t, tt, "Sun 1 Nov UTC", "Move the rail to 1024 px", "5 tasks")
}

func TestTheNarrowSheetOpensTheTasksPage(t *testing.T) {
	app := newApp(t, searchFixture(t))
	tt := sized(t, app, 880, 760, false)
	click(t, tt, "Menu")
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if app.Screen != desktop.TasksScreen {
		t.Fatalf("screen = %v, want the tasks page", app.Screen)
	}
	if desktop.SheetOpen(app) {
		t.Error("the pick left the sheet open")
	}
	mustText(t, tt, "Fleets / golden / tasks", "Move the rail to 1024 px")
}

func TestAFleetPageSearchStartsWithNoFilters(t *testing.T) {
	f := searchFixture(t)
	row := taskSearchRow("t1", "Fix login", "hand", "active", "waiting")
	row.UpdatedAt = "2026-11-01T09:00:00Z"
	f.search[desktop.TaskSearchQuery{Q: "login", Page: 1}] = desktop.TaskSearch{Q: "login",
		Page: 1, PageSize: 50, Total: 1, Pages: 1,
		Groups: []desktop.TaskSearchGroup{{Name: "Sun 1 Nov UTC", Count: 1, Tasks: []desktop.TaskSearchRow{row}}}}
	app := newApp(t, f)
	app.SearchStatus, app.SearchProject = "active", "web"
	app.SearchSince, app.SearchUntil = "2026-10-01", "2026-10-31"
	app.OpenTaskSearch("login")
	tt := newTester(app)
	if want := (desktop.TaskSearchQuery{Q: "login", Page: 1}); app.SearchQuery != want {
		t.Fatalf("query = %+v, want %+v", app.SearchQuery, want)
	}
	mustText(t, tt, `Search “login”`, "Fix login")
	mustNotText(t, tt, "Status Active")
	mustNotText(t, tt, "Project web")
}

func TestTheRailStartsTheTasksPageWithNoFilters(t *testing.T) {
	app := newApp(t, searchFixture(t))
	app.SearchStatus, app.SearchProject = "active", "web"
	tt := newTester(app)
	click(t, tt, "Tasks")
	tt.Frame()
	if want := (desktop.TaskSearchQuery{Page: 1}); app.SearchQuery != want {
		t.Fatalf("query = %+v, want %+v", app.SearchQuery, want)
	}
	mustText(t, tt, "Sun 1 Nov UTC", "Move the rail to 1024 px")
	mustNotText(t, tt, "Status Active")
}

func TestAPastTheLastPageWithOnePageOffersPrevious(t *testing.T) {
	f := searchFixture(t)
	f.search[desktop.TaskSearchQuery{Page: 2}] = desktop.TaskSearch{Page: 2, PageSize: 50, Total: 5, Pages: 1}
	app, tt := searchApp(t, f)
	app.SearchPage(2)
	tt.Frame()
	mustText(t, tt, "Previous", "No tasks match.", "5 tasks")
	mustNotText(t, tt, "Page 2 of 1")
}

func TestAChipRemovesOnlyTheAppliedFilter(t *testing.T) {
	f := searchFixture(t)
	row := taskSearchRow("t3", "Docs", "hand", "active", "running")
	row.UpdatedAt = "2026-11-01T09:00:00Z"
	f.search[desktop.TaskSearchQuery{Status: "active", Page: 1}] = desktop.TaskSearch{Status: "active",
		Page: 1, PageSize: 50, Total: 1, Pages: 1,
		Groups: []desktop.TaskSearchGroup{{Name: "Sun 1 Nov UTC", Count: 1, Tasks: []desktop.TaskSearchRow{row}}}}
	app, tt := searchApp(t, f)
	app.SearchStatus = "active"
	app.SearchNow()
	tt.Frame()
	mustText(t, tt, "Docs")
	app.SearchQ = "unsent"
	click(t, tt, "Status Active")
	tt.Frame()
	if want := (desktop.TaskSearchQuery{Page: 1}); app.SearchQuery != want {
		t.Fatalf("query = %+v, want %+v", app.SearchQuery, want)
	}
	mustText(t, tt, "5 tasks", "Move the rail to 1024 px")
	mustNotText(t, tt, "Search “unsent”")
}

func TestARepeatedSearchSaysSearchingWhileItRuns(t *testing.T) {
	f := searchFixture(t)
	app, tt := searchApp(t, f)
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	f.searchBlock = make(chan struct{})
	f.searchEntered = make(chan desktop.TaskSearchQuery, 1)
	app.SearchNow()
	select {
	case <-f.searchEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the search never started")
	}
	tt.Frame()
	mustText(t, tt, "Searching…")
	mustNotText(t, tt, "No tasks match.")
	mustNotText(t, tt, "0 tasks")
	close(f.searchBlock)
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "Sun 1 Nov UTC", "5 tasks")
}

func TestAFailedAutomaticSearchSaysStaleAndKeepsTheResults(t *testing.T) {
	f := searchFixture(t)
	app, tt := searchApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	f.searchErr = errors.New("the board did not answer")
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustText(t, tt, "Stale · retrying", "Sun 1 Nov UTC", "Move the rail to 1024 px")
	f.searchErr = nil
	for range 5 {
		clock.Advance(desktop.FocusedRefresh)
	}
	tt.Frame()
	mustNotText(t, tt, "Stale · retrying")
}

func TestAFailedProjectReadIsRetriedOnTheNextTick(t *testing.T) {
	f := searchFixture(t)
	st := f.status[fleetID]
	st.Projects = 2
	f.status[fleetID] = st
	f.projects[fleetID] = desktop.Projects{Projects: []string{"api", "web"}}
	f.projectsErr = errors.New("the board did not answer")
	app, tt := searchApp(t, f)
	mustNotText(t, tt, "All projects")
	f.projectsErr = nil
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	for range 5 {
		clock.Advance(desktop.FocusedRefresh)
	}
	tt.Frame()
	mustText(t, tt, "All projects")
}

func TestATaskOpenedFromTheTasksPageLeavesNoBoardTabs(t *testing.T) {
	app, tt := searchApp(t, searchFixture(t))
	tt.SetSize(880, 760)
	tt.Frame()
	click(t, tt, "task t1")
	tt.Frame()
	if app.Screen != desktop.TaskScreen {
		t.Fatalf("screen = %v, want the task page", app.Screen)
	}
	mustNotText(t, tt, "Chat")
	app.CloseViewer()
	tt.Frame()
	if app.Screen != desktop.TasksScreen {
		t.Fatalf("closing the task left the screen on %v", app.Screen)
	}
	mustText(t, tt, "Fleets / golden / tasks")
}

type quietBoard struct {
	desktop.ReadModel
	fresh bool
}

func (q *quietBoard) Poll(ctx context.Context, id string, screen desktop.Screen, ref string, since desktop.PollCursors) (desktop.Poll, error) {
	p, err := q.ReadModel.Poll(ctx, id, screen, ref, since)
	if !q.fresh {
		p.StatusFresh = false
	}
	return p, err
}

func quietApp(t *testing.T, f *fixture, fresh bool) (*desktop.App, *ui.Tester) {
	t.Helper()
	model := &quietBoard{ReadModel: f, fresh: fresh}
	app := desktop.New(func() (desktop.ReadModel, error) { return model, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	app.Show(desktop.TasksScreen)
	return app, newTester(app)
}

func TestAnUnchangedStatusTickRetriesAProjectRead(t *testing.T) {
	f := searchFixture(t)
	st := f.status[fleetID]
	st.Projects = 2
	f.status[fleetID] = st
	f.projects[fleetID] = desktop.Projects{Projects: []string{"api", "web"}}
	f.projectsErr = errors.New("the board did not answer")
	app, tt := quietApp(t, f, false)
	mustNotText(t, tt, "All projects")
	f.projectsErr = nil
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	for range 5 {
		clock.Advance(desktop.FocusedRefresh)
	}
	tt.Frame()
	mustText(t, tt, "All projects")
}

func TestAnUnchangedStatusTickRetriesAFailedSearch(t *testing.T) {
	f := searchFixture(t)
	app, tt := quietApp(t, f, true)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	f.searchErr = errors.New("the board did not answer")
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustText(t, tt, "Stale · retrying")
	model := app.Model.(*quietBoard)
	model.fresh = false
	f.searchErr = nil
	for range 5 {
		clock.Advance(desktop.FocusedRefresh)
	}
	tt.Frame()
	mustNotText(t, tt, "Stale · retrying")
	mustText(t, tt, "Sun 1 Nov UTC", "Move the rail to 1024 px")
}

func TestTheSearchStaysBeneathTheTaskViewer(t *testing.T) {
	app, tt := searchApp(t, searchFixture(t))
	click(t, tt, "task t1")
	tt.Frame()
	if app.Screen != desktop.TaskScreen {
		t.Fatalf("screen = %v, want the task page", app.Screen)
	}
	mustText(t, tt, "Fleets / golden / tasks")
}

func TestRetryIsTheKitsFlatButton(t *testing.T) {
	for _, dark := range []bool{false, true} {
		f := searchFixture(t)
		f.searchErr = &desktop.APIError{Status: 400, Code: "bad_request", Message: "page must be a number from 1 to 1000000"}
		app, _ := searchApp(t, f)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		retry, ok := tt.Find("Retry")
		if !ok {
			t.Fatalf("dark=%v: no Retry button", dark)
		}
		img := tt.Image()
		ground := img.At(600, 740)
		run := 0
		y := int(retry.Y) + 4
		for x := int(retry.X) + 10; x < int(retry.X)+60; x++ {
			if img.At(x, y) == ground {
				run++
			}
		}
		if run < 50 {
			t.Errorf("dark=%v: %d of 50 px above Retry's text are the page ground, want 50", dark, run)
		}
	}
}

func TestClearDropsEveryFilterAndTheUnsentEdit(t *testing.T) {
	app, tt := searchApp(t, searchFixture(t))
	app.SearchStatus, app.SearchSince = "active", "2026-10-01"
	app.SearchNow()
	tt.Frame()
	mustText(t, tt, "Status Active", "Since 2026-10-01")
	app.SearchQ = "unsent"
	click(t, tt, "Clear")
	tt.Frame()
	if want := (desktop.TaskSearchQuery{Page: 1}); app.SearchQuery != want {
		t.Fatalf("query = %+v, want %+v", app.SearchQuery, want)
	}
	mustText(t, tt, "5 tasks", "Move the rail to 1024 px")
	mustNotText(t, tt, "Status Active")
	mustNotText(t, tt, "Since 2026-10-01")
	mustNotText(t, tt, "Search “unsent”")
}

func TestEnteringThePageReReadsTheProjects(t *testing.T) {
	f := searchFixture(t)
	app := newApp(t, f)
	app.Show(desktop.TasksScreen)
	first := f.count("projects:" + fleetID)
	app.Show(desktop.BoardScreen)
	app.Show(desktop.TasksScreen)
	if got := f.count("projects:" + fleetID); got <= first {
		t.Fatalf("projects read %d times, want more than %d", got, first)
	}
}

func TestScenesTheTasksSearchStale(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "51-tasks-search-stale-1100x760-light"
		if dark {
			name = "51-tasks-search-stale-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			f := searchFixture(t)
			app, tt := searchApp(t, f)
			clock := newFakeClock()
			app.Clock = clock
			app.StartRefresh()
			f.searchErr = errors.New("the board did not answer")
			clock.Advance(desktop.FocusedRefresh)
			tt = sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "Stale · retrying", "Sun 1 Nov UTC", "Move the rail to 1024 px", "5 tasks")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestScenesTheTasksSearchSearching(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "52-tasks-search-searching-1100x760-light"
		if dark {
			name = "52-tasks-search-searching-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			f := searchFixture(t)
			app, tt := searchApp(t, f)
			posted := make(chan func(), 4)
			app.Post = func(fn func()) { posted <- fn }
			f.searchBlock = make(chan struct{})
			f.searchEntered = make(chan desktop.TaskSearchQuery, 1)
			app.SearchNow()
			select {
			case <-f.searchEntered:
			case <-time.After(5 * time.Second):
				t.Fatal("the search never started")
			}
			tt = sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "Searching…")
			mustNotText(t, tt, "No tasks match.")
			shotSized(t, tt, name, windowWidth, windowHeight)
			close(f.searchBlock)
			waitPost(t, posted)()
		})
	}
}

func TestScenesTheTasksSearchSecondPageOfOne(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "53-tasks-search-page-2-of-1-1100x760-light"
		if dark {
			name = "53-tasks-search-page-2-of-1-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			f := searchFixture(t)
			f.search[desktop.TaskSearchQuery{Page: 2}] = desktop.TaskSearch{Page: 2, PageSize: 50, Total: 5, Pages: 1}
			app, _ := searchApp(t, f)
			app.SearchPage(2)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "Previous", "No tasks match.", "5 tasks")
			mustNotText(t, tt, "Page 2 of 1")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestAHealthyUnchangedBoardReReadsNothing(t *testing.T) {
	f := searchFixture(t)
	app, _ := quietApp(t, f, false)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	search, projects := f.count("search"), f.count("projects:"+fleetID)
	for range 20 {
		clock.Advance(desktop.FocusedRefresh)
	}
	if got := f.count("search"); got != search {
		t.Errorf("searches = %d, want the %d already made", got, search)
	}
	if got := f.count("projects:" + fleetID); got != projects {
		t.Errorf("project reads = %d, want the %d already made", got, projects)
	}
}

func TestAFailingProjectReadBacksOffAndLeavesTheSearchAlone(t *testing.T) {
	f := searchFixture(t)
	st := f.status[fleetID]
	st.Projects = 2
	f.status[fleetID] = st
	f.projects[fleetID] = desktop.Projects{Projects: []string{"api", "web"}}
	f.projectsErr = errors.New("the board did not answer")
	app, tt := quietApp(t, f, false)
	search := f.count("search")
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	for range 20 {
		clock.Advance(desktop.FocusedRefresh)
	}
	if got := f.count("projects:" + fleetID); got > 5 {
		t.Errorf("project reads = %d over 20 unchanged ticks, want backoff", got)
	}
	if got := f.count("search"); got != search {
		t.Errorf("searches = %d, want the %d already made while only projects fail", got, search)
	}
	mustNotText(t, tt, "Stale · retrying")
}

func TestAFailingSearchBacksOff(t *testing.T) {
	f := searchFixture(t)
	app, tt := quietApp(t, f, false)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	model := app.Model.(*quietBoard)
	model.fresh = true
	f.searchErr = errors.New("the board did not answer")
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustText(t, tt, "Stale · retrying")
	model.fresh = false
	search, projects := f.count("search"), f.count("projects:"+fleetID)
	for range 20 {
		clock.Advance(desktop.FocusedRefresh)
	}
	if got := f.count("search") - search; got > 6 {
		t.Errorf("searches = %d over 20 unchanged ticks, want backoff", got)
	}
	if got := f.count("projects:" + fleetID); got != projects {
		t.Errorf("project reads = %d, want the %d already made while only the search fails", got, projects)
	}
	tt.Frame()
	mustText(t, tt, "Stale · retrying")
}

func TestScenesTheTasksSearchStaleWithChips(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "54-tasks-search-stale-chips-1100x760-light"
		if dark {
			name = "54-tasks-search-stale-chips-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			f := searchFixture(t)
			row := taskSearchRow("t3", "Docs", "hand", "active", "running")
			row.UpdatedAt = "2026-11-01T09:00:00Z"
			f.search[desktop.TaskSearchQuery{Status: "active", Page: 1}] = desktop.TaskSearch{Status: "active",
				Page: 1, PageSize: 50, Total: 1, Pages: 1,
				Groups: []desktop.TaskSearchGroup{{Name: "Sun 1 Nov UTC", Count: 1, Tasks: []desktop.TaskSearchRow{row}}}}
			app, _ := searchApp(t, f)
			app.SearchStatus = "active"
			app.SearchNow()
			clock := newFakeClock()
			app.Clock = clock
			app.StartRefresh()
			f.searchErr = errors.New("the board did not answer")
			clock.Advance(desktop.FocusedRefresh)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "Stale · retrying", "Status Active", "Clear", "1 task", "Docs")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestClosingTheViewerRefreshesTheSearch(t *testing.T) {
	f := searchFixture(t)
	app, tt := searchApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	click(t, tt, "task t1")
	tt.Frame()
	before := f.count("search")
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	app.CloseViewer()
	tt.Frame()
	if got := f.count("search"); got <= before {
		t.Fatalf("searches = %d after closing the viewer, want more than %d", got, before)
	}
	mustText(t, tt, "Fleets / golden / tasks", "Sun 1 Nov UTC")
}

func TestAStatusChangeDuringASearchReRunsItAfter(t *testing.T) {
	f := searchFixture(t)
	app, _ := searchApp(t, f)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	f.searchBlock = make(chan struct{})
	f.searchEntered = make(chan desktop.TaskSearchQuery, 1)
	app.SearchNow()
	select {
	case <-f.searchEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the search never started")
	}
	before := f.count("search")
	clock.Advance(desktop.FocusedRefresh)
	waitPost(t, posted)()
	waitPost(t, posted)()
	close(f.searchBlock)
	waitPost(t, posted)()
	waitPost(t, posted)()
	if got := f.count("search"); got != before+1 {
		t.Fatalf("searches = %d, want the %d plus one deferred refresh", got, before)
	}
}

func TestAFailedForegroundSearchRecoversOnUnchangedTicks(t *testing.T) {
	f := searchFixture(t)
	app, tt := quietApp(t, f, false)
	f.searchErr = errors.New("the board did not answer")
	app.SearchNow()
	tt.Frame()
	mustText(t, tt, "The tasks could not be searched")
	f.searchErr = nil
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	for range 5 {
		clock.Advance(desktop.FocusedRefresh)
	}
	tt.Frame()
	mustNotText(t, tt, "The tasks could not be searched")
	mustText(t, tt, "Sun 1 Nov UTC", "5 tasks")
}

type workerPoll struct {
	desktop.ReadModel
	cursor string
}

func (w *workerPoll) Poll(ctx context.Context, id string, screen desktop.Screen, ref string, since desktop.PollCursors) (desktop.Poll, error) {
	p, err := w.ReadModel.Poll(ctx, id, screen, ref, since)
	p.StatusFresh, p.StatusCursor, p.StatusRead = true, w.cursor, true
	return p, err
}

func workerApp(t *testing.T, f *fixture) *desktop.App {
	t.Helper()
	model := &workerPoll{ReadModel: f, cursor: "cursor-1"}
	app := desktop.New(func() (desktop.ReadModel, error) { return model, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	app.Show(desktop.TasksScreen)
	return app
}

func TestAWorkerBlockedBoardDoesNotReReadTheSearch(t *testing.T) {
	f := searchFixture(t)
	app := workerApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	search, projects := f.count("search"), f.count("projects:"+fleetID)
	for range 20 {
		clock.Advance(desktop.FocusedRefresh)
	}
	if got := f.count("search"); got != search {
		t.Errorf("searches = %d over 20 unchanged-cursor ticks, want %d", got, search)
	}
	if got := f.count("projects:" + fleetID); got != projects {
		t.Errorf("project reads = %d, want %d", got, projects)
	}
}

func TestAWorkerBlockedBoardBacksOffAFailingSearch(t *testing.T) {
	f := searchFixture(t)
	app := workerApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	f.searchErr = errors.New("the board did not answer")
	clock.Advance(desktop.FocusedRefresh)
	before := f.count("search")
	for range 20 {
		clock.Advance(desktop.FocusedRefresh)
	}
	if got := f.count("search") - before; got > 5 {
		t.Errorf("searches = %d over 20 unchanged-cursor ticks, want backoff", got)
	}
}

func TestAStatusChangeDuringAFailingSearchReRunsAfter(t *testing.T) {
	f := searchFixture(t)
	app, _ := searchApp(t, f)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	f.searchBlock = make(chan struct{})
	f.searchEntered = make(chan desktop.TaskSearchQuery, 1)
	app.SearchNow()
	select {
	case <-f.searchEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the search never started")
	}
	before := f.count("search")
	clock.Advance(desktop.FocusedRefresh)
	waitPost(t, posted)()
	waitPost(t, posted)()
	f.searchErr = errors.New("the board did not answer")
	close(f.searchBlock)
	waitPost(t, posted)()
	waitPost(t, posted)()
	if got := f.count("search"); got != before+1 {
		t.Fatalf("searches = %d, want the %d plus one deferred refresh", got, before)
	}
}

func TestTheSearchBackoffResetsAfterASuccess(t *testing.T) {
	f := searchFixture(t)
	app, _ := quietApp(t, f, false)
	model := app.Model.(*quietBoard)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	model.fresh = true
	f.searchErr = errors.New("the board did not answer")
	clock.Advance(desktop.FocusedRefresh)
	model.fresh = false
	clock.Advance(desktop.FocusedRefresh)
	clock.Advance(desktop.FocusedRefresh)
	f.searchErr = nil
	for range 5 {
		clock.Advance(desktop.FocusedRefresh)
	}
	model.fresh = true
	f.searchErr = errors.New("the board did not answer")
	clock.Advance(desktop.FocusedRefresh)
	model.fresh = false
	before := f.count("search")
	clock.Advance(desktop.FocusedRefresh)
	if got := f.count("search"); got != before {
		t.Fatalf("a retry came on the first tick after a fresh failure: %d, want %d", got, before)
	}
	clock.Advance(desktop.FocusedRefresh)
	if got := f.count("search"); got != before+1 {
		t.Fatalf("searches = %d, want the %d plus one retry", got, before)
	}
}

func TestTheSearchBackoffResetsOnAForegroundSearch(t *testing.T) {
	f := searchFixture(t)
	app, _ := quietApp(t, f, false)
	model := app.Model.(*quietBoard)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	model.fresh = true
	f.searchErr = errors.New("the board did not answer")
	clock.Advance(desktop.FocusedRefresh)
	model.fresh = false
	clock.Advance(desktop.FocusedRefresh)
	clock.Advance(desktop.FocusedRefresh)
	before := f.count("search")
	app.SearchNow()
	clock.Advance(desktop.FocusedRefresh)
	if got := f.count("search"); got != before+1 {
		t.Fatalf("a retry came before the second tick: %d, want %d", got, before+1)
	}
	clock.Advance(desktop.FocusedRefresh)
	if got := f.count("search"); got != before+2 {
		t.Fatalf("searches = %d, want the %d plus one retry", got, before+2-1)
	}
}

func TestARefusedSearchIsNotRetried(t *testing.T) {
	f := searchFixture(t)
	app, tt := quietApp(t, f, false)
	f.searchErr = &desktop.APIError{Status: 400, Code: "bad_request", Message: `since "x" is not a date; want YYYY-MM-DD`}
	app.SearchNow()
	tt.Frame()
	mustText(t, tt, "The tasks could not be searched")
	before := f.count("search")
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	for range 20 {
		clock.Advance(desktop.FocusedRefresh)
	}
	if got := f.count("search"); got != before {
		t.Errorf("searches = %d after a refused search, want %d", got, before)
	}
	tt.Frame()
	mustText(t, tt, "The tasks could not be searched")
}

func TestADeferredSearchDoesNotRunAfterLeavingTheTasksPage(t *testing.T) {
	f := searchFixture(t)
	app, _ := searchApp(t, f)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	f.searchBlock = make(chan struct{})
	f.searchEntered = make(chan desktop.TaskSearchQuery, 1)
	app.SearchNow()
	select {
	case <-f.searchEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the search never started")
	}
	before := f.count("search")
	clock.Advance(desktop.FocusedRefresh)
	waitPost(t, posted)()
	waitPost(t, posted)()
	app.Show(desktop.BoardScreen)
	waitPost(t, posted)()
	close(f.searchBlock)
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case fn := <-posted:
			fn()
		case <-deadline:
			if got := f.count("search"); got != before {
				t.Errorf("searches = %d after leaving the page, want %d", got, before)
			}
			return
		}
	}
}

func TestARetryDoesNotStartWhileASearchIsInFlight(t *testing.T) {
	f := searchFixture(t)
	app, _ := quietApp(t, f, false)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	f.searchErr = errors.New("the board did not answer")
	app.SearchNow()
	waitPost(t, posted)()
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	f.searchErr = nil
	f.searchBlock = make(chan struct{})
	f.searchEntered = make(chan desktop.TaskSearchQuery, 2)
	clock.Advance(desktop.FocusedRefresh)
	waitPost(t, posted)()
	waitPost(t, posted)()
	clock.Advance(desktop.FocusedRefresh)
	waitPost(t, posted)()
	waitPost(t, posted)()
	select {
	case <-f.searchEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the retry never started")
	}
	inFlight := f.count("search")
	clock.Advance(desktop.FocusedRefresh)
	waitPost(t, posted)()
	waitPost(t, posted)()
	select {
	case <-f.searchEntered:
		t.Error("a second search started while one was in flight")
	case <-time.After(200 * time.Millisecond):
	}
	if got := f.count("search"); got != inFlight {
		t.Errorf("searches = %d while one was in flight, want %d", got, inFlight)
	}
	close(f.searchBlock)
	waitPost(t, posted)()
}

func TestTheProjectBackoffResetsOnEntry(t *testing.T) {
	f := searchFixture(t)
	st := f.status[fleetID]
	st.Projects = 2
	f.status[fleetID] = st
	f.projects[fleetID] = desktop.Projects{Projects: []string{"api", "web"}}
	f.projectsErr = errors.New("the board did not answer")
	app, tt := quietApp(t, f, false)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	clock.Advance(desktop.FocusedRefresh)
	app.Show(desktop.BoardScreen)
	app.Show(desktop.TasksScreen)
	f.projectsErr = nil
	clock.Advance(desktop.FocusedRefresh)
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustText(t, tt, "All projects")
}
