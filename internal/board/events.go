package board

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
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
	stale       string
	revision    int64
	entries     uint64
	note        string
	context     transcript.Status
	workers     int
}

func (b *Board) version(ctx context.Context) (version, error) {
	var v version
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
			live := map[string]any{}
			b.live(ctx, sup, live)
			v.agent, _ = live["Agent"].(string)
			v.hint, _ = live["Hint"].(string)
			v.stale, _ = live["Stale"].(string)
			v.revision, _ = live["Revision"].(int64)
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

func (b *Board) regions(ctx context.Context, q url.Values) (map[string]string, error) {
	data, err := b.fleetData(ctx, q)
	if err != nil {
		return nil, err
	}
	data["Base"] = b.o.Base
	data["Titles"] = b.refTitle(ctx)
	out := make(map[string]string, len(regionNames))
	for _, name := range regionNames {
		var buf bytes.Buffer
		if err := pages.ExecuteTemplate(&buf, "region-"+name, data); err != nil {
			return nil, err
		}
		out[name] = buf.String()
	}
	return out, nil
}

func (b *Board) events(w http.ResponseWriter, r *http.Request) {
	ctx, q := r.Context(), r.URL.Query()
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	tick := time.NewTicker(b.o.Tick)
	defer tick.Stop()
	sent := map[string]string{}
	var last version
	for {
		v, err := b.version(ctx)
		if err != nil {
			return
		}
		if len(sent) == 0 || v != last {
			html, err := b.regions(ctx, q)
			if err != nil {
				return
			}
			for _, name := range regionNames {
				if old, ok := sent[name]; ok && old == html[name] {
					continue
				}
				if _, err := w.Write(event(name, html[name])); err != nil {
					return
				}
				sent[name] = html[name]
			}
			if err := rc.Flush(); err != nil {
				return
			}
			last = v
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
