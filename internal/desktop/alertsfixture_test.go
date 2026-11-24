package desktop_test

import (
	"context"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

func (f *fixture) Alerts(ctx context.Context, id string) (desktop.AlertsData, error) {
	f.note("alerts")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alertsReads++
	if f.alertsErr != nil {
		return desktop.AlertsData{}, f.alertsErr
	}
	return f.alerts, nil
}

func secs(n int64) *int64 { return &n }

func withAlerts(f *fixture) *fixture {
	f.features = append(f.features, "alerts")
	f.alerts = desktop.AlertsData{
		Days: 7, SlowAfter: 60,
		Stats: desktop.AlertStats{Total: 6, Unacked: 2, Slow: 1, P50: secs(11), P95: secs(95), Max: secs(95)},
		Alerts: []desktop.AlertReceipt{
			{Seq: 6, At: "2026-11-24T10:06:00Z", Kind: "worker.stuck", Ref: "tx", Text: "tx is stuck", FetchedAt: "2026-11-24T10:06:01Z", AckedAt: "2026-11-24T10:06:11Z", Delay: secs(11)},
			{Seq: 5, At: "2026-11-24T10:05:00Z", Kind: "worker.idle", Ref: "s1", Text: "s1 went quiet", FetchedAt: "2026-11-24T10:05:02Z", AckedAt: "2026-11-24T10:05:12Z", Delay: secs(10)},
			{Seq: 4, At: "2026-11-24T09:59:00Z", Kind: "decision.asked", Ref: "d4", Text: "d4 asks: which build?", FetchedAt: "2026-11-24T09:59:02Z"},
			{Seq: 3, At: "2026-11-24T09:00:00Z", Kind: "attempt.failed", Ref: "a3", Text: "a3 failed", FetchedAt: "2026-11-24T09:00:03Z", AckedAt: "2026-11-24T09:01:35Z", Delay: secs(95), Slow: true},
			{Seq: 2, At: "2026-11-23T14:30:00Z", Kind: "report.new", Ref: "r2", Text: "r2 filed for t5", FetchedAt: "2026-11-23T14:30:01Z"},
			{Seq: 1, At: "2026-11-22T08:00:00Z", Kind: "supervisor.stopped", Text: "the supervisor stopped", FetchedAt: "2026-11-22T08:00:01Z", AckedAt: "2026-11-22T08:01:00Z", Delay: secs(60)},
		},
	}
	return f
}

func alertsApp(t *testing.T, f *fixture) *desktop.App {
	t.Helper()
	app := newApp(t, f)
	app.Show(desktop.AlertsScreen)
	return app
}
