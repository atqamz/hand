package board

import (
	"cmp"
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

type timelineView struct {
	Base     string
	Titles   func(string) string
	Local    func(string) string
	Live     bool
	Controls bool
	Token    string
	Blocked  bool
	Working  *working
	Entries  []chatItem
	Older    int
	Note     string
}

func (b *Board) buildTimeline(ctx context.Context, q url.Values, sv supervisorView, c viewContext, live bool) (timelineView, error) {
	v := timelineView{Base: c.Base, Titles: c.Titles, Local: c.Local, Live: live, Controls: c.Controls, Token: c.Token}
	sup, ok, err := b.st.LatestSupervisor(ctx)
	if err != nil {
		return v, err
	}
	entries, note, sups, err := b.sessions(ctx)
	if err != nil {
		return v, err
	}
	v.Note = note
	if ok && sup.Status == state.AttemptRunning && sv.Agent == "working" && sv.Stale == "" {
		w, err := b.working(ctx, sup)
		if err != nil {
			return v, err
		}
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].Role == "supervisor" {
				w.Last, w.LastLabel = entries[i].At, elapsed(b.now().Sub(parse(entries[i].At)))
				break
			}
		}
		v.Working = &w
	}
	pending, err := b.st.PendingSupervisorInputs(ctx)
	if err != nil {
		return v, err
	}
	delivered, err := b.st.DeliveredSupervisorInputs(ctx, deliveredLookback)
	if err != nil {
		return v, err
	}
	sent := map[string][]state.SupervisorInput{}
	for _, in := range delivered {
		sent[strings.TrimSpace(in.Body)] = append(sent[strings.TrimSpace(in.Body)], in)
	}
	paired := map[int]state.SupervisorInput{}
	for i := len(entries) - 1; i >= 0; i-- {
		if k := strings.TrimSpace(entries[i].Text); entries[i].Role == "operator" && len(sent[k]) > 0 {
			paired[i], sent[k] = sent[k][0], sent[k][1:]
		}
	}
	models := map[string]string{}
	for _, s := range sups {
		models[state.SupervisorRef(s.ID)] = strings.TrimSpace(cmp.Or(s.Model, s.Harness) + " " + s.Effort)
	}
	all := make([]chatItem, 0, len(entries)+len(pending))
	no := 0
	for i, e := range entries {
		if e.Role == "hand" && launched.MatchString(e.Text) {
			continue
		}
		no++
		d := chatItem{Entry: e, Ref: refAt(sups, e.At), No: no}
		if in, ok := paired[i]; ok {
			d.Input, d.Delivered, d.Delivery = in.ID, in.DeliveredAt, "delivered"
			if in.Typed {
				d.Delivery = "typed anyway"
			}
		}
		all = append(all, d)
	}
	lines, err := b.lifecycle(ctx)
	if err != nil {
		return v, err
	}
	all = append(all, lines...)
	slices.SortStableFunc(all, func(x, y chatItem) int { return parse(x.At).Compare(parse(y.At)) })
	last := ""
	for i := range all {
		if all[i].Role == "supervisor" {
			if all[i].Ref != last {
				all[i].Model = models[all[i].Ref]
			}
			last = all[i].Ref
		}
	}
	all = groupWakes(all)
	for _, in := range pending {
		all = append(all, chatItem{Entry: transcript.Entry{Role: "operator", Text: in.Body, At: in.CreatedAt, Queued: true}, Input: in.ID, Delivery: "queued"})
	}
	for i := range all {
		if all[i].Role == "operator" {
			all[i].Text, all[i].Shots = shots(all[i].Text, b.o.Home, b.o.Base)
			for _, a := range all[i].Shots {
				b.links.add(b.o.Home, a.Path, true)
			}
		}
	}
	end := len(all)
	if n, err := strconv.Atoi(q.Get("before")); err == nil && n >= 0 && n < end {
		end = n
	}
	start := max(0, end-pageSize)
	page := slices.Clone(all[start:end])
	slices.Reverse(page)
	under := false
	for i := len(page) - 2; i >= 0; i-- {
		if day := parse(page[i].At).UTC(); day.Format(time.DateOnly) != parse(page[i+1].At).UTC().Format(time.DateOnly) {
			page[i].Day = day.Format("Mon 2 Jan") + " UTC"
			under = true
		}
		page[i].Clock = under
		for j := range page[i].Wakes {
			page[i].Wakes[j].Clock = under
		}
	}
	v.Entries, v.Older = page, start
	return v, nil
}
