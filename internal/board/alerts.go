package board

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/atqamz/hand/internal/state"
)

const (
	alertBatch = 50
	alertSlow  = time.Minute
)

var alertWaitMax = 25 * time.Second

type alertFleet struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type alertItem struct {
	Seq  int64  `json:"seq"`
	At   string `json:"at"`
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Text string `json:"text"`
	Link string `json:"link"`
}

type alertFeed struct {
	Version int         `json:"version"`
	Fleet   alertFleet  `json:"fleet"`
	Cursor  int64       `json:"cursor"`
	More    bool        `json:"more"`
	Alerts  []alertItem `json:"alerts"`
}

func (b *Board) alertFeed(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		b.failNeutral(w, http.StatusForbidden, "The alert feed is not served to requests from other sites.")
		return
	}
	ctx := r.Context()
	stamp := r.Method == http.MethodGet
	f, err := b.st.Fleet(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	feed := alertFeed{Version: 1, Fleet: alertFleet{ID: f.ID, Name: f.Name}, Alerts: []alertItem{}}
	reply := func() {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(feed); err != nil {
			b.failErr(w, err)
			return
		}
		body := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}
	latest, err := b.st.LatestAlertSeq(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	q := r.URL.Query()
	after, err := strconv.ParseInt(q.Get("after"), 10, 64)
	if err != nil || after < 0 || after > latest {
		feed.Cursor = latest
		reply()
		return
	}
	feed.Cursor = after
	if stamp {
		if err := b.st.AckAlerts(ctx, after); err != nil {
			b.failErr(w, err)
			return
		}
	}
	wait, _ := strconv.Atoi(q.Get("wait"))
	deadline := time.NewTimer(min(time.Duration(min(max(wait, 0), 3600))*time.Second, alertWaitMax))
	defer deadline.Stop()
	tick := time.NewTicker(b.o.Tick)
	defer tick.Stop()
	for {
		rows, err := b.st.AlertsAfter(ctx, after, alertBatch+1)
		if err != nil {
			b.failErr(w, err)
			return
		}
		if len(rows) > 0 {
			feed.More = len(rows) > alertBatch
			rows = rows[:min(len(rows), alertBatch)]
			feed.Cursor = rows[len(rows)-1].Seq
			if !b.stillSignedIn(ctx) {
				b.failNeutral(w, http.StatusForbidden, sessionFaults(companionGone, true))
				return
			}
			if stamp {
				if err := b.st.FetchedAlerts(ctx, rows[0].Seq, feed.Cursor); err != nil {
					b.failErr(w, err)
					return
				}
			}
			for _, a := range rows {
				feed.Alerts = append(feed.Alerts, alertItem{Seq: a.Seq, At: a.At.UTC().Format(time.RFC3339), Kind: a.Kind, Ref: a.Ref, Text: a.Text, Link: a.Link})
			}
			reply()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			reply()
			return
		case <-tick.C:
		}
		if !b.stillSignedIn(ctx) {
			b.failNeutral(w, http.StatusForbidden, sessionFaults(companionGone, true))
			return
		}
	}
}

type alertRow struct {
	Seq                   int64
	At, Kind, Ref, Text   string
	Fetched, Acked, Delay string
	Slow                  bool
}

type alertStats struct {
	Total, Unacked, Slow int
	P50, P95, Max        string
}

func rank(sorted []time.Duration, p float64) time.Duration {
	return sorted[max(int(math.Ceil(p*float64(len(sorted))))-1, 0)]
}

func alertSummary(rows []state.Alert) (alertStats, []alertRow) {
	st := alertStats{Total: len(rows)}
	var delays []time.Duration
	out := make([]alertRow, len(rows))
	for i, a := range rows {
		r := alertRow{Seq: a.Seq, At: when(a.At.Format(time.RFC3339Nano)), Kind: a.Kind, Ref: a.Ref, Text: a.Text, Fetched: "no", Acked: "no", Delay: "no"}
		if !a.FetchedAt.IsZero() {
			r.Fetched = when(a.FetchedAt.Format(time.RFC3339Nano))
		}
		if d, ok := a.Delay(); ok {
			r.Acked, r.Delay = when(a.AckedAt.Format(time.RFC3339Nano)), d.Round(time.Second).String()
			delays = append(delays, d)
			if d > alertSlow {
				st.Slow++
				r.Slow = true
			}
		} else {
			st.Unacked++
		}
		out[i] = r
	}
	if len(delays) > 0 {
		slices.Sort(delays)
		st.P50, st.P95, st.Max = rank(delays, .5).Round(time.Second).String(), rank(delays, .95).Round(time.Second).String(), delays[len(delays)-1].Round(time.Second).String()
	}
	return st, out
}

func (b *Board) alertsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := b.st.Alerts(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	stats, list := alertSummary(rows)
	b.render(w, http.StatusOK, "alerts.html", map[string]any{
		"Title": "alerts", "Page": "alerts", "Fleets": b.fleets(ctx), "Stats": stats, "Rows": list,
		"StripData": b.stripData(ctx), "Token": b.csrf(ctx),
	})
}
