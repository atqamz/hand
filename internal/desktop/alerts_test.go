package desktop_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func TestTheAlertsPageShowsStatsAndReceiptsNewestFirst(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			app := alertsApp(t, withAlerts(goldenFixture(t)))
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "Fleets / golden / alerts", "Alerts", "alerts", "not acknowledged", "slow",
				"median delay", "95th percentile delay", "longest delay", "1m0s", "1m35s",
				"decision.asked", "attempt.failed", "report.new", "supervisor.stopped",
				"d4 asks: which build?", "a3 failed", "slow", "Delay no")
			var seen []string
			for _, s := range tt.Texts() {
				for _, k := range []string{"decision.asked", "attempt.failed", "report.new", "supervisor.stopped"} {
					if s == k {
						seen = append(seen, k)
					}
				}
			}
			want := []string{"decision.asked", "attempt.failed", "report.new", "supervisor.stopped"}
			if !slices.Equal(seen, want) {
				t.Fatalf("order = %v, want %v", seen, want)
			}
		})
	}
}

func TestTheAlertsPageStatsLabelEachValue(t *testing.T) {
	app := alertsApp(t, withAlerts(goldenFixture(t)))
	tt := sized(t, app, windowWidth, windowHeight, false)
	for _, label := range []string{"alerts 4", "not acknowledged 2", "slow 1", "median delay 1m0s", "95th percentile delay 1m35s", "longest delay 1m35s"} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("no stat %q in %q", label, tt.Texts())
		}
	}
}

func TestTheAlertsPageMarksOnlyTheSlowReceipt(t *testing.T) {
	app := alertsApp(t, withAlerts(goldenFixture(t)))
	tt := sized(t, app, windowWidth, windowHeight, false)
	if n := exactTexts(tt, "slow"); n != 2 {
		t.Fatalf("%d \"slow\" texts, want the stat label and one marker", n)
	}
	mustText(t, tt, "Delay 1m0s")
	if n := exactTexts(tt, "Delay no"); n != 2 {
		t.Fatalf("%d unacknowledged receipts shown, want 2", n)
	}
}

func TestTheAlertsPageWithoutReceiptsSaysSo(t *testing.T) {
	f := goldenFixture(t)
	f.features = append(f.features, "alerts")
	f.alerts = desktop.AlertsData{Days: 7, SlowAfter: 60, Alerts: []desktop.AlertReceipt{}}
	app := alertsApp(t, f)
	tt := sized(t, app, windowWidth, windowHeight, true)
	mustText(t, tt, "No alerts", "No alerts in the last 7 days.")
	mustNotText(t, tt, "median delay")
}

func TestTheAlertsPageErrorOffersRetry(t *testing.T) {
	f := withAlerts(goldenFixture(t))
	f.alertsErr = errors.New("boom")
	app := alertsApp(t, f)
	tt := sized(t, app, windowWidth, windowHeight, false)
	mustText(t, tt, "The board could not be read", "boom", "Retry")
	f.mu.Lock()
	f.alertsErr = nil
	f.mu.Unlock()
	click(t, tt, "Retry")
	mustText(t, tt, "decision.asked")
	mustNotText(t, tt, "The board could not be read")
}

func TestTheAlertsPageOnAnOldHostSaysSoWithoutAnError(t *testing.T) {
	f := goldenFixture(t)
	f.features = []string{"fleets"}
	app := alertsApp(t, f)
	tt := sized(t, app, windowWidth, windowHeight, true)
	mustText(t, tt, "This host is too old for Alerts")
	mustNotText(t, tt, "The board could not be read")
	if f.alertsReads != 0 {
		t.Fatalf("an old host was asked for alerts %d times", f.alertsReads)
	}
}

func TestTheAlertsPageKeepsReceiptsAndMarksStaleWhenTheBoardGoesAway(t *testing.T) {
	f := withAlerts(goldenFixture(t))
	app := alertsApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := sized(t, app, windowWidth, windowHeight, false)
	f.mu.Lock()
	f.pollErr = errors.New("connection refused")
	f.mu.Unlock()
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustText(t, tt, "Stale · retrying", "decision.asked")
}

func TestTheAlertsPageFollowsThePoll(t *testing.T) {
	f := withAlerts(goldenFixture(t))
	app := alertsApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := sized(t, app, windowWidth, windowHeight, false)
	f.mu.Lock()
	f.alerts.Alerts = append([]desktop.AlertReceipt{{Seq: 5, At: "2026-11-24T10:00:00Z", Kind: "attempt.failed", Ref: "a9", Text: "a9 failed"}}, f.alerts.Alerts...)
	f.alerts.Stats.Total, f.alerts.Stats.Unacked = 5, 2
	f.mu.Unlock()
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustText(t, tt, "a9 failed")
}

func TestAnAlertRefOpensItsPage(t *testing.T) {
	f := withAlerts(goldenFixture(t))
	app := alertsApp(t, f)
	tt := sized(t, app, windowWidth, windowHeight, false)
	click(t, tt, "a3")
	if app.Screen != desktop.AttemptScreen || app.Ref != "a3" {
		t.Fatalf("screen %v ref %q", app.Screen, app.Ref)
	}
	app.CloseViewer()
	if app.Screen != desktop.AlertsScreen {
		t.Fatalf("close left %v", app.Screen)
	}
}

func TestTheAlertsPageTabOrderFollowsTheRows(t *testing.T) {
	app := alertsApp(t, withAlerts(goldenFixture(t)))
	tt := sized(t, app, windowWidth, windowHeight, false)
	var order []string
	for range 60 {
		tt.Key(0, ui.KeyTab)
		for _, ref := range []string{"d4", "a3", "r2"} {
			if tt.Focused(ref) && !slices.Contains(order, ref) {
				order = append(order, ref)
			}
		}
	}
	if !slices.Equal(order, []string{"d4", "a3", "r2"}) {
		t.Fatalf("tab order = %v", order)
	}
}

func TestTheAlertsRailItemIsCurrentOnlyOnThePage(t *testing.T) {
	app := alertsApp(t, withAlerts(goldenFixture(t)))
	tt := sized(t, app, windowWidth, windowHeight, false)
	mustText(t, tt, "Alerts")
	click(t, tt, "Tasks")
	if app.Screen != desktop.TasksScreen {
		t.Fatalf("screen %v", app.Screen)
	}
	click(t, tt, "Alerts")
	if app.Screen != desktop.AlertsScreen {
		t.Fatalf("screen %v", app.Screen)
	}
}

func TestScenesTheAlertsPage(t *testing.T) {
	cases := map[string]func(*fixture){
		"90-alerts-rows": func(f *fixture) { withAlerts(f) },
		"91-alerts-empty": func(f *fixture) {
			withAlerts(f)
			f.alerts = desktop.AlertsData{Days: 7, SlowAfter: 60, Alerts: []desktop.AlertReceipt{}}
		},
		"92-alerts-error": func(f *fixture) { withAlerts(f); f.alertsErr = errors.New("the board did not answer in time") },
		"93-alerts-old":   func(f *fixture) { f.features = []string{"fleets"} },
	}
	for base, setup := range cases {
		for _, dark := range []bool{false, true} {
			name := base + "-1100x760-" + theme(dark)
			t.Run(name, func(t *testing.T) {
				f := goldenFixture(t)
				setup(f)
				app := alertsApp(t, f)
				tt := sized(t, app, windowWidth, windowHeight, dark)
				shotSized(t, tt, name, windowWidth, windowHeight)
			})
		}
	}
}

func exactTexts(tt *ui.Tester, want string) int {
	n := 0
	for _, s := range tt.Texts() {
		if s == want {
			n++
		}
	}
	return n
}
