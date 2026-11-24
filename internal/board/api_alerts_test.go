package board_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
)

func alertsOf(t *testing.T, p *companionHost, f railFleet, cookies ...*http.Cookie) board.APIAlerts {
	t.Helper()
	if len(cookies) == 0 {
		cookies = []*http.Cookie{p.host(f)}
	}
	rec := p.papi(f, "GET", "/alerts", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /alerts = %d:\n%s", rec.Code, rec.Body.String())
	}
	var env struct{ Data board.APIAlerts }
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data
}

func raiseOne(t *testing.T, f railFleet, ref, text string) {
	t.Helper()
	if err := f.st.RecordAlert(context.Background(), "attempt.failed", ref, text, "handboard://open/"+f.id+"/attempt/"+ref); err != nil {
		t.Fatal(err)
	}
}

func secs(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func TestAPIAlertsAreEmptyWithNothingRaised(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	rec := p.papi(f, "GET", "/alerts", nil, []*http.Cookie{p.host(f)})
	got := alertsOf(t, p, f)
	if got.Days != 7 || got.SlowAfterSeconds != 60 || got.Stats.Total != 0 || got.Stats.P50Secs != nil || got.Stats.P95Secs != nil || got.Stats.MaxSecs != nil || got.Alerts == nil || len(got.Alerts) != 0 {
		t.Fatalf("empty = %+v", got)
	}
	if !strings.Contains(rec.Body.String(), `"alerts":[]`) || !strings.Contains(rec.Body.String(), `"p50_seconds":null`) {
		t.Fatalf("empty body:\n%s", rec.Body.String())
	}
}

func TestAPIAlertsOneUnacknowledgedReceipt(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raiseOne(t, f, "a1", "a1 failed: boom")
	got := alertsOf(t, p, f)
	if got.Stats.Total != 1 || got.Stats.Unacked != 1 || got.Stats.Slow != 0 || got.Stats.P50Secs != nil || len(got.Alerts) != 1 {
		t.Fatalf("one = %+v", got)
	}
	a := got.Alerts[0]
	if a.Seq != 1 || a.Kind != "attempt.failed" || a.Ref != "a1" || a.Text != "a1 failed: boom" || a.At == "" || a.FetchedAt != "" || a.AckedAt != "" || a.DelaySecs != nil || a.Slow {
		t.Fatalf("receipt = %+v", a)
	}
	if _, err := time.Parse(time.RFC3339Nano, a.At); err != nil {
		t.Fatal(err)
	}
}

func TestAPIAlertsMatchTheWebPageDelaysAndRounding(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raise(t, f, 4)
	decode(t, p.feed(f, "?after=0"))
	p.advance(4 * time.Second)
	decode(t, p.feed(f, "?after=2"))
	p.advance(90*time.Second + 600*time.Millisecond)
	decode(t, p.feed(f, "?after=3"))
	got := alertsOf(t, p, f)
	if got.Stats.Total != 4 || got.Stats.Unacked != 1 || got.Stats.Slow != 1 || secs(got.Stats.P50Secs) != int64(4) || secs(got.Stats.P95Secs) != int64(95) || secs(got.Stats.MaxSecs) != int64(95) {
		t.Fatalf("stats = %+v p50=%v p95=%v max=%v", got.Stats, secs(got.Stats.P50Secs), secs(got.Stats.P95Secs), secs(got.Stats.MaxSecs))
	}
	var seqs []int64
	for _, a := range got.Alerts {
		seqs = append(seqs, a.Seq)
	}
	if len(seqs) != 4 || seqs[0] != 4 || seqs[3] != 1 {
		t.Fatalf("order = %v", seqs)
	}
	a4, a3, a1 := got.Alerts[0], got.Alerts[1], got.Alerts[3]
	if a4.DelaySecs != nil || a4.Slow || a4.AckedAt != "" || a4.FetchedAt == "" {
		t.Fatalf("a4 = %+v", a4)
	}
	if secs(a3.DelaySecs) != int64(95) || !a3.Slow || a3.AckedAt == "" {
		t.Fatalf("a3 = %+v", a3)
	}
	if secs(a1.DelaySecs) != int64(4) || a1.Slow {
		t.Fatalf("a1 = %+v", a1)
	}
}

func TestAPIAlertsSlowIsStrictlyOverAMinute(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raise(t, f, 2)
	decode(t, p.feed(f, "?after=0"))
	p.advance(time.Minute)
	decode(t, p.feed(f, "?after=2"))
	got := alertsOf(t, p, f)
	if got.Stats.Slow != 0 || secs(got.Stats.MaxSecs) != int64(60) {
		t.Fatalf("a minute = %+v", got.Stats)
	}
	raiseOne(t, f, "a9", "a9 failed")
	decode(t, p.feed(f, "?after=0"))
	p.advance(time.Minute + time.Nanosecond)
	decode(t, p.feed(f, "?after=3"))
	got = alertsOf(t, p, f)
	if got.Stats.Slow != 1 || !got.Alerts[0].Slow || secs(got.Alerts[0].DelaySecs) != int64(60) {
		t.Fatalf("a minute and a nanosecond = %+v", got)
	}
}

func TestAPIAlertsKeepSevenDaysAndDropOlder(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raiseOne(t, f, "a1", "old")
	p.advance(24 * time.Hour)
	raiseOne(t, f, "a2", "newer")
	p.advance(6*24*time.Hour - time.Second)
	if got := alertsOf(t, p, f); len(got.Alerts) != 2 || got.Stats.Total != 2 {
		t.Fatalf("just inside = %+v", got)
	}
	p.advance(time.Second)
	got := alertsOf(t, p, f)
	if len(got.Alerts) != 2 {
		t.Fatalf("exactly 7 days is kept: %+v", got)
	}
	p.advance(time.Second)
	got = alertsOf(t, p, f)
	if len(got.Alerts) != 1 || got.Alerts[0].Ref != "a2" || got.Stats.Total != 1 || got.Stats.Unacked != 1 {
		t.Fatalf("one second past 7 days = %+v", got)
	}
	p.advance(24 * time.Hour)
	if got := alertsOf(t, p, f); len(got.Alerts) != 0 || got.Stats.Total != 0 {
		t.Fatalf("all old = %+v", got)
	}
}

func TestAPIAlertsManyKeepTheOrderAndThePercentiles(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	for i := range 20 {
		raiseOne(t, f, "a"+strconv.Itoa(i+1), "alert")
		p.advance(10 * time.Second)
	}
	decode(t, p.feed(f, "?after=0"))
	decode(t, p.feed(f, "?after=20"))
	got := alertsOf(t, p, f)
	if got.Stats.Total != 20 || got.Stats.Unacked != 0 || got.Stats.Slow != 14 || len(got.Alerts) != 20 {
		t.Fatalf("many = %+v", got.Stats)
	}
	if secs(got.Stats.P50Secs) != int64(100) || secs(got.Stats.P95Secs) != int64(190) || secs(got.Stats.MaxSecs) != int64(200) {
		t.Fatalf("percentiles = %v %v %v", secs(got.Stats.P50Secs), secs(got.Stats.P95Secs), secs(got.Stats.MaxSecs))
	}
	for i, a := range got.Alerts {
		if a.Seq != int64(20-i) || secs(a.DelaySecs) != int64(10*(i+1)) {
			t.Fatalf("receipt %d = %+v", i, a)
		}
	}
}

func TestAPIAlertsCarryNoLinkTokenOrTask(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raiseOne(t, f, "a1", "a1 failed: boom")
	rec := p.papi(f, "GET", "/alerts", nil, []*http.Cookie{p.host(f)})
	var env struct {
		Data struct{ Alerts []map[string]any }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Alerts[0]) != 9 {
		t.Fatalf("receipt fields = %v", env.Data.Alerts[0])
	}
	for _, secret := range []string{"handboard://", f.token, "task_id", "hand_board_", "hand_phone", "link"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("the answer holds %q:\n%s", secret, rec.Body.String())
		}
	}
}

func TestAPIAlertsAnswerAPairedCompanionAndRefuseTheRest(t *testing.T) {
	p := alertHost(t, "alpha", "beta")
	f, other := p.fleets[0], p.fleets[1]
	raiseOne(t, f, "a1", "a1 failed")
	phone := p.pair(f, nil)
	if got := alertsOf(t, p, f, phone); len(got.Alerts) != 1 {
		t.Fatalf("paired = %+v", got)
	}
	if rec := p.papi(f, "GET", "/alerts", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie = %d", rec.Code)
	}
	if rec := p.papi(other, "GET", "/alerts", nil, []*http.Cookie{phone}); rec.Code == http.StatusOK {
		t.Fatalf("another fleet's phone cookie = %d:\n%s", rec.Code, rec.Body.String())
	}
	rec := p.papi(f, "POST", "/alerts", nil, []*http.Cookie{p.host(f)})
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST = %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
	p.revoke(f, phone)
	if rec := p.papi(f, "GET", "/alerts", nil, []*http.Cookie{phone}); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("revoked = %d", rec.Code)
	}
}

func TestAPIAlertsCursorFollowsTheDataAndReadingStampsNothing(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raiseOne(t, f, "a1", "a1 failed")
	read := func(since string) *httptest.ResponseRecorder {
		path := "/alerts"
		if since != "" {
			path += "?since=" + since
		}
		return p.papi(f, "GET", path, nil, []*http.Cookie{p.host(f)})
	}
	first := read("")
	cursor := first.Header().Get("X-Hand-Cursor")
	if cursor == "" {
		t.Fatal("no cursor")
	}
	if rec := read(cursor); rec.Code != http.StatusNotModified || rec.Header().Get("X-Hand-Cursor") != cursor {
		t.Fatalf("same data = %d", rec.Code)
	}
	if got := alertsOf(t, p, f); got.Alerts[0].FetchedAt != "" || got.Alerts[0].AckedAt != "" {
		t.Fatalf("a read stamped the receipt: %+v", got.Alerts[0])
	}
	decode(t, p.feed(f, "?after=0"))
	if rec := read(cursor); rec.Code != http.StatusOK || rec.Header().Get("X-Hand-Cursor") == cursor {
		t.Fatalf("a fetch left the cursor alone: %d", rec.Code)
	}
	if rec := read("garbage"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor = %d", rec.Code)
	}
}

func TestAPIAlertsFeedAndPageAreUntouched(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raiseOne(t, f, "a1", "a1 failed")
	alertsOf(t, p, f)
	if feed := decode(t, p.feed(f, "?after=0")); len(feed.Alerts) != 1 {
		t.Fatalf("feed = %+v", feed)
	}
}
