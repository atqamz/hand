package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

type chord struct {
	Mods ui.Modifiers
	Key  ui.Key
}

type shortcut struct {
	Group  string
	Does   string
	Chords []chord
	Text   string
}

const (
	groupAnywhere = "Anywhere"
	groupDoc      = "On a document page"
	groupFind     = "In the find box"
	groupDecision = "On an open decision"
	groupMessage  = "In the message box"
	groupHelp     = "In this list"
	groupRail     = "Pages and fleets"
)

var (
	keyHelp      = shortcut{Group: groupAnywhere, Does: "Show this list", Chords: []chord{{ui.Shift, ui.KeySlash}}}
	keyZoomIn    = shortcut{Group: groupAnywhere, Does: "Zoom in", Chords: []chord{{ui.Ctrl, ui.KeyEqual}, {ui.Ctrl | ui.Shift, ui.KeyEqual}}}
	keyZoomOut   = shortcut{Group: groupAnywhere, Does: "Zoom out", Chords: []chord{{ui.Ctrl, ui.KeyMinus}}}
	keyZoomReset = shortcut{Group: groupAnywhere, Does: "Reset the zoom", Chords: []chord{{ui.Ctrl, ui.Key0}}}

	keyClosePage = shortcut{Group: groupDoc, Does: "Close the page", Chords: []chord{{0, ui.KeyEscape}}}
	keyFind      = shortcut{Group: groupDoc, Does: "Find in the document", Chords: []chord{{ui.Cmd, ui.KeyF}, {0, ui.KeySlash}}}
	keyPrevDoc   = shortcut{Group: groupDoc, Does: "Open the previous document in this folder", Chords: []chord{{0, ui.KeyBracketLeft}}}
	keyNextDoc   = shortcut{Group: groupDoc, Does: "Open the next document in this folder", Chords: []chord{{0, ui.KeyBracketRight}}}

	keyFindNext  = shortcut{Group: groupFind, Does: "Next match", Chords: []chord{{0, ui.KeyEnter}}}
	keyFindPrev  = shortcut{Group: groupFind, Does: "Previous match", Chords: []chord{{ui.Shift, ui.KeyEnter}}}
	keyFindClear = shortcut{Group: groupFind, Does: "Clear the search; with nothing typed, close the page", Chords: []chord{{0, ui.KeyEscape}}}

	keyChoose = shortcut{Group: groupDecision, Does: "Pick the option with that key", Text: "1–9, A–Z"}
	keyAnswer = shortcut{Group: groupDecision, Does: "Send the picked answer", Chords: []chord{{0, ui.KeyEnter}}}

	keySend = shortcut{Group: groupMessage, Does: "Send the message", Chords: []chord{{ui.Ctrl, ui.KeyEnter}, {ui.Super, ui.KeyEnter}}}

	keyPageBoard     = shortcut{Group: groupRail, Does: "Open the Board", Chords: []chord{{ui.Alt, ui.Key1}}}
	keyPageTasks     = shortcut{Group: groupRail, Does: "Open Tasks", Chords: []chord{{ui.Alt, ui.Key2}}}
	keyPageSkills    = shortcut{Group: groupRail, Does: "Open Skills", Chords: []chord{{ui.Alt, ui.Key3}}}
	keyPageAlerts    = shortcut{Group: groupRail, Does: "Open Alerts", Chords: []chord{{ui.Alt, ui.Key4}}}
	keyPageChat      = shortcut{Group: groupRail, Does: "Open the Conversation", Chords: []chord{{ui.Alt, ui.Key5}}}
	keyPageCompanion = shortcut{Group: groupRail, Does: "Open the Companion page", Chords: []chord{{ui.Alt, ui.Key6}}}
	keyRailToggle    = shortcut{Group: groupRail, Does: "Show or hide the rail labels", Chords: []chord{{ui.Alt, ui.KeyB}}}

	keyHelpClose = shortcut{Group: groupHelp, Does: "Close this list", Chords: []chord{{ui.Shift, ui.KeySlash}, {0, ui.KeyEscape}}}
)

var shortcuts = []shortcut{
	keyHelp, keyZoomIn, keyZoomOut, keyZoomReset,
	keyClosePage, keyFind, keyPrevDoc, keyNextDoc,
	keyFindNext, keyFindPrev, keyFindClear,
	keyChoose, keyAnswer,
	keySend,
	keyPageBoard, keyPageTasks, keyPageSkills, keyPageAlerts, keyPageChat, keyPageCompanion, keyRailToggle,
	keyHelpClose,
}

func (s shortcut) is(mods ui.Modifiers, key ui.Key) bool {
	for _, ch := range s.Chords {
		if ch.Mods == mods && ch.Key == key {
			return true
		}
	}
	return false
}

func (s shortcut) pressed(c *ui.Context) bool {
	for _, ch := range s.Chords {
		if c.Shortcut(ch.Mods, ch.Key) {
			return true
		}
	}
	return false
}

func (s shortcut) on(e ui.Element, fn func()) ui.Element {
	for _, ch := range s.Chords {
		e = e.OnShortcut(ch.Mods, ch.Key, fn)
	}
	return e
}

var keyNames = map[ui.Key]string{
	ui.KeyEscape: "Esc", ui.KeyEnter: "Enter", ui.KeySlash: "/", ui.KeyEqual: "=", ui.KeyMinus: "-",
	ui.Key0: "0", ui.Key1: "1", ui.Key2: "2", ui.Key3: "3", ui.Key4: "4", ui.Key5: "5", ui.Key6: "6", ui.KeyB: "B", ui.KeyF: "F", ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]",
}

var shiftedNames = map[ui.Key]string{ui.KeySlash: "?", ui.KeyEqual: "+"}

func (ch chord) parts() []string {
	mods, name := ch.Mods, keyNames[ch.Key]
	if shifted, ok := shiftedNames[ch.Key]; ok && mods&ui.Shift != 0 {
		mods, name = mods&^ui.Shift, shifted
	}
	var out []string
	if mods&ui.Ctrl != 0 {
		out = append(out, "Ctrl")
	}
	if mods&ui.Alt != 0 {
		out = append(out, "Alt")
	}
	if mods&ui.Shift != 0 {
		out = append(out, "Shift")
	}
	if mods&ui.Super != 0 {
		out = append(out, "⌘")
	}
	return append(out, name)
}

func (s shortcut) has(ch chord) bool {
	for _, o := range s.Chords {
		if o == ch {
			return true
		}
	}
	return false
}

func (s shortcut) shown(apple bool) []chord {
	var out []chord
	for _, ch := range s.Chords {
		switch {
		case ch.Mods&ui.Super != 0 && s.has(chord{ch.Mods&^ui.Super | ui.Ctrl, ch.Key}):
			if apple {
				out = append(out, ch)
			}
		case ch.Mods&ui.Ctrl != 0 && s.has(chord{ch.Mods&^ui.Ctrl | ui.Super, ch.Key}):
			if !apple {
				out = append(out, ch)
			}
		default:
			out = append(out, ch)
		}
	}
	return out
}

func (s shortcut) caps(apple bool) [][]string {
	if s.Text != "" {
		return [][]string{{s.Text}}
	}
	var out [][]string
	for _, ch := range s.shown(apple) {
		out = append(out, ch.parts())
	}
	return out
}

func (s shortcut) label(apple bool) string {
	var words []string
	for _, caps := range s.caps(apple) {
		words = append(words, strings.Join(caps, " "))
	}
	return strings.Join(words, " or ")
}

func (a *App) helpKeys(c *ui.Context) {
	if !a.keysOpen && keyHelp.pressed(c) {
		a.keysOpen = true
	}
}

func (a *App) keysSheet(c *ui.Context, k kit.Colors) {
	if !a.keysOpen {
		return
	}
	el := ui.DialogBase(c, &a.keysOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(k.Backdrop)
		w, h := c.Size()
		panel.Width(min(keysWidth, w-2*kit.Space16)).MaxHeight(h-2*kit.Space16).Background(k.BG).
			Border(1, k.Line).Radius(kit.RadiusLG).Shadow(0, 8, 24, 0, k.Shadow).Label("Keyboard shortcuts")
		panel.Children(func() {
			if keyHelpClose.pressed(c) {
				a.keysOpen = false
			}
			ui.Scroll(c).Grow(1).Padding(kit.Space16).Children(func() {
				ui.Column(c).Gap(kit.Space12).Children(func() {
					ui.Text(c, "Keyboard shortcuts").FontSize(kit.Heading.Size).Bold().TextColor(k.FG)
					group := ""
					for _, s := range shortcuts {
						if s.Group != group {
							group = s.Group
							kit.FieldLabel(c, k, group)
						}
						a.shortcutRow(c, k, s)
					}
				})
			})
		})
	})
	if el.Dismissed() {
		a.keysOpen = false
	}
}

const keysWidth = 520

func (a *App) shortcutRow(c *ui.Context, k kit.Colors, s shortcut) {
	ui.Row(c).Gap(kit.Space12).AlignItems(ui.Center).Label(s.Does + ": " + s.label(a.Apple)).Children(func() {
		ui.Row(c).Gap(kit.Space6).AlignItems(ui.Center).Width(keyColumn).Shrink(0).Children(func() {
			for i, caps := range s.caps(a.Apple) {
				if i > 0 {
					ui.Text(c, "or").FontSize(kit.Meta.Size).TextColor(k.Muted)
				}
				for _, part := range caps {
					ui.Box(c).Padding(kit.Space2, kit.Space6).Radius(kit.RadiusSM).Border(1, k.LineStrong).
						Background(k.Panel).Children(func() {
						ui.Text(c, part).FontSize(kit.Meta.Size).Bold().TextColor(k.FG).SingleLine()
					})
				}
			}
		})
		ui.Text(c, s.Does).FontSize(kit.Body.Size).TextColor(k.FG).Grow(1).MinWidth(0)
	})
}

const keyColumn = 190
