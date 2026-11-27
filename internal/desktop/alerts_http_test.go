package desktop_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

type alertsBoard struct {
	*fakeBoard
	mu          sync.Mutex
	features    []string
	cursor      string
	full        int
	text        string
	statusFails int
	notMod      int
	sinces      []string
}

func (b *alertsBoard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/"+fleetID)
	if rest == "/api/v1/status" {
		b.mu.Lock()
		fail := b.statusFails > 0
		if fail {
			b.statusFails--
		}
		b.mu.Unlock()
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
	}
	if rest != "/api/v1/alerts" && rest != "/api/v1/meta" {
		b.fakeBoard.ServeHTTP(w, r)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if rest == "/api/v1/meta" {
		json.NewEncoder(w).Encode(map[string]any{"version": 1, "data": map[string]any{"features": b.features}})
		return
	}
	since := r.URL.Query().Get("since")
	b.sinces = append(b.sinces, since)
	if since == b.cursor {
		b.notMod++
		w.WriteHeader(http.StatusNotModified)
		return
	}
	b.full++
	json.NewEncoder(w).Encode(map[string]any{
		"version": 1, "fleet": map[string]string{"id": fleetID, "name": "fake"}, "cursor": b.cursor,
		"data": map[string]any{"days": 7, "slow_after_seconds": 60,
			"stats":  map[string]any{"total": 1, "unacked": 1, "slow": 0, "p50_seconds": nil, "p95_seconds": nil, "max_seconds": nil},
			"alerts": []map[string]any{{"seq": 1, "at": "2026-11-24T09:59:00Z", "kind": "decision.asked", "ref": "d4", "text": b.bodyText(), "fetched_at": "", "acked_at": "", "delay_seconds": nil, "slow": false}}},
	})
}

func alertsHTTPApp(t *testing.T, b *alertsBoard) (*desktop.App, *fakeClock) {
	t.Helper()
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)
	c := &desktop.HTTPClient{
		Base:  srv.URL,
		Known: []desktop.Fleet{{ID: fleetID, Name: "fake"}},
		Token: func(string) (string, error) { return testToken, nil },
		HTTP:  srv.Client(),
	}
	app := newDesktop(func() (desktop.ReadModel, error) { return c, nil })
	app.DraftDir = t.TempDir()
	clock := newFakeClock()
	app.Clock = clock
	app.Boot()
	if app.Err != "" {
		t.Fatalf("boot: %s", app.Err)
	}
	return app, clock
}

func TestTheAlertsPollSendsItsCursorAndTakesA304(t *testing.T) {
	b := &alertsBoard{fakeBoard: newFakeBoard(), features: []string{"alerts"}, cursor: "c1.alerts"}
	app, clock := alertsHTTPApp(t, b)
	app.Show(desktop.AlertsScreen)
	app.StartRefresh()
	if len(app.Alerts.Alerts) != 1 {
		t.Fatalf("alerts = %+v", app.Alerts)
	}
	for range 3 {
		clock.Advance(desktop.FocusedRefresh)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.notMod == 0 || b.sinces[len(b.sinces)-1] != "c1.alerts" {
		t.Fatalf("no cursor sent or no 304 taken: sinces %q, 304s %d", b.sinces, b.notMod)
	}
	if len(app.Alerts.Alerts) != 1 || app.Stale != "" {
		t.Fatalf("a 304 changed the page: %+v stale %q", app.Alerts, app.Stale)
	}
}

func TestTheAlertsPollNeverAsksAnOldHost(t *testing.T) {
	b := &alertsBoard{fakeBoard: newFakeBoard(), features: []string{"fleets"}, cursor: "c1.alerts"}
	app, clock := alertsHTTPApp(t, b)
	app.Show(desktop.AlertsScreen)
	app.StartRefresh()
	for range 3 {
		clock.Advance(desktop.FocusedRefresh)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sinces) != 0 {
		t.Fatalf("an old host was asked for alerts: %q", b.sinces)
	}
	if app.Stale != "" {
		t.Fatalf("stale = %q on an old host", app.Stale)
	}
}

func (b *alertsBoard) bodyText() string {
	if b.text == "" {
		return "d4 asks: which build?"
	}
	return b.text
}

func TestTheAlertsPollTakesAChangedBodyThroughTheClient(t *testing.T) {
	b := &alertsBoard{fakeBoard: newFakeBoard(), features: []string{"alerts"}, cursor: "c1.alerts"}
	app, clock := alertsHTTPApp(t, b)
	app.Show(desktop.AlertsScreen)
	app.StartRefresh()
	b.mu.Lock()
	b.cursor, b.text = "c2.alerts", "d4 was answered"
	b.mu.Unlock()
	clock.Advance(desktop.FocusedRefresh)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.full < 2 {
		t.Fatalf("no second full read: %d", b.full)
	}
	if len(app.Alerts.Alerts) != 1 || app.Alerts.Alerts[0].Text != "d4 was answered" {
		t.Fatalf("poll did not carry the new body: %+v", app.Alerts)
	}
	if app.Stale != "" {
		t.Fatalf("stale = %q", app.Stale)
	}
}

func TestTheAlertsPageOnAnOldHostSaysSoAfterAFailedFirstStatus(t *testing.T) {
	b := &alertsBoard{fakeBoard: newFakeBoard(), features: []string{"fleets"}, cursor: "c1.alerts"}
	app, clock := alertsHTTPApp(t, b)
	b.mu.Lock()
	b.statusFails = 1
	b.mu.Unlock()
	app.Show(desktop.AlertsScreen)
	app.StartRefresh()
	for range 4 {
		clock.Advance(desktop.FocusedRefresh)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sinces) != 0 {
		t.Fatalf("an old host was asked for alerts: %q", b.sinces)
	}
	if app.Stale != "" {
		t.Fatalf("stale = %q", app.Stale)
	}
	tt := sized(t, app, windowWidth, windowHeight, false)
	mustText(t, tt, "This host is too old for Alerts")
}
