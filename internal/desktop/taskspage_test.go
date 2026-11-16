package desktop_test

import (
	"context"
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
