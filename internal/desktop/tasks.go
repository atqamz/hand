package desktop

import (
	"fmt"
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
			if len(group.Tasks) == 0 {
				continue
			}
			a.taskGroup(c, p, group)
		}
		if n := a.Tasks.Hidden; n > 0 {
			ui.Textf(c, "%d more %s not shown; use hand task list --status inbox,active,done,abandoned --limit 1000.", n, taskPlural(n)).
				FontSize(kit.Meta.Size).TextColor(p.neutral).Padding(kit.Space4, 0)
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
	inGroup := make(map[string]bool, len(g.Tasks))
	for _, r := range g.Tasks {
		inGroup[r.Ref] = true
	}
	ui.Column(c).Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).Children(func() {
		for i, row := range g.Tasks {
			under := ""
			if row.Parent != "" && (!inGroup[row.Parent] || row.Parent == row.Ref) {
				under = row.Parent
			}
			a.taskRow(c, p, row, depths[i], under)
		}
	})
}

func (a *App) taskRow(c *ui.Context, p palette, r TaskRow, depth int, under string) {
	indent := float32(min(depth, 3)) * kit.Space20
	el := ui.ButtonBase(c.Key("task:"+r.Ref)).Height(kit.RowLG).Gap(kit.Space8).
		Padding(kit.Space4, kit.Space4, kit.Space4, kit.Space4+indent).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).BorderWidth(0, 0, 1, 0).BorderColor(p.line).Label("task " + r.Ref)
	el.Children(func() {
		ui.Icon(c, stateIcon(r.State)).TextColor(stateColor(r.State, p)).
			Label("state " + stateWord(r.State))
		ui.Text(c, r.Ref).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.accent).SingleLine()
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, r.Title).FontSize(kit.Body.Size).FixedLineHeight(20).SingleLine()
			if (a.Status.Projects > 1 && r.Project != "") || r.Attempt != nil || r.Waits != nil ||
				under != "" || r.Children > 0 || depth > 3 {
				a.taskSubline(c, p, r, under, depth)
			}
		})
		if r.Delivered != nil {
			a.taskDeliverable(c, p, *r.Delivered)
		}
	})
	if el.Clicked() {
		a.OpenRef(r.Ref)
	}
}

func (a *App) taskSubline(c *ui.Context, p palette, r TaskRow, under string, depth int) {
	ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).MinWidth(0).Children(func() {
		if a.Status.Projects > 1 && r.Project != "" {
			ui.Badge(c, taskProject(r.Project)).Background(p.neutralBG).TextColor(p.neutral).
				Label("project " + r.Project).Tooltip(r.Project).Shrink(0)
		}
		if r.Attempt != nil {
			ui.Text(c, taskAttemptLine(r)).FontSize(kit.Meta.Size).TextColor(p.neutral).
				FixedLineHeight(18).SingleLine().MinWidth(0)
		}
		if r.Waits != nil {
			ui.Text(c, taskWaitsLine(*r.Waits)).FontSize(kit.Meta.Size).TextColor(p.neutral).
				FixedLineHeight(18).SingleLine().MinWidth(0)
		}
		if depth > 3 {
			ui.Textf(c, "depth %d", depth).FontSize(kit.Meta.Size).TextColor(p.neutral).
				FixedLineHeight(18).SingleLine()
		}
		if under != "" {
			ui.Text(c, "under "+under).FontSize(kit.Meta.Size).TextColor(p.neutral).
				FixedLineHeight(18).SingleLine()
		}
		if r.Children > 0 {
			ui.Textf(c, "%d %s", r.Children, taskSubtasks(r.Children)).FontSize(kit.Meta.Size).
				TextColor(p.neutral).FixedLineHeight(18).SingleLine()
		}
	})
}

func (a *App) taskDeliverable(c *ui.Context, p palette, d Deliverable) {
	if d.Kind == "file" {
		color := p.neutral
		if d.Href != "" {
			color = p.accent
		}
		ui.Text(c, "file: "+taskFileBase(d.Ref)).Font("monospace").FontSize(kit.Meta.Size).
			TextColor(color).SingleLine().MaxWidth(200).Shrink(0)
		return
	}
	ui.Badge(c, d.Kind).Background(ui.Transparent).TextColor(p.accent).Border(1, p.line).
		Label("deliverable " + d.Kind).Shrink(0)
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

func taskWaitsLine(w Waits) string {
	line := "Waits until " + taskDay(w.Due)
	if w.Note != "" {
		line += ": " + w.Note
	}
	return line
}

func taskDay(at string) string {
	t, ok := parseAt(at)
	if !ok {
		return at
	}
	return t.UTC().Format("Jan 2")
}

func taskProject(name string) string {
	const max = 24
	runes := []rune(name)
	if len(runes) <= max {
		return name
	}
	return string(runes[:max-1]) + "…"
}

func taskSubtasks(n int) string {
	if n == 1 {
		return "sub-task"
	}
	return "sub-tasks"
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

func taskFileBase(p string) string {
	p = strings.TrimRight(p, `\/`)
	return p[strings.LastIndexAny(p, `\/`)+1:]
}

func taskPlural(n int) string {
	if n == 1 {
		return "task is"
	}
	return "tasks are"
}
