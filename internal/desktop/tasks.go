package desktop

import (
	"fmt"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

func (a *App) tasksScreen(c *ui.Context, p palette) {
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, "Tasks").FontSize(kit.Display.Size).Bold()
		ui.Spacer(c)
		if ui.SearchField(c, &a.TaskQuery).Label("Search tasks").Placeholder("Search tasks in the browser").Width(240).Submitted() {
			a.SearchTasks()
		}
	})
	if len(a.Tasks.Groups) == 0 {
		a.emptyCard(c, p, "No tasks", "The supervisor has not captured a task yet.")
		return
	}
	ui.Column(c).Gap(0).Children(func() {
		for _, group := range a.Tasks.Groups {
			a.taskGroup(c, p, group)
		}
		if n := a.Tasks.Hidden; n > 0 {
			ui.Textf(c, "%d more %s not shown; use hand task list --limit 1000.", n, taskPlural(n)).
				FontSize(kit.Meta.Size).TextColor(p.neutral).Padding(kit.Space4, 0)
		}
		if a.Tasks.FinishedCount > 0 {
			a.taskBrowse(c, p)
		}
	})
}

func (a *App) taskGroup(c *ui.Context, p palette, g TaskGroup) {
	ui.Row(c).Gap(kit.Space6).Padding(kit.Space14, 0, kit.Space4).Label("group " + g.Name).Children(func() {
		ui.Text(c, g.Name).FontSize(kit.Meta.Size).Bold().TextColor(p.neutral)
		ui.Badge(c, fmt.Sprint(g.Count)).Background(p.neutralBG).TextColor(p.neutral).
			Label(fmt.Sprintf("%s %d", g.Name, g.Count))
	})
	depths := taskDepths(g.Tasks)
	ui.Column(c).Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).Children(func() {
		for i, row := range g.Tasks {
			a.taskRow(c, p, row, depths[i])
		}
	})
}

func (a *App) taskRow(c *ui.Context, p palette, r TaskRow, depth int) {
	indent := float32(min(depth, 3)) * kit.Space20
	el := ui.ButtonBase(c.Key("task:"+r.Ref)).Height(kit.RowLG).Gap(kit.Space8).
		Padding(kit.Space4, kit.Space4, kit.Space4, kit.Space4+indent).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).BorderWidth(0, 0, 1, 0).BorderColor(p.line).Label("task " + r.Ref)
	el.Children(func() {
		ui.Icon(c, stateIcon(r.State)).TextColor(stateColor(r.State, p)).Label("state " + stateIconName(r.State))
		ui.Text(c, r.Ref).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.accent).SingleLine()
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, r.Title).FontSize(kit.Body.Size).FixedLineHeight(20).SingleLine()
			if (a.Status.Projects > 1 && r.Project != "") || r.Attempt != nil || r.Delivered != nil {
				a.taskSubline(c, p, r)
			}
		})
	})
	if el.Clicked() {
		a.OpenRef(r.Ref)
	}
}

func (a *App) taskSubline(c *ui.Context, p palette, r TaskRow) {
	ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).MinWidth(0).Children(func() {
		if a.Status.Projects > 1 && r.Project != "" {
			ui.Badge(c, r.Project).Background(p.neutralBG).TextColor(p.neutral).Label("project " + r.Project)
		}
		if r.Attempt != nil {
			ui.Text(c, taskAttemptLine(r)).FontSize(kit.Meta.Size).TextColor(p.neutral).
				FixedLineHeight(18).SingleLine().MinWidth(0)
		}
		if r.Delivered != nil {
			a.taskDeliverable(c, p, *r.Delivered)
		}
	})
}

func (a *App) taskDeliverable(c *ui.Context, p palette, d Deliverable) {
	if d.Kind == "file" {
		color := p.neutral
		if d.Href != "" {
			color = p.accent
		}
		ui.Text(c, "file: "+path.Base(d.Ref)).Font("monospace").FontSize(kit.Meta.Size).
			TextColor(color).SingleLine().Label("deliverable file")
		return
	}
	ui.Badge(c, d.Kind).Background(ui.Transparent).TextColor(p.accent).Border(1, p.line).
		Label("deliverable " + d.Kind)
}

func (a *App) taskBrowse(c *ui.Context, p palette) {
	label := fmt.Sprintf("Browse finished tasks (%d)", a.Tasks.FinishedCount)
	el := ui.ButtonBase(c.Key("tasks:finished")).Padding(kit.Space8, 0).Justify(ui.Start).
		Cursor(ui.CursorPointer).Label(label)
	el.Children(func() {
		ui.Text(c, label).FontSize(kit.Meta.Size).TextColor(p.accent)
	})
	if el.Clicked() {
		a.runOpen("tasks")
	}
}

func taskAttemptLine(r TaskRow) string {
	a := r.Attempt
	word := r.Agent
	if word == "" {
		word = a.Status
	}
	parts := []string{a.Ref, a.Harness}
	if a.Model != "" {
		parts = append(parts, a.Model)
	}
	line := strings.Join(parts, " ")
	if word != "" {
		line += " · " + word
	}
	return line
}

func taskDepths(rows []TaskRow) []int {
	depths := make([]int, len(rows))
	at := make(map[string]int, len(rows))
	for i, r := range rows {
		if j, ok := at[r.Parent]; ok && r.Parent != r.Ref {
			depths[i] = depths[j] + 1
		}
		at[r.Ref] = i
	}
	return depths
}

func taskPlural(n int) string {
	if n == 1 {
		return "task is"
	}
	return "tasks are"
}
