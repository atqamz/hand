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
	entries     uint64
	note        string
	context     transcript.Status
	workers     int
	models      int64
}

func (b *Board) version(ctx context.Context, screen bool) (version, error) {
	v := version{models: b.refresh.done.Load()}
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
			} else if live.Blocked {
				v.revision = live.Revision
			}
		}
	}
	entries, note := b.conversation(ctx, sup, ok)
	sum := fnv.New64a()
	for _, e := range entries {
		_, _ = fmt.Fprintf(sum, "%s\x00%s\x00%t\x00%s\x00", e.Role, e.At, e.Queued, e.Text)
	}
	v.entries, v.note = sum.Sum64(), note
	if ok && b.o.Transcript != nil {
		v.context = b.o.Transcript.Status(sup.Harness, sup.Session)
	}
	live, err := b.st.LiveAttempts(ctx)
	if err != nil {
		return v, err
	}
	signals, err := b.signals(ctx, live)
	if err != nil {
		return v, err
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
		v, err := b.version(ctx, page == "supervisor")
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
