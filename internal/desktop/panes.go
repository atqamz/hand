package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	minChat     = 360
	minWork     = 440
	firstW      = 1100
	chatVW      = .38
	chatLow     = 400
	chatHigh    = 720
	blockedMax  = 180
	railOpenMin = kit.RailWidthOpen + minChat + minWork + 1
)

func chatWidth(pref, total, viewport float32) float32 {
	if pref <= 0 {
		pref = min(max(viewport*chatVW, chatLow), chatHigh)
	}
	return min(max(pref, minChat), max(minChat, total-minWork))
}

func (a *App) panes(c *ui.Context, k kit.Colors, p palette) {
	box := ui.Column(c).Grow(1).MinHeight(0)
	total, rail := box.Bounds().W, float32(kit.RailWidth)
	if a.RailOpen {
		rail = kit.RailWidthOpen
	}
	if total <= 0 {
		total = firstW - rail
	}
	if total != a.paneW {
		a.paneW = total
		c.Invalidate()
	}
	if a.RailOpen && total+rail < railOpenMin {
		a.RailOpen = false
		c.Invalidate()
	}
	work := total - chatWidth(a.Chat, total, total+rail)
	box.Children(func() {
		split := ui.Split(c, &work, func() { a.workPane(c, p) }, func() { a.chatPane(c, k, p) }).Grow(1)
		if split.Changed() {
			a.Chat = chatWidth(total-work, total, total+rail)
		}
	})
}

func (a *App) chatPane(c *ui.Context, k kit.Colors, p palette) {
	ui.Column(c).Fill().Background(k.Panel).Children(func() {
		a.paneHead(c, k, p)
		if sup := a.Status.Supervisor; sup != nil && sup.Blocked && a.known() {
			ui.Scroll(c).MaxHeight(blockedMax).Padding(12, 16, 8).Children(func() { a.blockedKeys(c, p, sup) })
		}
		ui.Scroll(c).Grow(1).Padding(16).Children(func() {
			ui.Column(c).Gap(8).Selectable().Children(func() {
				a.timelineError(c, p)
				a.timelineEntries(c, p)
				mark := ui.Box(c).Height(1)
				if tail := a.timelineTail(); tail != a.tail {
					a.tail = tail
					mark.ScrollIntoView()
				}
			})
		})
		ui.Column(c).Gap(6).Padding(0, 16, 16).Children(func() {
			a.composer(c, p)
			if a.Status.Supervisor == nil && a.Status.ComposerHint != "" && a.known() {
				ui.Text(c, a.Status.ComposerHint).FontSize(kit.Meta.Size).TextColor(p.neutral)
			}
		})
	})
}

func (a *App) timelineError(c *ui.Context, p palette) {
	fail := func(key, title, msg string, retry func()) {
		ui.Column(c).Gap(6).Padding(12).Radius(kit.RadiusLG).Background(p.failBG).Border(1, p.fail).Children(func() {
			ui.Text(c, title).FontSize(kit.Body.Size).Bold().TextColor(p.fail)
			ui.Text(c, msg).FontSize(kit.Meta.Size)
			if ui.Button(c.Key(key), "Retry").Clicked() {
				retry()
			}
		})
	}
	if a.TimelineErr != "" {
		fail("timeline:retry", "The conversation could not be read", a.TimelineErr, a.Retry)
	}
	if a.OlderErr != "" {
		fail("timeline:older-retry", "The earlier messages could not be read", a.OlderErr, a.Older)
	}
}

func (a *App) timelineTail() string {
	tail := ""
	if len(a.Timeline.Entries) > 0 {
		newest := a.Timeline.Entries[0]
		tail = fmt.Sprintf("%d/%s", newest.No, newest.At)
	}
	if a.Timeline.Working != nil {
		tail += "\x00working"
	}
	return tail
}

func (a *App) known() bool { return !a.Loading && !a.errStatus }

func (a *App) knownSupervisor() *Supervisor {
	if !a.known() {
		return nil
	}
	return a.Status.Supervisor
}

func (a *App) paneHead(c *ui.Context, k kit.Colors, p palette) {
	sup := a.Status.Supervisor
	ui.Row(c).MinHeight(kit.Sheet).Padding(kit.Space8, kit.Space16).Gap(kit.Space8).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Label("Supervisor").Children(func() {
		switch {
		case !a.known():
			ui.Spacer(c)
		case sup == nil:
			ui.Spacer(c)
			if headButton(c, "open-browser", "Open in browser", nil, look{k.LineStrong, k.FG, k.BG}, a.OpenBusy) {
				a.OpenInBrowser()
			}
		default:
			ui.Text(c, sup.Ref).Font("monospace").FontSize(kit.Meta.Size).Bold()
			ui.Text(c, modelLine(sup)).FontSize(kit.Body.Size).Bold().SingleLine().Ellipsis("…").Grow(1)
			a.controls(c, k, sup)
		}
		a.overflow(c, k, a.knownSupervisor())
	})
}

func modelLine(sup *Supervisor) string {
	line := sup.Model
	if line == "" {
		line = sup.Harness
	}
	if sup.Effort != "" {
		line += " · " + sup.Effort
	}
	return line
}

func (a *App) controls(c *ui.Context, k kit.Colors, sup *Supervisor) {
	button := func(action, label, busy string, icon *ui.SVG, l look) bool {
		if a.BusyAction == action {
			label = busy
		}
		return headButton(c, "control:"+action, label, icon, l, a.Busy)
	}
	plain := look{k.LineStrong, k.FG, k.BG}
	danger := look{k.Fail, k.Fail, k.BG}
	switch {
	case sup.Status == "running":
		if a.Status.AgentState == "working" && !sup.Blocked &&
			button("interrupt", "Interrupt", "Interrupting…", kit.Icon("interrupt"), danger) {
			a.Supervise("interrupt")
		}
		if sup.Blocked && button("force", "Force", "Forcing…", nil, plain) {
			a.Supervise("force")
		}
		stop, label := look{k.LineStrong, k.Muted, k.BG}, "Stop"
		if a.StopArmed && a.stopRef == sup.Ref {
			stop, label = look{k.Fail, k.Fail, k.FailBG}, "Press again to stop"
		}
		if button("stop", label, "Stopping…", nil, stop) {
			a.PressStop()
		}
	case sup.Live:
		ui.Text(c, "starting…").FontSize(kit.Body.Size).TextColor(k.Muted)
	case sup.Resumable:
		if button("resume", "Resume", "Resuming…", nil, look{ui.Transparent, k.OnPrimary, k.Primary}) {
			a.Supervise("resume")
		}
	default:
		if button("start", "Start new, same as "+sup.Ref, "Starting…", nil, plain) {
			a.Supervise("start")
		}
	}
}

func (a *App) blockedKeys(c *ui.Context, p palette, sup *Supervisor) {
	if sup.Ask != nil {
		a.supervisorAsk(c, p, sup)
		return
	}
	ui.Row(c).Gap(8).Wrap().Children(func() {
		ui.Text(c, "Keys").FontSize(kit.Meta.Size).Bold().TextColor(p.neutral).Padding(0, 8, 0, 0)
		for _, key := range supervisorControlKeys {
			label := key
			if l, ok := supervisorKeyLabels[key]; ok {
				label = l
			}
			if a.BusyAction == "key:"+key {
				label = "Sending…"
			}
			if ui.Button(c.Key("key:"+key), label).Disabled(a.Busy || sup.Keys == nil).Clicked() {
				a.Press(key)
			}
		}
	})
}

func (a *App) overflow(c *ui.Context, k kit.Colors, sup *Supervisor) {
	more := ui.ButtonBase(c.Key("more")).Size(kit.Control, kit.Control).Radius(kit.RadiusMD).
		Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label("More")
	more.Children(func() { ui.Icon(c, kit.Icon("more")).TextColor(k.Muted) })
	more.PopoverMenuButton(func(m *ui.Menu) {
		if sup != nil && sup.Resumable && !sup.Live && sup.Status != "running" && m.Item("New session").Disabled(a.Busy).Chosen() {
			a.Supervise("start")
		}
		if m.Item("Refresh now").Chosen() {
			a.Refresh()
		}
		if m.Item("Open in browser").Disabled(a.OpenBusy).Chosen() {
			a.OpenInBrowser()
		}
	})
}

type look struct{ edge, fg, bg ui.Color }

func headButton(c *ui.Context, key, label string, icon *ui.SVG, l look, disabled bool) bool {
	el := ui.ButtonBase(c.Key(key)).Height(kit.Control).Padding(0, kit.Space10).Gap(kit.Space6).
		Radius(kit.RadiusMD).Border(1, l.edge).Background(l.bg).Justify(ui.Center).AlignItems(ui.Center).
		Disabled(disabled).Cursor(ui.CursorPointer).Label(label)
	el.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).TextColor(l.fg)
		}
		ui.Text(c, label).FontSize(kit.Body.Size).TextColor(l.fg).SingleLine()
	})
	return el.Clicked()
}

func (a *App) toggleRail() {
	a.RailOpen = !a.RailOpen && a.paneW+kit.RailWidth >= railOpenMin
}
