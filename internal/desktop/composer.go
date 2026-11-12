package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const composerMax = 760

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
		ui.TextAreaBase(c, &a.Draft).Label("Message the supervisor").Placeholder("Message the supervisor").
			Lines(2, 8).MinHeight(kit.Tap).Padding(kit.Space8, kit.Space10).Disabled(a.Busy).
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
		if a.DraftErr != "" {
			ui.Text(c, a.DraftErr).FontSize(kit.Meta.Size).TextColor(p.fail).Padding(0, kit.Space10)
		}
		ui.Row(c).Gap(kit.Space12).AlignItems(ui.Center).Padding(0, kit.Space6, 0, kit.Space10).Children(func() {
			ui.Text(c, a.Status.ComposerHint).FontSize(kit.Meta.Size).TextColor(k.Muted).
				MinWidth(0).Grow(1).SingleLine().Ellipsis("…")
			kit.Keycap(c, k, "Ctrl Enter")
			a.sendButton(c, k)
		})
		if !a.draftAt.IsZero() {
			ui.Text(c, "Draft saved "+a.draftAt.Format("15:04")).Absolute().Top(-9).Right(14).
				Padding(0, kit.Space6).Background(k.BG).FontSize(kit.Meta.Size).TextColor(k.Muted).SingleLine()
		}
	})
	if box.FocusWithin() {
		box.BorderColor(k.Accent).Shadow(0, 0, 0, 1, k.Accent)
	}
}

func (a *App) sendButton(c *ui.Context, k kit.Colors) {
	label := "Send"
	if a.Busy {
		label = "Sending…"
	}
	b := ui.ButtonBase(c.Key("composer:send")).Height(kit.Control).Padding(0, kit.Space12).
		Radius(kit.RadiusMD).Background(k.Primary).Justify(ui.Center).AlignItems(ui.Center).
		Disabled(a.Busy || strings.TrimSpace(a.Draft) == "").Cursor(ui.CursorPointer).Label(label)
	b.Children(func() {
		ui.Text(c, label).FontSize(kit.Body.Size).Bold().TextColor(k.OnPrimary).SingleLine()
	})
	if b.Clicked() {
		a.Send()
	}
}
