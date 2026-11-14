package desktop

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

func (a *App) viewer(c *ui.Context, k kit.Colors, p palette) {
	dismissed := kit.Viewer(c, k, kit.ViewerOptions{
		Name:    a.viewerName(),
		Back:    len(a.back) > 0,
		OnBack:  a.Back,
		OnClose: a.CloseViewer,
		OnOpen:  a.OpenInBrowser,
		Body: func(*ui.Context) {
			a.viewerBody(c, k, p)
		},
	})
	if dismissed {
		a.CloseViewer()
	}
}

func (a *App) viewerName() string {
	if a.Screen == TaskScreen && a.Task.Task.Ref == a.Ref && a.Task.Task.Title != "" {
		return shownText(a.Task.Task.Title)
	}
	if a.Ref != "" {
		return shownText(a.Ref)
	}
	return "Page"
}

func (a *App) viewerBody(c *ui.Context, k kit.Colors, p palette) {
	if a.Err != "" && a.errScreen {
		a.pageScroll(c, func() {
			a.viewerError(c, p)
		})
		return
	}
	switch a.Screen {
	case TaskScreen:
		if a.Task.Task.Ref != a.Ref {
			a.readingPage(c, p)
			return
		}
		w, _ := c.Size()
		a.taskPage(c, k, p, w >= kit.ViewerColumns)
	case AttemptScreen:
		if a.Attempt.Attempt.Ref != a.Ref {
			a.readingPage(c, p)
			return
		}
		a.pageScroll(c, func() {
			a.actionCard(c, p)
			a.attemptScreen(c, p)
		})
	case DecisionScreen:
		if a.Decision.Decision.Ref != a.Ref {
			a.readingPage(c, p)
			return
		}
		a.pageScroll(c, func() {
			a.actionCard(c, p)
			a.decisionScreen(c, k, p)
		})
	case ReportScreen:
		if a.Report.Report.Ref != a.Ref {
			a.readingPage(c, p)
			return
		}
		a.pageScroll(c, func() {
			a.actionCard(c, p)
			a.reportScreen(c, p)
		})
	}
}

func (a *App) readingPage(c *ui.Context, p palette) {
	a.pageScroll(c, func() {
		ui.Text(c, "Reading "+shownText(a.Ref)+"…").FontSize(kit.Body.Size).TextColor(p.neutral)
	})
}

func (a *App) pageScroll(c *ui.Context, body func()) {
	page := ui.Scroll(c).Grow(1).MinHeight(0).Padding(kit.ViewerTop+kit.Space16, kit.Space20, 32).
		Focusable().FocusRing(false).TrackScroll(&a.viewerScroll).Label("The page").
		Children(func() {
			ui.Column(c).Gap(kit.Space16).Selectable().Children(func() {
				body()
			})
		})
	if a.viewerFocus {
		a.viewerFocus = false
		page.Focus()
	}
}

func (a *App) viewerError(c *ui.Context, p palette) {
	ui.Column(c).Gap(kit.Space10).Padding(kit.Space16).Radius(kit.RadiusLG).
		Background(p.failBG).Border(1, p.fail).Children(func() {
		ui.Text(c, "The page could not be read").FontSize(kit.Heading.Size).Bold().TextColor(p.fail)
		ui.Text(c, shownText(a.Err)).FontSize(kit.Body.Size)
		ui.Row(c).Gap(kit.Space8).Children(func() {
			if ui.Button(c.Key("viewer:error-close"), "Close").Width(100).Clicked() {
				a.CloseViewer()
			}
			if ui.Button(c.Key("viewer:error-retry"), "Retry").Width(100).Clicked() {
				a.Retry()
			}
		})
	})
}

func (a *App) taskPage(c *ui.Context, k kit.Colors, p palette, wide bool) {
	if wide {
		ui.Row(c).Grow(1).MinHeight(0).Gap(kit.ViewerAsideGap).Children(func() {
			a.taskMain(c, k, p, false)
			ui.Scroll(c).Width(kit.ViewerAside).FillHeight().
				Padding(kit.ViewerTop+kit.Space16, kit.Space20, 32, 0).TrackScroll(&a.asideScroll).
				Children(func() {
					ui.Column(c).Gap(kit.Space14).Selectable().Children(func() { a.taskAside(c, p) })
				})
		})
		return
	}
	a.taskMain(c, k, p, true)
}

func (a *App) taskMain(c *ui.Context, k kit.Colors, p palette, asideFirst bool) {
	page := ui.Scroll(c).Grow(1).FillHeight().MinHeight(0).Padding(kit.ViewerTop+kit.Space16, kit.Space20, 32).
		Focusable().FocusRing(false).TrackScroll(&a.viewerScroll).Label("The page").
		Children(func() {
			ui.Column(c).Gap(kit.Space14).Selectable().Children(func() {
				a.actionCard(c, p)
				a.taskHead(c, p)
				a.taskWaiting(c, p)
				if asideFirst {
					a.taskAside(c, p)
				}
				a.taskSections(c, p)
			})
		})
	if a.viewerFocus {
		a.viewerFocus = false
		page.Focus()
	}
}

func (a *App) taskHead(c *ui.Context, p palette) {
	t := a.Task
	ui.Column(c).Gap(kit.Space6).Children(func() {
		ui.Text(c, "Fleets / "+shownText(fleetName(a.Fleet))+" / "+shownText(t.Task.Ref)).
			FontSize(kit.Body.Size).TextColor(p.neutral).SingleLine()
		ui.Text(c, shownText(t.Task.Title)).FontSize(kit.Display.Size).Bold()
		ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
			bg, fg := toneColors(t.Pill, p)
			ui.Badge(c, shownText(t.Task.Status)).Background(bg).TextColor(fg)
			if t.Task.Project != "" {
				ui.Text(c, shownText(t.Task.Project)).FontSize(kit.Body.Size).SingleLine()
			}
			if t.Task.Parent != "" {
				ui.Text(c, "Sub-task of "+shownText(t.Task.Parent)).FontSize(kit.Meta.Size).TextColor(p.neutral)
			}
			if t.Plan != nil {
				ui.Text(c, "plan "+shownText(t.Plan.Ref)).FontSize(kit.Body.Size)
			}
			if t.Waits != nil {
				ui.Text(c, taskWaitsLine(*t.Waits)).FontSize(kit.Meta.Size).TextColor(p.neutral).SingleLine()
			}
			ui.Text(c, stamp(t.Task.CreatedAt)).FontSize(kit.Meta.Size).TextColor(p.neutral).
				FontFeatures("tnum")
		})
		ui.Box(c).Height(1).Background(p.line)
	})
}

func (a *App) taskWaiting(c *ui.Context, p palette) {
	t := a.Task
	if len(t.OpenDecisions) == 0 && len(t.Unread) == 0 {
		return
	}
	ui.Column(c).Gap(0).Children(func() {
		for _, d := range t.OpenDecisions {
			a.waitingRow(c, p, "DECISION", "wait", d.Ref, d.Headline, d.Ref)
		}
		for _, r := range t.Unread {
			a.waitingRow(c, p, "REPORT", "neutral", r.Ref, r.Summary, "")
		}
	})
}

func (a *App) waitingRow(c *ui.Context, p palette, word, tone, ref, title, open string) {
	ui.Row(c).MinHeight(kit.Row).Padding(kit.Space4).Gap(kit.Space8).
		AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.line).Children(func() {
		a.codeBadge(c, p, word, tone)
		ui.Text(c, shownText(ref)).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
		ui.Text(c, shownText(title)).FontSize(kit.Body.Size).Bold().Grow(1).MinWidth(0).SingleLine()
		if open == "" {
			return
		}
		answer := ui.ButtonBase(c.Key("wait-open:"+ref)).Height(kit.Control).
			Padding(0, kit.Space8).Cursor(ui.CursorPointer).Label("Answer " + ref)
		answer.Children(func() {
			ui.Text(c, "Answer").FontSize(kit.Body.Size).TextColor(p.accent)
		})
		if answer.Clicked() {
			a.OpenRef(open)
		}
	})
}

func (a *App) codeBadge(c *ui.Context, p palette, word, tone string) {
	bg, fg := toneColors(tone, p)
	ui.Row(c).Height(kit.Badge).Padding(0, kit.Space8).Radius(kit.RadiusPill).
		Background(bg).AlignItems(ui.Center).Label(word).Children(func() {
		ui.Text(c, strings.ToUpper(word)).Font("monospace").FontSize(kit.Meta.Size).
			Bold().LetterSpacing(.36).TextColor(fg)
	})
}

func (a *App) taskSections(c *ui.Context, p palette) {
	t := a.Task
	if t.Task.Goal != "" {
		ui.Column(c).Border(1, p.line).Radius(kit.RadiusLG).Clip().Children(func() {
			ui.Row(c).Padding(kit.Space6, kit.Space12).Background(p.card).
				BorderWidth(0, 0, 1, 0).BorderColor(p.line).Children(func() {
				ui.Text(c, "goal").FontSize(kit.Meta.Size).Bold()
			})
			ui.Box(c).Padding(kit.Space10, kit.Space12).Children(func() {
				ui.Text(c, shownText(t.Task.Goal)).FontSize(kit.Message.Size).
					FixedLineHeight(kit.Message.Line)
			})
		})
	}
	if t.Plan != nil {
		ui.Column(c).Border(1, p.line).Radius(kit.RadiusLG).Clip().Children(func() {
			ui.Row(c).Gap(kit.Space8).Padding(kit.Space6, kit.Space12).Background(p.card).
				BorderWidth(0, 0, 1, 0).BorderColor(p.line).Children(func() {
				ui.Text(c, "plan "+shownText(t.Plan.Ref)).FontSize(kit.Meta.Size).Bold()
				ui.Spacer(c)
				ui.Text(c, stamp(t.Plan.CreatedAt)).FontSize(kit.Meta.Size).TextColor(p.neutral).
					FontFeatures("tnum")
			})
			ui.Box(c).Padding(kit.Space10, kit.Space12).Children(func() {
				a.markdownCopy(c, shownText(t.Plan.Body))
			})
		})
	}
	a.mainHead(c, "Reports")
	if len(t.Reports) == 0 {
		a.quiet(c, p, "No reports yet.")
	}
	for _, r := range t.Reports {
		a.reportCard(c, p, r)
	}
	if t.MoreReports {
		more(c, p, "Only the latest 50 reports are shown.")
	}
	a.mainHead(c, "Decisions")
	if len(t.Decisions) == 0 {
		a.quiet(c, p, "No decisions yet.")
	}
	ui.Column(c).Gap(0).Children(func() {
		for _, d := range t.Decisions {
			a.decisionLine(c, p, d)
		}
	})
	a.mainHead(c, "Events")
	ui.Column(c).Gap(0).Children(func() {
		for _, e := range t.Events {
			ui.Row(c).Gap(kit.Space12).AlignItems(ui.Start).Padding(kit.Space6, kit.Space4).
				BorderWidth(0, 0, 1, 0).BorderColor(p.line).Children(func() {
				ui.Text(c, stamp(e.At)).Font("monospace").FontSize(kit.Meta.Size).
					TextColor(p.neutral).Width(96).FontFeatures("tnum")
				ui.Text(c, plainKind(e.Kind)).FontSize(kit.Meta.Size).Width(140)
				ui.Text(c, shownText(e.Detail)).FontSize(kit.Meta.Size).
					TextColor(p.neutral).Grow(1).MinWidth(0)
			})
		}
	})
	if t.MoreEvents {
		more(c, p, "Only the latest 50 events are shown.")
	}
}

func (a *App) mainHead(c *ui.Context, title string) {
	ui.Text(c, title).FontSize(kit.Body.Size).Bold().Padding(kit.Space10, 0, 0)
}

func (a *App) quiet(c *ui.Context, p palette, text string) {
	ui.Text(c, text).FontSize(kit.Body.Size).TextColor(p.neutral)
}

func (a *App) reportCard(c *ui.Context, p palette, r Report) {
	bg, fg := toneColors(reportTone(r.Status), p)
	ui.Column(c).Border(1, p.line).Radius(kit.RadiusLG).Clip().
		Label("report " + r.Ref).Children(func() {
		ui.Column(c).Gap(kit.Space2).Padding(kit.Space6, kit.Space12).
			Background(bg).BorderWidth(0, 0, 1, 0).BorderColor(p.line).Children(func() {
			ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, shownText(r.Ref)).Font("monospace").FontSize(kit.Meta.Size).Bold()
				ui.Text(c, shownText(r.Summary)).FontSize(kit.Body.Size).Bold().
					Grow(1).MinWidth(0).SingleLine()
				a.ackControl(c, p, r)
			})
			ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
				if r.Attempt != "" {
					from := ui.ButtonBase(c.Key("report-attempt:" + r.Ref)).Cursor(ui.CursorPointer).
						Label("from " + r.Attempt)
					from.Children(func() {
						ui.Text(c, "from "+shownText(r.Attempt)).Font("monospace").
							FontSize(kit.Meta.Size).TextColor(p.accent)
					})
					if from.Clicked() {
						a.OpenRef(r.Attempt)
					}
				}
				ui.Badge(c, shownText(r.Status)).Background(bg).TextColor(fg)
				ui.Text(c, stamp(r.CreatedAt)).FontSize(kit.Meta.Size).TextColor(p.neutral).
					FontFeatures("tnum")
			})
		})
		if rest := restBody(r.Body); rest != "" {
			ui.Box(c).Padding(kit.Space10, kit.Space12).Children(func() {
				a.markdownCopy(c, shownText(rest))
			})
		}
	})
}

func (a *App) ackControl(c *ui.Context, p palette, r Report) {
	if r.AckedAt != "" {
		ui.Text(c, "read by "+shownText(r.AckedBy)).FontSize(kit.Meta.Size).
			TextColor(p.neutral).SingleLine()
		return
	}
	label := "Mark read"
	if a.ackRef == r.Ref {
		label = "Marking read…"
	}
	el := ui.ButtonBase(c.Key("ack:"+r.Ref)).Height(kit.Control).Padding(0, kit.Space12).
		Border(1, p.lineStrong).Radius(kit.RadiusMD).Cursor(ui.CursorPointer).
		Label("Mark read " + r.Ref).Disabled(a.Busy)
	el.Children(func() {
		ui.Text(c, label).FontSize(kit.Body.Size)
	})
	if el.Clicked() {
		a.Ack(r.Ref)
	}
}

func (a *App) decisionLine(c *ui.Context, p palette, d Decision) {
	ui.Column(c).Gap(kit.Space2).Padding(kit.Space6, kit.Space4).
		BorderWidth(0, 0, 1, 0).BorderColor(p.line).Children(func() {
		ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
			ref := ui.ButtonBase(c.Key("decision:" + d.Ref)).Cursor(ui.CursorPointer).
				Label("Open " + d.Ref)
			ref.Children(func() {
				ui.Text(c, shownText(d.Ref)).Font("monospace").FontSize(kit.Meta.Size).
					TextColor(p.accent)
			})
			if ref.Clicked() {
				a.OpenRef(d.Ref)
			}
			ui.Text(c, shownText(d.Headline)).FontSize(kit.Body.Size).Bold().
				Grow(1).MinWidth(0).SingleLine()
			if d.Answer == "" {
				answer := ui.ButtonBase(c.Key("answer:" + d.Ref)).Cursor(ui.CursorPointer).
					Label("Answer " + d.Ref)
				answer.Children(func() {
					ui.Text(c, "Answer").FontSize(kit.Body.Size).TextColor(p.accent)
				})
				if answer.Clicked() {
					a.OpenRef(d.Ref)
				}
			}
		})
		if d.Answer != "" {
			ui.Text(c, shownText(d.Answer)).FontSize(kit.Body.Size).TextColor(p.neutral)
		}
	})
}

func (a *App) taskAside(c *ui.Context, p palette) {
	t := a.Task
	if len(t.Delivered) > 0 {
		a.asideHead(c, p, "Delivered")
		ui.Column(c).Gap(kit.Space8).Children(func() {
			for i, d := range t.Delivered {
				a.deliveredAside(c, p, i, d)
			}
		})
	}
	if len(t.Children) > 0 {
		a.asideHead(c, p, "Sub-tasks")
		ui.Column(c).Gap(0).Children(func() {
			for _, r := range t.Children {
				a.subtaskRow(c, p, r)
			}
		})
	}
	a.asideHead(c, p, "Attempts")
	if len(t.Attempts) == 0 {
		a.quiet(c, p, "No attempts yet.")
	}
	ui.Column(c).Gap(0).Children(func() {
		for _, at := range t.Attempts {
			a.asideAttempt(c, p, at)
		}
	})
	a.asideHead(c, p, "Details")
	a.taskFacts(c, p)
}

func (a *App) asideHead(c *ui.Context, p palette, title string) {
	ui.Text(c, title).FontSize(kit.Meta.Size).Bold().TextColor(p.neutral)
}

func (a *App) deliveredAside(c *ui.Context, p palette, i int, d Deliverable) {
	ui.Column(c).Gap(kit.Space2).Children(func() {
		dest, board := a.deliveredTarget(d)
		open := func() {
			if board {
				a.OpenBoardPath(dest)
			} else {
				c.OpenURL(dest)
			}
		}
		switch {
		case d.Kind == "file" && dest != "":
			el := ui.ButtonBase(c.Key("aside-file:" + strconv.Itoa(i))).Height(kit.RowSM).
				AlignSelf(ui.Start).Cursor(ui.CursorPointer).Label("open " + dest)
			el.Children(func() {
				ui.Text(c, taskFileBase(d.Ref)).Font("monospace").FontSize(kit.Meta.Size).
					TextColor(p.accent)
			})
			if el.Clicked() {
				open()
			}
		case d.Kind == "file":
			ui.Text(c, taskFileBase(d.Ref)).Font("monospace").FontSize(kit.Meta.Size).
				TextColor(p.neutral)
		case dest != "" && !board:
			el := ui.ButtonBase(c.Key("aside-link:" + strconv.Itoa(i))).
				AlignSelf(ui.Start).Cursor(ui.CursorPointer).Label("open " + d.Ref)
			el.Children(func() {
				ui.Badge(c, shownText(d.Kind)).Background(p.accentBG).TextColor(p.accent)
			})
			if el.Clicked() {
				open()
			}
		default:
			ui.Row(c).Children(func() {
				ui.Badge(c, shownText(d.Kind)).Background(p.accentBG).TextColor(p.accent)
			})
		}
		ui.Text(c, shownText(d.Ref)).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
	})
}

func (a *App) subtaskRow(c *ui.Context, p palette, r TaskRow) {
	el := ui.ButtonBase(c.Key("sub:" + r.Ref)).MinHeight(kit.Row).Padding(kit.Space4).
		Gap(kit.Space8).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label("task " + r.Ref)
	el.Children(func() {
		ui.Icon(c, stateIcon(r.State)).TextColor(stateColor(r.State, p)).
			Label("state " + stateWord(r.State))
		ui.Text(c, shownText(r.Ref)).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.accent)
		ui.Text(c, shownText(r.Title)).FontSize(kit.Body.Size).Grow(1).MinWidth(0).SingleLine()
		ui.Text(c, stateWord(r.State)).FontSize(kit.Meta.Size).TextColor(stateColor(r.State, p))
	})
	if el.Clicked() {
		a.OpenRef(r.Ref)
	}
}

func (a *App) asideAttempt(c *ui.Context, p palette, at AttemptRow) {
	el := ui.ButtonBase(c.Key("attempt:"+at.Ref)).Padding(kit.Space6, kit.Space4).
		Gap(kit.Space8).AlignItems(ui.Start).Cursor(ui.CursorPointer).Label("attempt " + at.Ref)
	el.Children(func() {
		ui.Icon(c, stateIcon(at.State)).TextColor(stateColor(at.State, p)).
			Label("state " + stateWord(at.State))
		ui.Column(c).Grow(1).MinWidth(0).Gap(kit.Space2).Children(func() {
			ui.Row(c).Gap(kit.Space6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, shownText(at.Ref)).Font("monospace").FontSize(kit.Meta.Size).
					TextColor(p.accent)
				ui.Text(c, attemptLine(at)).FontSize(kit.Body.Size).SingleLine().MinWidth(0)
			})
			ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, attemptStatus(at)).FontSize(kit.Meta.Size).TextColor(p.neutral).SingleLine()
				ui.Spacer(c)
				ui.Text(c, stamp(at.CreatedAt)).FontSize(kit.Meta.Size).TextColor(p.neutral).
					FontFeatures("tnum")
			})
		})
	})
	if el.Clicked() {
		a.OpenRef(at.Ref)
	}
}

func attemptLine(at AttemptRow) string {
	return strings.TrimSpace(strings.Join([]string{at.Harness, at.Model, at.Effort}, " "))
}

func attemptStatus(at AttemptRow) string {
	if at.Reason != "" {
		return shownText(at.Status) + " · " + shownText(at.Reason)
	}
	return shownText(at.Status)
}

func (a *App) taskFacts(c *ui.Context, p palette) {
	t := a.Task
	facts := []struct{ label, value string }{
		{"Project", shownText(t.Task.Project)},
		{"Plan", planValue(t.Plan)},
		{"Waits until", waitsValue(t.Waits)},
		{"Created", stamp(t.Task.CreatedAt)},
		{"Updated", stamp(t.Task.UpdatedAt)},
		{"Finished", stamp(t.Task.FinishedAt)},
	}
	ui.Column(c).Gap(kit.Space4).Children(func() {
		for _, f := range facts {
			if f.value == "" {
				continue
			}
			ui.Row(c).Gap(kit.Space12).AlignItems(ui.Start).Children(func() {
				ui.Text(c, f.label).FontSize(kit.Meta.Size).TextColor(p.neutral).Width(88)
				ui.Text(c, f.value).FontSize(kit.Meta.Size).Grow(1).MinWidth(0)
			})
		}
	})
}

func planValue(p *Plan) string {
	if p == nil {
		return ""
	}
	return shownText(p.Ref)
}

func waitsValue(w *Waits) string {
	if w == nil {
		return ""
	}
	line := taskDay(w.Due)
	if w.Note != "" {
		line += ": " + shownText(w.Note)
	}
	return line
}

func reportTone(status string) string {
	switch status {
	case "done":
		return "pass"
	case "blocked", "failed", "stuck":
		return "fail"
	}
	return "neutral"
}

func restBody(s string) string {
	_, rest, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(rest)
}

func plainKind(kind string) string {
	return strings.TrimSpace(strings.NewReplacer(".", " ", "_", " ").Replace(kind))
}

func bidi(r rune) bool {
	return r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069 ||
		r == 0x200E || r == 0x200F || r == 0x061C
}

// shownText is the Board's shown(): a control or bidirectional character
// shows as its code, so text from the board cannot draw itself as another.
func shownText(s string) string {
	var out strings.Builder
	for _, r := range strings.ReplaceAll(s, "\r\n", "\n") {
		if bidi(r) || unicode.IsControl(r) && r != '\n' && r != '\t' || r == 0x2028 || r == 0x2029 {
			fmt.Fprintf(&out, "U+%04X", r)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
