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
	Entry        Entry
	Day          string
	Dated        bool
	ModelChanged bool
}

func parseAt(at string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		t, err = time.Parse(time.RFC3339, at)
		if err != nil {
			return time.Time{}, false
		}
	}
	return t.Local(), true
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
	ref, model := "", ""
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
		r := row{Entry: e, Dated: !split}
		if e.Role == "supervisor" {
			r.ModelChanged = e.Model != "" && (e.Ref != ref || e.Model != model)
			ref, model = e.Ref, e.Model
		}
		rows = append(rows, r)
	}
	return rows
}

func applyTimelineUpsert(have Timeline, ev TimelineEvent) Timeline {
	if ev.Snapshot {
		return Timeline{
			Live: ev.Live, Note: ev.Note, Working: ev.Working,
			Older: max(ev.To-len(ev.Entries), 0), Entries: append([]Entry(nil), ev.Entries...),
		}
	}
	have.Entries = mergeTimelineEntries(have.Entries, ev)
	have.Live, have.Note, have.Working = ev.Live, ev.Note, ev.Working
	return have
}

func mergeTimelineEntries(have []Entry, ev TimelineEvent) []Entry {
	drop := make(map[string]bool, len(ev.Drop))
	for _, key := range ev.Drop {
		drop[key] = true
	}
	kept := have[:0:0]
	for _, e := range have {
		if !drop[entryKey(e)] {
			kept = append(kept, e)
		}
	}
	have = kept
	for _, e := range ev.Entries {
		key := entryKey(e)
		at, _ := parseAt(e.At)
		replaced := false
		for i := range have {
			if entryKey(have[i]) != key {
				continue
			}
			if e.Key == "" {
				e.Key = have[i].Key
			}
			have[i] = e
			replaced = true
			break
		}
		if replaced {
			continue
		}
		pos := len(have)
		for i := range have {
			other, ok := parseAt(have[i].At)
			if ok && at.After(other) {
				pos = i
				break
			}
		}
		have = append(have, Entry{})
		copy(have[pos+1:], have[pos:])
		have[pos] = e
	}
	return have
}

func (a *App) ApplyTimelineEvent(ev TimelineEvent) {
	if ev.Snapshot {
		a.Timeline = applyTimelineUpsert(a.Timeline, ev)
		a.timelineTo = ev.To
		return
	}
	if ev.From != a.timelineTo {
		if !a.Loading {
			a.Reload()
		}
		return
	}
	a.Timeline = applyTimelineUpsert(a.Timeline, ev)
	a.timelineTo = ev.To
}

func (a *App) speakLatest(c *ui.Context) {
	n := a.latestNew
	if n == 0 {
		a.latestCount, a.latestSpoken, a.latestSince = 0, 0, time.Time{}
		return
	}
	if n != a.latestCount {
		a.latestCount, a.latestSince = n, a.now()
	}
	if n == a.latestSpoken || a.latestSince.IsZero() || a.now().Sub(a.latestSince) < time.Second {
		return
	}
	a.latestSpoken = n
	if n == 1 {
		c.Announce("1 new message")
		return
	}
	c.Announce(fmt.Sprintf("%d new messages", n))
}

func newSupervisorReplies(have, next Timeline) int {
	if len(have.Entries) == 0 {
		return 0
	}
	seen := make(map[string]bool, len(have.Entries))
	newest := ""
	for _, e := range have.Entries {
		seen[entryKey(e)] = true
		if e.Role == "supervisor" && (newest == "" || e.At > newest) {
			newest = e.At
		}
	}
	n := 0
	for _, e := range next.Entries {
		if e.Role == "supervisor" && !seen[entryKey(e)] && (newest == "" || e.At > newest) {
			n++
		}
	}
	return n
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

func (a *App) timelineEntries(c *ui.Context, p palette, column float32) {
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
	measure := kit.MeasureOf(c)
	quiet := false
	for _, r := range timelineRows(a.Timeline.Entries) {
		line := r.Day == "" && (r.Entry.Role == "hand" || r.Entry.Role == "wakes")
		gap := float32(kit.Space14)
		if line && quiet {
			gap = kit.Space6
		}
		quiet = line
		ui.Column(c).Margin(gap, 0, 0).Children(func() { a.timelineRow(c, p, measure, column, r) })
	}
	a.working(c, p)
}

func (a *App) timelineRow(c *ui.Context, p palette, measure, column float32, r row) {
	e := r.Entry
	switch {
	case r.Day != "":
		a.dayDivider(c, p, r.Day)
	case e.Role == "hand":
		a.handLine(c, p, e.Text, e.At, r.Dated)
	case e.Role == "wakes" && len(e.Wakes) > 0:
		a.wakes(c, p, e, r.Dated)
	case e.Role == "supervisor":
		ui.Row(c).FillWidth().Children(func() {
			a.bubble(c, p, measure, column, r, false)
			ui.Spacer(c)
		})
	default:
		ui.Row(c).FillWidth().Children(func() {
			ui.Spacer(c)
			a.bubble(c, p, measure, column, r, true)
		})
	}
}

func (a *App) dayDivider(c *ui.Context, p palette, day string) {
	rule := func() { ui.Box(c).Grow(1).Height(1).Background(p.line) }
	ui.Row(c).FillWidth().Gap(kit.Space12).Padding(kit.Space6, 0).AlignItems(ui.Center).Children(func() {
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
	key := "wakes/" + e.Wakes[0].At
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

func (a *App) bubble(c *ui.Context, p palette, measure, column float32, r row, you bool) {
	e := r.Entry
	who, tone, bg := roleName(e), p.neutral, p.card
	box := ui.Column(c).Gap(kit.Space4).Label("Message from " + who)
	maxW := min(column, measure+28)
	if you {
		tone, bg = p.accent, p.accentBG
		maxW = min(maxW, column*.88, 560)
		box.Padding(kit.Space10, kit.Space14, kit.Space12).Radius(kit.RadiusLG, kit.RadiusLG, kit.RadiusSM, kit.RadiusLG).
			Background(bg).MaxWidth(maxW)
	} else {
		box.Padding(9, 13, 11).Radius(kit.RadiusLG).Background(bg).Border(1, p.lineStrong).MaxWidth(maxW)
	}
	box.Children(func() {
		ui.Row(c).Gap(kit.Space8).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, who).FontSize(kit.Meta.Size).Bold().TextColor(tone).SingleLine()
			ui.Text(c, clockOf(e.At, r.Dated)).FontSize(kit.Meta.Size).TextColor(p.neutral).SingleLine()
			if !you && r.ModelChanged {
				ui.Text(c, e.Model).FontSize(kit.Meta.Size).TextColor(p.neutral).SingleLine()
			}
			ui.Spacer(c)
			if you {
				switch {
				case e.Queued || e.Delivery == "queued":
					ui.Text(c, "queued").FontSize(kit.Meta.Size).TextColor(p.neutral).SingleLine()
				case e.Delivery != "":
					ui.Text(c, strings.TrimSpace(e.Delivery+" "+clockOf(e.Delivered, false))).FontSize(kit.Meta.Size).TextColor(p.neutral).SingleLine()
				}
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
		if a.Timeline.Live {
			ui.Row(c).FillWidth().Padding(kit.Space10, 0).Children(func() {
				ui.Spacer(c)
				ui.Badge(c, "live").Background(p.passBG).TextColor(p.pass)
			})
		}
		return
	}
	ui.Row(c).FillWidth().Gap(kit.Space8).Padding(kit.Space10, 0).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Size(8, 8).Margin(5, 0, 0).Radius(kit.RadiusPill).Background(p.accent)
		ui.Text(c, w.Ref+" working").FontSize(kit.Meta.Size).LineHeight(1.5).Bold().SingleLine().Shrink(0)
		ui.Text(c, "· "+workingText(w, a.now())).FontSize(kit.Meta.Size).LineHeight(1.5).TextColor(p.neutral).MinWidth(0)
		if a.Timeline.Live {
			ui.Spacer(c)
			ui.Badge(c, "live").Background(p.passBG).TextColor(p.pass)
		}
	})
}

func (a *App) away() bool { return a.scroll.MaxY-a.scroll.Y > awayBy }

func (a *App) latest(c *ui.Context, k kit.Colors) {
	a.speakLatest(c)
	box := ui.Box(c).Absolute().Top(-kit.Control-13).Right(-1).Size(kit.Control, kit.Control)
	box.Children(func() {
		if !a.away() {
			return
		}
		label := "Scroll to latest message"
		switch n := a.latestNew; {
		case n == 1:
			label = "Scroll to latest, 1 new message"
		case n > 1:
			label = fmt.Sprintf("Scroll to latest, %d new messages", n)
		}
		b := ui.ButtonBase(c.Key("timeline:latest")).Size(kit.Control, kit.Control).
			Radius(kit.RadiusPill).Border(1, k.LineStrong).Background(k.Panel).Justify(ui.Center).AlignItems(ui.Center).
			Cursor(ui.CursorPointer).KeepFocus().Label(label).
			Transition(ui.ElementTransition{
				Duration: 150 * time.Millisecond, Position: true,
				Enter: &ui.Motion{Y: 8, Opacity: 0}, Exit: &ui.Motion{Y: 8, Opacity: 0},
			})
		b.Children(func() { ui.Icon(c, kit.Icon("down")).TextColor(k.FG) })
		if b.Clicked() {
			if b.Focused() {
				a.focusComposer = true
			}
			a.latestNew = 0
			a.scroll.ScrollTo(0, math.MaxFloat32, ui.Smooth)
		}
		if n := a.latestNew; n > 0 {
			count := strconv.Itoa(n)
			if n > 99 {
				count = "99+"
			}
			ui.Badge(c, count).Absolute().Top(-6).Right(-8).Background(k.Primary).TextColor(k.OnPrimary)
		}
	})
}
