package board_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

const eventsPath = "/api/v1/events"

func sseData[T any](t *testing.T, e sseEvent) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(e.data), &v); err != nil {
		t.Fatalf("%s data: %v:\n%s", e.name, err, e.data)
	}
	return v
}

func apiStream(t *testing.T, fx *fixture) <-chan sseEvent {
	t.Helper()
	ch, _ := stream(t, serve(t, board.New(fx.st, token, fx.options))+eventsPath)
	first(t, ch, 3)
	return ch
}

func quiet(t *testing.T, ch <-chan sseEvent, n int) {
	t.Helper()
	for _, e := range first(t, ch, n) {
		if e.name != "beat" {
			t.Fatalf("nothing changed, yet the stream sent %q: %s", e.name, e.data)
		}
	}
}

func changes(evs []sseEvent) []string {
	return slices.DeleteFunc(eventNames(evs), func(name string) bool { return name == "beat" })
}

func TestEventsStartWithAFullSnapshot(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	ch, _ := stream(t, serve(t, board.New(fx.st, token, fx.options))+eventsPath)
	got := first(t, ch, 3)
	if names := eventNames(got); !slices.Equal(names, []string{"hello", "status", "timeline"}) {
		t.Fatalf("first events = %q", names)
	}
	hello := sseData[board.APIHello](t, got[0])
	if hello.Now != frozen.Format(time.RFC3339Nano) || sseData[struct{ Now string }](t, got[1]).Now != hello.Now {
		t.Fatalf("now = %q, status now = %q", hello.Now, got[1].data)
	}
	if hello.Version != 1 || hello.Fleet.ID != "f00000000e0aa" || hello.TickMS != apiTick.Milliseconds() || hello.BeatMS != 15000 {
		t.Fatalf("hello = %+v", hello)
	}
	if st := sseData[board.APIStatus](t, got[1]); st.Supervisor == nil || st.Supervisor.Ref == "" || st.Tasks.Active != 3 {
		t.Fatalf("status = %+v", st)
	}
	tl := sseData[board.APITimelineEvent](t, got[2])
	if !tl.Snapshot || tl.From != 0 || tl.To != tl.Older+len(tl.Entries) || !slices.ContainsFunc(tl.Entries, func(e board.APIKeyedEntry) bool { return e.Text == "All good." && e.Key != "" }) {
		t.Fatalf("timeline = %+v", tl)
	}
}

func TestEventsNeedTheSessionLikeTheOtherReads(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	rec := request(board.New(fx.st, token, fx.options), "GET", eventsPath, nil, false)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Content-Type") == "text/event-stream" {
		t.Fatalf("without the cookie = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestAQuietStreamSendsOnlyBeats(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Beat = 20 * time.Millisecond
	ch := apiStream(t, fx)
	quiet(t, ch, 4)
}

func TestAStatusChangeSendsOneStatusEvent(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Beat = 20 * time.Millisecond
	ch := apiStream(t, fx)
	fx.mu.Lock()
	fx.status = "blocked"
	fx.mu.Unlock()
	evs := until(t, ch, arrives("timeline"))
	if names := changes(evs); !slices.Equal(names, []string{"status", "timeline"}) {
		t.Fatalf("after the agent blocked = %q", names)
	}
	if st := sseData[board.APIStatus](t, evs[len(evs)-2]); st.Supervisor == nil || !st.Supervisor.Blocked || st.Supervisor.Keys == nil {
		t.Fatalf("status = %+v", st)
	}
	quiet(t, ch, 3)
}

func TestANewChatLineSendsATimelineUpsert(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Beat = 20 * time.Millisecond
	ch := apiStream(t, fx)
	claudeLog(t, fx, []string{
		rec("user", ago(20), "status?", 0),
		rec("assistant", ago(19), "All good.", 0),
		rec("assistant", ago(1), "Shipped it.", 0),
	})
	evs := until(t, ch, arrives("timeline", "Shipped it."))
	for _, e := range evs {
		if e.name == "status" || e.name == "touched" {
			t.Fatalf("a chat line sent %q: %s", e.name, e.data)
		}
	}
	tl := sseData[board.APITimelineEvent](t, evs[len(evs)-1])
	if tl.Snapshot || len(tl.Entries) != 1 || tl.Entries[0].Text != "Shipped it." || tl.To != tl.From+1 || len(tl.Drop) != 0 {
		t.Fatalf("timeline = %+v", tl)
	}
	quiet(t, ch, 3)
}

func TestATaskChangeSendsTouched(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Beat = 20 * time.Millisecond
	ch := apiStream(t, fx)
	tasks, err := fx.st.Tasks(context.Background(), nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	task := tasks[len(tasks)-1]
	if _, err := fx.st.Transition(context.Background(), task.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	evs := until(t, ch, arrives("touched"))
	touched := sseData[board.APITouched](t, evs[len(evs)-1])
	if !slices.Equal(touched.Topics, []string{"tasks"}) || !slices.Equal(touched.Tasks, []string{state.TaskRef(task.ID)}) {
		t.Fatalf("touched = %+v", touched)
	}
	quiet(t, ch, 3)
}

func TestTouchedNamesTheAttemptDecisionAndReportAnEventNames(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	ch := apiStream(t, fx)
	d, err := fx.st.Ask(context.Background(), 1, "Ship it?\n\n1. Yes\n2. No")
	if err != nil {
		t.Fatal(err)
	}
	evs := until(t, ch, arrives("touched"))
	if touched := sseData[board.APITouched](t, evs[len(evs)-1]); !slices.Equal(touched.Decisions, []string{state.DecisionRef(d.ID)}) {
		t.Fatalf("touched = %+v", touched)
	}
}

func TestACountdownDoesNotSendAnEventPerSecond(t *testing.T) {
	var mu sync.Mutex
	clock := frozen
	fx := apiFixture(t, func() time.Time { mu.Lock(); defer mu.Unlock(); return clock })
	fx.options.Beat = 20 * time.Millisecond
	fx.status = "blocked"
	fx.screen = "────────────────────────\n Bash command\n  rm -f $r/$f\n Claude Code will automatically deny this request in 4:55\n Do you want to proceed?\n❯ 1. Yes\n  2. No\n Esc to cancel · Tab to amend"
	ch, _ := stream(t, serve(t, board.New(fx.st, token, fx.options))+eventsPath)
	var need board.APINeed
	var keys board.APIKeys
	for _, e := range first(t, ch, 3) {
		if e.name == "status" {
			st := sseData[board.APIStatus](t, e)
			need = st.Needs.Items[0]
			keys = *st.Supervisor.Keys
		}
	}
	if need.DenyAt != frozen.Add(295*time.Second).Format(time.RFC3339) {
		t.Fatalf("deny_at = %q", need.DenyAt)
	}
	for second := 1; second <= 2; second++ {
		mu.Lock()
		clock = frozen.Add(time.Duration(second) * time.Second)
		mu.Unlock()
		fx.mu.Lock()
		fx.revision++
		fx.mu.Unlock()
		quiet(t, ch, 2)
	}
	if keys.Revision != 7 {
		t.Fatalf("keys = %+v", keys)
	}
	fx.mu.Lock()
	fx.screen = strings.Replace(fx.screen, "4:55", "1:00", 1)
	fx.revision++
	fx.mu.Unlock()
	evs := until(t, ch, arrives("status"))
	st := sseData[board.APIStatus](t, evs[len(evs)-1])
	if st.Needs.Items[0].DenyAt != frozen.Add(62*time.Second).Format(time.RFC3339) || st.Supervisor.Keys.Revision != 10 {
		t.Fatalf("after the countdown was reset = %+v %+v", st.Needs.Items[0], st.Supervisor.Keys)
	}
}

func TestAnOversizeEventBecomesAResync(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	claudeLog(t, fx, []string{rec("assistant", ago(1), strings.Repeat("x", 70<<10), 0)})
	ch, _ := stream(t, serve(t, board.New(fx.st, token, fx.options))+eventsPath)
	got := first(t, ch, 3)
	if names := eventNames(got); !slices.Equal(names, []string{"hello", "status", "resync"}) || got[2].data != `{"topics":["timeline"]}` {
		t.Fatalf("events = %q: %.80s", names, got[2].data)
	}
}

func TestTheStreamEndsWhenItsClientLeaves(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := board.New(fx.st, token, fx.options)
	left := make(chan struct{})
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(left)
		h.ServeHTTP(w, r)
	}))
	before := runtime.NumGoroutine()
	ch, cancel := stream(t, base+eventsPath)
	first(t, ch, 3)
	cancel()
	select {
	case <-left:
	case <-time.After(hung):
		t.Fatal("the stream still runs after its client left")
	}
	noGoroutinesLeft(t, before)
}

func noGoroutinesLeft(t *testing.T, before int) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); runtime.NumGoroutine() > before && time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Fatalf("goroutines %d after the stream ended, %d before", n, before)
	}
}

func TestTheStreamEndsWhenTheBoardShutsDown(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := board.NewHost(board.HostOptions{
		Loopback: true,
		Resolve:  func(string) (string, error) { return "/home", nil },
		Valid:    func(id, tok string) bool { return tok == token },
		Open: func(id, _ string) (http.Handler, io.Closer, error) {
			o := fx.options
			o.Base = "/" + id
			return board.New(fx.st, token, o), io.NopCloser(nil), nil
		},
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest("GET", srv.URL+"/f00000000e0aa"+eventsPath, nil)
	req.AddCookie(&http.Cookie{Name: "hand_board_f00000000e0aa", Value: token})
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("stream: %v %v", res, err)
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, res.Body); close(done) }()
	time.Sleep(3 * apiTick)
	_ = h.Close()
	select {
	case <-done:
	case <-time.After(hung):
		t.Fatal("the stream outlived the board")
	}
}

const counting = "────────────────────────\n Bash command\n  rm -f $r/$f\n Claude Code will automatically deny this request in %s\n Do you want to proceed?\n❯ 1. Yes\n  2. No\n Esc to cancel · Tab to amend"

func TestEveryCountingScreenHoldsItsDeadlineAndOnlyTheSupervisorsHoldsTheRevision(t *testing.T) {
	var mu sync.Mutex
	clock := frozen
	fx := apiFixture(t, func() time.Time { mu.Lock(); defer mu.Unlock(); return clock })
	fx.options.Beat = 20 * time.Millisecond
	fx.status = "blocked"
	fx.screen = fmt.Sprintf(counting, "4:55")
	fx.screens = map[string]string{"3": fmt.Sprintf(counting, "2:00")}
	if err := fx.st.NoteAttempt(context.Background(), 1, "blocked", "Esc to cancel · Tab to amend"); err != nil {
		t.Fatal(err)
	}
	ch, _ := stream(t, serve(t, board.New(fx.st, token, fx.options))+eventsPath)
	var st board.APIStatus
	for _, e := range first(t, ch, 3) {
		if e.name == "status" {
			st = sseData[board.APIStatus](t, e)
		}
	}
	deadlines := map[string]string{}
	for _, n := range st.Needs.Items {
		if n.DenyAt != "" {
			deadlines[n.Ref] = n.DenyAt
		}
	}
	if len(deadlines) != 2 || deadlines["s1"] == "" || deadlines["a1"] == "" {
		t.Fatalf("deadlines = %v", deadlines)
	}
	for second := 1; second <= 2; second++ {
		mu.Lock()
		clock = frozen.Add(time.Duration(second) * time.Second)
		mu.Unlock()
		fx.mu.Lock()
		fx.revision++
		fx.mu.Unlock()
		quiet(t, ch, 2)
	}
	fx.mu.Lock()
	fx.screen = fmt.Sprintf(counting, "1:00")
	fx.revision++
	revision := fx.revision
	fx.mu.Unlock()
	evs := until(t, ch, arrives("status"))
	st = sseData[board.APIStatus](t, evs[len(evs)-1])
	got := map[string]string{}
	for _, n := range st.Needs.Items {
		got[n.Ref] = n.DenyAt
	}
	if got["s1"] != frozen.Add(62*time.Second).Format(time.RFC3339) || got["a1"] != deadlines["a1"] || st.Supervisor.Keys.Revision != int64(revision) {
		t.Fatalf("after the supervisor's countdown was reset: deadlines %v, revision %d (want %d)", got, st.Supervisor.Keys.Revision, revision)
	}
}

func TestAHeadRequestEndsAtOnce(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	rec := request(board.New(fx.st, token, fx.options), "HEAD", eventsPath, nil, true)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" || rec.Body.Len() != 0 {
		t.Fatalf("HEAD = %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func TestABurstPastTheTickCapIsDrainedOnTheNextTicks(t *testing.T) {
	defer board.SetAPIEventLimits(64<<10, 2)()
	fx := apiFixture(t, func() time.Time { return frozen })
	ch := apiStream(t, fx)
	want := map[string]bool{}
	for i := range 5 {
		d, err := fx.st.Ask(context.Background(), 1, fmt.Sprintf("Question %d?\n\n1. Yes\n2. No", i))
		if err != nil {
			t.Fatal(err)
		}
		want[state.DecisionRef(d.ID)] = true
	}
	until(t, ch, func(e sseEvent) bool {
		if e.name == "touched" {
			for _, ref := range sseData[board.APITouched](t, e).Decisions {
				delete(want, ref)
			}
		}
		return len(want) == 0
	})
}

func TestAnOversizeTouchedBecomesATasksTopic(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	ch := apiStream(t, fx)
	defer board.SetAPIEventLimits(20, 10_000)()
	if _, err := fx.st.Ask(context.Background(), 1, "Ship it?\n\n1. Yes\n2. No"); err != nil {
		t.Fatal(err)
	}
	evs := until(t, ch, func(e sseEvent) bool { return e.name == "touched" })
	if got := evs[len(evs)-1].data; got != `{"topics":["tasks"]}` {
		t.Fatalf("touched = %s", got)
	}
}

const bigEntry = 16 << 20

func bigStream(t *testing.T, beat time.Duration) (net.Conn, <-chan struct{}, int) {
	t.Helper()
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Beat = beat
	claudeLog(t, fx, []string{rec("assistant", ago(1), strings.Repeat("x", bigEntry), 0)})
	t.Cleanup(board.SetAPIEventLimits(2*bigEntry, 10_000))
	h := board.New(fx.st, token, fx.options)
	left := make(chan struct{})
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(left)
		h.ServeHTTP(w, r)
	}))
	before := runtime.NumGoroutine()
	conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: x\r\nCookie: hand_board=%s\r\n\r\n", eventsPath, token); err != nil {
		t.Fatal(err)
	}
	return conn, left, before
}

func TestAClientThatNeverReadsIsCutOff(t *testing.T) {
	conn, left, before := bigStream(t, 100*time.Millisecond)
	select {
	case <-left:
	case <-time.After(hung):
		t.Fatal("the stream still waits on a client that reads nothing")
	}
	conn.Close()
	noGoroutinesLeft(t, before)
}

func TestASlowButSteadyReaderIsNotCutOff(t *testing.T) {
	conn, left, _ := bigStream(t, 4*time.Second)
	buf := make([]byte, 128<<10)
	for got := 0; got < bigEntry; {
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("cut off after %d of %d bytes: %v", got, bigEntry, err)
		}
		got += n
		time.Sleep(8 * time.Millisecond)
	}
	select {
	case <-left:
		t.Fatal("the stream ended on a client that was reading")
	default:
	}
}

type deadlineLog struct {
	*httptest.ResponseRecorder
	mu   sync.Mutex
	log  []string
	left []time.Duration
}

func (d *deadlineLog) SetWriteDeadline(t time.Time) error {
	d.mu.Lock()
	d.left = append(d.left, time.Until(t))
	d.mu.Unlock()
	return d.add("deadline")
}

func (d *deadlineLog) Write(p []byte) (int, error) {
	d.add(strings.SplitN(string(p), "\n", 2)[0])
	return d.ResponseRecorder.Write(p)
}

func (d *deadlineLog) add(s string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log = append(d.log, s)
	return nil
}

func (d *deadlineLog) events() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.log)
}

func TestEveryEventGetsItsOwnWriteDeadline(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Beat = time.Minute
	h := board.New(fx.st, token, fx.options)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", eventsPath, nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	w := &deadlineLog{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(w, req) }()
	events := func() (n int) {
		for _, line := range w.events() {
			if strings.HasPrefix(line, "event: ") {
				n++
			}
		}
		return n
	}
	for end := time.Now().Add(hung); events() < 3 && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
	}
	cancel()
	<-done
	log := w.events()
	if events() < 3 {
		t.Fatalf("log = %q", log)
	}
	for _, left := range w.left {
		if left > 2*time.Minute || left < 2*time.Minute-10*time.Second {
			t.Fatalf("a write deadline %v away, want 2 x beat", left)
		}
	}
	for i, line := range log {
		if strings.HasPrefix(line, "event: ") && (i == 0 || log[i-1] != "deadline") {
			t.Fatalf("event %q without a deadline of its own: %q", line, log)
		}
	}
}

func TestATickingCountdownScreenSendsNoEventPerSecond(t *testing.T) {
	var mu sync.Mutex
	clock := frozen
	fx := apiFixture(t, func() time.Time { mu.Lock(); defer mu.Unlock(); return clock })
	fx.options.Beat = 20 * time.Millisecond
	fx.status = "blocked"
	fx.screen = fmt.Sprintf(counting, "4:55")
	ch := apiStream(t, fx)
	for second := 1; second <= 3; second++ {
		mu.Lock()
		clock = frozen.Add(time.Duration(second) * time.Second)
		mu.Unlock()
		fx.mu.Lock()
		fx.screen = fmt.Sprintf(counting, fmt.Sprintf("4:%02d", 55-second))
		fx.revision++
		fx.mu.Unlock()
		quiet(t, ch, 2)
	}
	fx.mu.Lock()
	fx.screen = strings.Replace(fmt.Sprintf(counting, "4:52"), "rm -f $r/$f", "rm -f $r/$g", 1)
	fx.revision++
	fx.mu.Unlock()
	evs := until(t, ch, arrives("status"))
	st := sseData[board.APIStatus](t, evs[len(evs)-1])
	if st.Needs.Items[0].Excerpt == "" || !strings.Contains(st.Needs.Items[0].Excerpt, "$g") {
		t.Fatalf("a changed command did not reach the stream: %+v", st.Needs.Items[0])
	}
}

func TestABlockedWorkersChangedPromptReachesTheStreamWithinABeat(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Beat = 20 * time.Millisecond
	fx.status = "blocked"
	fx.panes = map[string]string{"3": "blocked"}
	fx.screens = map[string]string{"3": "Allow Bash?\n❯ 1. Yes\n  2. No"}
	if err := fx.st.NoteAttempt(context.Background(), 1, "blocked", "Allow Bash?"); err != nil {
		t.Fatal(err)
	}
	ch := apiStream(t, fx)
	fx.mu.Lock()
	fx.screens["3"] = "Delete the repo?\n❯ 1. Yes\n  2. No"
	fx.mu.Unlock()
	evs := until(t, ch, arrives("status", "Delete the repo?"))
	st := sseData[board.APIStatus](t, evs[len(evs)-1])
	i := slices.IndexFunc(st.Needs.Items, func(n board.APINeed) bool { return n.Kind == "worker" })
	if i < 0 || !strings.Contains(st.Needs.Items[i].Excerpt, "Delete the repo?") {
		t.Fatalf("status = %+v", st.Needs.Items)
	}
}

func TestTheStreamStopsRebuildingTheStatusOnceNoWorkerIsBlocked(t *testing.T) {
	var mu sync.Mutex
	clock := frozen
	fx := apiFixture(t, func() time.Time { mu.Lock(); defer mu.Unlock(); return clock })
	fx.options.Beat = 20 * time.Millisecond
	fx.status = "blocked"
	fx.screen = fmt.Sprintf(counting, "4:55")
	fx.panes = map[string]string{"3": "blocked"}
	fx.screens = map[string]string{"3": "Allow Bash?\n❯ 1. Yes\n  2. No"}
	if err := fx.st.NoteAttempt(context.Background(), 1, "blocked", "Allow Bash?"); err != nil {
		t.Fatal(err)
	}
	ch := apiStream(t, fx)
	fx.mu.Lock()
	fx.panes["3"] = "working"
	fx.mu.Unlock()
	until(t, ch, func(e sseEvent) bool {
		return e.name == "status" && !slices.ContainsFunc(sseData[board.APIStatus](t, e).Needs.Items, func(n board.APINeed) bool { return n.Kind == "worker" })
	})
	first(t, ch, 3)
	mu.Lock()
	clock = frozen.Add(time.Minute)
	mu.Unlock()
	if names := changes(first(t, ch, 10)); slices.Contains(names, "status") {
		t.Fatalf("the stream rebuilt the status with no worker blocked: %q", names)
	}
}
