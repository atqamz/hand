package board

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/atqamz/hand/internal/state"
)

const apiDenyJitter = 2 * time.Second

var apiEventMax, apiEventsPerTick atomic.Int64

func init() {
	apiEventMax.Store(64 << 10)
	apiEventsPerTick.Store(10_000)
}

var touchedRef = regexp.MustCompile(`^[adr][1-9][0-9]*:?$`)

type APIHello struct {
	Version int      `json:"version"`
	Fleet   APIFleet `json:"fleet"`
	TickMS  int64    `json:"tick_ms"`
	BeatMS  int64    `json:"beat_ms"`
	Now     string   `json:"now"`
}

type APIStatusEvent struct {
	Now string `json:"now"`
	APIStatus
}

type APIKeyedEntry struct {
	Key string `json:"key"`
	APIEntry
}

type APITimelineEvent struct {
	Snapshot bool            `json:"snapshot,omitempty"`
	From     int             `json:"from"`
	To       int             `json:"to"`
	Older    int             `json:"older"`
	Live     bool            `json:"live"`
	Note     string          `json:"note,omitempty"`
	Working  *APIWorking     `json:"working"`
	Entries  []APIKeyedEntry `json:"entries"`
	Drop     []string        `json:"drop,omitempty"`
}

type APITouched struct {
	Topics    []string `json:"topics,omitempty"`
	Tasks     []string `json:"tasks,omitempty"`
	Attempts  []string `json:"attempts,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
	Reports   []string `json:"reports,omitempty"`
}

type APIResync struct {
	Topics []string `json:"topics"`
}

type apiSent struct{ at, raw string }

type apiStream struct {
	status   APIStatus
	statusJS string
	entries  map[string]apiSent
	meta     string
	to       int
	tasks    string
	seq      int64
	started  bool
}

type apiOut struct {
	name string
	data any
}

func (b *Board) apiEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, err := b.st.Fleet(ctx)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	tick := time.NewTicker(b.o.Tick)
	defer tick.Stop()
	deadline := func() { _ = rc.SetWriteDeadline(time.Now().Add(2 * b.o.Beat)) }
	write := func(name string, data any) error {
		deadline()
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if len(raw) > int(apiEventMax.Load()) {
			if name == "touched" {
				raw = []byte(`{"topics":["tasks"]}`)
			} else {
				name, raw = "resync", fmt.Appendf(nil, `{"topics":[%q]}`, name)
			}
		}
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, raw)
		return err
	}
	s := &apiStream{entries: map[string]apiSent{}}
	hello := APIHello{Version: apiVersion, Fleet: APIFleet{ID: f.ID, Name: f.Name}, TickMS: b.o.Tick.Milliseconds(), BeatMS: b.o.Beat.Milliseconds(), Now: b.now().UTC().Format(time.RFC3339Nano)}
	if write("hello", hello) != nil || rc.Flush() != nil {
		return
	}
	var last version
	wrote := time.Now()
	for {
		if !b.stillSignedIn(ctx) {
			return
		}
		v, err := b.sharedVersion(ctx, false)
		if err != nil {
			return
		}
		if !s.started || v != last {
			outs, more, err := b.apiDelta(ctx, s, v)
			if err != nil {
				return
			}
			for _, o := range outs {
				if write(o.name, o.data) != nil {
					return
				}
				wrote = time.Now()
			}
			if rc.Flush() != nil {
				return
			}
			if !more {
				last = v
			}
		}
		if time.Since(wrote) >= b.o.Beat {
			deadline()
			if _, err := io.WriteString(w, "event: beat\ndata:\n\n"); err != nil || rc.Flush() != nil {
				return
			}
			wrote = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (b *Board) apiDelta(ctx context.Context, s *apiStream, v version) (outs []apiOut, more bool, err error) {
	first := !s.started
	status, err := b.apiStatusData(ctx)
	if err != nil {
		return nil, false, err
	}
	s.steady(&status)
	raw, err := json.Marshal(status)
	if err != nil {
		return nil, false, err
	}
	if string(raw) != s.statusJS {
		outs = append(outs, apiOut{"status", APIStatusEvent{Now: b.now().UTC().Format(time.RFC3339Nano), APIStatus: status}})
		s.status, s.statusJS = status, string(raw)
	}
	tl, err := b.apiTimelineData(ctx, url.Values{})
	if err != nil {
		return nil, false, err
	}
	if ev, ok := s.timeline(tl); ok {
		outs = append(outs, apiOut{"timeline", ev})
	}
	tasks, err := b.apiTasksData(ctx, url.Values{})
	if err != nil {
		return nil, false, err
	}
	raw, err = json.Marshal(tasks)
	if err != nil {
		return nil, false, err
	}
	var touched APITouched
	if !first && string(raw) != s.tasks {
		touched.Topics = []string{"tasks"}
	}
	s.tasks = string(raw)
	if first {
		s.seq = v.seq
	} else {
		limit := int(apiEventsPerTick.Load())
		events, err := b.st.EventsAfter(ctx, s.seq, nil, limit)
		if err != nil {
			return nil, false, err
		}
		more = len(events) == limit
		for _, e := range events {
			s.seq = e.Seq
			touched.add(state.TaskRef(e.TaskID), e.TaskID != 0)
			words := strings.Fields(e.Detail)
			for _, word := range words[:min(2, len(words))] {
				touched.add(strings.TrimSuffix(word, ":"), touchedRef.MatchString(word))
			}
		}
	}
	s.started = true
	if touched.seal() {
		outs = append(outs, apiOut{"touched", touched})
	}
	return outs, more, nil
}

func (t *APITouched) add(ref string, ok bool) {
	if !ok {
		return
	}
	switch ref[0] {
	case 't':
		t.Tasks = append(t.Tasks, ref)
	case 'a':
		t.Attempts = append(t.Attempts, ref)
	case 'd':
		t.Decisions = append(t.Decisions, ref)
	case 'r':
		t.Reports = append(t.Reports, ref)
	}
}

func (t *APITouched) seal() bool {
	for _, refs := range []*[]string{&t.Tasks, &t.Attempts, &t.Decisions, &t.Reports} {
		slices.Sort(*refs)
		*refs = slices.Compact(*refs)
	}
	return len(t.Topics)+len(t.Tasks)+len(t.Attempts)+len(t.Decisions)+len(t.Reports) > 0
}

// A counting screen moves deny_at by a second of phase and the keys revision
// each tick, and its screen's own countdown text moves too; the CLI accepts
// a stale revision while the digest matches.
func (s *apiStream) steady(cur *APIStatus) {
	own := false
	for i := range cur.Needs.Items {
		is := &cur.Needs.Items[i]
		if is.DenyAt == "" {
			continue
		}
		j := slices.IndexFunc(s.status.Needs.Items, func(n APINeed) bool { return n.Ref == is.Ref && n.DenyAt != "" })
		if j < 0 {
			continue
		}
		was := s.status.Needs.Items[j].DenyAt
		a, _ := time.Parse(time.RFC3339, was)
		c, _ := time.Parse(time.RFC3339, is.DenyAt)
		if c.Sub(a).Abs() > apiDenyJitter {
			continue
		}
		is.DenyAt = was
		if old := s.status.Needs.Items[j].Excerpt; autoDeny.ReplaceAllLiteralString(old, "") == autoDeny.ReplaceAllLiteralString(is.Excerpt, "") {
			is.Excerpt = old
		}
		own = own || cur.Supervisor != nil && is.Ref == cur.Supervisor.Ref
	}
	if o, n := s.status.Supervisor, cur.Supervisor; own && o != nil && n != nil && o.Keys != nil && n.Keys != nil && o.Keys.Digest == n.Keys.Digest && o.Keys.Pressed == n.Keys.Pressed {
		n.Keys.Revision = o.Keys.Revision
	}
}

func apiKeyed(entries []APIEntry) []APIKeyedEntry {
	out, seen := make([]APIKeyedEntry, len(entries)), map[string]int{}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		key := e.Role + "@" + e.At
		if e.Input != 0 {
			key = "i" + strconv.FormatInt(e.Input, 10)
		}
		if seen[key]++; seen[key] > 1 {
			key += "#" + strconv.Itoa(seen[key])
		}
		out[i] = APIKeyedEntry{Key: key, APIEntry: e}
	}
	return out
}

func (s *apiStream) timeline(tl APITimeline) (APITimelineEvent, bool) {
	first := !s.started
	ev := APITimelineEvent{Snapshot: first, From: s.to, To: tl.Older + len(tl.Entries), Older: tl.Older, Live: tl.Live, Note: tl.Note, Working: tl.Working, Entries: []APIKeyedEntry{}}
	next := map[string]apiSent{}
	for _, e := range apiKeyed(tl.Entries) {
		raw, _ := json.Marshal(e)
		next[e.Key] = apiSent{e.At, string(raw)}
		if old, ok := s.entries[e.Key]; !ok || old.raw != string(raw) {
			ev.Entries = append(ev.Entries, e)
		}
	}
	var oldest time.Time
	if n := len(tl.Entries); n > 0 {
		oldest = parse(tl.Entries[n-1].At)
	}
	for key, old := range s.entries {
		if _, ok := next[key]; !ok && (len(tl.Entries) == 0 || !oldest.After(parse(old.at))) {
			ev.Drop = append(ev.Drop, key)
		}
	}
	slices.Sort(ev.Drop)
	meta, _ := json.Marshal(struct {
		Live    bool
		Note    string
		Working *APIWorking
	}{tl.Live, tl.Note, tl.Working})
	changed := first || len(ev.Entries)+len(ev.Drop) > 0 || string(meta) != s.meta
	if changed {
		s.entries, s.meta, s.to = next, string(meta), ev.To
	}
	return ev, changed
}
