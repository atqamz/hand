package desktop

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	historyPage        = 20
	historyMax         = 50
	historyRevealPages = 20
	historyPrefix      = 64

	historyHint    = "Search what was said: your messages, the supervisor's replies, reports, decisions and task titles."
	historyMissing = "That entry is no longer in the conversation view."
	historyCapped  = "The entry is further back than the 20 pages read so far. The conversation goes back further."
)

type HistoryHit struct {
	Ref        string `json:"ref"`
	Who        string `json:"who"`
	At         string `json:"at"`
	Text       string `json:"text"`
	Supervisor string `json:"supervisor,omitempty"`
	Task       string `json:"task,omitempty"`
}

type HistorySearch struct {
	Q     string       `json:"q"`
	Total int          `json:"total"`
	More  bool         `json:"more"`
	Hits  []HistoryHit `json:"hits"`
}

type HistoryEntry struct {
	Ref        string `json:"ref"`
	Who        string `json:"who"`
	At         string `json:"at"`
	Supervisor string `json:"supervisor,omitempty"`
	Task       string `json:"task,omitempty"`
	Text       string `json:"text"`
	Truncated  bool   `json:"truncated"`
}

func (c *HTTPClient) HistorySearch(ctx context.Context, fleet, q string, limit int) (HistorySearch, error) {
	v := url.Values{"q": {q}, "limit": {strconv.Itoa(limit)}}
	var out HistorySearch
	_, err := c.get(ctx, fleet, "/history/search?"+v.Encode(), &out)
	return out, err
}

func (c *HTTPClient) HistoryEntry(ctx context.Context, fleet, ref string) (HistoryEntry, error) {
	var out HistoryEntry
	_, err := c.get(ctx, fleet, "/history/"+url.PathEscape(ref), &out)
	return out, err
}

func (r *Router) HistorySearch(ctx context.Context, fleet, q string, limit int) (HistorySearch, error) {
	return route(r, fleet, func(m ReadModel) (HistorySearch, error) { return m.HistorySearch(ctx, fleet, q, limit) })
}

func (r *Router) HistoryEntry(ctx context.Context, fleet, ref string) (HistoryEntry, error) {
	return route(r, fleet, func(m ReadModel) (HistoryEntry, error) { return m.HistoryEntry(ctx, fleet, ref) })
}

type histOpen struct {
	hit     HistoryHit
	entry   HistoryEntry
	loading bool
	err     string
}

type histReveal struct {
	hit    HistoryHit
	text   string
	pages  int
	capped bool
}

type histState struct {
	scroll ui.ScrollState

	tgen, ogen, sgen, egen int

	old       bool
	tl        Timeline
	loaded    bool
	err       string
	olderBusy bool
	olderErr  string
	stale     string
	skip      int
	fails     int

	q         string
	hits      []HistoryHit
	total     int
	more      bool
	limit     int
	searching bool
	searchErr string

	open *histOpen

	inTimeline bool
	rev        *histReveal
	focus      string
	focusRef   string
	focusDone  bool
	missing    bool
}

func (a *App) resetHistory() {
	h := a.hi
	a.hi = histState{tgen: h.tgen + 1, ogen: h.ogen + 1, sgen: h.sgen + 1, egen: h.egen + 1}
	a.hq = ""
}

func (a *App) offThread(work func(ctx context.Context) func()) {
	a.offThreadFor(a.pollTimeout(), work)
}

func (a *App) openHistory() {
	if a.Model == nil || a.Fleet.ID == "" {
		return
	}
	a.resetHistory()
	a.loadHistory()
}

func (a *App) historyReady() bool {
	return a.Model != nil && a.Fleet.ID != "" && a.Screen == HistoryScreen
}

func (a *App) loadHistory() {
	if a.Model == nil || a.Fleet.ID == "" {
		return
	}
	a.hi.tgen++
	gen, fleet, model, first := a.hi.tgen, a.Fleet.ID, a.Model, !a.hi.loaded
	a.offThread(func(ctx context.Context) func() {
		var old bool
		if first {
			if fs, err := model.Features(ctx, fleet); err == nil && !slices.Contains(fs, "history") {
				old = true
			}
		}
		tl, err := model.Timeline(ctx, fleet, 0)
		return func() {
			if a.Fleet.ID != fleet || a.hi.tgen != gen {
				return
			}
			if first {
				a.hi.old = old
			}
			if err != nil {
				a.historyFailed(err)
				return
			}
			a.hi.err, a.hi.stale, a.hi.fails, a.hi.skip = "", "", 0, 0
			if a.hi.loaded {
				a.hi.tl = mergeTimeline(a.hi.tl, tl)
			} else {
				a.hi.tl = tl
			}
			a.hi.loaded = true
			a.revealNext()
		}
	})
}

func (a *App) historyFailed(err error) {
	if a.hi.loaded && searchRetriable(err) {
		a.hi.stale = friendly(err)
		a.hi.fails++
		a.hi.skip = a.pollSkips(a.hi.fails)
		return
	}
	if !a.hi.loaded {
		a.hi.err = friendly(err)
	}
}

func (a *App) RetryHistory() {
	if a.hi.q != "" && !a.hi.inTimeline && a.hi.searchErr != "" {
		a.runHistorySearch(a.hi.q, max(a.hi.limit, historyPage))
		return
	}
	a.hi.err = ""
	a.loadHistory()
}

func (a *App) pollHistory(moved bool) {
	if !a.historyReady() || a.hi.searching {
		return
	}
	switch {
	case a.hi.stale != "":
		if a.hi.skip > 0 {
			a.hi.skip--
			return
		}
		a.loadHistory()
	case moved && a.hi.loaded:
		a.loadHistory()
	}
}

func (a *App) OlderHistory() {
	if !a.historyReady() || a.hi.olderBusy || a.hi.tl.Older <= 0 {
		return
	}
	a.hi.ogen++
	a.hi.olderBusy, a.hi.olderErr = true, ""
	gen, fleet, model, before := a.hi.ogen, a.Fleet.ID, a.Model, a.hi.tl.Older
	a.offThread(func(ctx context.Context) func() {
		tl, err := model.Timeline(ctx, fleet, before)
		return func() {
			if a.Fleet.ID != fleet || a.hi.ogen != gen {
				return
			}
			a.hi.olderBusy = false
			if err != nil {
				a.hi.olderErr = friendly(err)
				return
			}
			merged := a.hi.tl
			merged.Entries = mergeTimelineEntries(merged.Entries, TimelineEvent{Entries: tl.Entries})
			merged.Older = tl.Older
			a.hi.tl = merged
			a.revealNext()
		}
	})
}

func (a *App) SearchHistory() {
	q := strings.TrimSpace(a.hq)
	if q == "" {
		a.ClearHistorySearch()
		return
	}
	a.runHistorySearch(q, historyPage)
}

func (a *App) MoreHistory() {
	if !a.hi.more || a.hi.limit >= historyMax || a.hi.searching {
		return
	}
	a.runHistorySearch(a.hi.q, historyMax)
}

func (a *App) ClearHistorySearch() {
	a.hi.sgen++
	a.hi.egen++
	a.hi.q, a.hi.hits, a.hi.total, a.hi.more, a.hi.limit = "", nil, 0, false, 0
	a.hi.searching, a.hi.searchErr, a.hi.open = false, "", nil
	a.hi.inTimeline, a.hi.rev, a.hi.missing = false, nil, false
	a.hq = ""
}

func (a *App) runHistorySearch(q string, limit int) {
	if a.Model == nil || a.Fleet.ID == "" {
		return
	}
	a.hi.sgen++
	gen, fleet, model := a.hi.sgen, a.Fleet.ID, a.Model
	same := a.hi.q == q
	if !same {
		a.hi.hits, a.hi.total, a.hi.more = nil, 0, false
	}
	a.hi.q, a.hi.limit = q, limit
	a.hi.searching, a.hi.searchErr = true, ""
	a.hi.open, a.hi.inTimeline, a.hi.rev, a.hi.missing = nil, false, nil, false
	a.hi.egen++
	a.offThread(func(ctx context.Context) func() {
		s, err := model.HistorySearch(ctx, fleet, q, limit)
		return func() {
			if a.Fleet.ID != fleet || a.hi.sgen != gen {
				return
			}
			a.hi.searching = false
			if err != nil {
				a.hi.searchErr = friendly(err)
				return
			}
			a.hi.hits, a.hi.total, a.hi.more = s.Hits, s.Total, s.More
		}
	})
}

func (a *App) OpenHistoryHit(hit HistoryHit) {
	if a.Model == nil || a.Fleet.ID == "" || hit.Ref == "" {
		return
	}
	a.hi.egen++
	gen, fleet, model := a.hi.egen, a.Fleet.ID, a.Model
	a.hi.open = &histOpen{hit: hit, loading: true}
	a.offThread(func(ctx context.Context) func() {
		e, err := model.HistoryEntry(ctx, fleet, hit.Ref)
		return func() {
			if a.Fleet.ID != fleet || a.hi.egen != gen || a.hi.open == nil || a.hi.open.hit != hit {
				return
			}
			if err != nil {
				a.hi.open = &histOpen{hit: hit, err: friendly(err)}
				return
			}
			a.hi.open = &histOpen{hit: hit, entry: e}
		}
	})
}

func (a *App) CloseHistoryEntry() {
	a.hi.egen++
	a.hi.open = nil
}

func (a *App) ShowInConversation(hit HistoryHit, text string) {
	if a.Fleet.ID == "" || hit.Ref == "" {
		return
	}
	a.hi.open = nil
	a.hi.egen++
	a.hi.inTimeline = true
	a.hi.focus, a.hi.focusDone, a.hi.missing = "", false, false
	a.hi.rev = &histReveal{hit: hit, text: text}
	a.revealNext()
}

func (a *App) BackToResults() {
	a.hi.inTimeline, a.hi.rev, a.hi.focus, a.hi.missing = false, nil, "", false
}

func (a *App) KeepLooking() {
	if r := a.hi.rev; r != nil && r.capped {
		r.capped, r.pages = false, 0
		a.revealNext()
	}
}

func (a *App) revealNext() {
	r := a.hi.rev
	if r == nil || !a.hi.loaded {
		return
	}
	if e, ok := historyFind(a.hi.tl.Entries, r.hit, r.text); ok {
		a.hi.focus, a.hi.focusRef, a.hi.focusDone = entryKey(e), r.hit.Ref, false
		a.hi.rev = nil
		return
	}
	if a.hi.tl.Older <= 0 {
		a.hi.rev, a.hi.missing = nil, true
		return
	}
	if r.pages >= historyRevealPages {
		r.capped = true
		return
	}
	if r.capped || a.hi.olderBusy {
		return
	}
	r.pages++
	a.OlderHistory()
}

func historyMatches(e Entry, hit HistoryHit) bool {
	if hit.Ref == "" {
		return false
	}
	switch hit.Ref[0] {
	case 'i':
		return e.Input != 0 && "i"+strconv.FormatInt(e.Input, 10) == hit.Ref
	case 'y':
		return e.Role == "supervisor" && e.At == hit.At
	}
	return false
}

func historyFind(entries []Entry, hit HistoryHit, text string) (Entry, bool) {
	var found []Entry
	for _, e := range entries {
		if historyMatches(e, hit) {
			found = append(found, e)
		}
	}
	if len(found) == 0 {
		return Entry{}, false
	}
	if hit.Ref[0] == 'i' {
		return found[0], true
	}
	wanted := strings.TrimSpace(text)
	if wanted == "" {
		return found[0], len(found) == 1
	}
	for _, e := range found {
		if strings.TrimSpace(e.Text) == wanted {
			return e, true
		}
	}
	prefix := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) > historyPrefix {
			return s[:historyPrefix]
		}
		return s
	}
	var near []Entry
	for _, e := range found {
		if t := strings.TrimSpace(e.Text); t != "" && (prefix(t) == prefix(wanted) || strings.HasPrefix(wanted, t)) {
			near = append(near, e)
		}
	}
	if len(near) == 1 {
		return near[0], true
	}
	return Entry{}, false
}

func historyWho(who string) string {
	switch who {
	case "operator":
		return "You"
	case "supervisor":
		return "Supervisor"
	case "report":
		return "Report"
	case "decision":
		return "Decision"
	case "task":
		return "Task"
	}
	return who
}

type snippetPart struct {
	text  string
	match bool
}

func historySnippet(text string) []snippetPart {
	var parts []snippetPart
	rest := text
	for rest != "" {
		open := strings.Index(rest, "«")
		closeAt := -1
		if open >= 0 {
			if i := strings.Index(rest[open+len("«"):], "»"); i >= 0 {
				closeAt = open + len("«") + i
			}
		}
		if open < 0 || closeAt < 0 {
			parts = append(parts, snippetPart{rest, false})
			break
		}
		if open > 0 {
			parts = append(parts, snippetPart{rest[:open], false})
		}
		parts = append(parts, snippetPart{rest[open+len("«") : closeAt], true})
		rest = rest[closeAt+len("»"):]
	}
	return parts
}

func (a *App) historyPage(c *ui.Context, k kit.Colors, p palette) {
	w, _ := c.Size()
	m := max(float32(kit.Space20), (w-kit.Measure)/2)
	page := ui.Scroll(c).Key("history-scroll").Grow(1).Padding(kit.Space20, m, 32).
		TrackScroll(&a.hi.scroll).Focusable().FocusRing(false).Label("Conversation")
	page.Children(func() {
		ui.Column(c).Gap(kit.Space16).Selectable().Children(func() {
			ui.Text(c, "Fleets / "+fleetName(a.Fleet)+" / conversation").FontSize(kit.Body.Size).TextColor(p.neutral)
			ui.Text(c, "Conversation").FontSize(kit.Display.Size).Bold()
			a.historySearchBar(c, k, p)
			a.historyBody(c, k, p)
		})
	})
}

func (a *App) historySearchBar(c *ui.Context, k kit.Colors, p palette) {
	if a.hi.old {
		a.emptyCard(c, p, "This host is too old to search the conversation", "Update hand on the host and restart its Board to search here. The conversation below still reads.")
		return
	}
	ui.Column(c).Gap(kit.Space8).Padding(kit.Space12).Radius(kit.RadiusLG).
		Background(k.Panel).Border(1, p.line).Children(func() {
		ui.Row(c).Gap(kit.Space8).Children(func() {
			submitted := ui.SearchField(c, &a.hq).Grow(1).Height(kit.Control).
				Label("Search the conversation").Placeholder("Search the conversation").Submitted()
			if kit.PrimaryButton(c, k, "history:search", "Search", 96, a.hi.searching) || submitted {
				a.SearchHistory()
			}
		})
		if a.hi.q == "" {
			ui.Text(c, historyHint).FontSize(kit.Meta.Size).TextColor(p.neutral)
		}
	})
}

func (a *App) historyStatus(c *ui.Context, k kit.Colors, p palette, left string) {
	ui.Row(c).MinHeight(24).AlignItems(ui.Center).Children(func() {
		if left != "" {
			ui.Text(c, left).FontSize(kit.Meta.Size).TextColor(p.neutral)
		}
		ui.Spacer(c)
		if a.hi.stale != "" {
			ui.Text(c, "Stale · retrying").FontSize(kit.Meta.Size).TextColor(p.wait).
				MarginEnd(kit.Space12).Tooltip(a.hi.stale)
		}
		if a.hi.q != "" && kit.TextButton(c, k, "history:clear", "Clear search") {
			a.ClearHistorySearch()
		}
	})
}

func (a *App) historyBody(c *ui.Context, k kit.Colors, p palette) {
	switch {
	case a.hi.open != nil:
		a.historyEntryCard(c, k, p, a.hi.open)
	case a.hi.q != "" && !a.hi.inTimeline:
		a.historyResults(c, k, p)
	default:
		a.historyTimeline(c, k, p)
	}
}

func historyShowing(shown, total int, more bool) string {
	switch {
	case more && shown >= historyMax:
		return fmt.Sprintf("Showing the first %d of %d. Narrow the search.", shown, total)
	case more:
		return fmt.Sprintf("Showing %d of %d", shown, total)
	}
	if total == 1 {
		return "1 match"
	}
	return fmt.Sprintf("%d matches", total)
}

func (a *App) historyFailCard(c *ui.Context, k kit.Colors, p palette, title, msg, key string, retry func()) {
	ui.Column(c).Gap(kit.Space8).Padding(kit.Space16).Radius(kit.RadiusLG).
		Background(p.failBG).Border(1, p.fail).Children(func() {
		ui.Text(c, title).FontSize(kit.Heading.Size).Bold().TextColor(p.fail)
		ui.Text(c, msg).FontSize(kit.Body.Size)
		if kit.Button(c, k, key, "Retry", 100, false) {
			retry()
		}
	})
}

func (a *App) historyResults(c *ui.Context, k kit.Colors, p palette) {
	h := &a.hi
	switch {
	case h.searchErr != "":
		a.historyStatus(c, k, p, "")
		a.historyFailCard(c, k, p, "The conversation could not be searched", h.searchErr, "history:retry", a.RetryHistory)
		return
	case h.searching && len(h.hits) == 0:
		a.historyStatus(c, k, p, "")
		ui.Text(c, "Searching…").FontSize(kit.Body.Size).TextColor(p.neutral)
		return
	case len(h.hits) == 0:
		a.historyStatus(c, k, p, "")
		a.emptyCard(c, p, "No matches", "Nothing said here matches “"+h.q+"”.")
		return
	}
	a.historyStatus(c, k, p, historyShowing(len(h.hits), h.total, h.more))
	ui.Column(c).Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).Label("matches").Children(func() {
		for i, hit := range h.hits {
			a.historyHit(c, p, i, hit)
		}
	})
	if h.more && h.limit < historyMax && kit.Button(c, k, "history:more", "More matches", 140, h.searching) {
		a.MoreHistory()
	}
}

func (a *App) historyHit(c *ui.Context, p palette, i int, hit HistoryHit) {
	key := "history:hit:" + hit.Ref
	el := ui.ButtonBase(c.Key(key)).Cursor(ui.CursorPointer).Padding(kit.Space8, kit.Space4).
		BorderWidth(0, 0, 1, 0).BorderColor(p.line).Role(ui.RoleListItem).
		Label("Open " + hit.Ref + " from " + historyWho(hit.Who))
	el.Children(func() {
		ui.Column(c).Gap(kit.Space4).FillWidth().Children(func() {
			ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Wrap().Children(func() {
				ui.Text(c, historyWho(hit.Who)).FontSize(kit.Meta.Size).Bold()
				ui.Text(c, hit.Ref).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
				ui.Text(c, stamp(hit.At)).FontSize(kit.Meta.Size).TextColor(p.neutral)
			})
			a.snippet(c, p, hit.Text)
		})
	})
	if el.Clicked() {
		a.OpenHistoryHit(hit)
	}
}

func (a *App) snippet(c *ui.Context, p palette, text string) {
	ui.RichText(c).Children(func() {
		for _, part := range historySnippet(text) {
			t := ui.Text(c, part.text).FontSize(kit.Body.Size)
			if part.match {
				t.Bold().TextColor(p.accent).Background(p.accentBG)
			}
		}
	})
}

func (a *App) historyEntryCard(c *ui.Context, k kit.Colors, p palette, o *histOpen) {
	if kit.TextButton(c, k, "history:back", "Back to results") {
		a.CloseHistoryEntry()
	}
	switch {
	case o.loading:
		ui.Text(c, "Reading "+o.hit.Ref+"…").FontSize(kit.Body.Size).TextColor(p.neutral)
		return
	case o.err != "":
		a.historyFailCard(c, k, p, "The entry could not be read", o.err, "history:retry-entry", func() { a.OpenHistoryHit(o.hit) })
		return
	}
	e := o.entry
	ui.Column(c).Gap(kit.Space8).Padding(kit.Space16).Radius(kit.RadiusLG).
		Background(p.card).Border(1, p.line).Label("entry " + e.Ref).Children(func() {
		ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, historyWho(e.Who)).FontSize(kit.Meta.Size).Bold()
			ui.Text(c, e.Ref).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
			ui.Text(c, stamp(e.At)).FontSize(kit.Meta.Size).TextColor(p.neutral)
			if e.Task != "" {
				ui.Text(c, "Task "+e.Task).FontSize(kit.Meta.Size).TextColor(p.neutral)
			}
		})
		ui.Text(c, e.Text).FontSize(kit.Message.Size).LineHeight(kit.Message.Line / kit.Message.Size)
		if e.Truncated {
			ui.Text(c, "Cut at 16 KiB.").FontSize(kit.Meta.Size).TextColor(p.neutral)
		}
		ui.Row(c).Gap(kit.Space8).Children(func() {
			switch o.hit.Ref[:1] {
			case "i", "y":
				if kit.Button(c, k, "history:show", "Show in conversation", 200, false) {
					a.ShowInConversation(o.hit, e.Text)
				}
			default:
				if kit.Button(c, k, "history:open", "Open "+e.Ref, 140, false) {
					a.OpenRef(e.Ref)
				}
			}
		})
	})
}

func (a *App) historyTimeline(c *ui.Context, k kit.Colors, p palette) {
	h := &a.hi
	if h.inTimeline && h.q != "" {
		if kit.TextButton(c, k, "history:results", "Back to results") {
			a.BackToResults()
		}
	}
	switch {
	case !h.loaded && h.err != "":
		a.historyFailCard(c, k, p, "The conversation could not be read", h.err, "history:retry", a.RetryHistory)
		return
	case !h.loaded:
		a.emptyCard(c, p, "Reading the conversation", "The window waits for the board to answer.")
		return
	}
	a.historyStatus(c, k, p, "")
	if h.missing {
		a.emptyCard(c, p, "Not in the conversation", historyMissing)
	}
	if h.rev != nil && h.rev.capped {
		a.emptyCard(c, p, "Not found yet", historyCapped)
		if kit.Button(c, k, "history:keep", "Keep looking", 140, false) {
			a.KeepLooking()
		}
	}
	if len(h.tl.Entries) == 0 {
		note := h.tl.Note
		if note == "" {
			note = "Nothing yet: the conversation appears once the supervisor speaks."
		}
		a.emptyCard(c, p, "No messages", note)
		return
	}
	for _, e := range h.tl.Entries {
		for _, s := range e.Shots {
			if s.Image && !s.Missing && inboxName.MatchString(s.Name) {
				a.shotFor(s.Name)
			}
		}
	}
	measure := kit.MeasureOf(c)
	for _, r := range historyRows(h.tl.Entries) {
		a.historyRow(c, k, p, measure, r)
	}
	if h.tl.Older > 0 {
		if kit.Button(c, k, "history:older", "Older messages", 160, h.olderBusy) {
			a.OlderHistory()
		}
		if h.olderErr != "" {
			ui.Text(c, "Older messages could not be read: "+h.olderErr).FontSize(kit.Meta.Size).TextColor(p.fail)
		}
	}
}

func historyRows(newest []Entry) []row {
	rows := make([]row, 0, len(newest)+2)
	prev := ""
	for _, e := range newest {
		if t, ok := parseAt(e.At); ok {
			if day := t.Format("Mon, Jan 2"); day != prev {
				rows = append(rows, row{Day: day})
				prev = day
			}
		}
		rows = append(rows, row{Entry: e})
	}
	return rows
}

func (a *App) historyRow(c *ui.Context, k kit.Colors, p palette, measure float32, r row) {
	focused := r.Day == "" && a.hi.focus != "" && entryKey(r.Entry) == a.hi.focus
	box := ui.Column(c).Margin(kit.Space14, 0, 0)
	if focused {
		box.Padding(kit.Space6).Radius(kit.RadiusLG).Border(2, p.accent).Label("Matched entry " + a.hi.focusRef)
	}
	box.Children(func() { a.timelineRow(c, k, p, measure, measure, r) })
	if focused && !a.hi.focusDone {
		a.hi.focusDone = true
		box.ScrollIntoView()
	}
}
