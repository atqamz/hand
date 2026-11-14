package desktop

import (
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/atqamz/hand/internal/desktop/kit"
)

type palette struct {
	bg                                          ui.Color
	line, lineStrong, panel, card, hover        ui.Color
	fail, wait, pass, neutral, accent           ui.Color
	failBG, waitBG, passBG, neutralBG, accentBG ui.Color
	screen, screenFG                            ui.Color
}

func paletteFor(dark bool) palette {
	k := kit.ColorsFor(dark)
	return palette{
		bg:   k.BG,
		line: k.Line, lineStrong: k.LineStrong, panel: k.Panel, card: k.Card, hover: k.Hover,
		fail: k.Fail, wait: k.Wait, pass: k.Pass, neutral: k.Neutral, accent: k.Accent,
		failBG: k.FailBG, waitBG: k.WaitBG, passBG: k.PassBG, neutralBG: k.NeutralBG, accentBG: k.AccentBG,
		screen: k.Screen, screenFG: k.ScreenFG,
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
	c.SetZoom(a.drawnZoom(c))
	a.zoomKeys(c)
	dark := c.Theme().Dark
	p, k := paletteFor(dark), kit.ColorsFor(dark)
	c.SetTheme(boardTheme(dark))
	if a.receipt != "" {
		c.AddToast(ui.Toast{ID: a.receiptKey, Title: a.receipt, Timeout: receiptFor})
		a.receipt, a.receiptKey = "", ""
	}
	if a.notice != "" {
		timeout := receiptFor
		kind := ""
		if a.noticeError {
			timeout, kind = errorFor, "error"
		}
		c.AddToast(ui.Toast{ID: a.noticeKey, Title: a.noticeTitle, Description: a.notice,
			Type: kind, Timeout: timeout})
		a.notice, a.noticeTitle, a.noticeKey, a.noticeError = "", "", "", false
	}
	ui.Row(c).Fill().Children(func() {
		a.rail(c, k, p)
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
			a.masthead(c, k, p)
			a.body(c, k, p)
		})
	})
	if a.viewerOpen() {
		a.viewer(c, k, p)
	}
	a.toasts(c, k, p)
}

func (a *App) toasts(c *ui.Context, k kit.Colors, p palette) {
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(kit.Space16).Gap(kit.Space8).AlignItems(ui.End)
		for _, t := range toasts {
			parts := ui.ToastBase(c, t)
			border := p.line
			if t.Type == "error" {
				border = p.fail
			}
			parts.Root.Column().Gap(kit.Space2).MaxWidth(28*kit.Body.Size).
				Padding(kit.Space10, kit.Space14).Radius(kit.RadiusLG).
				Background(p.bg).Border(1, border).
				Shadow(0, 8, 24, 0, k.Shadow).Children(func() {
				ui.Text(c, t.Title).FontSize(kit.Body.Size).Bold().TextColor(k.FG)
				if t.Description != "" {
					ui.Text(c, t.Description).FontSize(kit.Body.Size).TextColor(k.Muted)
				}
			})
		}
	})
}

func (a *App) viewerOpen() bool {
	return a.Model != nil && a.Fleet.ID != "" && a.Screen != BoardScreen
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
		if item(kit.NavItem{Key: "screen:needs", Label: "Board", Icon: "board", Current: true,
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
	if a.Model == nil || a.Fleet.ID == "" || a.auth || a.Starting || a.StartErr != "" {
		ui.Scroll(c).Grow(1).Padding(20, 20, 32).Children(func() {
			ui.Column(c).Gap(16).Children(func() {
				a.openCard(c, p)
				switch {
				case a.Starting || a.StartErr != "" || a.auth || a.noFleets:
					a.stateCard(c, k, p)
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
	page := ui.Scroll(c).Key("board-scroll").Grow(1).Padding(20, 20, 32).TrackScroll(&a.boardScroll).
		Focusable().FocusRing(false).Label("The board")
	if a.boardFocus {
		a.boardFocus = false
		page.Focus()
	}
	page.Children(func() {
		ui.Column(c).Gap(16).Selectable().Children(func() {
			a.openCard(c, p)
			switch {
			case a.Err != "" && !a.errScreen:
				a.errorCard(c, p)
			case a.Loading && a.Status.Pill == "" && len(a.Tasks.Groups) == 0:
				a.emptyCard(c, p, "Reading the board", "The window waits for the board to answer.")
			default:
				a.needsScreen(c, k, p)
				a.tasksScreen(c, p)
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
	if a.OpenBusy {
		ui.Column(c).Gap(8).Padding(14).Radius(10).Background(p.card).
			Border(1, p.line).Children(func() {
			ui.Text(c, "Opening in the browser…").FontSize(kit.Title.Size).Bold()
			ui.Text(c, "`hand open` is starting this page.").FontSize(kit.Meta.Size).
				TextColor(p.neutral)
		})
		return
	}
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
			a.keyRow(c, p, askKeyChoices(sup.Ask), sup.Keys == nil, func(choice keyChoice) {
				a.Press(choice.Label, choice.Keys...)
			})
		})
	})
}

var supervisorKeyLabels = map[string]string{"enter": "Enter", "esc": "Esc", "up": "↑", "down": "↓"}

func more(c *ui.Context, p palette, text string) {
	ui.Text(c, text).FontSize(kit.Meta.Size).TextColor(p.neutral)
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
		return shownText(at)
	}
	return t.Local().Format("Jan 2, 15:04")
}

func (a *App) markdown(c *ui.Context, src string) {
	a.markdownAt(c, src, nil)
}

// markdownCopy renders a plan or a report body in the Board's Copy type:
// 15px on a 24px line, filling its card.
func (a *App) markdownCopy(c *ui.Context, src string) {
	a.markdownAt(c, src, &markdown.Theme{
		FontSize:   kit.Message.Size,
		LineHeight: kit.Message.Line / kit.Message.Size,
	})
}

func (a *App) markdownAt(c *ui.Context, src string, theme *markdown.Theme) {
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
	a.md.Theme = theme
	markdown.View(c, src, &a.md)
	a.md.Theme = nil
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
