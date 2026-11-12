package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const receiptFor = 4 * time.Second

func needKey(n Need) string { return n.Kind + ":" + n.Ref }

func needHasBody(n Need) bool {
	switch n.Kind {
	case "blocked", "decision", "report":
		return true
	}
	return n.Attempt != "" || n.Excerpt != "" || n.Status != ""
}

func (a *App) visibleNeeds() []Need {
	items := a.Status.Needs.Items
	if len(a.ackDone) == 0 {
		return items
	}
	out := make([]Need, 0, len(items))
	for _, n := range items {
		if !a.ackDone[needRef(n)] {
			out = append(out, n)
		}
	}
	return out
}

func (a *App) waitingNeeds() int {
	if len(a.ackDone) == 0 {
		return a.Status.Needs.Waiting
	}
	hidden := 0
	for _, n := range a.Status.Needs.Items {
		if a.ackDone[needRef(n)] {
			hidden++
		}
	}
	return max(0, a.Status.Needs.Waiting-hidden)
}

func (a *App) reconcileAcked() {
	if len(a.ackDone) == 0 {
		return
	}
	live := make(map[string]bool, len(a.Status.Needs.Items))
	for _, n := range a.Status.Needs.Items {
		live[needRef(n)] = true
	}
	for ref := range a.ackDone {
		if !live[ref] {
			delete(a.ackDone, ref)
		}
	}
}

func (a *App) openNeed(items []Need) string {
	for _, n := range items {
		if a.needChosen[needKey(n)] && needHasBody(n) {
			return needKey(n)
		}
	}
	top := needKey(items[0])
	if closed, ok := a.needChosen[top]; ok && !closed {
		return ""
	}
	if top == a.needClosedTop || !needHasBody(items[0]) {
		return ""
	}
	return top
}

func (a *App) syncAnswer(items []Need, open string) {
	ref := ""
	for _, n := range items {
		if needKey(n) == open && n.Kind == "decision" {
			ref = n.Decision
			break
		}
	}
	if ref == a.answerRef {
		return
	}
	a.answerRef, a.AnswerChoice, a.AnswerNote = ref, "", ""
}

func (a *App) toggleNeed(key, top string, open bool) {
	if a.needChosen == nil {
		a.needChosen = map[string]bool{}
	}
	if open {
		a.needChosen[key] = false
		a.needClosedTop = top
	} else {
		for k, v := range a.needChosen {
			if v && k != key {
				a.needChosen[k] = false
			}
		}
		a.needChosen[key] = true
	}
	a.decTried, a.decErr = "", ""
	a.attTried, a.attErr = "", ""
	a.answerRef, a.AnswerChoice, a.AnswerNote = "", "", ""
}

func (a *App) nextNeed(done string) {
	if a.Screen != BoardScreen {
		return
	}
	a.needChosen = nil
	a.decTried, a.decErr = "", ""
	a.attTried, a.attErr = "", ""
	a.answerRef, a.AnswerChoice, a.AnswerNote = "", "", ""
	items := a.visibleNeeds()
	for _, n := range items {
		if needRef(n) != done && needHasBody(n) {
			a.needChosen = map[string]bool{needKey(n): true}
			return
		}
	}
	if len(items) > 0 {
		a.needClosedTop = needKey(items[0])
	}
}

func (a *App) answerReceipt(ref string) string {
	if sup := a.Status.Supervisor; sup != nil && sup.Status == "running" {
		return ref + " answered · " + sup.Ref + " reads it now"
	}
	return ref + " answered · no supervisor is running; it waits"
}

func (a *App) loadNeedDecision(ref string) {
	if a.Model == nil || a.Fleet.ID == "" || a.decTried == ref {
		return
	}
	a.decTried, a.decErr = ref, ""
	a.decGen++
	gen := a.decGen
	model, fleet, post := a.Model, a.Fleet.ID, a.Post
	ctx, cancel := context.WithTimeout(a.Context, a.pollTimeout())
	apply := func(d DecisionDetail, err error) {
		if a.Fleet.ID != fleet || a.decTried != ref || a.decGen != gen {
			return
		}
		if err != nil {
			a.decErr = friendly(err)
			return
		}
		a.Decision = d
	}
	if post == nil {
		d, err := model.Decision(ctx, fleet, ref)
		cancel()
		apply(d, err)
		return
	}
	go func() {
		d, err := model.Decision(ctx, fleet, ref)
		cancel()
		post(func() { apply(d, err) })
	}()
}

func (a *App) loadNeedAttempt(ref string) {
	if a.Model == nil || a.Fleet.ID == "" || ref == "" || a.attTried == ref {
		return
	}
	a.attTried, a.attErr = ref, ""
	a.attGen++
	gen := a.attGen
	model, fleet, post := a.Model, a.Fleet.ID, a.Post
	ctx, cancel := context.WithTimeout(a.Context, a.pollTimeout())
	apply := func(d AttemptDetail, err error) {
		if a.Fleet.ID != fleet || a.attTried != ref || a.attGen != gen {
			return
		}
		if err != nil {
			a.attErr = friendly(err)
			return
		}
		a.needAttempt = d
	}
	if post == nil {
		d, err := model.Attempt(ctx, fleet, ref)
		cancel()
		apply(d, err)
		return
	}
	go func() {
		d, err := model.Attempt(ctx, fleet, ref)
		cancel()
		post(func() { apply(d, err) })
	}()
}

func (a *App) needsScreen(c *ui.Context, k kit.Colors, p palette) {
	needs := a.Status.Needs
	items := a.visibleNeeds()
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, "Needs").FontSize(kit.Display.Size).Bold()
		if n := a.waitingNeeds(); n > 0 {
			bg, fg := toneColors(needs.Worst, p)
			ui.Badge(c, fmt.Sprint(n)).Background(bg).TextColor(fg)
		}
	})
	if len(items) == 0 {
		a.allClear(c, k)
		return
	}
	open := a.openNeed(items)
	a.syncAnswer(items, open)
	top := needKey(items[0])
	ui.Column(c).Children(func() {
		prev, afterOpen := "", false
		for i, item := range items {
			need := item
			first := need.Word != prev
			if first {
				_, ink := toneColors(need.Tone, p)
				ui.Text(c, capitalize(strings.ToLower(need.Word))).FontSize(kit.Meta.Size).Bold().TextColor(ink).
					Padding(kit.Space20*float32(min(i, 1)), kit.Space16, kit.Space4)
				prev = need.Word
			}
			isOpen := needKey(need) == open
			a.needRow(c, k, p, need, top, first, afterOpen, isOpen)
			afterOpen = isOpen
		}
	})
	if needs.More > 0 {
		ui.Textf(c, "%d more waiting", needs.More).FontSize(kit.Meta.Size).TextColor(k.Muted)
	}
}

func (a *App) allClear(c *ui.Context, k kit.Colors) {
	n := a.Status.Needs
	rest := ""
	if n.Cleared != "" {
		rest += " since " + stamp(n.Cleared)
	}
	if n.Summary != "" {
		rest += " · " + n.Summary
	}
	ui.Row(c).Gap(kit.Space6).Padding(kit.Space12, 0).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Icon(c, kit.Icon("passing")).TextColor(k.Pass)
		ui.Text(c, "All clear").FontSize(kit.Body.Size).Bold()
		if rest = strings.TrimSpace(rest); rest != "" {
			ui.Text(c, rest).FontSize(kit.Body.Size).TextColor(k.Muted)
		}
	})
}

func (a *App) needRow(c *ui.Context, k kit.Colors, p palette, need Need, top string, first, afterOpen, open bool) {
	inert := !needHasBody(need)
	open = open && !inert
	bg, ink := toneColors(need.Tone, p)
	refs := []string{need.Ref}
	if need.Task != "" && need.Task != need.Ref {
		refs = append(refs, need.Task)
	}
	slug := strings.TrimSpace(strings.Join(refs, " "))
	card := ui.Column(c)
	if open {
		card.Margin(kit.Space6, 0, kit.Space10).Border(1, k.Line).Radius(kit.RadiusLG).Clip()
	} else {
		top := float32(0)
		if first || afterOpen {
			top = 1
		}
		card.BorderWidth(top, 0, 1, 0).BorderColor(k.Line)
	}
	card.Children(func() {
		head := ui.ButtonBase(c.Key("need:" + needKey(need))).Gap(kit.Space10).Label(need.Title)
		if !inert {
			head.Cursor(ui.CursorPointer)
		}
		if open {
			head.Background(bg).Padding(kit.Space12, 15, kit.Space10).AlignItems(ui.Start)
		} else {
			head.MinHeight(kit.Tap).Padding(0, kit.Space16).AlignItems(ui.Center)
		}
		head.Children(func() {
			icon := ui.Icon(c, stateIcon(need.Tone)).TextColor(ink)
			if open {
				icon.Margin(3, 0, 0)
			}
			meta := func() {
				line := slug
				if open && need.Kind == "report" {
					if need.Attempt != "" {
						line += " " + need.Attempt
					}
					if need.Status != "" {
						line += " · " + need.Status
					}
				}
				if line != "" {
					ui.Text(c, line).Font("monospace").FontSize(kit.Meta.Size).TextColor(k.Muted).SingleLine()
				}
				if need.CreatedAt != "" {
					ui.Text(c, stamp(need.CreatedAt)).FontSize(kit.Meta.Size).TextColor(k.Muted).SingleLine()
				}
			}
			title := needLabel(need)
			if open {
				ui.Column(c).Grow(1).MinWidth(0).Gap(kit.Space2).Children(func() {
					ui.Text(c, title).FontSize(kit.Title.Size).Bold()
					ui.Row(c).Gap(kit.Space10).Children(meta)
				})
			} else {
				ui.Text(c, title).FontSize(kit.Body.Size).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
				meta()
			}
			if !inert {
				chevron := ui.Icon(c, kit.Icon("chevron")).TextColor(k.Muted)
				if open {
					chevron.Rotate(180)
				}
			}
		})
		if head.Clicked() && !inert {
			a.toggleNeed(needKey(need), top, open)
		}
		if open {
			a.needBody(c, k, p, need)
		}
	})
}

func (a *App) needBody(c *ui.Context, k kit.Colors, p palette, need Need) {
	switch need.Kind {
	case "decision":
		a.loadNeedDecision(need.Decision)
	case "failure":
		a.loadNeedAttempt(need.Attempt)
	}
	ui.Column(c).Gap(kit.Space10).Padding(kit.Space10, 15, 15, 41).Children(func() {
		link := func(ref string) {
			if ref == "" {
				return
			}
			label := "View " + ref
			switch need.Kind {
			case "decision":
				label = "Open " + ref
			case "report":
				if need.Cut {
					label = "read more"
				} else {
					label = "Open " + ref
				}
			}
			el := ui.ButtonBase(c.Key("open:" + ref)).Height(kit.Control).AlignSelf(ui.Start).Cursor(ui.CursorPointer).Label(label)
			el.Children(func() { ui.Text(c, label).FontSize(kit.Body.Size).TextColor(k.Accent) })
			if el.Clicked() {
				a.OpenRef(ref)
			}
		}
		switch need.Kind {
		case "report":
			if need.Excerpt != "" {
				a.markdown(c, need.Excerpt)
			}
			ui.Row(c).Gap(kit.Space12).AlignItems(ui.Center).Children(func() {
				label := "Mark read"
				if a.Busy {
					label = "Marking read…"
				}
				if ui.Button(c.Key("ack:"+need.Report), label).Disabled(a.Busy).Clicked() {
					a.Ack(need.Report)
				}
				link(need.Report)
			})
		case "decision":
			d := a.Decision
			switch {
			case a.decTried == need.Decision && a.decErr != "":
				a.needReadError(c, p, "retry-decision:"+need.Decision, a.decErr, func() { a.decTried, a.decErr = "", "" })
			case d.Decision.Ref == need.Decision:
				if body := questionBody(d.Decision.Question, d.Decision.Headline, len(d.Options) > 0); body != "" {
					a.markdown(c, body)
				}
				a.answerForm(c, p, d, false)
				if d.Decision.Answer != "" {
					ui.Text(c, "Answer").FontSize(kit.Meta.Size).Bold().TextColor(k.Muted)
					a.markdown(c, d.Decision.Answer)
				}
			default:
				ui.Text(c, "Reading "+need.Decision+"…").FontSize(kit.Body.Size).TextColor(k.Muted)
			}
			link(need.Decision)
		case "blocked":
			if sup := a.knownSupervisor(); sup != nil && sup.Blocked {
				a.blockedKeys(c, p, sup)
			}
		case "failure":
			switch {
			case a.attErr != "" && a.attTried == need.Attempt:
				a.needReadError(c, p, "retry-attempt:"+need.Attempt, a.attErr, func() { a.attTried, a.attErr = "", "" })
			case a.needAttempt.Attempt.Ref == need.Attempt:
				if reason := a.needAttempt.Attempt.Reason; reason != "" {
					ui.Text(c, reason).FontSize(kit.Body.Size).TextColor(k.Muted)
				}
			default:
				if need.Attempt != "" {
					ui.Text(c, "Reading "+need.Attempt+"…").FontSize(kit.Body.Size).TextColor(k.Muted)
				}
			}
			link(need.Attempt)
		default:
			link(need.Attempt)
		}
	})
}

func (a *App) needReadError(c *ui.Context, p palette, key, msg string, retry func()) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, msg).FontSize(kit.Body.Size).TextColor(p.fail)
		if ui.Button(c.Key(key), "Retry").Clicked() {
			retry()
		}
	})
}

func (a *App) answerForm(c *ui.Context, p palette, d DecisionDetail, heading bool) {
	open := d.Decision.Status == "open"
	other := false
	for _, o := range d.Options {
		if a.AnswerChoice == o.Value && o.Other {
			other = true
		}
	}
	if len(d.Options) > 0 {
		if heading {
			a.section(c, p, "Options")
		}
		ui.Column(c).Gap(6).Children(func() {
			for _, o := range d.Options {
				option := o
				selected := a.AnswerChoice == option.Value
				bg, border := p.card, p.line
				if selected {
					bg, border = p.passBG, p.pass
				}
				el := ui.ButtonBase(c.Key("option:"+option.Key)).Gap(10).Padding(8, 10).
					Radius(8).Background(bg).Border(1, border).Disabled(!open || a.Busy)
				if open && !a.Busy {
					el.Cursor(ui.CursorPointer)
				}
				el.Children(func() {
					ui.Text(c, option.Key).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral).Width(18)
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Row(c).Gap(8).Children(func() {
							ui.Text(c, option.Label).FontSize(kit.Body.Size).Bold()
							if option.Recommended {
								ui.Badge(c, "Recommended").Background(p.passBG).TextColor(p.pass)
							}
						})
						if option.Note != "" {
							ui.Text(c, option.Note).FontSize(kit.Meta.Size).TextColor(p.neutral)
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
		if other {
			placeholder = "Write your answer (required)"
		}
		ui.TextArea(c, &a.AnswerNote).Label("Note").Placeholder(placeholder).Height(64).
			Disabled(a.Busy)
		if other && strings.TrimSpace(a.AnswerNote) == "" {
			ui.Text(c, "That option needs a note.").FontSize(kit.Meta.Size).TextColor(p.fail)
		}
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
}
