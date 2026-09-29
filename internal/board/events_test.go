package board_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

type sseEvent struct{ name, data string }

func serve(t *testing.T, h http.Handler) (string, *atomic.Int32) {
	t.Helper()
	var live atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		live.Add(1)
		defer live.Add(-1)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &live
}

func stream(t *testing.T, url string) (<-chan sseEvent, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events = %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	ch := make(chan sseEvent, 64)
	go func() {
		defer close(ch)
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		sc.Split(sseLines)
		var name string
		var data []string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if name != "" || data != nil {
					ch <- sseEvent{name, strings.Join(data, "\n")}
				}
				name, data = "", nil
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = append(data, strings.TrimPrefix(line, "data: "))
			case line == "data:":
				data = append(data, "")
			}
		}
	}()
	return ch, cancel
}

func sseLines(data []byte, atEOF bool) (int, []byte, error) {
	for i, c := range data {
		switch {
		case c == '\n':
			return i + 1, data[:i], nil
		case c == '\r' && i+1 < len(data):
			if data[i+1] == '\n' {
				return i + 2, data[:i], nil
			}
			return i + 1, data[:i], nil
		case c == '\r' && atEOF:
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func collect(ch <-chan sseEvent, d time.Duration) []sseEvent {
	var out []sseEvent
	timer := time.After(d)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-timer:
			return out
		}
	}
}

func first(t *testing.T, ch <-chan sseEvent, n int) []sseEvent {
	t.Helper()
	var out []sseEvent
	deadline := time.After(3 * time.Second)
	for len(out) < n {
		select {
		case e, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed after %d events", len(out))
			}
			out = append(out, e)
		case <-deadline:
			t.Fatalf("only %d events within 3s", len(out))
		}
	}
	return out
}

func eventNames(evs []sseEvent) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.name)
	}
	return out
}

var regionNames = []string{"status", "console", "timeline", "queue", "tasks"}

func quick(st *state.Store, o board.Options) http.Handler {
	o.Tick = 10 * time.Millisecond
	return board.New(st, token, o)
}

func TestEventsNeedTheCookie(t *testing.T) {
	rec := request(quick(open(t), board.Options{}), "GET", "/events", nil, false)
	if rec.Code != http.StatusForbidden || rec.Header().Get("Content-Type") == "text/event-stream" {
		t.Fatalf("events without the cookie = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestEventsSendEveryRegionOnConnect(t *testing.T) {
	st := open(t)
	seed(t, st)
	base, _ := serve(t, quick(st, board.Options{}))
	ch, _ := stream(t, base+"/events")
	if got := eventNames(first(t, ch, 5)); !slices.Equal(got, regionNames) {
		t.Fatalf("first events = %q", got)
	}
}

func TestEventsSendOnlyChangedRegions(t *testing.T) {
	st := open(t)
	seed(t, st)
	base, _ := serve(t, quick(st, board.Options{}))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	if evs := collect(ch, 100*time.Millisecond); len(evs) != 0 {
		t.Fatalf("a quiet board sent %q", eventNames(evs))
	}
	if _, err := st.Ask(context.Background(), 2, "Ship the docs today?"); err != nil {
		t.Fatal(err)
	}
	evs := collect(ch, 300*time.Millisecond)
	got := eventNames(evs)
	if !slices.Contains(got, "queue") || slices.Contains(got, "status") || slices.Contains(got, "timeline") || len(got) != len(slices.Compact(slices.Sorted(slices.Values(got)))) {
		t.Fatalf("after a decision = %q", got)
	}
	for _, e := range evs {
		if e.name == "queue" && (!strings.Contains(e.data, "Ship the docs today?") || !strings.Contains(e.data, `data-waiting="3"`)) {
			t.Fatalf("queue event:\n%s", e.data)
		}
	}
	if evs := collect(ch, time.Second); len(evs) != 0 {
		t.Fatalf("a quiet second sent %q", eventNames(evs))
	}
}

func TestEventsFollowTheAgent(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	base, _ := serve(t, quick(fx.st, fx.options))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	fx.mu.Lock()
	fx.status, fx.hint = "blocked", "Trust this folder?"
	fx.mu.Unlock()
	evs := collect(ch, 300*time.Millisecond)
	if got := eventNames(evs); !slices.Equal(got, []string{"status", "console", "timeline", "queue"}) || !strings.Contains(evs[3].data, `name="revision" value="7"`) || strings.Contains(evs[2].data, "data-working") || strings.Contains(evs[1].data, "supervisor/interrupt") {
		t.Fatalf("after the agent blocked = %q", evs)
	}
	fx.mu.Lock()
	fx.revision = 8
	fx.mu.Unlock()
	evs = collect(ch, 300*time.Millisecond)
	if got := eventNames(evs); !slices.Equal(got, []string{"queue"}) || !strings.Contains(evs[0].data, `name="revision" value="8"`) {
		t.Fatalf("after the screen changed = %q", evs)
	}
}

func TestAClosedStreamStopsItsTicker(t *testing.T) {
	base, live := serve(t, quick(open(t), board.Options{}))
	ch, cancel := stream(t, base+"/events")
	first(t, ch, 5)
	if n := live.Load(); n != 1 {
		t.Fatalf("live streams = %d", n)
	}
	cancel()
	deadline := time.Now().Add(200 * time.Millisecond)
	for live.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the stream still runs 200ms after its client left")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNoRegionHoldsTheComposer(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	h := quick(fx.st, fx.options)
	contains(t, "fleet page", get(t, h, "/"), `id="composer"`)
	base, _ := serve(t, h)
	ch, _ := stream(t, base+"/events")
	for _, e := range first(t, ch, 5) {
		if strings.Contains(e.data, `id="composer"`) || strings.Contains(e.data, "<textarea") {
			t.Fatalf("region %s holds the composer:\n%s", e.name, e.data)
		}
	}
}

func TestRegionFragmentsMatchTheFullPage(t *testing.T) {
	fx := newFixture(t)
	seed(t, fx.st)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("hello board")})
	h := quick(fx.st, fx.options)
	page := get(t, h, "/")
	base, _ := serve(t, h)
	ch, _ := stream(t, base+"/events")
	for _, e := range first(t, ch, 5) {
		if e.data == "" || !strings.Contains(page, `data-region="`+e.name+`">`+e.data+"</section>") {
			t.Fatalf("region %s differs from the page:\n%s\n\npage:\n%s", e.name, e.data, page)
		}
	}
}

func TestFailedControlsCarryTheirMessage(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	fx.fail = fmt.Errorf("%w: supervisor s1 is still running\nfrom /home/me/fleet/hand.db", state.ErrConflict)
	rec := request(fx.handler(), "POST", "/supervisor/stop", url.Values{"csrf": {token}}, true)
	got := rec.Header().Get("X-Hand-Error")
	if rec.Code != http.StatusConflict || got != "conflict: supervisor s1 is still running from hand.db" {
		t.Fatalf("failed control = %d %q", rec.Code, got)
	}
	fx.fail = errors.New("plain failure")
	if rec := request(fx.handler(), "POST", "/supervisor/stop", url.Values{"csrf": {"stale"}}, true); rec.Header().Get("X-Hand-Error") != "this form is stale; reload the page and try again" {
		t.Fatalf("stale form = %d %q", rec.Code, rec.Header().Get("X-Hand-Error"))
	}
}

func TestACarriageReturnCannotSplitAnEvent(t *testing.T) {
	st := open(t)
	seed(t, st)
	base, _ := serve(t, quick(st, board.Options{}))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	if _, err := st.Ask(context.Background(), 2, "x\r\revent: status\rdata: forged"); err != nil {
		t.Fatal(err)
	}
	evs := collect(ch, 300*time.Millisecond)
	for _, e := range evs {
		if e.name == "status" || e.name == "" || strings.Contains(e.data, "\r") {
			t.Fatalf("a carriage return split an event: %q", evs)
		}
	}
	if got := eventNames(evs); !slices.Contains(got, "queue") {
		t.Fatalf("after the decision = %q", got)
	}
}

func TestEventsFollowAnEditedTranscript(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("a first draft of the operator message that is long")})
	base, _ := serve(t, quick(fx.st, fx.options))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	claudeLog(t, fx, []string{userRecord("edited")})
	evs := collect(ch, 300*time.Millisecond)
	if got := eventNames(evs); !slices.Equal(got, []string{"timeline"}) || !strings.Contains(evs[0].data, "edited") {
		t.Fatalf("after the transcript was rewritten = %q", evs)
	}
}
