package desktop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	findMax     = 200
	findStep    = 20
	findWait    = 250 * time.Millisecond
	findTimeout = time.Minute
	followEvery = 2 * time.Second
	followKeep  = 5000
	jumpBefore  = 20
	endTail     = 100

	noMatch        = "not in this file"
	followScrolled = "Follow stopped: you scrolled up."
	followReset    = "The file was replaced or shortened; reload to read it again."
	badLine        = "Enter a line number, 1 or more."
)

type docFind struct {
	query, pending, ran string
	pendAt              time.Time
	gen                 int
	cancel              context.CancelFunc
	hits                []int
	next                int
	done, busy          bool
	at                  int
	err                 string
}

type docFollow struct {
	on, busy, seek, rearm, stick bool
	nextAt                       time.Time
	gen                          int
	note                         string
	bad                          bool
}

func noLine(n, end int) string {
	if end > 0 {
		return fmt.Sprintf("This file has no line %s; it ends at line %s.", thousands(n), thousands(end))
	}
	return fmt.Sprintf("This file has no line %s.", thousands(n))
}

func foldPrefix(s, needle string) int {
	i := 0
	for _, nr := range needle {
		if i >= len(s) {
			return -1
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		if r != nr && unicode.ToLower(r) != unicode.ToLower(nr) && unicode.ToUpper(r) != unicode.ToUpper(nr) {
			return -1
		}
		i += w
	}
	return i
}

func foldSpans(line, needle string) [][2]int {
	if needle == "" {
		return nil
	}
	var out [][2]int
	for i := 0; i < len(line); {
		if n := foldPrefix(line[i:], needle); n > 0 {
			out = append(out, [2]int{i, i + n})
			i += n
			continue
		}
		_, w := utf8.DecodeRuneInString(line[i:])
		i += w
	}
	return out
}

func memHits(lines []string, needle string) []int {
	var hits []int
	for i, l := range lines {
		if len(foldSpans(l, needle)) > 0 {
			hits = append(hits, i+1)
			if len(hits) >= findMax {
				break
			}
		}
	}
	return hits
}

func scanHits(ctx context.Context, m ReadModel, fleet, rel, needle string, from, want, room int) (hits []int, next int, done bool, err error) {
	for {
		w, err := m.DocWindow(ctx, fleet, rel, from)
		if err != nil {
			return hits, from, false, err
		}
		for i, l := range w.Lines {
			if len(hits) < room && len(foldSpans(l, needle)) > 0 {
				hits = append(hits, w.From+i)
			}
		}
		if w.Next == 0 || len(hits) >= room {
			return hits, w.Next, true, nil
		}
		from = w.Next
		if len(hits) >= want {
			return hits, from, false, nil
		}
	}
}

func isBadRequest(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == http.StatusBadRequest
}

func tailWindow(ctx context.Context, m ReadModel, fleet, rel string) (DocWindow, error) {
	probe := func(n int) (DocWindow, bool, error) {
		w, err := m.DocWindow(ctx, fleet, rel, n)
		switch {
		case err == nil:
			return w, true, nil
		case isBadRequest(err):
			return w, false, nil
		}
		return w, false, err
	}
	finish := func(w DocWindow) (DocWindow, error) {
		last := w.From + len(w.Lines) - 1
		start := max(1, last-endTail+1)
		if w.From <= start || w.From == 1 {
			return w, nil
		}
		return m.DocWindow(ctx, fleet, rel, start)
	}
	lo, hi, from := 0, 0, 1
	for hi == 0 {
		w, ok, err := probe(from)
		if err != nil {
			return w, err
		}
		if !ok {
			hi = from
			break
		}
		if w.Next == 0 {
			return finish(w)
		}
		lo = w.Next - 1
		from = max(w.Next, lo*2)
	}
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		w, ok, err := probe(mid)
		if err != nil {
			return w, err
		}
		switch {
		case !ok:
			hi = mid
		case w.Next == 0:
			return finish(w)
		default:
			lo = max(mid, w.Next-1)
		}
	}
	return m.DocWindow(ctx, fleet, rel, max(1, lo-endTail+1))
}

func (a *App) docLineSet() (int, []string) {
	st := &a.doc
	if st.doc.Kind == "markdown" {
		if st.srcLines == nil {
			st.srcLines = strings.Split(strings.TrimSuffix(st.body, "\n"), "\n")
			for i, l := range st.srcLines {
				st.srcLines[i] = strings.TrimSuffix(l, "\r")
			}
		}
		return 1, st.srcLines
	}
	return st.window.From, st.window.Lines
}

func (a *App) docGuard(op int, rel, fleet string) bool {
	return a.docOp == op && a.Screen == DocScreen && a.Ref == rel && a.Fleet.ID == fleet
}

func (a *App) startFind(q string) {
	f := &a.doc.find
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
	f.gen++
	f.ran, f.hits, f.next, f.done, f.at, f.err, f.busy = q, nil, 1, false, -1, "", false
	if q == "" {
		return
	}
	if a.doc.doc.Kind == "markdown" {
		_, lines := a.docLineSet()
		f.hits, f.done = memHits(lines, q), true
		a.stepFind(1)
		return
	}
	a.scanFind(1)
}

func (a *App) scanFind(step int) {
	f := &a.doc.find
	if a.Model == nil {
		return
	}
	f.busy = true
	ctx, cancel := context.WithTimeout(a.Context, findTimeout)
	f.cancel = cancel
	gen, op, model, fleet, rel, q, from, have := f.gen, a.docOp, a.Model, a.Fleet.ID, a.Ref, f.ran, f.next, len(f.hits)
	a.offload(func() func() {
		want := findStep
		if have == 0 {
			want = 1
		}
		hits, next, done, err := scanHits(ctx, model, fleet, rel, q, from, want, findMax-have)
		cancel()
		return func() {
			f := &a.doc.find
			if f.gen != gen || !a.docGuard(op, rel, fleet) {
				return
			}
			f.busy, f.cancel = false, nil
			if err != nil {
				f.err = friendly(err)
				return
			}
			f.hits = append(f.hits, hits...)
			f.next, f.done = next, done
			a.stepFind(step)
		}
	})
}

func (a *App) stepFind(step int) {
	f := &a.doc.find
	if f.busy || f.ran == "" || len(f.hits) == 0 {
		return
	}
	i := f.at + step
	if i >= len(f.hits) && !f.done {
		a.scanFind(step)
		return
	}
	switch {
	case i >= len(f.hits):
		i = 0
	case i < 0 && f.done:
		i = len(f.hits) - 1
	case i < 0:
		i = 0
	}
	f.at = i
	a.showLine(f.hits[i])
}

func (a *App) findLabel() string {
	f := &a.doc.find
	switch {
	case f.err != "":
		return "Find stopped: " + f.err
	case f.ran == "":
		return ""
	case f.busy && len(f.hits) == 0:
		return "Searching…"
	case len(f.hits) == 0:
		return noMatch
	}
	more := ""
	if !f.done {
		more = "+"
	}
	return fmt.Sprintf("%d of %d%s", f.at+1, len(f.hits), more)
}

func (a *App) showLine(n int) {
	st := &a.doc
	a.stopFollow("", false)
	st.navGen++
	st.lineNote, st.lineBad = "", false
	if st.doc.Kind == "markdown" {
		if _, lines := a.docLineSet(); n > len(lines) {
			st.lineNote, st.lineBad = noLine(n, len(lines)), true
			return
		}
	} else if w := st.window; n < w.From || n > w.From+len(w.Lines)-1 {
		a.loadAround(n)
		return
	}
	st.mark, st.scrollMark = n, true
	st.scrollSeq++
}

func (a *App) loadAround(n int) {
	st := &a.doc
	if a.Model == nil {
		return
	}
	st.navGen++
	gen, op, model, fleet, rel := st.navGen, a.docOp, a.Model, a.Fleet.ID, a.Ref
	ctx, cancel := context.WithTimeout(a.Context, a.pollTimeout())
	a.offload(func() func() {
		w, err := model.DocWindow(ctx, fleet, rel, max(1, n-jumpBefore))
		cancel()
		return func() {
			st := &a.doc
			if st.navGen != gen || !a.docGuard(op, rel, fleet) {
				return
			}
			switch {
			case err != nil && isBadRequest(err):
				st.lineNote, st.lineBad = noLine(n, 0), true
			case err != nil:
				st.lineNote, st.lineBad = "Could not read line "+thousands(n)+": "+friendly(err), true
			case w.Next == 0 && n > w.From+len(w.Lines)-1:
				st.lineNote, st.lineBad = noLine(n, w.From+len(w.Lines)-1), true
			default:
				a.putWindow(w)
				st.mark, st.scrollMark = n, true
				st.scrollSeq++
			}
		}
	})
}

func (a *App) putWindow(w DocWindow) {
	st := &a.doc
	st.froms = append(st.froms, st.from)
	st.from, st.window = w.From, w
	a.viewerScroll = ui.ScrollState{}
}

func (a *App) goLine() {
	st := &a.doc
	n, err := strconv.Atoi(strings.TrimSpace(st.lineIn))
	if err != nil || n < 1 {
		st.lineNote, st.lineBad = badLine, true
		return
	}
	a.showLine(n)
}

func (a *App) seekEnd() {
	st := &a.doc
	if a.Model == nil {
		return
	}
	st.navGen++
	if f := &st.follow; f.on {
		f.gen++
		f.busy, f.seek = false, true
	}
	gen, op, model, fleet, rel := st.navGen, a.docOp, a.Model, a.Fleet.ID, a.Ref
	ctx, cancel := context.WithTimeout(a.Context, findTimeout)
	a.offload(func() func() {
		w, err := tailWindow(ctx, model, fleet, rel)
		cancel()
		return func() {
			st := &a.doc
			if st.navGen != gen || !a.docGuard(op, rel, fleet) {
				return
			}
			f := &st.follow
			f.seek = false
			if err != nil {
				st.lineNote, st.lineBad = "Could not reach the end: "+friendly(err), true
				a.stopFollow("", false)
				return
			}
			a.putWindow(w)
			st.mark = 0
			a.viewerScroll.Y = math.MaxFloat32
			f.rearm = true
		}
	})
}

func (a *App) toggleFollow() {
	st := &a.doc
	f := &st.follow
	if f.on {
		a.stopFollow("", false)
		return
	}
	f.on, f.note, f.bad, f.busy = true, "", false, false
	f.gen++
	st.mark, st.lineNote = 0, ""
	if st.window.Next > 0 {
		f.seek = true
		a.seekEnd()
		return
	}
	f.stick, f.rearm = true, true
	a.viewerScroll.Y = math.MaxFloat32
}

func (a *App) stopFollow(note string, bad bool) {
	f := &a.doc.follow
	was := f.on
	if f.seek {
		a.doc.navGen++
	}
	f.on, f.busy, f.seek, f.stick, f.rearm = false, false, false, false, false
	f.gen++
	if was || note != "" {
		f.note, f.bad = note, bad
	}
}

func (a *App) followTick(c *ui.Context) {
	f := &a.doc.follow
	if !f.on {
		return
	}
	if !f.stick && !f.seek && a.viewerScroll.MaxY-a.viewerScroll.Y > awayBy {
		a.stopFollow(followScrolled, false)
		return
	}
	if f.stick {
		a.viewerScroll.Y = math.MaxFloat32
		f.stick = false
	}
	if f.rearm {
		f.nextAt, f.rearm = c.Now().Add(followEvery), false
	}
	if f.busy || f.seek {
		return
	}
	if d := f.nextAt.Sub(c.Now()); d > 0 {
		c.After(d)
		return
	}
	a.pullFollow()
}

func (a *App) pullFollow() {
	st := &a.doc
	f := &st.follow
	if a.Model == nil {
		return
	}
	f.busy = true
	held := len(st.window.Lines) > 0
	from := max(1, st.window.From+len(st.window.Lines)-1)
	gen, op, model, fleet, rel := f.gen, a.docOp, a.Model, a.Fleet.ID, a.Ref
	ctx, cancel := context.WithTimeout(a.Context, a.pollTimeout())
	a.offload(func() func() {
		nw, err := model.DocWindow(ctx, fleet, rel, from)
		cancel()
		return func() {
			st := &a.doc
			f := &st.follow
			if f.gen != gen || !f.on || !a.docGuard(op, rel, fleet) {
				return
			}
			f.busy, f.rearm = false, true
			switch {
			case err != nil && isBadRequest(err) && held:
				a.stopFollow(followReset, true)
			case err != nil:
				f.note, f.bad = "Follow could not read the file: "+friendly(err), true
			default:
				f.note, f.bad = "", false
				if !a.mergeFollow(nw) {
					a.stopFollow(followReset, true)
				}
			}
		}
	})
}

func (a *App) mergeFollow(nw DocWindow) bool {
	st := &a.doc
	w := &st.window
	f := &st.follow
	if len(w.Lines) == 0 {
		if len(nw.Lines) > 0 {
			f.stick = true
		}
		nw.Path, nw.Name = w.Path, w.Name
		*w = nw
		st.from = w.From
		return true
	}
	if len(nw.Lines) == 0 {
		return false
	}
	last := len(w.Lines) - 1
	if !w.Partial && nw.Lines[0] != w.Lines[last] {
		return false
	}
	grew := len(nw.Lines) > 1 || nw.Lines[0] != w.Lines[last]
	lastNo := w.From + last
	w.Lines[last] = nw.Lines[0]
	w.Lines = append(w.Lines, nw.Lines[1:]...)
	cuts := w.Cut[:0:0]
	for _, c := range w.Cut {
		if c.Line != lastNo {
			cuts = append(cuts, c)
		}
	}
	w.Cut = append(cuts, nw.Cut...)
	w.Next, w.Partial = nw.Next, nw.Partial
	if drop := len(w.Lines) - followKeep; drop > 0 {
		w.Lines = w.Lines[drop:]
		w.From += drop
		st.from = w.From
		kept := w.Cut[:0:0]
		for _, c := range w.Cut {
			if c.Line >= w.From {
				kept = append(kept, c)
			}
		}
		w.Cut = kept
	}
	if grew {
		f.stick = true
	}
	return true
}

func (a *App) docMarks(shown []string, first int) kit.DocMarks {
	st := &a.doc
	switch {
	case !st.scrollMark:
	case st.scrollArmed != st.scrollSeq:
		st.scrollArmed, st.scrollFrom = st.scrollSeq, a.viewerScroll.Y
	case a.viewerScroll.Y != st.scrollFrom:
		st.scrollMark = false
	}
	marks := kit.DocMarks{Line: st.mark, Scroll: st.scrollMark, Seq: st.scrollSeq}
	f := &st.find
	if f.ran == "" {
		return marks
	}
	now := -1
	if f.at >= 0 && f.at < len(f.hits) {
		now = f.hits[f.at]
	}
	for i, line := range shown {
		spans := foldSpans(line, f.ran)
		if len(spans) == 0 {
			continue
		}
		if marks.Hits == nil {
			marks.Hits = map[int][]kit.DocMatch{}
		}
		for j, s := range spans {
			marks.Hits[i] = append(marks.Hits[i], kit.DocMatch{From: s[0], To: s[1], Now: first+i == now && j == 0})
		}
	}
	return marks
}

func (a *App) docFindRow(c *ui.Context, k kit.Colors, p palette) {
	st := &a.doc
	f, fo := &st.find, &st.follow
	text := a.docWindowed()
	q := strings.TrimSpace(f.query)
	switch {
	case q != f.pending:
		f.pending, f.pendAt = q, c.Now()
		c.After(findWait)
	case q != f.ran:
		if d := findWait - c.Now().Sub(f.pendAt); d > 0 {
			c.After(d)
		} else {
			a.startFind(q)
		}
	}
	ui.Row(c).GapY(kit.Space6).GapX(kit.Space8).AlignItems(ui.Center).Wrap().Label("Find and go to line").Children(func() {
		keyFindClear.on(ui.TextInput(c, &f.query).Bind(&a.findBox).Placeholder("Find in file").Label("Find in file").
			Width(220).FontSize(kit.Body.Size).
			OnTextKey(func(key ui.TextKey) bool {
				switch {
				case keyFindNext.is(key.Mods, key.Key), keyFindPrev.is(key.Mods, key.Key):
					f.query = key.Text()
					step := 1
					if keyFindPrev.is(key.Mods, key.Key) {
						step = -1
					}
					if t := strings.TrimSpace(f.query); t != f.ran {
						f.pending = t
						a.startFind(t)
					} else {
						a.stepFind(step)
					}
					return true
				}
				return false
			}), func() {
			if f.query == "" {
				a.CloseViewer()
				return
			}
			f.query, f.pending = "", ""
			a.startFind("")
		})
		if st.focusFind {
			st.focusFind = false
			a.findBox.Focus(c)
		}
		if kit.Button(c, k, "doc:find:prev", "Previous match", 0, len(f.hits) == 0 || f.busy) {
			a.stepFind(-1)
		}
		if kit.Button(c, k, "doc:find:next", "Next match", 0, len(f.hits) == 0 || f.busy) {
			a.stepFind(1)
		}
		label := a.findLabel()
		if label != "" {
			tone := p.neutral
			if f.err != "" {
				tone = p.fail
			}
			if label != st.findSaid {
				st.findSaid = label
				c.Announce(label)
			}
			ui.Text(c, shownText(label)).FontSize(kit.Meta.Size).TextColor(tone).FontFeatures("tnum")
		}
		line := ui.TextInput(c, &st.lineIn).Bind(&a.lineBox).Placeholder("Line").Label("Go to line").Width(90).FontSize(kit.Body.Size).Submitted()
		if kit.Button(c, k, "doc:line:go", "Go", 0, false) || line {
			a.goLine()
		}
		if text {
			label := "Follow"
			if fo.on {
				label = "Following"
			}
			if kit.DocToggle(c, k, "doc:tool:follow", label, fo.on) {
				a.toggleFollow()
			}
			if kit.Button(c, k, "doc:tool:end", "End", 0, fo.seek) {
				a.seekEnd()
			}
		}
	})
	msg, bad := st.lineNote, st.lineBad
	if msg == "" {
		msg, bad = fo.note, fo.bad
	}
	if fo.on && msg == "" {
		msg = "Following the end of the file."
	}
	if msg != "" {
		tone := p.neutral
		if bad {
			tone = p.fail
		}
		if msg != st.lineSaid {
			st.lineSaid = msg
			c.Announce(msg)
		}
		ui.Text(c, shownText(msg)).FontSize(kit.Meta.Size).TextColor(tone)
	}
}

func (a *App) focusFindBox() {
	if a.docLinesShown() {
		a.doc.focusFind = true
	}
}
