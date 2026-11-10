package board

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

var (
	regionNames = []string{"status", "console", "timeline", "queue", "tasks"}
	lineBreaks  = strings.NewReplacer("\r\n", "\n", "\r", "\n")
)

type version struct {
	seq         int64
	pending     int
	sup         int64
	status      string
	session     string
	agent, hint string
	limited     string
	stale       string
	screen      string
	digest      string
	revision    int64
	screenNote  string
	entries     uint64
	note        string
	context     transcript.Status
	workers     int
	signals     uint64
	alerts      int64
	models      int64
	links       uint64
	boot        int64
	old         string
}

type sharedVersion struct {
	mu   sync.Mutex
	at   time.Time
	v    version
	ok   bool
	gen  uint64
	call *versionCall
}

type versionCall struct {
	done chan struct{}
	v    version
	err  error
}

const versionBudget = 30 * time.Second

func (b *Board) sharedVersion(ctx context.Context, screen bool) (version, error) {
	s := &b.versions[0]
	if screen {
		s = &b.versions[1]
	}
	s.mu.Lock()
	if s.ok && time.Since(s.at) < b.o.Tick*3/4 {
		defer s.mu.Unlock()
		return s.v, nil
	}
	c := s.call
	if c == nil {
		c = &versionCall{done: make(chan struct{})}
		s.call = c
		go b.computeVersion(s, c, s.gen, screen)
	}
	s.mu.Unlock()
	select {
	case <-c.done:
		return c.v, c.err
	case <-ctx.Done():
		return version{}, ctx.Err()
	}
}

func (b *Board) computeVersion(s *sharedVersion, c *versionCall, gen uint64, screen bool) {
	ctx, cancel := context.WithTimeout(context.Background(), versionBudget)
	defer cancel()
	v, err := b.safeVersion(ctx, screen)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.call == c {
		s.call = nil
	}
	if err == nil {
		b.computed.Add(1)
		if s.gen == gen {
			s.v, s.at, s.ok = v, time.Now(), true
		}
	}
	c.v, c.err = v, err
	close(c.done)
}

func (b *Board) safeVersion(ctx context.Context, screen bool) (v version, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("reading the board version: %v", r)
		}
	}()
	return b.version(ctx, screen)
}

func (b *Board) dropVersions() {
	for i := range b.versions {
		s := &b.versions[i]
		s.mu.Lock()
		s.ok, s.call = false, nil
		s.gen++
		s.mu.Unlock()
	}
}

func (b *Board) version(ctx context.Context, screen bool) (version, error) {
	v := version{models: b.refresh.done.Load(), boot: b.boot}
	var err error
	if v.seq, err = b.st.LastEventSeq(ctx); err != nil {
		return v, err
	}
	pending, err := b.st.PendingSupervisorInputs(ctx)
	if err != nil {
		return v, err
	}
	v.pending = len(pending)
	sup, ok, err := b.st.LatestSupervisor(ctx)
	if err != nil {
		return v, err
	}
	if ok {
		v.sup, v.status, v.session = sup.ID, sup.Status, sup.Session
		if sup.Status == state.AttemptRunning {
			live := b.pane(ctx, sup)
			v.agent, v.hint, v.limited, v.stale = live.Agent, live.Hint, live.Limited, live.Stale
			if screen {
				v.screen, v.digest, v.revision = live.Screen, live.Digest, live.Revision
				v.screenNote = live.ScreenNote
			} else if live.Blocked {
				v.revision = live.Revision
			}
		}
	}
	entries, note := b.conversation(ctx, sup, ok, false)
	sum := fnv.New64a()
	for _, e := range entries {
		_, _ = fmt.Fprintf(sum, "%s\x00%s\x00%t\x00%s\x00", e.Role, e.At, e.Queued, e.Text)
	}
	v.entries, v.note = sum.Sum64(), note
	if ok && b.o.Transcript != nil {
		v.context = b.o.Transcript.Status(sup.Harness, sup.Session)
	}
	if v.alerts, err = b.st.LatestAlertSeq(ctx); err != nil {
		return v, err
	}
	v.old, _ = b.artifactWarning()
	v.links = b.linkDigest()
	live, err := b.st.LiveAttempts(ctx)
	if err != nil {
		return v, err
	}
	signals, err := b.signals(ctx, live)
	if err != nil {
		return v, err
	}
	for id, e := range signals {
		h := fnv.New64a()
		_, _ = fmt.Fprintf(h, "%d\x00%d\x00%s", id, e.Seq, e.Kind)
		v.signals += h.Sum64()
	}
	workers, err := b.workers(ctx, live, signals, ok && sup.Status == state.AttemptRunning, false)
	v.workers = len(workers)
	return v, err
}

func pageRegions(page string) []string {
	if page == "supervisor" {
		return []string{"status", "sup"}
	}
	return regionNames
}

func (b *Board) regions(ctx context.Context, page string, q url.Values) (map[string]string, error) {
	var data map[string]any
	var err error
	if page == "supervisor" {
		data, err = b.supervisorScreenData(ctx, q)
	} else {
		data, err = b.fleetData(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	names := pageRegions(page)
	out := make(map[string]string, len(names))
	for _, name := range names {
		var dot any = data
		switch name {
		case "status":
			dot = data["Status"]
		case "console":
			dot = data["Console"]
		case "queue":
			dot = data["Needs"]
		case "sup":
			dot = data["Screen"]
		case "timeline":
			dot = data["Timeline"]
		case "tasks":
			dot = data["Tasks"]
		}
		var buf bytes.Buffer
		if err := pages.ExecuteTemplate(&buf, "region-"+name, dot); err != nil {
			return nil, err
		}
		out[name] = buf.String()
	}
	return out, nil
}

func (b *Board) events(w http.ResponseWriter, r *http.Request) {
	ctx, q := r.Context(), r.URL.Query()
	page := q.Get("page")
	names := pageRegions(page)
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	tick := time.NewTicker(b.o.Tick)
	defer tick.Stop()
	sent := map[string]string{}
	var last version
	wrote := time.Now()
	for {
		if !b.stillSignedIn(ctx) {
			return
		}
		v, err := b.sharedVersion(ctx, page == "supervisor")
		if err != nil {
			return
		}
		if len(sent) == 0 || v != last {
			html, err := b.regions(ctx, page, q)
			if err != nil {
				return
			}
			for _, name := range names {
				if old, ok := sent[name]; ok && old == html[name] {
					continue
				}
				if _, err := w.Write(event(name, html[name])); err != nil {
					return
				}
				sent[name] = html[name]
				wrote = time.Now()
			}
			if err := rc.Flush(); err != nil {
				return
			}
			last = v
		}
		if time.Since(wrote) >= b.o.Beat {
			if _, err := io.WriteString(w, "event: beat\ndata:\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
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

func event(name, html string) []byte {
	var buf bytes.Buffer
	buf.WriteString("event: " + name + "\n")
	for line := range strings.SplitSeq(lineBreaks.Replace(html), "\n") {
		buf.WriteString("data: " + line + "\n")
	}
	buf.WriteString("\n")
	return buf.Bytes()
}
