package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	receiptFor       = 4 * time.Second
	errorFor         = 8 * time.Second
	noteRequiredText = "Write your answer: that option asks for one."
)

func needKey(n Need) string { return n.Kind + ":" + n.Ref }

func needHasBody(n Need) bool {
	switch n.Kind {
	case "blocked", "decision", "report":
		return true
	}
	return n.Attempt != "" || n.Excerpt != "" || n.Status != ""
}

func (a *App) markSent(ref, label string) {
	if ref == "" {
		return
	}
	if a.sentNeeds == nil {
		a.sentNeeds = map[string]string{}
	}
	a.sentNeeds[ref] = "Sent " + label + " · waiting…"
}

func sentLabel(choice string) string {
	head, _, _ := strings.Cut(strings.TrimSpace(choice), ".")
	if head = strings.TrimSpace(head); head == "" {
		return "answer"
	}
	return head
}

func (a *App) sentNeed(n Need) (string, bool) {
	for _, ref := range []string{n.Decision, n.Ref} {
		if ref == "" {
			continue
		}
		if sent, ok := a.sentNeeds[ref]; ok {
			return sent, true
		}
	}
	return "", false
}

func (a *App) reconcileSent() {
	if len(a.sentNeeds) == 0 {
		return
	}
	live := make(map[string]bool, 2*len(a.Status.Needs.Items))
	for _, n := range a.Status.Needs.Items {
		if n.Decision != "" {
			live[n.Decision] = true
		}
		if n.Ref != "" {
			live[n.Ref] = true
		}
	}
	for ref := range a.sentNeeds {
		if !live[ref] {
			delete(a.sentNeeds, ref)
		}
	}
}

func (a *App) needOpens(n Need) bool {
	if !needHasBody(n) {
		return false
	}
	_, sent := a.sentNeed(n)
	return !sent
}

func (a *App) visibleNeeds() []Need {
	items := a.Status.Needs.Items
	a.reconcileSent()
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
	top := needKey(items[0])
	if top != a.needTop {
		a.needTop = top
		if a.needOpens(items[0]) {
			a.needChosen, a.needClosedTop = nil, ""
		}
	}
	for _, n := range items {
		if a.needChosen[needKey(n)] && a.needOpens(n) {
			return needKey(n)
		}
	}
	if closed, ok := a.needChosen[top]; ok && !closed {
		return ""
	}
	if top == a.needClosedTop || !a.needOpens(items[0]) {
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
		if needRef(n) != done && a.needOpens(n) {
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
		if n := a.waitingNeeds(); n > 0 {
			ui.Textf(c, "%d more waiting", n).FontSize(kit.Meta.Size).TextColor(k.Muted)
			return
		}
		a.allClear(c, k)
		return
	}
	open := a.openNeed(items)
	a.syncAnswer(items, open)
	a.choiceKeysFor(c, items, open)
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
	sent, wasSent := a.sentNeed(need)
	inert := !needHasBody(need) || wasSent
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
				if wasSent {
					ui.Text(c, sent).FontSize(kit.Meta.Size).TextColor(k.Muted).SingleLine()
				} else {
					meta()
				}
			}
			if !wasSent && need.DenyAt != "" {
				a.needCountdown(c, p, need)
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
				a.answerForm(c, k, p, d)
			default:
				ui.Text(c, "Reading "+need.Decision+"…").FontSize(kit.Body.Size).TextColor(k.Muted)
			}
			link(need.Decision)
		case "blocked":
			a.blockedNeedBody(c, k, p, need)
		case "worker":
			a.workerNeedBody(c, p, need)
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

func (a *App) blockedNeedBody(c *ui.Context, k kit.Colors, p palette, need Need) {
	sup := a.knownSupervisor()
	var ask *Ask
	if sup != nil && sup.Blocked {
		ask = sup.Ask
	}
	if ask != nil && ask.Question != "" {
		ui.Text(c, ask.Question).FontSize(kit.Body.Size).Bold()
	}
	a.screenExcerpt(c, p, need.Excerpt, need.Cut)
	choices := supervisorKeyChoices(&Supervisor{Ask: ask})
	enabled := sup != nil && sup.Blocked && sup.Keys != nil
	a.keyRow(c, p, choices, !enabled, func(choice keyChoice) {
		a.Press(choice.Label, choice.Keys...)
	})
	if sup != nil && sup.Blocked {
		a.misread(c, p, need)
	}
}

func (a *App) workerNeedBody(c *ui.Context, p palette, need Need) {
	a.screenExcerpt(c, p, need.Excerpt, need.Cut)
	a.workerKeyButtons(c, p, need)
}

func (a *App) screenExcerpt(c *ui.Context, p palette, text string, cut bool) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if cut && !strings.HasSuffix(strings.TrimSpace(text), "…") {
		text += "\n…"
	}
	ui.Column(c).Background(p.screen).Radius(kit.RadiusMD).Padding(10, 12).Border(1, p.line).Children(func() {
		ui.Text(c, text).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.screenFG)
	})
}

func (a *App) misread(c *ui.Context, p palette, need Need) {
	ui.Collapsible(c, "Screen misread?", a.misreadFor(needKey(need)), func() {
		ui.Column(c).Gap(kit.Space8).Children(func() {
			ui.Text(c, "Types the queued messages, or else the wakes, into its prompt.").
				FontSize(kit.Meta.Size).TextColor(p.neutral)
			label := "Type queued messages anyway"
			if a.BusyAction == "force" {
				label = "Forcing…"
			}
			if ui.Button(c.Key("force:"+need.Ref), label).Disabled(a.Busy).Clicked() {
				a.Supervise("force")
			}
		})
	})
}

func (a *App) misreadFor(key string) *bool {
	if a.misreadOpen == nil {
		a.misreadOpen = map[string]*bool{}
	}
	if a.misreadOpen[key] == nil {
		a.misreadOpen[key] = new(bool)
	}
	return a.misreadOpen[key]
}

func (a *App) pressWorker(need Need, choice keyChoice) {
	if a.Busy || a.Model == nil || a.Fleet.ID == "" || need.Keys == nil || len(choice.Keys) == 0 {
		return
	}
	ref := need.Attempt
	if ref == "" {
		ref = need.Ref
	}
	guard := *need.Keys
	model, fleet := a.Model, a.Fleet.ID
	id, ctx, cancel := a.nextLoad()
	post := a.Post
	a.Busy, a.BusyAction = true, choice.Action
	sent := sentKeyLabel(choice.Label)
	applied := func() {
		if id != a.loadID {
			return
		}
		a.Busy, a.BusyAction = false, ""
		a.markSent(ref, sent)
		a.receipt = "Sent " + sent + " to " + ref
		a.receiptKey = "akey:" + ref + ":" + sent
	}
	failed := func(err error) {
		if id != a.loadID {
			return
		}
		a.Busy, a.BusyAction = false, ""
		if isConflict(err) {
			delete(a.sentNeeds, ref)
			a.receipt = "the screen moved; nothing was sent"
			a.receiptKey = "akey:" + ref
			a.reopenNeed(need)
			a.Reload()
			return
		}
		a.ActionErr = friendly(err)
		a.ActionWarn = false
	}
	fetch := func() {
		_, err := model.PressAttemptKeys(ctx, fleet, ref, strings.Join(choice.Keys, " "), guard.Revision, guard.Pressed, guard.Digest)
		cancel()
		if err != nil {
			if post == nil {
				failed(err)
			} else {
				post(func() { failed(err) })
			}
			return
		}
		if post == nil {
			applied()
		} else {
			post(applied)
		}
	}
	if post == nil {
		fetch()
		return
	}
	post(func() {})
	go fetch()
}

func sentKeyLabel(label string) string {
	if fields := strings.Fields(label); len(fields) > 0 {
		return fields[0]
	}
	return "key"
}

func (a *App) reopenNeed(n Need) {
	if a.needChosen == nil {
		a.needChosen = map[string]bool{}
	}
	a.needChosen[needKey(n)] = true
	a.needClosedTop = ""
}

func (a *App) needCountdown(c *ui.Context, p palette, need Need) {
	text, ok := denyText(need.DenyAt, a.now())
	if !ok {
		return
	}
	ui.Row(c).Height(kit.Badge).Padding(0, kit.Space6).Radius(kit.RadiusSM).
		Background(p.bg).Border(1, p.line).AlignItems(ui.Center).Label("Auto-deny countdown").Children(func() {
		ui.Text(c, text).Font("monospace").FontSize(kit.Meta.Size).Bold().TextColor(p.fail).FontFeatures("tnum")
	})
}

func denyText(at string, now time.Time) (string, bool) {
	deadline, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return "", false
	}
	left := deadline.Sub(now)
	if left <= 0 {
		return "denying…", true
	}
	secs := int((left + time.Second - 1) / time.Second)
	return fmt.Sprintf("%d:%02d", secs/60, secs%60), true
}

func (a *App) resetDeny() {
	a.denyGen++
	if a.denyTimer != nil {
		a.denyTimer()
		a.denyTimer = nil
	}
	a.denyWatch = ""
	a.denyFired = nil
}

func (a *App) watchDeny() {
	a.pruneDeny()
	next := a.nextDeny()
	if next == "" || (a.Clock == nil && a.Post == nil) {
		a.denyWatch = ""
		if a.denyTimer != nil {
			a.denyTimer()
			a.denyTimer = nil
		}
		return
	}
	if next == a.denyWatch && a.denyTimer != nil {
		return
	}
	a.denyWatch = next
	if a.denyTimer != nil {
		a.denyTimer()
		a.denyTimer = nil
	}
	a.denyGen++
	gen := a.denyGen
	a.denyTimer = a.clock().After(time.Second, func() {
		if a.Post != nil {
			a.Post(func() { a.denyTick(gen) })
			return
		}
		a.denyTick(gen)
	})
}

func (a *App) pruneDeny() {
	if len(a.denyFired) == 0 {
		return
	}
	live := make(map[string]string, len(a.Status.Needs.Items))
	for _, n := range a.Status.Needs.Items {
		if n.DenyAt != "" {
			live[n.Ref] = n.DenyAt
		}
	}
	for ref, at := range a.denyFired {
		if live[ref] != at {
			delete(a.denyFired, ref)
		}
	}
}

func (a *App) nextDeny() string {
	for _, n := range a.Status.Needs.Items {
		if n.DenyAt != "" && a.denyFired[n.Ref] != n.DenyAt {
			return n.DenyAt
		}
	}
	return ""
}

func (a *App) denyTick(gen int) {
	if gen != a.denyGen {
		return
	}
	a.denyTimer = nil
	a.denyWatch = ""
	if a.denyFired == nil {
		a.denyFired = map[string]string{}
	}
	now := a.now()
	ref, deadline := "", ""
	for _, n := range a.Status.Needs.Items {
		if n.DenyAt == "" || a.denyFired[n.Ref] == n.DenyAt {
			continue
		}
		at, err := time.Parse(time.RFC3339, n.DenyAt)
		if err != nil || at.After(now) {
			continue
		}
		ref, deadline = n.Ref, n.DenyAt
		break
	}
	if ref != "" {
		if a.Busy {
			a.watchDeny()
			return
		}
		a.denyFired[ref] = deadline
		a.receipt = ref + " denied · refreshing"
		a.receiptKey = "deny:" + ref
		a.Reload()
		return
	}
	a.watchDeny()
}

func (a *App) needReadError(c *ui.Context, p palette, key, msg string, retry func()) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, msg).FontSize(kit.Body.Size).TextColor(p.fail)
		if ui.Button(c.Key(key), "Retry").Clicked() {
			retry()
		}
	})
}

func (a *App) pickChoice(value string, options []Option) {
	a.AnswerChoice = value
	for _, o := range options {
		if o.Value == value {
			a.focusSend, a.focusNote = !o.Other, o.Other
			return
		}
	}
}

func (a *App) answerNeedsNote(choice, note string) bool {
	if strings.TrimSpace(note) != "" {
		return false
	}
	for _, o := range a.Decision.Options {
		if o.Value == choice {
			return o.Other
		}
	}
	return false
}

func (a *App) answerForm(c *ui.Context, k kit.Colors, p palette, d DecisionDetail) {
	if d.Decision.Status != "open" {
		a.answeredDecision(c, k, p, d)
		return
	}
	other := false
	for _, o := range d.Options {
		if a.AnswerChoice == o.Value && o.Other {
			other = true
		}
	}
	if len(d.Options) > 0 {
		options := make([]kit.Choice, 0, len(d.Options))
		for _, o := range d.Options {
			options = append(options, kit.Choice{
				Value: o.Value, Key: o.Key, Label: shownText(o.Label), Note: shownText(o.Note),
				Recommended: o.Recommended, Long: o.Long,
				Picked: a.AnswerChoice == o.Value, Disabled: a.Busy,
			})
		}
		if picked := kit.ChoiceList(c, k, options); picked != "" && !a.Busy {
			a.pickChoice(picked, d.Options)
		}
	}
	placeholder := "Your answer"
	if len(d.Options) > 0 {
		placeholder = "Add a note (optional)"
	}
	if other {
		placeholder = "Write your answer (required)"
	}
	note := ui.TextArea(c, &a.AnswerNote).Label("Note").Placeholder(placeholder).Height(64).
		Disabled(a.Busy)
	if a.focusNote {
		note.Focus()
		a.focusNote = false
	}
	if other && strings.TrimSpace(a.AnswerNote) == "" {
		ui.Text(c, noteRequiredText).FontSize(kit.Meta.Size).TextColor(p.fail)
	}
	a.sendAnswer(c, k)
}

func (a *App) sendAnswer(c *ui.Context, k kit.Colors) {
	label := "Send answer"
	if a.Busy {
		label = "Sending…"
	}
	b := ui.ButtonBase(c.Key("send-answer")).Height(kit.Control).Padding(0, kit.Space12).
		Radius(kit.RadiusMD).Background(k.Primary).Justify(ui.Center).AlignItems(ui.Center).
		Disabled(a.Busy).Cursor(ui.CursorPointer).Label(label)
	b.Children(func() {
		ui.Text(c, label).FontSize(kit.Body.Size).Bold().TextColor(k.OnPrimary).SingleLine()
	})
	if a.focusSend {
		b.Focus()
		a.focusSend = false
	}
	if b.Clicked() {
		a.Answer()
	}
}

var choiceDigits = [...]ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9}

func (a *App) choiceKeys(c *ui.Context, d DecisionDetail) {
	if a.Busy || d.Decision.Status != "open" || len(d.Options) == 0 {
		return
	}
	for i, key := range choiceDigits {
		if !c.Shortcut(0, key) {
			continue
		}
		want := fmt.Sprint(i + 1)
		for _, o := range d.Options {
			if o.Key == want {
				a.pickChoice(o.Value, d.Options)
				break
			}
		}
	}
	if a.AnswerChoice != "" && c.Shortcut(0, ui.KeyEnter) {
		a.Answer()
	}
}

func (a *App) choiceKeysFor(c *ui.Context, items []Need, open string) {
	if open == "" {
		return
	}
	for _, n := range items {
		if needKey(n) != open || n.Kind != "decision" {
			continue
		}
		if a.Decision.Decision.Ref == n.Decision {
			a.choiceKeys(c, a.Decision)
		}
		return
	}
}
