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
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

type sseEvent struct{ name, data string }

func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
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

const hung = time.Minute

func until(t *testing.T, ch <-chan sseEvent, stop func(sseEvent) bool) []sseEvent {
	t.Helper()
	var out []sseEvent
	deadline := time.After(hung)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed after %q", eventNames(out))
			}
			out = append(out, e)
			if stop(e) {
				return out
			}
		case <-deadline:
			t.Fatalf("the awaited event did not arrive; got %q", eventNames(out))
		}
	}
}

func first(t *testing.T, ch <-chan sseEvent, n int) []sseEvent {
	t.Helper()
	var seen int
	return until(t, ch, func(sseEvent) bool { seen++; return seen == n })
}

func arrives(name string, texts ...string) func(sseEvent) bool {
	return func(e sseEvent) bool {
		return e.name == name && !slices.ContainsFunc(texts, func(s string) bool { return !strings.Contains(e.data, s) })
	}
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
	base := serve(t, quick(st, board.Options{}))
	ch, _ := stream(t, base+"/events")
	if got := eventNames(first(t, ch, 5)); !slices.Equal(got, regionNames) {
		t.Fatalf("first events = %q", got)
	}
}

func TestAQuietStreamSendsABeatAndRefreshesNothing(t *testing.T) {
	st := open(t)
	seed(t, st)
	base := serve(t, quick(st, board.Options{Beat: 20 * time.Millisecond}))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	for _, e := range first(t, ch, 3) {
		if e.name != "beat" || e.data != "" {
			t.Fatalf("a quiet stream sent %q with %q", e.name, e.data)
		}
	}
}

func TestTheBeatWaitsFifteenSecondsByDefault(t *testing.T) {
	st := open(t)
	seed(t, st)
	base := serve(t, quick(st, board.Options{}))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	select {
	case e := <-ch:
		t.Fatalf("a quiet stream sent %q within 300ms", e.name)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestEventsSendOnlyChangedRegions(t *testing.T) {
	st := open(t)
	seed(t, st)
	base := serve(t, quick(st, board.Options{}))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	if _, err := st.Ask(context.Background(), 2, "Ship the docs today?"); err != nil {
		t.Fatal(err)
	}
	evs := until(t, ch, arrives("queue", "Ship the docs today?"))
	if _, err := st.Ask(context.Background(), 2, "Ship the docs tomorrow?"); err != nil {
		t.Fatal(err)
	}
	next := until(t, ch, arrives("queue", "Ship the docs tomorrow?"))
	if got := eventNames(evs); !slices.Equal(got, []string{"status", "queue"}) || !strings.Contains(evs[1].data, `data-waiting="4"`) {
		t.Fatalf("after a decision = %q", got)
	}
	if got := eventNames(next); !slices.Equal(got, []string{"tasks", "status", "queue"}) {
		t.Fatalf("after a second decision = %q", got)
	}
}

func TestEventsFollowTheAgent(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	base := serve(t, quick(fx.st, fx.options))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	fx.mu.Lock()
	fx.status, fx.hint = "blocked", "Trust this folder?"
	fx.mu.Unlock()
	evs := first(t, ch, 4)
	if got := eventNames(evs); !slices.Equal(got, []string{"status", "console", "timeline", "queue"}) || !strings.Contains(evs[3].data, `name="revision" value="7"`) || strings.Contains(evs[2].data, "data-working") || strings.Contains(evs[1].data, "supervisor/interrupt") {
		t.Fatalf("after the agent blocked = %q", evs)
	}
	for rev := 8; rev <= 9; rev++ {
		fx.mu.Lock()
		fx.revision = rev
		fx.mu.Unlock()
		evs = until(t, ch, arrives("queue"))
		if got := eventNames(evs); !slices.Equal(got, []string{"queue"}) || !strings.Contains(evs[0].data, fmt.Sprintf(`name="revision" value="%d"`, rev)) {
			t.Fatalf("after the screen changed to %d = %q", rev, evs)
		}
	}
}

func TestAClosedStreamStopsItsTicker(t *testing.T) {
	h := quick(open(t), board.Options{})
	left := make(chan struct{})
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(left)
		h.ServeHTTP(w, r)
	}))
	ch, cancel := stream(t, base+"/events")
	first(t, ch, 5)
	cancel()
	select {
	case <-left:
	case <-time.After(hung):
		t.Fatal("the stream still runs after its client left")
	}
}

func TestNoRegionHoldsTheComposer(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	h := quick(fx.st, fx.options)
	contains(t, "fleet page", get(t, h, "/"), `id="composer"`)
	base := serve(t, h)
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
	fx.options.Now = func() time.Time { return time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC) }
	h := quick(fx.st, fx.options)
	page := get(t, h, "/")
	contains(t, "page", page, `<time datetime="2026-09-26T00:00:00Z" data-since>144h 0m</time>`)
	base := serve(t, h)
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
	got, _ := url.PathUnescape(rec.Header().Get("X-Hand-Error"))
	if rec.Code != http.StatusConflict || got != "conflict: supervisor s1 is still running from hand.db" {
		t.Fatalf("failed control = %d %q", rec.Code, got)
	}
	fx.fail = errors.New("plain failure")
	rec = request(fx.handler(), "POST", "/supervisor/stop", url.Values{"csrf": {"stale"}}, true)
	if msg, _ := url.PathUnescape(rec.Header().Get("X-Hand-Error")); msg != "this form is stale; reload the page and try again" {
		t.Fatalf("stale form = %d %q", rec.Code, msg)
	}
}

func TestACarriageReturnCannotSplitAnEvent(t *testing.T) {
	st := open(t)
	seed(t, st)
	base := serve(t, quick(st, board.Options{}))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	if _, err := st.Ask(context.Background(), 2, "x\r\revent: status\rdata: forged"); err != nil {
		t.Fatal(err)
	}
	evs := until(t, ch, arrives("queue", "event: status"))
	if _, err := st.Ask(context.Background(), 2, "y"); err != nil {
		t.Fatal(err)
	}
	evs = append(evs, until(t, ch, arrives("queue", ">y<"))...)
	for _, e := range evs {
		if (e.name == "status" && e.data == "forged") || e.name == "" || strings.Contains(e.data, "\r") {
			t.Fatalf("a carriage return split an event: %q", evs)
		}
	}
}

func TestEventsFollowAnEditedTranscript(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("a first draft of the operator message that is long")})
	base := serve(t, quick(fx.st, fx.options))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	for _, text := range []string{"edited", "again"} {
		claudeLog(t, fx, []string{userRecord(text)})
		evs := until(t, ch, arrives("timeline", text))
		if got := eventNames(evs); !slices.Equal(got, []string{"timeline"}) {
			t.Fatalf("after the transcript was rewritten to %q = %q", text, got)
		}
	}
}

func TestEventsPushWhenAQuietWorkerOutlastsTheGrace(t *testing.T) {
	fx := newFixture(t)
	fx.status = "idle"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if _, _, err := fx.st.RecordQuiet(context.Background(), a.ID, ""); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	now := time.Date(2026, 9, 26, 0, 9, 0, 0, time.UTC)
	fx.options.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	base := serve(t, quick(fx.st, fx.options))
	ch, _ := stream(t, base+"/events")
	first(t, ch, 5)
	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()
	until(t, ch, arrives("queue", `data-kind="quiet"`))
}
