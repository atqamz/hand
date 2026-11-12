package desktop

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/atqamz/hand/internal/desktop/kit"
)

type palette struct {
	line, lineStrong, panel, card, hover        ui.Color
	fail, wait, pass, neutral, accent           ui.Color
	failBG, waitBG, passBG, neutralBG, accentBG ui.Color
}

func paletteFor(dark bool) palette {
	k := kit.ColorsFor(dark)
	return palette{
		line: k.Line, lineStrong: k.LineStrong, panel: k.Panel, card: k.Card, hover: k.Hover,
		fail: k.Fail, wait: k.Wait, pass: k.Pass, neutral: k.Neutral, accent: k.Accent,
		failBG: k.FailBG, waitBG: k.WaitBG, passBG: k.PassBG, neutralBG: k.NeutralBG, accentBG: k.AccentBG,
	}
}

func boardTheme(dark bool) *ui.Theme {
	t := ui.LightTheme()
	if dark {
		t = ui.DarkTheme()
	}
	k := kit.ColorsFor(dark)
	p := paletteFor(dark)
	t.Background = k.BG
	t.Surface = p.panel
	t.SurfaceHover = p.hover
	t.SurfacePressed = p.card
	t.Border = p.lineStrong
	t.Text = k.FG
	t.TextMuted = p.neutral
	t.Accent = p.accent
	t.AccentHover = p.accent
	t.AccentPressed = p.accent
	t.AccentText = k.OnPrimary
	t.Danger = p.fail
	t.Warning = p.wait
	t.Success = p.pass
	t.Selection = p.accentBG
	t.Focus = p.accent
	t.Scrollbar = p.lineStrong
	t.Radius = kit.RadiusMD
	t.Spacing = kit.Space4
	t.FontSize = kit.Body.Size
	return t
}

func (a *App) View(c *ui.Context) {
	dark := c.Theme().Dark
	p, k := paletteFor(dark), kit.ColorsFor(dark)
	c.SetTheme(boardTheme(dark))
	if a.receipt != "" {
		c.AddToast(ui.Toast{ID: a.receiptKey, Title: a.receipt, Timeout: receiptFor})
		a.receipt, a.receiptKey = "", ""
	}
	ui.Row(c).Fill().Children(func() {
		a.rail(c, k, p)
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
			a.masthead(c, k, p)
			a.body(c, k, p)
		})
	})
}

func (a *App) rail(c *ui.Context, k kit.Colors, p palette) {
	bg, fg := toneColors(a.Status.Needs.Worst, p)
	if a.Status.Needs.Worst == "" {
		bg, fg = p.neutralBG, p.neutral
	}
	item := func(n kit.NavItem) bool {
		n.Open = a.RailOpen
		return kit.Nav(c, k, n)
	}
	kit.Rail(c, k, a.RailOpen, a.toggleRail, func() {
		if item(kit.NavItem{Key: "screen:needs", Label: "Board", Icon: "board", Current: a.Screen == BoardScreen || a.Screen == DecisionScreen || a.Screen == ReportScreen,
			Count: a.waitingNeeds(), CountBG: bg, CountFG: fg}) {
			a.Show(BoardScreen)
		}
		item(kit.NavItem{Key: "screen:tasks", Label: "Tasks", Icon: "tasks", Inert: true})
		item(kit.NavItem{Key: "screen:conversation", Label: "Conversation", Icon: "chat", Inert: true, Dot: a.Status.AgentState == "working"})
		if item(kit.NavItem{Key: "screen:screen", Label: "Screen", Icon: "screen"}) {
			a.OpenScreen()
		}
		if item(kit.NavItem{Key: "screen:phone", Label: "Phone", Icon: "phone"}) {
			a.OpenPhone()
		}
		if len(a.Fleets) > 0 {
			kit.RailHead(c, k, a.RailOpen, "Fleets")
		}
		for _, f := range a.Fleets {
			fleet := f
			if item(kit.NavItem{Key: "fleet:" + fleet.ID, Label: fleetName(fleet), Fleet: fleet.ID, Current: fleet.ID == a.Fleet.ID}) {
				a.SelectFleet(fleet.ID)
			}
		}
	})
}

func (a *App) masthead(c *ui.Context, k kit.Colors, p palette) {
	name := fleetName(a.Fleet)
	if name == "" {
		name = "Hand"
	}
	kit.Masthead(c, k, a.Fleet.ID, name, func() {
		ui.Row(c).Gap(kit.Space12).AlignItems(ui.Center).Children(func() {
			if a.Model != nil && a.Fleet.ID != "" {
				statusPill(c, p, a.Status.Pill)
				if sup := a.Status.Supervisor; sup != nil && sup.Pending > 0 {
					ui.Textf(c, "%d queued", sup.Pending).FontSize(kit.Meta.Size).TextColor(p.neutral)
				}
				if g := a.Status.Gauge; g.Text != "" {
					_, fg := toneColors(g.Level, p)
					if g.Level == "" {
						fg = p.accent
					}
					kit.ContextBar(c, k, g.Pct, fg)
					text := ui.Text(c, g.Text).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral).FontFeatures("tnum")
					if g.Level != "" {
						text.TextColor(fg)
					}
					if g.Level == "fail" {
						text.Bold()
					}
				}
			}
			if a.Stale != "" {
				ui.Text(c, "Stale · retrying").FontSize(kit.Meta.Size).TextColor(p.wait).Tooltip(a.Stale)
			}
		})
	})
}

func (a *App) body(c *ui.Context, k kit.Colors, p palette) {
	if a.Model == nil || a.Fleet.ID == "" {
		ui.Scroll(c).Grow(1).Padding(20, 20, 32).Children(func() {
			ui.Column(c).Gap(16).Children(func() {
				a.openCard(c, p)
				switch {
				case a.Err != "":
					a.errorCard(c, p)
				case a.Loading:
					a.emptyCard(c, p, "Reading the board", "The window waits for the board to answer.")
				default:
					a.emptyCard(c, p, "Nothing to show", "Register a fleet and start its board with `hand open`.")
				}
			})
		})
		return
	}
	a.actionCard(c, p)
	a.panes(c, k, p)
}

func (a *App) workPane(c *ui.Context, k kit.Colors, p palette) {
	ui.Scroll(c).Grow(1).Padding(20, 20, 32).Children(func() {
		ui.Column(c).Gap(16).Selectable().Children(func() {
			a.openCard(c, p)
			switch {
			case a.Err != "":
				a.errorCard(c, p)
			case a.Loading:
				a.emptyCard(c, p, "Reading the board", "The window waits for the board to answer.")
			case a.Screen == BoardScreen:
				a.needsScreen(c, k, p)
				a.tasksScreen(c, p)
			case a.Screen == TaskScreen:
				a.taskScreen(c, p)
			case a.Screen == AttemptScreen:
				a.attemptScreen(c, p)
			case a.Screen == DecisionScreen:
				a.decisionScreen(c, p)
			case a.Screen == ReportScreen:
				a.reportScreen(c, p)
			}
		})
	})
}

func (a *App) errorCard(c *ui.Context, p palette) {
	ui.Column(c).Gap(10).Padding(16).Radius(10).Background(p.failBG).
		Border(1, p.fail).Children(func() {
		ui.Text(c, "The board could not be read").FontSize(kit.Heading.Size).Bold().TextColor(p.fail)
		ui.Text(c, a.Err).FontSize(kit.Body.Size)
		if len(a.back) > 0 && ui.Button(c.Key("back"), "Back").Width(100).Clicked() {
			a.Back()
		}
		if ui.Button(c.Key("retry"), "Retry").Width(100).Clicked() {
			a.Retry()
		}
	})
}

func (a *App) openCard(c *ui.Context, p palette) {
	if a.OpenErr == "" {
		return
	}
	ui.Column(c).Gap(8).Padding(14).Radius(10).Background(p.failBG).
		Border(1, p.fail).Children(func() {
		ui.Text(c, "The browser did not open").FontSize(kit.Title.Size).Bold().TextColor(p.fail)
		ui.Text(c, a.OpenErr).FontSize(kit.Meta.Size)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c.Key("dismiss-open"), "Dismiss").Clicked() {
				a.OpenErr = ""
			}
		})
	})
}

func (a *App) emptyCard(c *ui.Context, p palette, title, body string) {
	ui.Column(c).Gap(6).Padding(20).Radius(10).Background(p.card).
		Border(1, p.line).Children(func() {
		ui.Text(c, title).FontSize(kit.Heading.Size).Bold()
		ui.Text(c, body).FontSize(kit.Body.Size).TextColor(p.neutral)
	})
}

func (a *App) actionCard(c *ui.Context, p palette) {
	if a.ActionErr == "" {
		return
	}
	title, bg, tone := "The action did not go through", p.failBG, p.fail
	if a.ActionWarn {
		title, bg, tone = "The action went through", p.waitBG, p.wait
	}
	ui.Column(c).Gap(8).Padding(14).Margin(8, 16, 0).Radius(10).Background(bg).
		Border(1, tone).Children(func() {
		ui.Text(c, title).FontSize(kit.Title.Size).Bold().TextColor(tone)
		ui.Text(c, a.ActionErr).FontSize(kit.Meta.Size)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c.Key("dismiss-action"), "Dismiss").Clicked() {
				a.ActionErr = ""
				a.ActionWarn = false
			}
			if ui.Button(c.Key("retry-action"), "Refresh").Clicked() {
				a.Retry()
			}
		})
	})
}

func (a *App) supervisorAsk(c *ui.Context, p palette, sup *Supervisor) {
	ui.Column(c).Gap(8).Padding(12).Radius(8).Background(p.card).Border(1, p.line).Children(func() {
		ui.Text(c, sup.Ask.Question).FontSize(kit.Body.Size).Bold()
		ui.Column(c).Gap(8).Children(func() {
			for _, option := range sup.Ask.Options {
				name := "key:" + strings.Join(option.Keys, " ")
				label := option.Label
				if a.BusyAction == name {
					label = "Sending…"
				}
				if ui.Button(c.Key(name), label).Disabled(a.Busy || sup.Keys == nil).Clicked() {
					a.Press(option.Keys...)
				}
			}
		})
	})
}

var supervisorKeyLabels = map[string]string{"enter": "Enter", "esc": "Esc", "up": "↑", "down": "↓"}

func (a *App) taskScreen(c *ui.Context, p palette) {
	t := a.Task
	a.detailHeader(c, p, t.Task.Ref, t.Task.Title)
	ui.Row(c).Gap(8).Children(func() {
		statusPill(c, p, t.Pill)
		ui.Text(c, t.Task.Status).FontSize(kit.Meta.Size).TextColor(p.neutral)
		if t.Task.Project != "" && a.Status.Projects > 1 {
			ui.Badge(c, t.Task.Project).Background(p.neutralBG).TextColor(p.neutral)
		}
		ui.Text(c, stamp(t.Task.CreatedAt)).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
	})
	if t.Task.Goal != "" {
		a.paragraph(c, p, "Goal", t.Task.Goal)
	}
	if t.Plan != nil {
		a.paragraph(c, p, fmt.Sprintf("Plan · %s", t.Plan.Ref), t.Plan.Body)
	}
	a.delivered(c, p, t.Delivered)
	if len(t.OpenDecisions) > 0 {
		a.section(c, p, "Open decisions")
		ui.Column(c).Gap(0).Children(func() {
			for _, d := range t.OpenDecisions {
				a.decisionRow(c, p, d)
			}
		})
	}
	if history := closedDecisions(t.Decisions, t.OpenDecisions); len(history) > 0 {
		a.section(c, p, "Decisions")
		ui.Column(c).Gap(0).Children(func() {
			for _, d := range history {
				a.decisionRow(c, p, d)
			}
		})
	}
	if len(t.Reports) > 0 {
		a.section(c, p, "Reports")
		ui.Column(c).Gap(0).Children(func() {
			for _, report := range t.Reports {
				a.reportRow(c, p, report)
			}
		})
		if t.MoreReports {
			more(c, p, "Only the latest 50 reports are shown.")
		}
	}
	if len(t.Attempts) > 0 {
		a.section(c, p, "Attempts")
		ui.Column(c).Gap(0).Children(func() {
			for _, attempt := range t.Attempts {
				a.attemptRow(c, p, attempt)
			}
		})
	}
	if len(t.Events) > 0 {
		a.section(c, p, "Events")
		ui.Column(c).Gap(0).Children(func() {
			for _, event := range t.Events {
				a.eventRow(c, p, event)
			}
		})
		if t.MoreEvents {
			more(c, p, "Only the latest 50 events are shown.")
		}
	}
}

func (a *App) attemptScreen(c *ui.Context, p palette) {
	at := a.Attempt
	a.detailHeader(c, p, at.Attempt.Ref, at.Task.Title)
	ui.Row(c).Gap(8).Children(func() {
		statusPill(c, p, at.State)
		ui.Text(c, fmt.Sprintf("%s %s %s", at.Attempt.Harness, at.Attempt.Model, at.Attempt.Effort)).FontSize(kit.Body.Size)
		ui.Text(c, at.Attempt.Status).FontSize(kit.Meta.Size).TextColor(p.neutral)
		if at.Duration != "" {
			ui.Text(c, at.Duration).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
		}
	})
	if at.Attempt.Branch != "" {
		ui.Text(c, at.Attempt.Branch).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
	}
	if at.Attempt.Reason != "" {
		a.paragraph(c, p, "Reason", at.Attempt.Reason)
	}
	if at.Brief != "" {
		a.paragraph(c, p, "Brief", at.Brief)
	}
	if at.ModelNote != "" {
		a.paragraph(c, p, "Model", at.ModelNote)
	}
	a.delivered(c, p, at.Delivered)
	if len(at.Reports) > 0 {
		a.section(c, p, "Reports")
		ui.Column(c).Gap(0).Children(func() {
			for _, report := range at.Reports {
				a.reportRow(c, p, report)
			}
		})
		if at.MoreReports {
			more(c, p, "Only the latest 50 reports are shown.")
		}
	}
	if at.Transcript.Shown {
		a.section(c, p, "Transcript")
		if at.Transcript.Model != "" {
			ui.Text(c, at.Transcript.Model).FontSize(kit.Meta.Size).TextColor(p.neutral)
		}
		for _, turn := range at.Transcript.Turns {
			ui.Column(c).Gap(2).Padding(6, 0, 8).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, turn.Role).FontSize(kit.Meta.Size).Bold().TextColor(p.neutral)
					ui.Text(c, stamp(turn.At)).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
					if turn.Queued {
						ui.Badge(c, "queued").Background(p.neutralBG).TextColor(p.neutral)
					}
				})
				a.markdown(c, turn.Text)
			})
		}
		if at.Transcript.More {
			more(c, p, "Only the latest 50 entries are shown.")
		}
	} else if at.Transcript.Note != "" {
		a.section(c, p, "Transcript")
		ui.Text(c, at.Transcript.Note).FontSize(kit.Meta.Size).TextColor(p.neutral)
	}
	if len(at.Events) > 0 {
		a.section(c, p, "Events")
		ui.Column(c).Gap(0).Children(func() {
			for _, event := range at.Events {
				a.eventRow(c, p, event)
			}
		})
		if at.MoreEvents {
			more(c, p, "Only the latest 50 events are shown.")
		}
	}
}

func (a *App) decisionScreen(c *ui.Context, p palette) {
	d := a.Decision
	a.detailHeader(c, p, d.Decision.Ref, d.Decision.Headline)
	ui.Row(c).Gap(8).Children(func() {
		statusPill(c, p, d.Decision.Pill)
		ui.Text(c, d.Decision.Status).FontSize(kit.Meta.Size).TextColor(p.neutral)
		ui.Text(c, d.Task.Ref).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
		ui.Text(c, d.Task.Title).FontSize(kit.Body.Size).SingleLine().Grow(1)
		if ui.Button(c.Key("open-task"), "Open task").Clicked() {
			a.OpenRef(d.Task.Ref)
		}
	})
	if d.Decision.Question != "" && d.Decision.Question != d.Decision.Headline {
		if body := questionBody(d.Decision.Question, d.Decision.Headline, len(d.Options) > 0); body != "" {
			a.markdown(c, body)
		}
	}
	a.answerForm(c, p, d, true)
	if d.Decision.Answer != "" {
		a.paragraph(c, p, "Answer", d.Decision.Answer)
	}
	a.section(c, p, "Task")
	ui.Textf(c, "%s · %s", d.Task.Ref, d.Task.Title).FontSize(kit.Body.Size)
}

func (a *App) reportScreen(c *ui.Context, p palette) {
	r := a.Report
	a.detailHeader(c, p, r.Report.Ref, r.Report.Summary)
	ui.Row(c).Gap(8).Children(func() {
		_, statusFg := toneColors(r.Report.Status, p)
		ui.Badge(c, r.Report.Status).Background(p.neutralBG).TextColor(statusFg)
		ui.Text(c, r.Report.Attempt).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
		ui.Text(c, r.Report.Task).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
		ui.Text(c, stamp(r.Report.CreatedAt)).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
		ui.Spacer(c)
		if r.Report.AckedAt == "" {
			label := "Mark read"
			if a.Busy {
				label = "Marking read…"
			}
			if ui.Button(c.Key("ack-report"), label).Disabled(a.Busy).Clicked() {
				a.Ack(r.Report.Ref)
			}
		} else {
			ui.Textf(c, "Read by %s", r.Report.AckedBy).FontSize(kit.Meta.Size).TextColor(p.neutral)
		}
		if ui.Button(c.Key("open-task"), "Open task").Clicked() {
			a.OpenRef(r.Task.Ref)
		}
	})
	a.delivered(c, p, r.Delivered)
	a.markdown(c, r.Report.Body)
	if r.Report.AckedAt != "" {
		ui.Textf(c, "Read %s", stamp(r.Report.AckedAt)).FontSize(kit.Meta.Size).TextColor(p.neutral)
	}
}

func (a *App) detailHeader(c *ui.Context, p palette, ref, title string) {
	ui.Row(c).Gap(10).Children(func() {
		if ui.Button(c.Key("back"), "Back").Clicked() {
			a.Back()
		}
		ui.Text(c, ref).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
		ui.Text(c, title).FontSize(kit.Display.Size).Bold().SingleLine().Grow(1)
	})
}

func (a *App) section(c *ui.Context, p palette, title string) {
	section(c, p, title)
}

func more(c *ui.Context, p palette, text string) {
	ui.Text(c, text).FontSize(kit.Meta.Size).TextColor(p.neutral)
}

func section(c *ui.Context, p palette, title string) {
	ui.Text(c, title).FontSize(kit.Meta.Size).Bold().TextColor(p.neutral).Padding(0, 0, 2)
}

func (a *App) paragraph(c *ui.Context, p palette, title, body string) {
	ui.Column(c).Gap(6).Children(func() {
		section(c, p, title)
		a.markdown(c, body)
	})
}

func closedDecisions(all, open []Decision) []Decision {
	shown := make(map[string]bool, len(open))
	for _, d := range open {
		shown[d.Ref] = true
	}
	var out []Decision
	for _, d := range all {
		if d.Status != "open" && !shown[d.Ref] {
			out = append(out, d)
		}
	}
	return out
}

func (a *App) decisionRow(c *ui.Context, p palette, d Decision) {
	el := ui.ButtonBase(c.Key("decision:"+d.Ref)).Gap(8).Padding(6, 8).Cursor(ui.CursorPointer).
		BorderWidth(0, 0, 1, 0).BorderColor(p.line)
	el.Children(func() {
		ui.Text(c, d.Ref).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
		ui.Text(c, d.Headline).FontSize(kit.Body.Size).SingleLine().Grow(1)
		if d.Answer != "" {
			ui.Text(c, firstLine(d.Answer)).FontSize(kit.Meta.Size).TextColor(p.neutral).SingleLine()
		}
		statusPill(c, p, d.Pill)
	})
	if el.Clicked() {
		a.OpenRef(d.Ref)
	}
}

func (a *App) reportRow(c *ui.Context, p palette, r Report) {
	el := ui.ButtonBase(c.Key("report:"+r.Ref)).Gap(8).Padding(6, 8).Cursor(ui.CursorPointer).
		BorderWidth(0, 0, 1, 0).BorderColor(p.line)
	el.Children(func() {
		ui.Text(c, r.Ref).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
		ui.Text(c, r.Summary).FontSize(kit.Body.Size).SingleLine().Grow(1)
		_, statusFg := toneColors(r.Status, p)
		ui.Badge(c, r.Status).Background(p.neutralBG).TextColor(statusFg)
	})
	if el.Clicked() {
		a.OpenRef(r.Ref)
	}
}

func (a *App) attemptRow(c *ui.Context, p palette, at AttemptRow) {
	el := ui.ButtonBase(c.Key("attempt:"+at.Ref)).Gap(8).Padding(6, 8).Cursor(ui.CursorPointer).
		BorderWidth(0, 0, 1, 0).BorderColor(p.line)
	el.Children(func() {
		ui.Text(c, at.Ref).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
		ui.Text(c, attemptMeta(&at.Attempt)).FontSize(kit.Body.Size).Grow(1).SingleLine()
		ui.Text(c, stateWord(at.State)).FontSize(kit.Meta.Size).TextColor(stateColor(at.State, p))
	})
	if el.Clicked() {
		a.OpenRef(at.Ref)
	}
}

func (a *App) eventRow(c *ui.Context, p palette, e Event) {
	ui.Row(c).Gap(8).Padding(3, 0).Children(func() {
		ui.Text(c, stamp(e.At)).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral).Width(112)
		ui.Text(c, e.Kind).Font("monospace").FontSize(kit.Meta.Size)
		if e.Detail != "" {
			ui.Text(c, e.Detail).FontSize(kit.Meta.Size).TextColor(p.neutral).SingleLine().Grow(1)
		}
	})
}

func (a *App) delivered(c *ui.Context, p palette, ds []Deliverable) {
	if len(ds) == 0 {
		return
	}
	a.section(c, p, "Delivered")
	ui.Column(c).Gap(2).Children(func() {
		for i, d := range ds {
			a.deliveredRow(c, p, i, d)
		}
	})
}

func (a *App) deliveredRow(c *ui.Context, p palette, i int, d Deliverable) {
	dest, board := a.deliveredTarget(d)
	if dest == "" {
		ui.Row(c).Gap(8).Children(func() {
			ui.Badge(c, d.Kind).Background(p.accentBG).TextColor(p.accent)
			ui.Text(c, d.Ref).Font("monospace").FontSize(kit.Meta.Size)
		})
		return
	}
	el := ui.ButtonBase(c.Key("delivered:"+strconv.Itoa(i))).Gap(8).Padding(2, 0).Cursor(ui.CursorPointer)
	el.Children(func() {
		ui.Badge(c, d.Kind).Background(p.accentBG).TextColor(p.accent)
		ui.Text(c, d.Ref).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.accent)
		ui.Spacer(c)
	})
	if el.Clicked() {
		if board {
			a.OpenBoardPath(dest)
		} else {
			c.OpenURL(dest)
		}
	}
}

func (a *App) deliveredTarget(d Deliverable) (string, bool) {
	switch {
	case d.Href != "":
		return a.linkTarget(d.Href)
	case d.Kind != "file":
		return a.linkTarget(d.Ref)
	}
	return "", false
}

func statusPill(c *ui.Context, p palette, word string) {
	if word == "" {
		word = "unknown"
	}
	bg, fg := toneColors(word, p)
	ui.Row(c).Gap(5).Padding(1, 8).Radius(999).Background(bg).Children(func() {
		ui.Icon(c, stateIcon(word)).TextColor(fg)
		ui.Text(c, word).FontSize(kit.Meta.Size).Bold().TextColor(fg)
	})
}

func toneOf(state string) string {
	switch state {
	case "fail", "failing", "failed", "failure", "blocked", "worker", "resume", "interrupted", "stuck":
		return "fail"
	case "wait", "warn", "waiting", "limited", "nosup", "stalled", "decision", "pending":
		return "wait"
	case "pass", "ready", "running", "passing", "done":
		return "pass"
	case "accent", "working":
		return "accent"
	}
	return "neutral"
}

func toneColors(tone string, p palette) (bg, fg ui.Color) {
	switch toneOf(tone) {
	case "fail":
		return p.failBG, p.fail
	case "wait":
		return p.waitBG, p.wait
	case "pass":
		return p.passBG, p.pass
	case "accent":
		return p.accentBG, p.accent
	}
	return p.neutralBG, p.neutral
}

func stateIcon(state string) *ui.SVG { return kit.Icon(stateIconName(state)) }

func stateIconName(state string) string {
	switch state {
	case "running", "working":
		return "running"
	}
	switch toneOf(state) {
	case "fail":
		return "failure"
	case "wait":
		return "waiting"
	case "pass":
		return "passing"
	}
	return "idle"
}

func stateColor(state string, p palette) ui.Color {
	_, fg := toneColors(state, p)
	return fg
}

func stateWord(state string) string {
	if state == "" {
		return "idle"
	}
	return state
}

func attemptMeta(a *Attempt) string {
	if a == nil {
		return ""
	}
	parts := []string{a.Harness}
	if a.Model != "" {
		parts = append(parts, a.Model)
	}
	return strings.Join(parts, " ")
}

func needLabel(need Need) string {
	switch {
	case need.Title != "":
		return need.Title
	case need.Headline != "":
		return need.Headline
	}
	word := needWord(need.Kind)
	if need.Ref != "" {
		return word + " · " + need.Ref
	}
	return word
}

func needWord(kind string) string {
	switch kind {
	case "nosup":
		return "No supervisor"
	case "quiet":
		return "Quiet turn"
	case "long":
		return "Long turn"
	case "kept", "artifacts":
		return capitalize(kind)
	}
	return capitalize(strings.ReplaceAll(kind, "_", " "))
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func needRef(need Need) string {
	switch {
	case need.Decision != "":
		return need.Decision
	case need.Report != "":
		return need.Report
	case need.Attempt != "":
		return need.Attempt
	}
	return need.Task
}

func roleName(e Entry) string {
	switch e.Role {
	case "operator":
		return "you"
	case "supervisor":
		if e.Ref != "" {
			return e.Ref
		}
		return "supervisor"
	}
	return "hand"
}

func roleColor(e Entry, p palette) ui.Color {
	switch e.Role {
	case "operator":
		return p.accent
	case "supervisor":
		return p.accent
	}
	return p.neutral
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

var (
	optionLine  = regexp.MustCompile(`^\s*(\d+|[A-Z])[.)]\s+(.+)$`)
	recommended = regexp.MustCompile(`(?i)\s*\(recommended\)`)
)

func optionKey(line string) (string, bool) {
	m := optionLine.FindStringSubmatch(line)
	if m == nil || strings.TrimSpace(recommended.ReplaceAllString(m[2], "")) == "" {
		return "", false
	}
	return m[1], true
}

func questionBody(question, headline string, options bool) string {
	body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(question), strings.TrimSpace(headline)))
	if !options {
		return body
	}
	seen := map[string]bool{}
	var keep []string
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
		} else if key, ok := optionKey(line); ok && !fenced && !seen[key] {
			seen[key] = true
			continue
		}
		keep = append(keep, line)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

func fleetName(f Fleet) string {
	if f.Name != "" {
		return f.Name
	}
	return f.ID
}

func stamp(at string) string {
	t, ok := parseAt(at)
	if !ok {
		return at
	}
	return t.Local().Format("Jan 2, 15:04")
}

func (a *App) markdown(c *ui.Context, src string) {
	a.md.ShareSelection = true
	a.md.OnLink = func(dest string) {
		target, board := a.linkTarget(dest)
		switch {
		case board:
			a.OpenBoardPath(target)
		case target != "":
			c.OpenURL(target)
		}
	}
	markdown.View(c, src, &a.md)
}

func (a *App) linkTarget(dest string) (string, bool) {
	if strings.ContainsAny(dest, "\\\x00") || strings.HasPrefix(dest, "//") {
		return "", false
	}
	u, err := url.Parse(dest)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host != "" && ui.SafeToOpen(dest) {
			return dest, false
		}
	case "":
		if a.Fleet.ID == "" || u.Host != "" || !strings.HasPrefix(u.EscapedPath(), "/"+a.Fleet.ID+"/") && u.EscapedPath() != "/"+a.Fleet.ID {
			return "", false
		}
		if slices.Contains(strings.Split(u.Path, "/"), "..") {
			return "", false
		}
		return dest, true
	}
	return "", false
}
