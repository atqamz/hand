package desktop

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type palette struct {
	line, lineStrong, panel, card, hover        ui.Color
	fail, wait, pass, neutral, accent           ui.Color
	failBG, waitBG, passBG, neutralBG, accentBG ui.Color
}

func paletteFor(dark bool) palette {
	if dark {
		return palette{
			line: ui.Hex("#262b32"), lineStrong: ui.Hex("#6b7482"), panel: ui.Hex("#15181d"),
			card: ui.Hex("#1b1f25"), hover: ui.Hex("#1e232a"),
			fail: ui.Hex("#ff6b6b"), wait: ui.Hex("#e6ac45"), pass: ui.Hex("#56c77a"),
			neutral: ui.Hex("#9ba2ad"), accent: ui.Hex("#7ea6ff"),
			failBG: ui.Hex("#2c1719"), waitBG: ui.Hex("#2a2212"), passBG: ui.Hex("#13281b"),
			neutralBG: ui.Hex("#1e232a"), accentBG: ui.Hex("#172238"),
		}
	}
	return palette{
		line: ui.Hex("#e3e6ea"), lineStrong: ui.Hex("#80889a"), panel: ui.Hex("#f6f7f9"),
		card: ui.Hex("#f1f3f5"), hover: ui.Hex("#eef0f3"),
		fail: ui.Hex("#c42b32"), wait: ui.Hex("#8a5b00"), pass: ui.Hex("#1a7a3e"),
		neutral: ui.Hex("#5b626e"), accent: ui.Hex("#2457c5"),
		failBG: ui.Hex("#fdeeee"), waitBG: ui.Hex("#fcf3dc"), passBG: ui.Hex("#e4f4e9"),
		neutralBG: ui.Hex("#eef0f3"), accentBG: ui.Hex("#ebf1fd"),
	}
}

func boardTheme(dark bool) *ui.Theme {
	t := ui.LightTheme()
	if dark {
		t = ui.DarkTheme()
	}
	p := paletteFor(dark)
	t.Background = ui.Hex("#ffffff")
	if dark {
		t.Background = ui.Hex("#111317")
	}
	t.Surface = p.panel
	t.SurfaceHover = p.hover
	t.SurfacePressed = p.card
	t.Border = p.lineStrong
	t.Text = ui.Hex("#15171c")
	if dark {
		t.Text = ui.Hex("#e8eaee")
	}
	t.TextMuted = p.neutral
	t.Accent = p.accent
	t.AccentHover = p.accent
	t.AccentPressed = p.accent
	t.AccentText = ui.Hex("#ffffff")
	if dark {
		t.AccentText = ui.Hex("#111317")
	}
	t.Danger = p.fail
	t.Warning = p.wait
	t.Success = p.pass
	t.Selection = p.accentBG
	t.Focus = p.accent
	t.Scrollbar = p.lineStrong
	t.Radius = 6
	t.Spacing = 4
	t.FontSize = 14
	return t
}

func (a *App) View(c *ui.Context) {
	p := paletteFor(c.Theme().Dark)
	c.SetTheme(boardTheme(c.Theme().Dark))
	ui.Row(c).Fill().Children(func() {
		a.rail(c, p)
		ui.Column(c).Grow(1).FillHeight().Children(func() {
			a.masthead(c, p)
			a.body(c, p)
		})
	})
}

func (a *App) rail(c *ui.Context, p palette) {
	ui.Column(c).Width(208).FillHeight().Background(p.panel).
		BorderWidth(0, 1, 0, 0).BorderColor(p.line).Children(func() {
		ui.Column(c).Padding(16, 16, 12).Gap(2).Children(func() {
			ui.Text(c, "Hand").FontSize(18).Bold()
			ui.Text(c, "desktop").FontSize(12).TextColor(p.neutral)
		})
		ui.Text(c, "Fleets").FontSize(12).Bold().TextColor(p.neutral).Padding(8, 16)
		if len(a.Fleets) == 0 {
			ui.Text(c, "No fleets").FontSize(12).TextColor(p.neutral).Padding(0, 16, 12)
		}
		ui.Column(c).Padding(0, 8).Gap(2).Children(func() {
			for _, f := range a.Fleets {
				fleet := f
				a.nav(c, p, "fleet:"+fleet.ID, fleetName(fleet), fleet.ID == a.Fleet.ID, 0, "", func() {
					a.SelectFleet(fleet.ID)
				})
			}
		})
		ui.Divider(c).Margin(12, 8)
		ui.Text(c, "Read").FontSize(12).Bold().TextColor(p.neutral).Padding(8, 16)
		ui.Column(c).Padding(0, 8).Gap(2).Children(func() {
			a.nav(c, p, "screen:status", "Status", a.Screen == StatusScreen, 0, "", func() { a.Show(StatusScreen) })
			a.nav(c, p, "screen:needs", "Needs", a.Screen == NeedsScreen, a.Status.Needs.Waiting, a.Status.Needs.Worst, func() { a.Show(NeedsScreen) })
			a.nav(c, p, "screen:timeline", "Timeline", a.Screen == TimelineScreen, 0, "", func() { a.Show(TimelineScreen) })
			a.nav(c, p, "screen:tasks", "Tasks", a.Screen == TasksScreen, a.Status.Tasks.Active+a.Status.Tasks.Inbox, "", func() { a.Show(TasksScreen) })
		})
		ui.Spacer(c)
		if a.Model != nil {
			ui.Text(c, "Answers and acks go to the board.").FontSize(12).TextColor(p.neutral).Padding(8, 16, 14)
		}
	})
}

func (a *App) nav(c *ui.Context, p palette, key, label string, current bool, count int, tone string, action func()) {
	el := ui.ButtonBase(c.Key(key)).Gap(8).Padding(6, 8).Radius(6).Cursor(ui.CursorPointer)
	if current {
		el.Background(p.accentBG)
	} else {
		el.Background(ui.Transparent)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(14).SingleLine().Grow(1)
		if count > 0 {
			bg, fg := toneColors(tone, p)
			if tone == "" {
				bg, fg = p.neutralBG, p.neutral
			}
			ui.Badge(c, fmt.Sprint(count)).Background(bg).TextColor(fg)
		}
	})
	if el.Clicked() {
		action()
	}
}

func (a *App) masthead(c *ui.Context, p palette) {
	ui.Row(c).Height(56).Padding(0, 16).Gap(12).
		BorderWidth(0, 0, 1, 0).BorderColor(p.line).Children(func() {
		ui.Text(c, fleetName(a.Fleet)).FontSize(18).Bold().SingleLine()
		if a.Status.Projects > 1 {
			ui.Text(c, fmt.Sprintf("%d projects", a.Status.Projects)).FontSize(12).TextColor(p.neutral)
		}
		ui.Spacer(c)
		if a.Model != nil && a.Fleet.ID != "" {
			statusPill(c, p, a.Status.Pill)
			if sup := a.Status.Supervisor; sup != nil {
				ui.Text(c, supervisorLine(sup)).FontSize(12).TextColor(p.neutral)
			}
			if a.Status.Gauge.Text != "" {
				gauge(c, p, a.Status.Gauge)
			}
		}
		if ui.Button(c.Key("refresh"), "Refresh").Clicked() && a.Fleet.ID != "" {
			a.Refresh()
		}
	})
}

func (a *App) body(c *ui.Context, p palette) {
	ui.Scroll(c).Grow(1).Padding(20, 20, 32).Children(func() {
		ui.Column(c).Gap(16).Children(func() {
			switch {
			case a.Err != "":
				a.errorCard(c, p)
			case a.Loading:
				a.emptyCard(c, p, "Reading the board", "The window waits for the board to answer; Refresh reads again.")
			case a.Model == nil || a.Fleet.ID == "":
				a.emptyCard(c, p, "Nothing to show", "Register a fleet, start its board with `hand open`, then refresh.")
			case a.Screen == StatusScreen:
				a.statusScreen(c, p)
			case a.Screen == NeedsScreen:
				a.needsScreen(c, p)
			case a.Screen == TimelineScreen:
				a.timelineScreen(c, p)
			case a.Screen == TasksScreen:
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
		ui.Text(c, "The board could not be read").FontSize(18).Bold().TextColor(p.fail)
		ui.Text(c, a.Err).FontSize(14)
		if len(a.back) > 0 && ui.Button(c.Key("back"), "Back").Width(100).Clicked() {
			a.Back()
		}
		if ui.Button(c.Key("retry"), "Retry").Width(100).Clicked() {
			a.Retry()
		}
	})
}

func (a *App) emptyCard(c *ui.Context, p palette, title, body string) {
	ui.Column(c).Gap(6).Padding(20).Radius(10).Background(p.card).
		Border(1, p.line).Children(func() {
		ui.Text(c, title).FontSize(18).Bold()
		ui.Text(c, body).FontSize(14).TextColor(p.neutral)
	})
}

func (a *App) actionCard(c *ui.Context, p palette) {
	if a.ActionErr == "" {
		return
	}
	ui.Column(c).Gap(8).Padding(14).Radius(10).Background(p.failBG).
		Border(1, p.fail).Children(func() {
		ui.Text(c, "The action did not go through").FontSize(15).Bold().TextColor(p.fail)
		ui.Text(c, a.ActionErr).FontSize(13)
		if ui.Button(c.Key("dismiss-action"), "Dismiss").Clicked() {
			a.ActionErr = ""
		}
	})
}

func (a *App) statusScreen(c *ui.Context, p palette) {
	sup := a.Status.Supervisor
	title := "No supervisor"
	if sup != nil {
		title = supervisorLine(sup)
	}
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, "Status").FontSize(22).Bold()
		ui.Text(c, title).FontSize(15)
		if a.Status.Line != "" {
			ui.Text(c, a.Status.Line).FontSize(14).TextColor(p.neutral)
		}
	})
	ui.Row(c).Gap(8).Children(func() {
		a.stat(c, p, fmt.Sprint(a.Status.Tasks.Active), "Active", p.pass)
		a.stat(c, p, fmt.Sprint(a.Status.Tasks.Inbox), "Inbox", p.neutral)
		a.stat(c, p, fmt.Sprint(a.Status.Tasks.Done), "Done", p.pass)
		a.stat(c, p, fmt.Sprint(a.Status.Tasks.Abandoned), "Abandoned", p.neutral)
		a.stat(c, p, fmt.Sprint(a.Status.Projects), "Projects", p.accent)
	})
	if sup != nil {
		ui.Column(c).Gap(8).Padding(14).Radius(10).Background(p.card).
			Border(1, p.line).Children(func() {
			section(c, p, "Supervisor")
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, sup.Ref).Font("monospace").FontSize(12).TextColor(p.neutral)
				ui.Text(c, strings.TrimSpace(sup.Harness+" "+sup.Model+" "+sup.Effort)).FontSize(14)
				statusPill(c, p, sup.Status)
				if sup.Live {
					ui.Badge(c, "live").Background(p.passBG).TextColor(p.pass)
				}
			})
			if sup.Pending > 0 {
				ui.Textf(c, "%d message(s) queued", sup.Pending).FontSize(12).TextColor(p.neutral)
			}
			if sup.Resumable {
				ui.Text(c, "This session can be resumed").FontSize(12).TextColor(p.neutral)
			}
		})
	}
	ui.Column(c).Gap(8).Padding(14).Radius(10).Background(p.card).
		Border(1, p.line).Children(func() {
		section(c, p, "Agent")
		ui.Textf(c, "agent %s", emptyAs(a.Status.AgentState, "unknown")).FontSize(14)
		if a.Status.Gauge.Text != "" {
			gauge(c, p, a.Status.Gauge)
		}
		if a.Status.Compactions > 0 {
			ui.Textf(c, "%d compaction(s)", a.Status.Compactions).FontSize(12).TextColor(p.neutral)
		}
		if a.Status.ComposerHint != "" {
			ui.Text(c, a.Status.ComposerHint).FontSize(12).TextColor(p.neutral)
		}
	})
}

func (a *App) stat(c *ui.Context, p palette, value, label string, tone ui.Color) {
	ui.Column(c).Grow(1).Gap(2).Padding(12).Radius(8).Background(p.card).
		Border(1, p.line).Children(func() {
		ui.Text(c, value).FontSize(22).Bold().TextColor(tone)
		ui.Text(c, label).FontSize(12).TextColor(p.neutral)
	})
}

func (a *App) needsScreen(c *ui.Context, p palette) {
	items := a.Status.Needs.Items
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, "Needs").FontSize(22).Bold()
		if a.Status.Needs.Waiting > 0 {
			bg, fg := toneColors(a.Status.Needs.Worst, p)
			ui.Badge(c, fmt.Sprint(a.Status.Needs.Waiting)).Background(bg).TextColor(fg)
		}
		ui.Spacer(c)
		if a.Status.Needs.Summary != "" {
			ui.Text(c, a.Status.Needs.Summary).FontSize(12).TextColor(p.neutral)
		}
	})
	if len(items) == 0 {
		body := a.Status.Needs.Summary
		if body == "" {
			body = a.Status.Needs.Cleared
		}
		if body == "" {
			body = "Nothing waits on you."
		}
		a.emptyCard(c, p, "All clear", body)
		return
	}
	a.actionCard(c, p)
	ui.Column(c).Gap(0).Children(func() {
		for _, item := range items {
			need := item
			ref := needRef(need)
			ui.Row(c).Gap(6).Children(func() {
				el := ui.ButtonBase(c.Key("need:"+need.Kind+":"+need.Ref)).Gap(8).Padding(6, 8).
					BorderWidth(0, 0, 1, 0).BorderColor(p.line).Grow(1)
				if ref != "" {
					el.Cursor(ui.CursorPointer)
				}
				el.Children(func() {
					bg, fg := toneColors(need.Tone, p)
					ui.Badge(c, need.Word).Background(bg).TextColor(fg)
					if need.Ref != "" {
						ui.Text(c, need.Ref).Font("monospace").FontSize(12).TextColor(p.neutral)
					}
					ui.Text(c, needLabel(need)).FontSize(14).SingleLine().Grow(1)
					if need.Excerpt != "" {
						ui.Text(c, firstLine(need.Excerpt)).FontSize(12).TextColor(p.neutral).SingleLine()
					}
				})
				if el.Clicked() && ref != "" {
					a.OpenRef(ref)
				}
				if need.Kind == "report" && need.Report != "" {
					label := "Ack"
					if a.Busy {
						label = "Acknowledging…"
					}
					if ui.Button(c.Key("ack:"+need.Report), label).Disabled(a.Busy).Clicked() {
						a.Ack(need.Report)
					}
				}
			})
		}
	})
}

func (a *App) timelineScreen(c *ui.Context, p palette) {
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, "Timeline").FontSize(22).Bold()
		ui.Spacer(c)
		if a.Timeline.Live {
			ui.Badge(c, "live").Background(p.passBG).TextColor(p.pass)
		}
	})
	if w := a.Timeline.Working; w != nil {
		ui.Row(c).Gap(8).Padding(10, 12).Radius(8).Background(p.accentBG).Children(func() {
			ui.Textf(c, "%s is working", w.Ref).FontSize(14).TextColor(p.accent)
			ui.Text(c, w.On).FontSize(12).TextColor(p.neutral)
		})
	}
	if len(a.Timeline.Entries) == 0 {
		note := a.Timeline.Note
		if note == "" {
			note = "Nothing yet: the conversation appears once the supervisor speaks."
		}
		a.emptyCard(c, p, "No messages", note)
		return
	}
	for _, entry := range a.Timeline.Entries {
		e := entry
		ui.Column(c).Gap(3).Padding(8, 0, 12).BorderWidth(0, 0, 1, 0).
			BorderColor(p.line).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, roleName(e)).FontSize(12).Bold().TextColor(roleColor(e, p))
				ui.Text(c, stamp(e.At)).Font("monospace").FontSize(12).TextColor(p.neutral)
				ui.Spacer(c)
				if e.Queued {
					ui.Badge(c, "queued").Background(p.neutralBG).TextColor(p.neutral)
				}
				if e.Delivery != "" {
					ui.Text(c, e.Delivery).FontSize(12).TextColor(p.neutral)
				}
			})
			for _, wake := range e.Wakes {
				ui.Text(c, wake.Text).FontSize(12).TextColor(p.neutral)
			}
			if e.Text != "" {
				ui.Text(c, e.Text).FontSize(15)
			}
		})
	}
	if a.Timeline.Older > 0 {
		if ui.Button(c.Key("timeline:older"), "Older messages").Clicked() {
			a.Older()
		}
	}
}

func (a *App) tasksScreen(c *ui.Context, p palette) {
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, "Tasks").FontSize(22).Bold()
		ui.Spacer(c)
		if a.Tasks.FinishedCount > 0 {
			ui.Textf(c, "%d finished", a.Tasks.FinishedCount).FontSize(12).TextColor(p.neutral)
		}
	})
	if len(a.Tasks.Groups) == 0 {
		a.emptyCard(c, p, "No tasks", "The supervisor has not captured a task yet.")
		return
	}
	for _, group := range a.Tasks.Groups {
		g := group
		ui.Column(c).Gap(2).Children(func() {
			ui.Row(c).Gap(8).Padding(6, 0).Children(func() {
				section(c, p, g.Name)
				ui.Text(c, fmt.Sprint(g.Count)).FontSize(12).TextColor(p.neutral)
			})
			for _, row := range g.Tasks {
				r := row
				el := ui.ButtonBase(c.Key("task:"+r.Ref)).Gap(8).Padding(6, 8).Cursor(ui.CursorPointer).
					BorderWidth(0, 0, 1, 0).BorderColor(p.line)
				el.Children(func() {
					ui.Text(c, stateWord(r.State)).FontSize(12).TextColor(stateColor(r.State, p)).Width(52)
					ui.Text(c, r.Ref).Font("monospace").FontSize(12).TextColor(p.neutral)
					ui.Text(c, r.Title).FontSize(14).SingleLine().Grow(1)
					if r.Project != "" && a.Status.Projects > 1 {
						ui.Badge(c, r.Project).Background(p.neutralBG).TextColor(p.neutral)
					}
					if r.Attempt != nil {
						ui.Text(c, attemptMeta(r.Attempt)).FontSize(12).TextColor(p.neutral)
					}
					if r.Delivered != nil {
						ui.Badge(c, r.Delivered.Kind).Background(p.accentBG).TextColor(p.accent)
					}
				})
				if el.Clicked() {
					a.OpenRef(r.Ref)
				}
			}
		})
	}
}

func (a *App) taskScreen(c *ui.Context, p palette) {
	t := a.Task
	a.detailHeader(c, p, t.Task.Ref, t.Task.Title)
	ui.Row(c).Gap(8).Children(func() {
		statusPill(c, p, t.Pill)
		ui.Text(c, t.Task.Status).FontSize(12).TextColor(p.neutral)
		if t.Task.Project != "" && a.Status.Projects > 1 {
			ui.Badge(c, t.Task.Project).Background(p.neutralBG).TextColor(p.neutral)
		}
		ui.Text(c, stamp(t.Task.CreatedAt)).Font("monospace").FontSize(12).TextColor(p.neutral)
	})
	if t.Task.Goal != "" {
		a.paragraph(c, p, "Goal", t.Task.Goal)
	}
	if t.Plan != nil {
		a.paragraph(c, p, fmt.Sprintf("Plan · %s", t.Plan.Ref), t.Plan.Body)
	}
	if len(t.Delivered) > 0 {
		a.section(c, p, "Delivered")
		ui.Column(c).Gap(2).Children(func() {
			for _, d := range t.Delivered {
				row(c, p, d.Kind, d.Ref)
			}
		})
	}
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
		ui.Text(c, fmt.Sprintf("%s %s %s", at.Attempt.Harness, at.Attempt.Model, at.Attempt.Effort)).FontSize(14)
		ui.Text(c, at.Attempt.Status).FontSize(12).TextColor(p.neutral)
		if at.Duration != "" {
			ui.Text(c, at.Duration).Font("monospace").FontSize(12).TextColor(p.neutral)
		}
	})
	if at.Attempt.Branch != "" {
		ui.Text(c, at.Attempt.Branch).Font("monospace").FontSize(12).TextColor(p.neutral)
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
	if len(at.Delivered) > 0 {
		a.section(c, p, "Delivered")
		ui.Column(c).Gap(2).Children(func() {
			for _, d := range at.Delivered {
				row(c, p, d.Kind, d.Ref)
			}
		})
	}
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
			ui.Text(c, at.Transcript.Model).FontSize(12).TextColor(p.neutral)
		}
		for _, turn := range at.Transcript.Turns {
			ui.Column(c).Gap(2).Padding(6, 0, 8).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, turn.Role).FontSize(12).Bold().TextColor(p.neutral)
					ui.Text(c, stamp(turn.At)).Font("monospace").FontSize(12).TextColor(p.neutral)
					if turn.Queued {
						ui.Badge(c, "queued").Background(p.neutralBG).TextColor(p.neutral)
					}
				})
				ui.Text(c, turn.Text).FontSize(14)
			})
		}
		if at.Transcript.More {
			more(c, p, "Only the latest 50 entries are shown.")
		}
	} else if at.Transcript.Note != "" {
		a.section(c, p, "Transcript")
		ui.Text(c, at.Transcript.Note).FontSize(12).TextColor(p.neutral)
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
	a.actionCard(c, p)
	ui.Row(c).Gap(8).Children(func() {
		statusPill(c, p, d.Decision.Pill)
		ui.Text(c, d.Decision.Status).FontSize(12).TextColor(p.neutral)
		ui.Text(c, d.Task.Ref).Font("monospace").FontSize(12).TextColor(p.neutral)
		ui.Text(c, d.Task.Title).FontSize(14).SingleLine().Grow(1)
		if ui.Button(c.Key("open-task"), "Open task").Clicked() {
			a.OpenRef(d.Task.Ref)
		}
	})
	if d.Decision.Question != "" && d.Decision.Question != d.Decision.Headline {
		if body := questionBody(d.Decision.Question, d.Decision.Headline, len(d.Options) > 0); body != "" {
			ui.Text(c, body).FontSize(15)
		}
	}
	open := d.Decision.Status == "open"
	if len(d.Options) > 0 {
		a.section(c, p, "Options")
		ui.Column(c).Gap(6).Children(func() {
			for _, o := range d.Options {
				option := o
				selected := a.AnswerChoice == option.Value
				bg, border := p.card, p.line
				if selected {
					bg, border = p.passBG, p.pass
				}
				el := ui.ButtonBase(c.Key("option:"+option.Key)).Gap(10).Padding(8, 10).
					Radius(8).Background(bg).Border(1, border)
				if open && !a.Busy {
					el.Cursor(ui.CursorPointer)
				}
				el.Children(func() {
					ui.Text(c, option.Key).Font("monospace").FontSize(12).TextColor(p.neutral).Width(18)
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Row(c).Gap(8).Children(func() {
							ui.Text(c, option.Label).FontSize(14).Bold()
							if option.Recommended {
								ui.Badge(c, "Recommended").Background(p.passBG).TextColor(p.pass)
							}
						})
						if option.Note != "" {
							ui.Text(c, option.Note).FontSize(12).TextColor(p.neutral)
						}
					})
				})
				if open && el.Clicked() && !a.Busy {
					a.AnswerChoice = option.Value
				}
			}
		})
	}
	if open {
		placeholder := "Your answer"
		if len(d.Options) > 0 {
			placeholder = "Add a note (optional)"
		}
		ui.TextArea(c, &a.AnswerNote).Label("Note").Placeholder(placeholder).Height(64).
			Disabled(a.Busy)
		ui.Row(c).Gap(8).Children(func() {
			label := "Send answer"
			if a.Busy {
				label = "Sending…"
			}
			if ui.Button(c.Key("send-answer"), label).Disabled(a.Busy).Clicked() {
				a.Answer()
			}
		})
	}
	if d.Decision.Answer != "" {
		a.paragraph(c, p, "Answer", d.Decision.Answer)
	}
	a.section(c, p, "Task")
	ui.Textf(c, "%s · %s", d.Task.Ref, d.Task.Title).FontSize(14)
}

func (a *App) reportScreen(c *ui.Context, p palette) {
	r := a.Report
	a.detailHeader(c, p, r.Report.Ref, r.Report.Summary)
	a.actionCard(c, p)
	ui.Row(c).Gap(8).Children(func() {
		_, statusFg := toneColors(r.Report.Status, p)
		ui.Badge(c, r.Report.Status).Background(p.neutralBG).TextColor(statusFg)
		ui.Text(c, r.Report.Attempt).Font("monospace").FontSize(12).TextColor(p.neutral)
		ui.Text(c, r.Report.Task).Font("monospace").FontSize(12).TextColor(p.neutral)
		ui.Text(c, stamp(r.Report.CreatedAt)).Font("monospace").FontSize(12).TextColor(p.neutral)
		ui.Spacer(c)
		if r.Report.AckedAt == "" {
			label := "Ack"
			if a.Busy {
				label = "Acknowledging…"
			}
			if ui.Button(c.Key("ack-report"), label).Disabled(a.Busy).Clicked() {
				a.Ack(r.Report.Ref)
			}
		} else {
			ui.Textf(c, "Acknowledged by %s", r.Report.AckedBy).FontSize(12).TextColor(p.neutral)
		}
		if ui.Button(c.Key("open-task"), "Open task").Clicked() {
			a.OpenRef(r.Task.Ref)
		}
	})
	ui.Text(c, r.Report.Body).FontSize(15)
	if r.Report.AckedAt != "" {
		ui.Textf(c, "Acknowledged %s", stamp(r.Report.AckedAt)).FontSize(12).TextColor(p.neutral)
	}
}

func (a *App) detailHeader(c *ui.Context, p palette, ref, title string) {
	ui.Row(c).Gap(10).Children(func() {
		if ui.Button(c.Key("back"), "Back").Clicked() {
			a.Back()
		}
		ui.Text(c, ref).Font("monospace").FontSize(12).TextColor(p.neutral)
		ui.Text(c, title).FontSize(22).Bold().SingleLine().Grow(1)
	})
}

func (a *App) section(c *ui.Context, p palette, title string) {
	section(c, p, title)
}

func more(c *ui.Context, p palette, text string) {
	ui.Text(c, text).FontSize(12).TextColor(p.neutral)
}

func section(c *ui.Context, p palette, title string) {
	ui.Text(c, title).FontSize(12).Bold().TextColor(p.neutral).Padding(0, 0, 2)
}

func (a *App) paragraph(c *ui.Context, p palette, title, body string) {
	ui.Column(c).Gap(6).Children(func() {
		section(c, p, title)
		ui.Text(c, body).FontSize(14)
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
		ui.Text(c, d.Ref).Font("monospace").FontSize(12).TextColor(p.neutral)
		ui.Text(c, d.Headline).FontSize(14).SingleLine().Grow(1)
		if d.Answer != "" {
			ui.Text(c, firstLine(d.Answer)).FontSize(12).TextColor(p.neutral).SingleLine()
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
		ui.Text(c, r.Ref).Font("monospace").FontSize(12).TextColor(p.neutral)
		ui.Text(c, r.Summary).FontSize(14).SingleLine().Grow(1)
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
		ui.Text(c, at.Ref).Font("monospace").FontSize(12).TextColor(p.neutral)
		ui.Text(c, attemptMeta(&at.Attempt)).FontSize(14).Grow(1).SingleLine()
		ui.Text(c, stateWord(at.State)).FontSize(12).TextColor(stateColor(at.State, p))
	})
	if el.Clicked() {
		a.OpenRef(at.Ref)
	}
}

func (a *App) eventRow(c *ui.Context, p palette, e Event) {
	ui.Row(c).Gap(8).Padding(3, 0).Children(func() {
		ui.Text(c, stamp(e.At)).Font("monospace").FontSize(12).TextColor(p.neutral).Width(112)
		ui.Text(c, e.Kind).Font("monospace").FontSize(12)
		if e.Detail != "" {
			ui.Text(c, e.Detail).FontSize(12).TextColor(p.neutral).SingleLine().Grow(1)
		}
	})
}

func row(c *ui.Context, p palette, kind, ref string) {
	ui.Row(c).Gap(8).Children(func() {
		ui.Badge(c, kind).Background(p.accentBG).TextColor(p.accent)
		ui.Text(c, ref).Font("monospace").FontSize(12)
	})
}

func statusPill(c *ui.Context, p palette, word string) {
	if word == "" {
		word = "unknown"
	}
	bg, fg := toneColors(word, p)
	ui.Row(c).Gap(5).Padding(1, 8).Radius(999).Background(bg).Children(func() {
		ui.Box(c).Size(7, 7).Radius(4).Background(fg)
		ui.Text(c, word).FontSize(12).Bold().TextColor(fg)
	})
}

func gauge(c *ui.Context, p palette, g Gauge) {
	_, fg := toneColors(g.Level, p)
	if g.Level == "" {
		fg = p.accent
	}
	pct := g.Pct
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	ui.Column(c).Width(150).Gap(3).Children(func() {
		ui.Text(c, g.Text).Font("monospace").FontSize(12).TextColor(fg).
			FontFeatures("tnum")
		ui.Box(c).Height(5).Radius(3).Background(p.line).Children(func() {
			if pct > 0 {
				ui.Box(c).WidthPercent(float32(pct)).Height(5).Radius(3).Background(fg)
			}
		})
	})
}

func toneColors(tone string, p palette) (bg, fg ui.Color) {
	switch tone {
	case "fail", "failing", "failed", "blocked", "interrupted", "stuck":
		return p.failBG, p.fail
	case "wait", "waiting", "limited", "decision", "pending":
		return p.waitBG, p.wait
	case "pass", "ready", "running", "passing", "done":
		return p.passBG, p.pass
	case "accent", "working":
		return p.accentBG, p.accent
	}
	return p.neutralBG, p.neutral
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

func supervisorLine(sup *Supervisor) string {
	if sup == nil {
		return ""
	}
	parts := []string{sup.Ref, sup.Harness}
	if sup.Model != "" {
		parts = append(parts, sup.Model)
	}
	if sup.Effort != "" {
		parts = append(parts, sup.Effort)
	}
	return strings.TrimSpace(strings.Join(parts, " ") + " · " + sup.Status)
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

func emptyAs(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func fleetName(f Fleet) string {
	if f.Name != "" {
		return f.Name
	}
	return f.ID
}

func stamp(at string) string {
	if at == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		t, err = time.Parse(time.RFC3339, at)
		if err != nil {
			return at
		}
	}
	return t.Local().Format("Jan 2 15:04")
}
