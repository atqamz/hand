package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	composerMax  = 760
	draftNoteTop = -9
	draftNoteX   = 14
)

func composerMargin(pane float32) float32 {
	return max(float32(kit.Space16), (pane-composerMax)/2)
}

func (a *App) composer(c *ui.Context, k kit.Colors, p palette, pane float32) {
	m := composerMargin(pane)
	box := ui.Column(c).Key("composer").Label("Composer").MaxWidth(composerMax).
		Margin(0, m, kit.Space16).Padding(kit.Space4, kit.Space4, kit.Space8).
		Radius(kit.RadiusLG).Background(k.BG).Border(1, k.LineStrong)
	box.Children(func() {
		a.latest(c, k)
		_, viewport := c.Size()
		message := ui.TextAreaBase(c, &a.Draft).Label("Message the supervisor").Placeholder("Message the supervisor").
			Lines(1, composerLines(viewport)).FixedLineHeight(kit.Body.Line).
			MinHeight(kit.Tap).Padding(kit.Space8, kit.Space10).Disabled(a.Busy).
			TextColor(k.FG).PlaceholderColor(k.Muted).
			OnChange(func() { a.saveDraft() }).
			OnTextKey(func(key ui.TextKey) bool {
				if key.Key != ui.KeyEnter || (key.Mods != ui.Ctrl && key.Mods != ui.Super) {
					return false
				}
				a.Draft = key.Text()
				a.saveDraft()
				a.Send()
				return true
			})
		if a.focusComposer {
			message.Focus()
			a.focusComposer = false
		}
		if a.DraftErr != "" {
			ui.Text(c, a.DraftErr).FontSize(kit.Meta.Size).TextColor(p.fail).Padding(0, kit.Space10)
		}
		if a.SendErr != "" {
			ui.Column(c).Gap(kit.Space2).Padding(0, kit.Space10, kit.Space4).Children(func() {
				ui.Text(c, "The action did not go through").FontSize(kit.Meta.Size).Bold().TextColor(p.fail)
				ui.Text(c, a.SendErr).FontSize(kit.Meta.Size).TextColor(p.fail)
			})
		}
		ui.Row(c).Gap(kit.Space12).AlignItems(ui.Center).Padding(0, kit.Space6, 0, kit.Space10).Children(func() {
			a.composerHint(c, k, p)
			ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
				if key := a.sendKeyHint(pane); key != "" {
					kit.Keycap(c, k, key)
				}
				a.sendButton(c, k)
			})
		})
		if note := a.draftNote(); note != "" {
			ui.Text(c, note).Absolute().Top(draftNoteTop).Right(draftNoteX).
				Padding(0, kit.Space6).Background(k.BG).FontSize(kit.Meta.Size).TextColor(k.Muted).SingleLine()
		}
	})
	if box.FocusWithin() {
		box.BorderColor(k.Accent).Shadow(0, 0, 0, 1, k.Accent)
	}
}

func composerLines(viewport float32) int {
	if viewport <= 0 {
		viewport = 760
	}
	lines := int((viewport*.4 - 2*kit.Space8) / kit.Body.Line)
	return max(lines, 1)
}

func (a *App) composerHint(c *ui.Context, k kit.Colors, p palette) {
	hint := a.Status.ComposerHint
	if hint == "" {
		ui.Spacer(c).Grow(1)
		return
	}
	if a.blockedHint() {
		link := ui.ButtonBase(c.Key("composer:blocked")).Grow(1).MinWidth(0).Cursor(ui.CursorPointer).Label(hint)
		link.Children(func() {
			ui.Text(c, hint).FontSize(kit.Meta.Size).LineHeight(1.5).TextColor(k.Accent).
				MinWidth(0).Grow(1).Wrap()
		})
		if link.Clicked() {
			a.openBlockedNeed()
		}
		return
	}
	ui.Text(c, hint).FontSize(kit.Meta.Size).LineHeight(1.5).TextColor(p.neutral).
		MinWidth(0).Grow(1).Wrap()
}

func (a *App) blockedHint() bool {
	sup := a.Status.Supervisor
	return a.known() && sup != nil && sup.Blocked
}

func (a *App) openBlockedNeed() {
	if a.Screen != BoardScreen {
		a.Show(BoardScreen)
	}
	items := a.visibleNeeds()
	if len(items) > 0 {
		a.needTop = needKey(items[0])
	}
	for _, n := range items {
		if n.Kind == "blocked" && a.needOpens(n) {
			a.needChosen = map[string]bool{needKey(n): true}
			a.needClosedTop = ""
			return
		}
	}
}

func (a *App) sendKeyHint(pane float32) string {
	if pane < 520 {
		return ""
	}
	if a.Apple {
		return "⌘ Enter"
	}
	return "Ctrl Enter"
}

func (a *App) draftNote() string {
	if a.Draft == "" {
		return ""
	}
	if a.draftRestored {
		return "Draft restored"
	}
	if a.draftAt.IsZero() {
		return ""
	}
	return "Draft saved " + a.draftAt.Format("15:04")
}

func (a *App) startFormInHead() bool {
	return a.known() && headShowsResume(a.Status.Supervisor)
}

func (a *App) sendStyle(k kit.Colors) (bg, fg, edge ui.Color) {
	if a.startFormInHead() {
		return k.BG, k.FG, k.LineStrong
	}
	return k.Primary, k.OnPrimary, ui.Transparent
}

func (a *App) sendButton(c *ui.Context, k kit.Colors) {
	label := "Send"
	if a.Busy {
		label = "Sending…"
	}
	bg, fg, edge := a.sendStyle(k)
	disabled := a.Busy || strings.TrimSpace(a.Draft) == ""
	b := ui.ButtonBase(c.Key("composer:send")).Height(kit.Control).Padding(0, kit.Space12).
		Radius(kit.RadiusMD).Background(bg).Border(1, edge).Justify(ui.Center).AlignItems(ui.Center).
		Disabled(disabled).Cursor(ui.CursorPointer).Label(label)
	if disabled {
		b.Opacity(.55)
	}
	if a.Busy {
		b.Opacity(.6)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(kit.Body.Size).TextColor(fg).Bold().SingleLine()
	})
	if b.Clicked() {
		a.Send()
	}
}
