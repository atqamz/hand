package board

import (
	"net/http"
	"slices"
	"time"

	"github.com/atqamz/hand/internal/state"
)

type APIAlert struct {
	Seq       int64  `json:"seq"`
	At        string `json:"at"`
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
	Text      string `json:"text"`
	FetchedAt string `json:"fetched_at"`
	AckedAt   string `json:"acked_at"`
	DelaySecs *int64 `json:"delay_seconds"`
	Slow      bool   `json:"slow"`
}

type APIAlertStats struct {
	Total   int    `json:"total"`
	Unacked int    `json:"unacked"`
	Slow    int    `json:"slow"`
	P50Secs *int64 `json:"p50_seconds"`
	P95Secs *int64 `json:"p95_seconds"`
	MaxSecs *int64 `json:"max_seconds"`
}

type APIAlerts struct {
	Days             int           `json:"days"`
	SlowAfterSeconds int           `json:"slow_after_seconds"`
	Stats            APIAlertStats `json:"stats"`
	Alerts           []APIAlert    `json:"alerts"`
}

func apiAlertTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func apiSeconds(d time.Duration) *int64 {
	s := int64(d.Round(time.Second) / time.Second)
	return &s
}

func apiAlertsOf(rows []state.Alert) APIAlerts {
	out := APIAlerts{
		Days:             int(state.AlertKeep / (24 * time.Hour)),
		SlowAfterSeconds: int(alertSlow / time.Second),
		Stats:            APIAlertStats{Total: len(rows)},
		Alerts:           make([]APIAlert, len(rows)),
	}
	var delays []time.Duration
	for i, a := range rows {
		r := APIAlert{Seq: a.Seq, At: apiAlertTime(a.At), Kind: a.Kind, Ref: a.Ref, Text: a.Text, FetchedAt: apiAlertTime(a.FetchedAt), AckedAt: apiAlertTime(a.AckedAt)}
		if d, ok := a.Delay(); ok {
			r.DelaySecs = apiSeconds(d)
			delays = append(delays, d)
			if d > alertSlow {
				out.Stats.Slow++
				r.Slow = true
			}
		} else {
			out.Stats.Unacked++
		}
		out.Alerts[i] = r
	}
	if len(delays) > 0 {
		slices.Sort(delays)
		out.Stats.P50Secs, out.Stats.P95Secs, out.Stats.MaxSecs = apiSeconds(rank(delays, .5)), apiSeconds(rank(delays, .95)), apiSeconds(delays[len(delays)-1])
	}
	return out
}

func (b *Board) apiAlerts(w http.ResponseWriter, r *http.Request) {
	f, since, ok := b.apiSince(w, r)
	if !ok {
		return
	}
	rows, err := b.st.Alerts(r.Context())
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiOK(w, f, since, apiAlertsOf(rows))
}
