package desktop

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const awayBy = 48

type row struct {
	Entry Entry
	Day   string
	Dated bool
}

func parseAt(at string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, at)
	return t.Local(), err == nil
}

func clockOf(at string, dated bool) string {
	t, ok := parseAt(at)
	switch {
	case !ok:
		return at
	case dated:
		return t.Format("Jan 2, 15:04")
	}
	return t.Format("15:04")
}

func timelineRows(newest []Entry) []row {
	rows := make([]row, 0, len(newest)+2)
	prev, split := "", false
	for i := len(newest) - 1; i >= 0; i-- {
		e := newest[i]
		if t, ok := parseAt(e.At); ok {
			day := t.Format(time.DateOnly)
			if prev != "" && day != prev {
				rows = append(rows, row{Day: t.Format("Mon, Jan 2")})
				split = true
			}
			prev = day
		}
		rows = append(rows, row{Entry: e, Dated: !split})
	}
	return rows
}

func elapsed(since string, now time.Time) string {
	t, ok := parseAt(since)
	if !ok {
		return ""
	}
	m := int(max(now.Sub(t), 0) / time.Minute)
	switch {
	case m < 1:
		return "<1m"
	case m < 60:
		return strconv.Itoa(m) + "m"
	}
	return strconv.Itoa(m/60) + "h " + strconv.Itoa(m%60) + "m"
}

func workingText(w *Working, now time.Time) string {
	parts := []string{elapsed(w.Since, now)}
	if w.Last != "" {
		parts = append(parts, "last output "+elapsed(w.Last, now)+" ago")
	}
	if from, ok := parseAt(w.From); ok && w.Input != 0 {
		parts = append(parts, "on your "+from.Format("15:04")+" message")
	} else {
		parts = append(parts, w.On)
	}
	return strings.Join(parts, " · ")
}

func wakeSpan(e Entry) string {
	return clockOf(e.Wakes[0].At, false) + "–" + clockOf(e.At, false)
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *App) timelineEntries(c *ui.Context, p palette) {
	if a.Timeline.Live {
		ui.Row(c).Children(func() {
			ui.Spacer(c)
			ui.Badge(c, "live").Background(p.passBG).TextColor(p.pass)
		})
	}
	if a.Timeline.Older > 0 {
		if ui.Button(c.Key("timeline:older"), "Older messages").Clicked() {
			a.Older()
		}
	}
	if len(a.Timeline.Entries) == 0 {
		note := a.Timeline.Note
		if note == "" {
			note = "Nothing yet: the conversation appears once the supervisor speaks."
		}
		a.emptyCard(c, p, "No messages", note)
		a.working(c, p)
		return
	}
	quiet := false
	for _, r := range timelineRows(a.Timeline.Entries) {
		line := r.Day == "" && (r.Entry.Role == "hand" || r.Entry.Role == "wakes")
		gap := float32(kit.Space14)
		if line && quiet {
			gap = kit.Space6
		}
		quiet = line
		ui.Column(c).Margin(gap, 0, 0).Children(func() { a.timelineRow(c, p, r) })
	}
	a.working(c, p)
}

func (a *App) timelineRow(c *ui.Context, p palette, r row) {
	e := r.Entry
	switch {
	case r.Day != "":
		a.dayDivider(c, p, r.Day)
	case e.Role == "hand":
		a.handLine(c, p, e.Text, e.At, r.Dated)
	case e.Role == "wakes" && len(e.Wakes) > 0:
		a.wakes(c, p, e, r.Dated)
	case e.Role == "supervisor":
		ui.Row(c).Children(func() {
			a.bubble(c, p, r, false)
			ui.Spacer(c)
		})
	default:
		ui.Row(c).Children(func() {
			ui.Spacer(c)
			a.bubble(c, p, r, true)
		})
	}
}

func (a *App) dayDivider(c *ui.Context, p palette, day string) {
	rule := func() { ui.Box(c).Grow(1).Height(1).Background(p.line) }
	ui.Row(c).Gap(kit.Space12).Padding(kit.Space6, 0).AlignItems(ui.Center).Children(func() {
		rule()
		ui.Row(c).Height(kit.Badge).Padding(0, kit.Space8).Radius(kit.RadiusPill).Border(1, p.line).AlignItems(ui.Center).Children(func() {
			ui.Text(c, day).FontSize(kit.Meta.Size).Bold().TextColor(p.neutral).SingleLine()
		})
		rule()
	})
}

func (a *App) ring(c *ui.Context, p palette) {
	ui.Box(c).Size(8, 8).Margin(5, 0, 0).Radius(kit.RadiusPill).Border(1.5, p.lineStrong)
}

func (a *App) handLine(c *ui.Context, p palette, text, at string, dated bool) {
	ui.Row(c).Gap(kit.Space8).AlignItems(ui.Start).Children(func() {
		a.ring(c, p)
		ui.Text(c, text).FontSize(kit.Meta.Size).LineHeight(1.5).TextColor(p.neutral).MaxLines(1).MinWidth(0)
		ui.Text(c, clockOf(at, dated)).FontSize(kit.Meta.Size).LineHeight(1.5).TextColor(p.neutral).SingleLine()
	})
}

func (a *App) wakes(c *ui.Context, p palette, e Entry, dated bool) {
	key := fmt.Sprintf("%s/%d/%s", a.Fleet.ID, e.No, e.At)
	open := a.wakesOpen[key]
	head := ui.ButtonBase(c.Key("wakes:" + key)).Height(kit.Meta.Line).Gap(kit.Space8).Justify(ui.Start).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(fmt.Sprintf("%d wakes", len(e.Wakes)))
	head.Children(func() {
		fg := p.neutral
		if head.Hovered() {
			fg = c.Theme().Text
		}
		a.ring(c, p)
		ui.Text(c, fmt.Sprintf("%d wakes", len(e.Wakes))).FontSize(kit.Meta.Size).TextColor(fg).SingleLine()
		ui.Text(c, wakeSpan(e)).FontSize(kit.Meta.Size).TextColor(fg).SingleLine()
	})
	if head.Clicked() {
		if a.wakesOpen == nil {
			a.wakesOpen = map[string]bool{}
		}
		a.wakesOpen[key] = !open
	}
	if open {
		ui.Column(c).Gap(kit.Space6).Margin(kit.Space6, 0, 0, kit.Space16).Children(func() {
			for _, w := range e.Wakes {
				a.handLine(c, p, w.Text, w.At, dated)
			}
		})
	}
}

func (a *App) bubble(c *ui.Context, p palette, r row, you bool) {
	e := r.Entry
	who, tone, bg := roleName(e), p.neutral, p.card
	box := ui.Column(c).Gap(kit.Space4).Label("Message from " + who)
	if you {
		tone, bg = p.accent, p.accentBG
		box.Padding(kit.Space10, kit.Space14, kit.Space12).Radius(kit.RadiusLG, kit.RadiusLG, kit.RadiusSM, kit.RadiusLG).
			Background(bg).MaxWidthPercent(88).MaxWidth(560)
	} else {
		box.Padding(9, 13, 11).Radius(kit.RadiusLG).Background(bg).Border(1, p.lineStrong).MaxWidth(kit.Measure + 30)
	}
	box.Children(func() {
		ui.Row(c).Gap(kit.Space8).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, who).FontSize(kit.Meta.Size).Bold().TextColor(tone).SingleLine()
			if e.Model != "" {
				ui.Text(c, e.Model).FontSize(kit.Meta.Size).TextColor(p.neutral).SingleLine()
			}
			ui.Text(c, clockOf(e.At, r.Dated)).FontSize(kit.Meta.Size).TextColor(p.neutral).SingleLine()
			ui.Spacer(c)
			switch {
			case e.Queued || e.Delivery == "queued":
				ui.Text(c, "queued").FontSize(kit.Meta.Size).TextColor(p.neutral).SingleLine()
			case e.Delivery != "":
				ui.Text(c, strings.TrimSpace(e.Delivery+" "+clockOf(e.Delivered, false))).FontSize(kit.Meta.Size).TextColor(p.neutral).SingleLine()
			}
		})
		switch {
		case e.Text == "":
		case you:
			ui.Text(c, e.Text).FontSize(kit.Message.Size).LineHeight(kit.Message.Line / kit.Message.Size)
		default:
			a.md.Theme = &markdown.Theme{FontSize: kit.Message.Size, LineHeight: kit.Message.Line / kit.Message.Size}
			a.markdown(c, e.Text)
			a.md.Theme = nil
		}
	})
}

func (a *App) working(c *ui.Context, p palette) {
	w := a.Timeline.Working
	if w == nil {
		return
	}
	ui.Row(c).Gap(kit.Space8).Padding(kit.Space10, 0).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Size(8, 8).Margin(5, 0, 0).Radius(kit.RadiusPill).Background(p.accent)
		ui.Text(c, w.Ref+" working").FontSize(kit.Meta.Size).LineHeight(1.5).Bold()
		ui.Text(c, "· "+workingText(w, a.now())).FontSize(kit.Meta.Size).LineHeight(1.5).TextColor(p.neutral).MinWidth(0)
	})
}

func (a *App) away() bool { return a.scroll.MaxY-a.scroll.Y > awayBy }

func (a *App) latest(c *ui.Context, k kit.Colors) {
	if !a.away() {
		return
	}
	b := ui.ButtonBase(c.Key("timeline:latest")).Absolute().Top(-kit.Control-13).Right(15).Size(kit.Control, kit.Control).
		Radius(kit.RadiusPill).Border(1, k.LineStrong).Background(k.Panel).Justify(ui.Center).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label("Scroll to latest message")
	b.Children(func() { ui.Icon(c, kit.Icon("down")).TextColor(k.FG) })
	if b.Clicked() {
		a.scroll.ScrollTo(0, math.MaxFloat32, ui.Smooth)
	}
}
