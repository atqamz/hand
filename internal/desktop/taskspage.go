package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
	"github.com/atqamz/hand/internal/taskscope"
)

type taskChip struct{ Key, Label string }

func (a *App) openTasks() {
	if a.Model == nil || a.Fleet.ID == "" {
		return
	}
	a.projectsFor = ""
	a.projectsFails, a.projectsSkip = 0, 0
	a.loadProjects()
	a.SearchQuery = a.formQuery(1)
	a.runTaskSearch(false)
}

func (a *App) OpenTaskSearch(q string) {
	a.SearchQ = strings.TrimSpace(q)
	a.SearchStatus, a.SearchProject = "", ""
	a.SearchSince, a.SearchUntil = "", ""
	if a.Screen == TasksScreen {
		a.SearchNow()
		return
	}
	a.Show(TasksScreen)
}

func (a *App) SearchNow() {
	a.SearchQuery = a.formQuery(1)
	a.runTaskSearch(false)
}

func (a *App) SearchPage(page int) {
	if page < 1 {
		return
	}
	a.SearchQuery.Page = page
	a.runTaskSearch(false)
}

func (a *App) refreshTaskSearch() {
	if a.Model == nil || a.Fleet.ID == "" {
		return
	}
	if a.SearchBusy {
		a.searchAgain = true
		return
	}
	a.runTaskSearch(true)
}

func (a *App) pollSkips(fails int) int {
	ticks := int(maxRefreshBackoff / a.pollDelay())
	return min(1<<min(fails, 16), max(ticks, 1)) - 1
}

func (a *App) retryProjects() {
	if a.projectsSkip > 0 {
		a.projectsSkip--
		return
	}
	a.loadProjects()
}

func searchRetriable(err error) bool {
	var api *APIError
	if !errors.As(err, &api) {
		return true
	}
	return api.Status < 400 || api.Status >= 500 ||
		api.Status == http.StatusRequestTimeout || api.Status == http.StatusTooManyRequests
}

func (a *App) retryStaleSearch() {
	if a.SearchBusy {
		return
	}
	if a.searchSkip > 0 {
		a.searchSkip--
		return
	}
	a.runTaskSearch(true)
}

func (a *App) formQuery(page int) TaskSearchQuery {
	return TaskSearchQuery{
		Q:       strings.TrimSpace(a.SearchQ),
		Status:  a.SearchStatus,
		Project: a.SearchProject,
		Since:   strings.TrimSpace(a.SearchSince),
		Until:   strings.TrimSpace(a.SearchUntil),
		Page:    max(page, 1),
	}
}

func (a *App) runTaskSearch(quiet bool) {
	read, fleet := a.Model, a.Fleet.ID
	q := a.SearchQuery
	if q.Page < 1 {
		q.Page = 1
	}
	a.searchGen++
	gen, post := a.searchGen, a.Post
	a.SearchBusy = true
	if !quiet {
		a.Search = TaskSearch{}
		a.SearchErr, a.searchFor = "", TaskSearchQuery{}
		a.searchStale, a.searchStaleErr = false, ""
		a.searchFails, a.searchSkip, a.searchAgain = 0, 0, false
	}
	ctx, cancel := context.WithTimeout(a.Context, a.pollTimeout())
	apply := func(s TaskSearch, err error) {
		if a.Fleet.ID != fleet || a.searchGen != gen {
			return
		}
		a.SearchBusy = false
		again := func() {
			if !a.searchAgain {
				return
			}
			a.searchAgain = false
			if a.Screen == TasksScreen {
				a.runTaskSearch(true)
			}
		}
		if err != nil {
			if !quiet {
				a.SearchErr, a.searchFor = friendly(err), TaskSearchQuery{}
			}
			if !searchRetriable(err) {
				a.searchStale, a.searchStaleErr = false, ""
				a.searchFails, a.searchSkip = 0, 0
				again()
				return
			}
			a.searchStale, a.searchStaleErr = true, friendly(err)
			a.searchFails++
			a.searchSkip = a.pollSkips(a.searchFails)
			again()
			return
		}
		a.Search, a.SearchErr, a.searchFor = s, "", q
		a.searchStale, a.searchStaleErr = false, ""
		a.searchFails, a.searchSkip = 0, 0
		again()
		a.SearchQuery.Page = s.Page
	}
	if post == nil {
		s, err := read.TaskSearch(ctx, fleet, q)
		cancel()
		apply(s, err)
		return
	}
	go func() {
		s, err := read.TaskSearch(ctx, fleet, q)
		cancel()
		post(func() { apply(s, err) })
	}()
}

func (a *App) loadProjects() {
	if a.Model == nil || a.Fleet.ID == "" || a.projectsFor == a.Fleet.ID {
		return
	}
	read, fleet, post := a.Model, a.Fleet.ID, a.Post
	a.projectsFor = fleet
	ctx, cancel := context.WithTimeout(a.Context, a.pollTimeout())
	apply := func(p Projects, err error) {
		if a.Fleet.ID != fleet {
			return
		}
		if err != nil {
			a.projectsFor = ""
			a.projectsFails++
			a.projectsSkip = a.pollSkips(a.projectsFails)
			return
		}
		a.Projects = p
		a.projectsFails, a.projectsSkip = 0, 0
	}
	if post == nil {
		p, err := read.Projects(ctx, fleet)
		cancel()
		apply(p, err)
		return
	}
	go func() {
		p, err := read.Projects(ctx, fleet)
		cancel()
		post(func() { apply(p, err) })
	}()
}

func (a *App) taskChips() []taskChip {
	q := a.SearchQuery
	var out []taskChip
	if q.Q != "" {
		out = append(out, taskChip{"q", clipTaskChip(`Search “` + q.Q + `”`)})
	}
	if q.Status != "" {
		out = append(out, taskChip{"status", clipTaskChip("Status " + taskscope.Label(q.Status))})
	}
	if q.Project != "" {
		out = append(out, taskChip{"project", clipTaskChip("Project " + q.Project)})
	}
	if q.Since != "" {
		out = append(out, taskChip{"since", clipTaskChip("Since " + q.Since)})
	}
	if q.Until != "" {
		out = append(out, taskChip{"until", clipTaskChip("Until " + q.Until)})
	}
	return out
}

func (a *App) resetTaskForm() {
	a.SearchQ, a.SearchStatus, a.SearchProject = "", "", ""
	a.SearchSince, a.SearchUntil = "", ""
}

func (a *App) syncTaskForm() {
	a.SearchQ, a.SearchStatus, a.SearchProject = a.SearchQuery.Q, a.SearchQuery.Status, a.SearchQuery.Project
	a.SearchSince, a.SearchUntil = a.SearchQuery.Since, a.SearchQuery.Until
}

func (a *App) dropTaskChip(key string) {
	a.syncTaskForm()
	switch key {
	case "q":
		a.SearchQ = ""
	case "status":
		a.SearchStatus = ""
	case "project":
		a.SearchProject = ""
	case "since":
		a.SearchSince = ""
	case "until":
		a.SearchUntil = ""
	}
	a.SearchNow()
}

func (a *App) clearTaskFilters() {
	a.resetTaskForm()
	a.SearchNow()
}

func clipTaskChip(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

func statusOptions() []kit.Option {
	out := make([]kit.Option, 0, len(taskscope.Scopes))
	for _, s := range taskscope.Scopes {
		out = append(out, kit.Option{Value: s.Value, Label: s.Label})
	}
	return out
}

func (a *App) projectOptions() []kit.Option {
	out := []kit.Option{{Value: "", Label: "All projects"}}
	for _, name := range a.Projects.Projects {
		out = append(out, kit.Option{Value: name, Label: name})
	}
	return out
}

func (a *App) tasksPage(c *ui.Context, k kit.Colors, p palette) {
	w, _ := c.Size()
	m := max(float32(kit.Space20), (w-kit.Measure)/2)
	page := ui.Scroll(c).Key("tasks-scroll").Grow(1).Padding(kit.Space20, m, 32).
		TrackScroll(&a.searchScroll).Focusable().FocusRing(false).Label("Tasks")
	page.Children(func() {
		ui.Column(c).Gap(kit.Space16).Selectable().Children(func() {
			ui.Text(c, "Fleets / "+fleetName(a.Fleet)+" / tasks").FontSize(kit.Body.Size).TextColor(p.neutral)
			ui.Text(c, "Tasks").FontSize(kit.Display.Size).Bold()
			a.taskFilters(c, k, p)
			a.taskSummary(c, k, p)
			a.taskResults(c, k, p)
		})
	})
}

func (a *App) taskFilters(c *ui.Context, k kit.Colors, p palette) {
	ui.Column(c).Gap(kit.Space8).Padding(kit.Space12).Radius(kit.RadiusLG).
		Background(k.Panel).Border(1, p.line).Children(func() {
		ui.Row(c).Gap(kit.Space8).Children(func() {
			submitted := ui.SearchField(c, &a.SearchQ).Grow(1).Height(kit.Control).
				Label("Search tasks").Placeholder("Title, goal or ref").Submitted()
			if kit.PrimaryButton(c, k, "tasks:search", "Search", 96, a.SearchBusy) || submitted {
				a.SearchNow()
			}
		})
		fields := func() {
			ui.Column(c).Grow(1).MinWidth(140).Children(func() {
				kit.SelectLabel(c, k, "Status", &a.SearchStatus, statusOptions())
			})
			if len(a.Projects.Projects) > 1 {
				ui.Column(c).Grow(1).MinWidth(140).Children(func() {
					kit.SelectLabel(c, k, "Project", &a.SearchProject, a.projectOptions())
				})
			}
			kit.Field(c, k, "Since", "YYYY-MM-DD", &a.SearchSince)
			kit.Field(c, k, "Until", "YYYY-MM-DD", &a.SearchUntil)
		}
		if a.narrow {
			ui.Column(c).Gap(kit.Space8).Children(fields)
			return
		}
		ui.Row(c).Gap(kit.Space8).Children(fields)
	})
}

func (a *App) taskSummary(c *ui.Context, k kit.Colors, p palette) {
	if a.SearchErr != "" || a.searchFor != a.SearchQuery {
		return
	}
	chips := a.taskChips()
	ui.Row(c).MinHeight(24).AlignItems(ui.Center).Children(func() {
		for _, chip := range chips {
			item := chip
			el := ui.ButtonBase(c.Key("tasks:chip:"+item.Key)).Height(kit.Badge+4).
				Padding(0, kit.Space8).Gap(kit.Space6).Radius(kit.RadiusPill).
				Background(p.accentBG).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Label(item.Label)
			el.Children(func() {
				ui.Text(c, item.Label).FontSize(kit.Meta.Size).TextColor(p.accent).SingleLine()
				ui.Icon(c, kit.Icon("close")).Size(12, 12).TextColor(p.accent)
			})
			if el.Clicked() {
				a.dropTaskChip(item.Key)
			}
		}
		ui.Spacer(c)
		if a.searchStale {
			ui.Text(c, "Stale · retrying").FontSize(kit.Meta.Size).TextColor(p.wait).
				MarginEnd(kit.Space12).Tooltip(a.searchStaleErr)
		}
		if len(chips) > 0 {
			if kit.TextButton(c, k, "tasks:clear-chips", "Clear") {
				a.clearTaskFilters()
			}
		}
		ui.Textf(c, "%d %s", a.Search.Total, taskCount(a.Search.Total)).FontSize(kit.Meta.Size).
			TextColor(p.neutral)
	})
}

func taskCount(n int) string {
	if n == 1 {
		return "task"
	}
	return "tasks"
}

func (a *App) taskResults(c *ui.Context, k kit.Colors, p palette) {
	if a.SearchErr != "" {
		ui.Column(c).Gap(kit.Space8).Padding(kit.Space16).Radius(kit.RadiusLG).
			Background(p.failBG).Border(1, p.fail).Children(func() {
			ui.Text(c, "The tasks could not be searched").FontSize(kit.Heading.Size).Bold().TextColor(p.fail)
			ui.Text(c, a.SearchErr).FontSize(kit.Body.Size)
			if kit.Button(c, k, "tasks:retry", "Retry", 100, false) {
				a.SearchNow()
			}
		})
		return
	}
	if a.searchFor != a.SearchQuery {
		if a.SearchBusy {
			ui.Text(c, "Searching…").FontSize(kit.Body.Size).TextColor(p.neutral)
		}
		return
	}
	if len(a.Search.Groups) == 0 {
		ui.Text(c, "No tasks match.").FontSize(kit.Body.Size).TextColor(p.neutral)
	}
	for _, g := range a.Search.Groups {
		if len(g.Tasks) == 0 {
			continue
		}
		a.searchGroup(c, p, g)
	}
	a.taskPager(c, k, p)
}

func (a *App) searchGroup(c *ui.Context, p palette, g TaskSearchGroup) {
	ui.Row(c).Gap(kit.Space6).Padding(kit.Space14, 0, kit.Space4).Label("group " + g.Name).Children(func() {
		ui.Text(c, g.Name).FontSize(kit.Meta.Size).Bold().TextColor(p.neutral)
		ui.Badge(c, fmt.Sprint(g.Count)).Background(p.neutralBG).TextColor(p.neutral).
			Label(fmt.Sprintf("%s %d", g.Name, g.Count))
	})
	ui.Column(c).Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).Children(func() {
		for _, row := range g.Tasks {
			a.searchRow(c, p, row.TaskRow, row.FinishedAt)
		}
	})
}

func (a *App) searchRow(c *ui.Context, p palette, r TaskRow, finished string) {
	el := ui.ButtonBase(c.Key("search-task:"+r.Ref)).Height(kit.RowLG).Gap(kit.Space8).
		Padding(kit.Space4).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		BorderWidth(0, 0, 1, 0).BorderColor(p.line).Label("task " + r.Ref)
	el.Children(func() {
		ui.Icon(c, stateIcon(r.State)).TextColor(stateColor(r.State, p)).
			Label("state " + stateWord(r.State))
		ui.Text(c, r.Ref).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.accent).SingleLine()
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, r.Title).FontSize(kit.Body.Size).FixedLineHeight(20).SingleLine()
			if a.Status.Projects > 1 && r.Project != "" || r.Attempt != nil || r.Waits != nil || r.Delivered != nil {
				row := r
				row.Children = 0
				a.taskSubline(c, p, row, "", 0)
			}
		})
		if finished != "" {
			ui.Text(c, taskClock(finished)).Font("monospace").FontSize(kit.Meta.Size).
				TextColor(p.neutral).FontFeatures("tnum").SingleLine()
		}
	})
	if el.Clicked() {
		a.OpenRef(r.Ref)
	}
}

func taskClock(at string) string {
	t, ok := parseAt(at)
	if !ok {
		return ""
	}
	return t.UTC().Format("15:04")
}

func (a *App) taskPager(c *ui.Context, k kit.Colors, p palette) {
	page, pages := a.Search.Page, a.Search.Pages
	if pages <= 1 && page <= 1 {
		return
	}
	ui.Row(c).Gap(kit.Space12).AlignItems(ui.Center).Children(func() {
		if page > 1 && kit.Button(c, k, "tasks:prev", "Previous", 100, a.SearchBusy) {
			a.SearchPage(page - 1)
		}
		ui.Spacer(c)
		if page <= pages {
			ui.Textf(c, "Page %d of %d", page, pages).FontSize(kit.Meta.Size).TextColor(p.neutral).
				FontFeatures("tnum")
		}
		if page < pages && kit.Button(c, k, "tasks:next", "Next", 80, a.SearchBusy) {
			a.SearchPage(page + 1)
		}
	})
}
