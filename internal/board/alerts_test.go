package board_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
)

type feedBody struct {
	Version int
	Fleet   struct{ ID, Name string }
	Cursor  int64
	More    bool
	Alerts  []struct {
		Seq                       int64
		At, Kind, Ref, Text, Link string
	}
}

func alertHost(t *testing.T, names ...string) *phoneHost {
	t.Helper()
	p := newPhoneHost(t, true, names...)
	p.tick = 10 * time.Millisecond
	p.restart()
	return p
}

func raise(t *testing.T, f railFleet, n int) {
	t.Helper()
	for i := range n {
		if err := f.st.RecordAlert(context.Background(), "attempt.failed", "a"+strconv.Itoa(i+1), "a"+strconv.Itoa(i+1)+" failed: boom", "handboard://open/"+f.id+"/attempt/a"+strconv.Itoa(i+1)); err != nil {
			t.Fatal(err)
		}
	}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) feedBody {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("feed = %d:\n%s", rec.Code, rec.Body.String())
	}
	var out feedBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v:\n%s", err, rec.Body.String())
	}
	return out
}

func (p *phoneHost) feed(f railFleet, query string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	p.t.Helper()
	if len(cookies) == 0 {
		cookies = []*http.Cookie{p.laptop(f)}
	}
	return p.send("GET", "/"+f.id+"/alerts/feed"+query, nil, cookies, nil)
}

func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s took longer than %v", what, d)
	}
}

func TestTheFirstPollGetsTheCursorAndNoHistory(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raise(t, f, 3)
	for _, query := range []string{"?wait=25", "?after=&wait=25", "?after=abc&wait=25", "?after=-1&wait=25", "?after=4&wait=25", "?after=99999999999&wait=25"} {
		var rec *httptest.ResponseRecorder
		within(t, 2*time.Second, "a poll without a usable cursor"+query, func() { rec = p.feed(f, query) })
		got := decode(t, rec)
		if got.Cursor != 3 || got.More || len(got.Alerts) != 0 || got.Version != 1 || got.Fleet.ID != f.id || got.Fleet.Name != "alpha" {
			t.Fatalf("%s = %+v", query, got)
		}
		if !strings.Contains(rec.Body.String(), `"alerts":[]`) {
			t.Fatalf("%s body = %s", query, rec.Body.String())
		}
	}
	if rows, err := f.st.Alerts(context.Background()); err != nil || len(rows) != 3 || !rows[0].FetchedAt.IsZero() || !rows[0].AckedAt.IsZero() {
		t.Fatalf("a first poll touched the receipts: %+v, %v", rows, err)
	}
}

func TestAPollWithNoAlertsHasCursorZero(t *testing.T) {
	p := alertHost(t, "alpha")
	got := decode(t, p.feed(p.fleets[0], ""))
	if got.Cursor != 0 || len(got.Alerts) != 0 {
		t.Fatalf("feed = %+v", got)
	}
}

func TestAPollReturnsTheAlertsAfterItsCursorInTheDocumentedShape(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	link := "handboard://open/" + f.id + "/decision/d3"
	if err := f.st.RecordAlert(context.Background(), "decision.asked", "d3", "t3 decision: Keep the old cookie name?", link); err != nil {
		t.Fatal(err)
	}
	rec := p.feed(f, "?after=0&wait=25")
	want := `{"version":1,"fleet":{"id":"` + f.id + `","name":"alpha"},"cursor":1,"more":false,"alerts":[{"seq":1,"at":"2026-10-22T12:00:00Z","kind":"decision.asked","ref":"d3","text":"t3 decision: Keep the old cookie name?","link":"` + link + `"}]}`
	if rec.Body.String() != want {
		t.Fatalf("body =\n%s\nwant\n%s", rec.Body.String(), want)
	}
	h := rec.Header()
	if h.Get("Content-Length") != strconv.Itoa(len(want)) || h.Get("Content-Type") != "application/json; charset=utf-8" || h.Get("Cache-Control") != "no-store" || h.Get("Transfer-Encoding") != "" {
		t.Fatalf("headers = %v", h)
	}
}

func TestAPollReturnsAtMostFiftyAndSaysThereIsMore(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raise(t, f, 60)
	first := decode(t, p.feed(f, "?after=0"))
	if len(first.Alerts) != 50 || !first.More || first.Cursor != 50 || first.Alerts[0].Seq != 1 || first.Alerts[49].Seq != 50 {
		t.Fatalf("first = %d alerts, more %v, cursor %d", len(first.Alerts), first.More, first.Cursor)
	}
	second := decode(t, p.feed(f, "?after=50"))
	if len(second.Alerts) != 10 || second.More || second.Cursor != 60 || second.Alerts[0].Seq != 51 {
		t.Fatalf("second = %d alerts, more %v, cursor %d", len(second.Alerts), second.More, second.Cursor)
	}
}

func TestAPollSkipsAlertsOlderThanADay(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raise(t, f, 2)
	p.advance(25 * time.Hour)
	raise(t, f, 1)
	got := decode(t, p.feed(f, "?after=0"))
	if len(got.Alerts) != 1 || got.Alerts[0].Seq != 3 || got.Cursor != 3 {
		t.Fatalf("feed = %+v", got)
	}
	got = decode(t, p.feed(f, "?after=3"))
	if len(got.Alerts) != 0 || got.Cursor != 3 {
		t.Fatalf("feed = %+v", got)
	}
}

func TestAHeldPollWakesWithinATickOfANewAlert(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raise(t, f, 1)
	var got feedBody
	began := time.Now()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		got = decode(t, p.feed(f, "?after=1&wait=25"))
	}()
	time.Sleep(150 * time.Millisecond)
	if err := f.st.RecordAlert(context.Background(), "attempt.blocked", "a7", "a7 blocked", "handboard://open/"+f.id+"/attempt/a7"); err != nil {
		t.Fatal(err)
	}
	within(t, 3*time.Second, "the held poll", wg.Wait)
	if took := time.Since(began); took > 2*time.Second || len(got.Alerts) != 1 || got.Alerts[0].Seq != 2 || got.Alerts[0].Kind != "attempt.blocked" || got.Cursor != 2 {
		t.Fatalf("took %v: %+v", took, got)
	}
}

func TestAnEmptyPollEndsAtItsWaitAndTheWaitIsCapped(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raise(t, f, 2)
	old := *board.AlertWaitMax
	*board.AlertWaitMax = 400 * time.Millisecond
	t.Cleanup(func() { *board.AlertWaitMax = old })
	for _, query := range []string{"?after=2&wait=25", "?after=2&wait=100000", "?after=2&wait=9223372036854775807"} {
		began := time.Now()
		var rec *httptest.ResponseRecorder
		within(t, 3*time.Second, query, func() { rec = p.feed(f, query) })
		got := decode(t, rec)
		if took := time.Since(began); took < 350*time.Millisecond || got.Cursor != 2 || got.More || len(got.Alerts) != 0 {
			t.Fatalf("%s took %v: %+v", query, took, got)
		}
	}
	for _, query := range []string{"?after=2", "?after=2&wait=0", "?after=2&wait=-5", "?after=2&wait=soon"} {
		began := time.Now()
		decode(t, p.feed(f, query))
		if took := time.Since(began); took > 300*time.Millisecond {
			t.Fatalf("%s held for %v", query, took)
		}
	}
}

func TestTheNextPollAcknowledgesWhatTheLastOneFetched(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raise(t, f, 3)
	decode(t, p.feed(f, "?after=0"))
	p.advance(3 * time.Second)
	decode(t, p.feed(f, "?after=2"))
	rows, err := f.st.Alerts(context.Background())
	if err != nil || len(rows) != 3 {
		t.Fatalf("alerts = %+v, %v", rows, err)
	}
	third, first := rows[0], rows[2]
	if !first.FetchedAt.Equal(phoneEpoch) || !first.AckedAt.Equal(phoneEpoch.Add(3*time.Second)) || !rows[1].AckedAt.Equal(phoneEpoch.Add(3*time.Second)) {
		t.Fatalf("first = %+v, second = %+v", first, rows[1])
	}
	if !third.FetchedAt.Equal(phoneEpoch) || !third.AckedAt.IsZero() {
		t.Fatalf("third = %+v", third)
	}
}

func TestOnlyAGetFromTheAppStampsTheReceipts(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raise(t, f, 2)
	stamped := func() (fetched, acked int) {
		rows, err := f.st.Alerts(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range rows {
			if !a.FetchedAt.IsZero() {
				fetched++
			}
			if !a.AckedAt.IsZero() {
				acked++
			}
		}
		return
	}
	if rec := p.send("HEAD", "/"+f.id+"/alerts/feed?after=0", nil, []*http.Cookie{p.laptop(f)}, nil); rec.Code != http.StatusOK {
		t.Fatalf("HEAD = %d", rec.Code)
	}
	if fetched, acked := stamped(); fetched != 0 || acked != 0 {
		t.Fatalf("a HEAD stamped %d fetched, %d acked", fetched, acked)
	}
	rec := p.send("GET", "/"+f.id+"/alerts/feed?after=0", nil, []*http.Cookie{p.laptop(f)}, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), `"alerts"`) {
		t.Fatalf("a cross-site GET = %d:\n%s", rec.Code, rec.Body.String())
	}
	if fetched, acked := stamped(); fetched != 0 || acked != 0 {
		t.Fatalf("a cross-site GET stamped %d fetched, %d acked", fetched, acked)
	}
	for _, site := range []string{"same-origin", "same-site", "none", ""} {
		if rec := p.send("GET", "/"+f.id+"/alerts/feed?after=0", nil, []*http.Cookie{p.laptop(f)}, map[string]string{"Sec-Fetch-Site": site}); rec.Code != http.StatusOK {
			t.Fatalf("Sec-Fetch-Site %q = %d", site, rec.Code)
		}
	}
	if fetched, _ := stamped(); fetched != 2 {
		t.Fatalf("a GET stamped %d fetched", fetched)
	}
}

func TestARevokedPhoneIsRefusedWhileItsPollIsHeld(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	raise(t, f, 1)
	phone := p.pair(f, nil)
	if got := decode(t, p.feed(f, "?after=1", phone)); got.Cursor != 1 {
		t.Fatalf("a paired phone's poll = %+v", got)
	}
	var rec *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec = p.feed(f, "?after=1&wait=25", phone)
	}()
	time.Sleep(150 * time.Millisecond)
	p.revoke(f, phone)
	within(t, 3*time.Second, "the held poll after the revoke", wg.Wait)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Header().Get("X-Hand-Error"), "no%20longer%20paired") || strings.Contains(rec.Body.String(), `"alerts"`) {
		t.Fatalf("held poll after revoke = %d, %q:\n%s", rec.Code, rec.Header().Get("X-Hand-Error"), rec.Body.String())
	}
}

func TestAnUnpairedPhoneAndAnotherFleetsCookieGetNoFeed(t *testing.T) {
	r := func() *peerRig {
		p := newPhoneHost(t, true, "alpha", "bravo")
		p.tick = 10 * time.Millisecond
		p.restart()
		return servePeerRig(t, p)
	}()
	a, b := r.fleets[0], r.fleets[1]
	raise(t, a, 2)
	raise(t, b, 2)
	path := func(f railFleet) string { return "/" + f.id + "/alerts/feed?after=0&wait=25" }
	if rep := r.as(peerA).do("GET", path(a), nil, nil); rep.Code != http.StatusForbidden || strings.Contains(rep.Body, `"alerts"`) {
		t.Fatalf("an unpaired phone = %d:\n%s", rep.Code, rep.Body)
	}
	c := r.as(peerA)
	phone := r.mustPair(c, a)
	if rep := c.do("GET", path(b), nil, nil, phone); rep.Code != http.StatusForbidden || strings.Contains(rep.Body, `"alerts"`) {
		t.Fatalf("another fleet's cookie = %d:\n%s", rep.Code, rep.Body)
	}
	if rep := c.do("GET", path(a), nil, nil, r.laptop(a)); rep.Code != http.StatusForbidden || strings.Contains(rep.Body, `"alerts"`) {
		t.Fatalf("the laptop cookie over the peer listener = %d:\n%s", rep.Code, rep.Body)
	}
	if rep := c.do("GET", path(a)+"&token="+a.token, nil, nil); rep.Code != http.StatusForbidden {
		t.Fatalf("a token link over the peer listener = %d", rep.Code)
	}
	if rep := r.as(peerB).do("GET", path(a), nil, nil, phone); rep.Code != http.StatusForbidden {
		t.Fatalf("the cookie from another peer = %d", rep.Code)
	}
}

func TestAPairedPeerGetsTheFeedWithALengthAndNoSecret(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	p.tick = 10 * time.Millisecond
	p.restart()
	r := servePeerRig(t, p)
	f := r.fleets[0]
	c := r.as(peerA)
	phone := r.mustPair(c, f)
	if err := f.st.RecordAlert(context.Background(), "attempt.limited", "a4", "a4 limited: resets at 3pm", "handboard://open/"+f.id+"/attempt/a4"); err != nil {
		t.Fatal(err)
	}
	rep := c.do("GET", "/"+f.id+"/alerts/feed?after=0&wait=25", nil, map[string]string{"Connection": "close"}, phone)
	if rep.Code != http.StatusOK || rep.Header.Get("Content-Length") != strconv.Itoa(len(rep.Body)) || len(rep.Header.Values("Transfer-Encoding")) != 0 {
		t.Fatalf("feed = %d, %v:\n%s", rep.Code, rep.Header, rep.Body)
	}
	var generic map[string]any
	if err := json.Unmarshal([]byte(rep.Body), &generic); err != nil || len(generic) != 5 {
		t.Fatalf("top-level fields = %v, %v", generic, err)
	}
	item := generic["alerts"].([]any)[0].(map[string]any)
	if len(item) != 6 {
		t.Fatalf("alert fields = %v", item)
	}
	for _, secret := range []string{phone.Value, f.token, peerA, peerSecret, "/homes", "hand_phone"} {
		if strings.Contains(rep.Body, secret) {
			t.Fatalf("the feed holds %q:\n%s", secret, rep.Body)
		}
	}
}

func TestAFeedPollWhoseSendSideEndsIsRefusedNotAnswered(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	p.tick = 10 * time.Millisecond
	p.restart()
	r := servePeerRig(t, p)
	f := r.fleets[0]
	phone := r.mustPair(r.as(peerA), f)
	raise(t, f, 1)
	c := r.as(peerA)
	req, err := http.NewRequest("GET", "http://localhost/"+f.id+"/alerts/feed?after=0&wait=25", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "close")
	req.AddCookie(phone)
	_ = c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c.conn, c.line); err != nil {
		t.Fatal(err)
	}
	if err := req.Write(c.conn); err != nil {
		t.Fatal(err)
	}
	if err := c.conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(c.br, req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusForbidden || strings.Contains(string(body), `"alerts"`) {
		t.Fatalf("a poll that ended its send side = %d:\n%s", res.StatusCode, body)
	}
}

func TestTheAlertsPageListsTheWeekWithItsReceipts(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	if empty := p.send("GET", "/"+f.id+"/alerts", nil, []*http.Cookie{p.laptop(f)}, nil).Body.String(); !strings.Contains(empty, "No alerts in the last 7 days.") {
		t.Fatalf("empty page:\n%s", empty)
	}
	raise(t, f, 4)
	decode(t, p.feed(f, "?after=0"))
	p.advance(4 * time.Second)
	decode(t, p.feed(f, "?after=2"))
	p.advance(90 * time.Second)
	decode(t, p.feed(f, "?after=3"))
	rec := p.send("GET", "/"+f.id+"/alerts", nil, []*http.Cookie{p.laptop(f)}, nil)
	page := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("page = %d", rec.Code)
	}
	for _, want := range []string{
		`<span class="badge">4</span><span>alerts</span>`,
		`data-tone="wait">1</span><span>not acknowledged</span>`,
		`data-tone="wait">1</span><span>slow</span>`,
		`<span class="badge">4s</span><span>median delay</span>`,
		`<span class="badge">1m34s</span><span>95th percentile delay</span>`,
		`<span class="badge">1m34s</span><span>longest delay</span>`,
		"<td>a4 failed: boom</td>",
		`<span class="badge" data-tone="wait">1m34s</span>`,
		"Oct 22 12:00 UTC",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if !strings.Contains(page, "<td>no</td>") {
		t.Error("the unacknowledged alert shows no receipt")
	}
}

func TestTheLargestPossibleResponseStaysUnder64KiB(t *testing.T) {
	p := alertHost(t, "alpha")
	f := p.fleets[0]
	for _, glyph := range []string{"😀", `"`, "<", `\`} {
		for range 50 {
			if err := f.st.RecordAlert(context.Background(), "supervisor.blocked", "supervisor", strings.Repeat(glyph, 300), "handboard://open/"+f.id+"/attempt/a999999999"); err != nil {
				t.Fatal(err)
			}
		}
	}
	rec := p.feed(f, "?after=150")
	got := decode(t, rec)
	if len(got.Alerts) != 50 || rec.Body.Len() >= 64<<10 || len([]rune(got.Alerts[0].Text)) != 200 {
		t.Fatalf("%d alerts, %d bytes", len(got.Alerts), rec.Body.Len())
	}
	if strings.Contains(rec.Body.String(), `<`) {
		t.Fatal("the body escapes < as six bytes")
	}
}

func TestTheVectorExchangesAreWhatTheBoardAnswers(t *testing.T) {
	raw, err := os.ReadFile("../../docs/companion-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Alerts struct {
			Exchanges []struct {
				Name    string
				Given   []struct{ Kind, Ref, Text, Link string }
				Request string
				Status  int
				Body    json.RawMessage
			}
		}
	}
	if err := json.Unmarshal(raw, &v); err != nil || len(v.Alerts.Exchanges) == 0 {
		t.Fatalf("%d exchanges, %v", len(v.Alerts.Exchanges), err)
	}
	for _, x := range v.Alerts.Exchanges {
		p := alertHost(t, "hand")
		f := p.fleets[0]
		fix := func(s string) string { return strings.ReplaceAll(s, "f0123456789ab", f.id) }
		for _, g := range x.Given {
			if err := f.st.RecordAlert(context.Background(), g.Kind, g.Ref, g.Text, fix(g.Link)); err != nil {
				t.Fatal(err)
			}
		}
		rec := p.feed(f, x.Request)
		var got, want any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || json.Unmarshal([]byte(fix(string(x.Body))), &want) != nil {
			t.Fatalf("%s: %v:\n%s", x.Name, err, rec.Body.String())
		}
		if rec.Code != x.Status || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %d\n%s\nwant\n%s", x.Name, rec.Code, rec.Body.String(), fix(string(x.Body)))
		}
	}
}
