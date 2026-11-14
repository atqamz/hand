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
		Notice: func(nc *ui.Context) {
			if a.OpenBusy || a.OpenErr != "" {
				ui.Box(nc).Padding(kit.Space10, kit.Space20, 0).Children(func() {
					a.openCard(nc, p)
				})
			}
		},
		Body: func(*ui.Context) {
			a.viewerBody(c, k, p)
		},
	})
	if dismissed {
		a.CloseViewer()
	}
}

func (a *App) viewerName() string {
	switch a.Screen {
	case TaskScreen:
		if a.Task.Task.Ref == a.Ref && a.Task.Task.Title != "" {
			return shownText(a.Task.Task.Title)
		}
	case AttemptScreen:
		if a.Attempt.Attempt.Ref == a.Ref {
			if title := attemptTitle(a.Attempt); title != "" {
				return title
			}
		}
	case DecisionScreen:
		if a.Decision.Decision.Ref == a.Ref && a.Decision.Decision.Headline != "" {
			return shownText(a.Decision.Decision.Headline)
		}
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
		w, _ := c.Size()
		a.attemptPage(c, k, p, w >= kit.ViewerColumns)
	case DecisionScreen:
		if a.Decision.Decision.Ref != a.Ref {
			a.readingPage(c, p)
			return
		}
		a.pageScroll(c, func() {
			a.actionCard(c, p)
			a.decisionPage(c, k, p)
		})
	}
}

func (a *App) readingPage(c *ui.Context, p palette) {
	a.pageScroll(c, func() {
		ui.Text(c, "Reading "+shownText(a.Ref)+"…").FontSize(kit.Body.Size).TextColor(p.neutral)
	})
}

func (a *App) pageScroll(c *ui.Context, body func()) {
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(kit.Space16, kit.Space20, 32).
		Focusable().FocusRing(false).TrackScroll(&a.viewerScroll).Label("The page").
		Bind(&a.viewerPage).
		Children(func() {
			ui.Column(c).Gap(kit.Space16).Selectable().Children(func() {
				body()
			})
		})
	if a.viewerFocus {
		a.viewerFocus = false
		a.viewerPage.Focus(c)
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
				Padding(kit.Space16, kit.Space20, 32, 0).TrackScroll(&a.asideScroll).
				Children(func() {
					ui.Column(c).Gap(kit.Space14).Selectable().Children(func() { a.taskAside(c, p) })
				})
		})
		return
	}
	a.taskMain(c, k, p, true)
}

func (a *App) taskMain(c *ui.Context, k kit.Colors, p palette, asideFirst bool) {
	ui.Scroll(c).Grow(1).FillHeight().MinHeight(0).Padding(kit.Space16, kit.Space20, 32).
		Focusable().FocusRing(false).TrackScroll(&a.viewerScroll).Label("The page").
		Bind(&a.viewerPage).
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
		a.viewerPage.Focus(c)
	}
}

func (a *App) taskHead(c *ui.Context, p palette) {
	t := a.Task
	ui.Column(c).Gap(kit.Space6).Children(func() {
		ui.Text(c, "Fleets / "+shownText(fleetName(a.Fleet))+" / "+shownText(t.Task.Ref)).
			FontSize(kit.Body.Size).TextColor(p.neutral).SingleLine()
		ui.Text(c, shownText(t.Task.Title)).FontSize(kit.Display.Size).
			FixedLineHeight(kit.Display.Line).FontWeight(600)
		ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
			bg, fg := toneColors(t.Pill, p)
			ui.Badge(c, shownText(t.Task.Status)).Background(bg).TextColor(fg)
			if t.Task.Project != "" {
				ui.Text(c, shownText(t.Task.Project)).FontSize(kit.Meta.Size).
					FixedLineHeight(kit.Meta.Line).SingleLine()
			}
			if t.Task.Parent != "" {
				ui.Text(c, "Sub-task of "+shownText(t.Task.Parent)).FontSize(kit.Meta.Size).TextColor(p.neutral)
			}
			if t.Plan != nil {
				ui.Text(c, "plan "+shownText(t.Plan.Ref)).FontSize(kit.Meta.Size).
					FixedLineHeight(kit.Meta.Line)
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

func attemptTitle(at AttemptDetail) string {
	return shownText(strings.TrimSpace(strings.Join([]string{at.Attempt.Harness, at.Attempt.Model, at.Attempt.Effort}, " ")))
}

func (a *App) attemptPage(c *ui.Context, k kit.Colors, p palette, wide bool) {
	if wide {
		ui.Row(c).Grow(1).MinHeight(0).Gap(kit.ViewerAsideGap).Children(func() {
			a.attemptMain(c, k, p, false)
			ui.Scroll(c).Width(kit.ViewerAside).FillHeight().
				Padding(kit.Space16, kit.Space20, 32, 0).TrackScroll(&a.asideScroll).
				Children(func() {
					ui.Column(c).Gap(kit.Space14).Selectable().Children(func() { a.attemptAside(c, p) })
				})
		})
		return
	}
	a.attemptMain(c, k, p, true)
}

func (a *App) attemptMain(c *ui.Context, k kit.Colors, p palette, asideFirst bool) {
	ui.Scroll(c).Grow(1).FillHeight().MinHeight(0).Padding(kit.Space16, kit.Space20, 32).
		Focusable().FocusRing(false).TrackScroll(&a.viewerScroll).Label("The page").
		Bind(&a.viewerPage).
		Children(func() {
			ui.Column(c).Gap(kit.Space14).Selectable().Children(func() {
				a.actionCard(c, p)
				a.attemptHead(c, p)
				if asideFirst {
					a.attemptAside(c, p)
				}
				a.attemptSections(c, p)
			})
		})
	if a.viewerFocus {
		a.viewerFocus = false
		a.viewerPage.Focus(c)
	}
}

func (a *App) attemptHead(c *ui.Context, p palette) {
	at := a.Attempt
	ui.Column(c).Gap(kit.Space6).Children(func() {
		ui.Text(c, "Fleets / "+shownText(fleetName(a.Fleet))+" / "+shownText(at.Task.Ref)+" / "+shownText(at.Attempt.Ref)).
			FontSize(kit.Body.Size).TextColor(p.neutral).SingleLine()
		ui.Text(c, attemptTitle(at)).FontSize(kit.Display.Size).
			FixedLineHeight(kit.Display.Line).FontWeight(600)
		ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
			bg, fg := toneColors(at.State, p)
			ui.Badge(c, shownText(at.Attempt.Status)).Background(bg).TextColor(fg)
			ui.Text(c, shownText(at.Attempt.Ref)).Font("monospace").FontSize(kit.Meta.Size).
				TextColor(p.neutral)
			if at.Attempt.Reason != "" {
				ui.Text(c, shownText(at.Attempt.Reason)).FontSize(kit.Meta.Size).
					TextColor(p.neutral).SingleLine().Grow(1).MinWidth(0)
			} else {
				ui.Spacer(c)
			}
			task := ui.ButtonBase(c.Key("attempt-task:" + at.Task.Ref)).Cursor(ui.CursorPointer).
				Label("open task " + at.Task.Ref)
			task.Children(func() {
				ui.Text(c, shownText(at.Task.Title)).FontSize(kit.Body.Size).
					TextColor(p.accent).SingleLine().Ellipsis("…")
			})
			if task.Clicked() {
				a.OpenRef(at.Task.Ref)
			}
		})
		ui.Box(c).Height(1).Background(p.line)
	})
}

func (a *App) attemptAside(c *ui.Context, p palette) {
	at := a.Attempt
	a.asideHead(c, p, "Details")
	a.attemptFacts(c, p)
	if len(at.Delivered) > 0 {
		a.asideHead(c, p, "Delivered")
		ui.Column(c).Gap(kit.Space8).Children(func() {
			for i, d := range at.Delivered {
				a.deliveredAside(c, p, i, d)
			}
		})
	}
}

func (a *App) attemptFacts(c *ui.Context, p palette) {
	at := a.Attempt
	facts := []factRow{
		{"Harness", shownText(at.Attempt.Harness), ""},
		{"Model", shownText(at.Attempt.Model), ""},
		{"Effort", shownText(at.Attempt.Effort), ""},
		{"Answered by", shownText(at.Transcript.Answered), ""},
		{"Started", stamp(at.Attempt.CreatedAt), ""},
	}
	if at.Attempt.EndedAt != "" {
		facts = append(facts,
			factRow{"Stopped", stamp(at.Attempt.EndedAt), ""},
			factRow{"Duration", shownText(at.Duration), ""})
	} else {
		facts = append(facts, factRow{"Running for", shownText(at.Duration), ""})
	}
	facts = append(facts,
		factRow{"Continues", shownText(at.Attempt.Continues), at.Attempt.Continues},
		factRow{"Branch", shownText(at.Attempt.Branch), ""})
	ui.Column(c).Gap(kit.Space4).Children(func() {
		for _, fact := range facts {
			if fact.value == "" {
				continue
			}
			label, value, open := fact.label, fact.value, fact.open
			ui.Row(c).Gap(kit.Space12).AlignItems(ui.Start).Children(func() {
				ui.Text(c, label).FontSize(kit.Meta.Size).TextColor(p.neutral).Width(88)
				if open == "" {
					ui.Text(c, value).FontSize(kit.Meta.Size).Grow(1).MinWidth(0)
					return
				}
				el := ui.ButtonBase(c.Key("attempt-fact:" + open)).Cursor(ui.CursorPointer).
					Label(label + " " + open)
				el.Children(func() {
					ui.Text(c, value).FontSize(kit.Meta.Size).TextColor(p.accent).SingleLine()
				})
				if el.Clicked() {
					a.OpenRef(open)
				}
			})
		}
	})
}

type factRow struct{ label, value, open string }

func (a *App) attemptSections(c *ui.Context, p palette) {
	at := a.Attempt
	if at.ModelNote != "" {
		a.warnLine(c, p, at.ModelNote)
	}
	a.mainHead(c, "Reports")
	if len(at.Reports) == 0 {
		a.quiet(c, p, "No reports yet.")
	}
	for _, r := range at.Reports {
		a.reportCard(c, p, r, false)
	}
	if at.MoreReports {
		more(c, p, "Older reports are not shown; run hand report list --attempt "+
			at.Attempt.Ref+" --limit 500.")
	}
	a.mainHead(c, "Brief")
	if at.Brief == "" {
		a.quiet(c, p, "No brief is recorded for this attempt.")
	} else {
		a.preBox(c, p, at.Brief)
	}
	if at.Transcript.Shown || at.Transcript.Note != "" {
		a.mainHead(c, "Transcript")
	}
	switch {
	case at.Transcript.Shown && len(at.Transcript.Turns) == 0:
		if note := at.Transcript.Note; note != "" {
			a.quiet(c, p, shownText(note))
		} else {
			a.quiet(c, p, "Nothing in the transcript yet.")
		}
	case at.Transcript.Shown:
		if at.Transcript.Model != "" {
			a.warnLine(c, p, at.Transcript.Model)
		}
		for _, turn := range at.Transcript.Turns {
			ui.Column(c).Gap(kit.Space2).Padding(0, 0, kit.Space8).Children(func() {
				ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, shownText(turn.Role)).FontSize(kit.Meta.Size).Bold().
						TextColor(p.neutral)
					ui.Text(c, stamp(turn.At)).Font("monospace").FontSize(kit.Meta.Size).
						TextColor(p.neutral).FontFeatures("tnum")
					if turn.Queued {
						ui.Badge(c, "queued").Background(p.neutralBG).TextColor(p.neutral)
					}
				})
				a.markdown(c, shownText(turn.Text))
			})
		}
		if at.Transcript.More {
			more(c, p, "Only the latest 50 entries are shown.")
		}
	default:
		if note := at.Transcript.Note; note != "" {
			a.quiet(c, p, shownText(note))
		}
	}
	a.mainHead(c, "Timeline")
	a.events(c, p, at.Events)
	if at.MoreEvents {
		more(c, p, "Only the latest 50 events are shown.")
	}
}

func (a *App) events(c *ui.Context, p palette, events []Event) {
	ui.Column(c).Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).Children(func() {
		for _, e := range events {
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
}

func (a *App) decisionPage(c *ui.Context, k kit.Colors, p palette) {
	d := a.Decision
	ui.Column(c).Gap(kit.Space14).Children(func() {
		ui.Column(c).Gap(kit.Space6).Children(func() {
			ui.Text(c, "Fleets / "+shownText(fleetName(a.Fleet))+" / "+shownText(d.Task.Ref)+" / "+shownText(d.Decision.Ref)).
				FontSize(kit.Body.Size).TextColor(p.neutral).SingleLine()
			ui.Text(c, shownText(d.Decision.Headline)).FontSize(kit.Display.Size).
				FixedLineHeight(kit.Display.Line).FontWeight(600)
			ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
				bg, fg := toneColors(d.Decision.Pill, p)
				ui.Badge(c, shownText(d.Decision.Status)).Background(bg).TextColor(fg)
				ui.Text(c, shownText(d.Decision.Ref)).Font("monospace").FontSize(kit.Meta.Size).
					TextColor(p.neutral)
				ui.Text(c, stamp(d.Decision.CreatedAt)).FontSize(kit.Meta.Size).
					TextColor(p.neutral).FontFeatures("tnum")
			})
			if body := decisionProse(d); body != "" {
				a.markdown(c, body)
			}
			task := ui.ButtonBase(c.Key("decision-task:" + d.Task.Ref)).AlignSelf(ui.Start).
				Cursor(ui.CursorPointer).Label("open task " + d.Task.Ref)
			task.Children(func() {
				ui.Text(c, "for "+shownText(d.Task.Title)).FontSize(kit.Meta.Size).
					TextColor(p.accent).SingleLine()
			})
			if task.Clicked() {
				a.OpenRef(d.Task.Ref)
			}
			ui.Box(c).Height(1).Background(p.line)
		})
		a.choiceKeys(c, d)
		a.answerForm(c, k, p, d)
	})
}

func decisionProse(d DecisionDetail) string {
	return shownText(questionBody(d.Body, "", len(d.Options) > 0))
}

func decisionAnswer(d DecisionDetail) (key, note string) {
	first, rest, _ := strings.Cut(d.Decision.Answer, "\n")
	if m := optionLine.FindStringSubmatch(first); m != nil {
		for _, o := range d.Options {
			if o.Key == m[1] {
				return o.Key, strings.TrimSpace(rest)
			}
		}
	}
	return "", strings.TrimSpace(d.Decision.Answer)
}

func (a *App) answeredDecision(c *ui.Context, k kit.Colors, p palette, d DecisionDetail) {
	key, note := decisionAnswer(d)
	ui.Column(c).Gap(kit.Space12).Children(func() {
		if key != "" && len(d.Options) > 0 {
			choices := make([]kit.Choice, 0, len(d.Options))
			for _, o := range d.Options {
				choices = append(choices, kit.Choice{
					Value: o.Value, Key: o.Key, Label: shownText(o.Label), Note: shownText(o.Note),
					Recommended: o.Recommended, Long: o.Long, Disabled: true,
					Picked: o.Key == key, Answer: o.Key == key,
				})
			}
			kit.ChoiceList(c, k, choices)
		}
		if note == "" {
			line := "Answered"
			if d.Decision.Status == "withdrawn" {
				line = "Withdrawn by the supervisor"
			}
			if d.Decision.Status != "withdrawn" && d.Decision.AnsweredBy != "" {
				line += " by " + shownText(d.Decision.AnsweredBy)
			}
			if at := stamp(d.Decision.ClosedAt); at != "" {
				line += " · " + at
			}
			a.quiet(c, p, line)
			return
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			ui.Column(c).MaxWidthPercent(75).Border(1, p.accentBG).Radius(kit.RadiusLG).
				Background(p.accentBG).Clip().Children(func() {
				ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Padding(kit.Space6, kit.Space12).
					BorderWidth(0, 0, 1, 0).BorderColor(p.line).Children(func() {
					ui.Text(c, "you").FontSize(kit.Meta.Size).Bold().TextColor(p.accent)
					meta := "answered"
					if key != "" {
						meta = "note"
					}
					if by := d.Decision.AnsweredBy; by != "" && by != "operator" {
						meta += " by " + shownText(by)
					}
					ui.Text(c, meta).FontSize(kit.Meta.Size).TextColor(p.neutral)
					ui.Spacer(c)
					if at := stamp(d.Decision.ClosedAt); at != "" {
						ui.Text(c, at).FontSize(kit.Meta.Size).TextColor(p.neutral).
							FontFeatures("tnum")
					}
				})
				ui.Box(c).Padding(kit.Space10, kit.Space12).Children(func() {
					ui.Text(c, shownText(note)).FontSize(kit.Message.Size).
						FixedLineHeight(kit.Message.Line)
				})
			})
		})
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
	target := a.scrollTo
	for _, r := range t.Reports {
		if r.Ref == target {
			a.scrollTo = ""
		}
		a.reportCard(c, p, r, r.Ref == target)
	}
	if r, ok := a.pageReportCard(); ok {
		if r.Ref == target {
			a.scrollTo = ""
		}
		a.reportCard(c, p, r, r.Ref == target)
	}
	if t.MoreReports {
		more(c, p, "Only the latest 50 reports are shown.")
	}
	a.mainHead(c, "Decisions")
	if len(t.Decisions) == 0 {
		a.quiet(c, p, "No decisions yet.")
	} else {
		ui.Column(c).Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).Children(func() {
			for _, d := range t.Decisions {
				a.decisionLine(c, p, d)
			}
		})
	}
	a.mainHead(c, "Events")
	a.events(c, p, t.Events)
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

func (a *App) warnLine(c *ui.Context, p palette, text string) {
	ui.Text(c, shownText(text)).FontSize(kit.Body.Size).TextColor(p.wait)
}

// briefMax is board.css's 24em .brief box at the window's 12px mono.
var briefMax = 24 * kit.Meta.Size

func (a *App) preBox(c *ui.Context, p palette, text string) {
	ui.Scroll(c).MaxHeight(briefMax).Padding(kit.Space10, kit.Space12).
		Border(1, p.line).Radius(kit.RadiusLG).Background(p.card).
		Focusable().Label("The brief").Children(func() {
		ui.Text(c, shownText(text)).Font("monospace").FontSize(kit.Meta.Size).
			FixedLineHeight(kit.Meta.Line)
	})
}

func (a *App) reportCard(c *ui.Context, p palette, r Report, target bool) {
	bg, _ := toneColors(reportTone(r.Status), p)
	badgeBG, badgeFG := toneColors(reportBadgeTone(r.Status), p)
	card := ui.Column(c).Border(1, p.line).Radius(kit.RadiusLG).Clip().
		Label("report " + r.Ref).Children(func() {
		ui.Column(c).Gap(kit.Space2).Padding(kit.Space6, kit.Space12).
			Background(bg).BorderWidth(0, 0, 1, 0).BorderColor(p.line).Children(func() {
			ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
				open := ui.ButtonBase(c.Key("open-report:" + r.Ref)).
					Cursor(ui.CursorPointer).Label("open report " + r.Ref)
				open.Children(func() {
					ui.Text(c, shownText(r.Ref)).Font("monospace").FontSize(kit.Meta.Size).
						Bold().TextColor(p.accent)
				})
				if open.Clicked() {
					a.openReport(r.Ref)
				}
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
				ui.Badge(c, shownText(r.Status)).Background(badgeBG).TextColor(badgeFG)
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
	if target {
		card.ScrollIntoView()
	}
}

func (a *App) ackDisabled(ref string) bool {
	return a.Busy && a.ackRef == ref
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
		Label("Mark read " + r.Ref).Disabled(a.ackDisabled(r.Ref))
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
			switch {
			case !board:
				c.OpenURL(dest)
			case !a.openBoardPage(dest):
				a.OpenBoardPath(dest)
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
		case dest != "":
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

func reportBadgeTone(status string) string {
	if status == "progress" {
		return "wait"
	}
	return reportTone(status)
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
