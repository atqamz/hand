package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

type answerFixture struct {
	t    *testing.T
	w    *watcher
	st   *state.Store
	file string
}

func newAnswerFixture(t *testing.T) *answerFixture {
	t.Helper()
	now := time.Date(2026, 11, 17, 4, 40, 0, 0, time.UTC)
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Argv: []string{"claude"}, Session: "sess1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "1", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"HOME": t.TempDir()}
	r := &runner{env: Env{Getenv: func(k string) string { return vars[k] }, Stdout: io.Discard, Stderr: io.Discard, Now: func() time.Time { return now }}, home: t.TempDir()}
	dir := filepath.Join(vars["HOME"], ".claude", "projects", "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "sess1.jsonl")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return &answerFixture{t: t, w: &watcher{r: r, st: st}, st: st, file: file}
}

func (f *answerFixture) send(body string) int64 {
	f.t.Helper()
	in, err := f.st.AddSupervisorInput(context.Background(), body)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.st.DeliverSupervisorInput(context.Background(), in.ID, false); err != nil {
		f.t.Fatal(err)
	}
	return in.ID
}

func (f *answerFixture) append(lines ...string) {
	f.t.Helper()
	fh, err := os.OpenFile(f.file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	defer fh.Close()
	for _, l := range lines {
		if _, err := fmt.Fprintln(fh, l); err != nil {
			f.t.Fatal(err)
		}
	}
}

func claudeUser(at, text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"role":"user","content":%q}}`, at, text)
}

func claudeReply(at, text string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":%q}]}}`, at, text)
}

func (f *answerFixture) status(id int64) (string, string) {
	f.t.Helper()
	in, err := f.st.SupervisorInput(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return in.Status(), in.AnsweredAt
}

func TestAMessageIsAnsweredByTheSupervisorsFirstReplyAfterTheHarnessTookIt(t *testing.T) {
	f := newAnswerFixture(t)
	id := f.send("status?")
	f.w.stampAnswers(context.Background())
	if s, _ := f.status(id); s != "delivered" {
		t.Fatalf("with no transcript yet = %s, want delivered", s)
	}
	f.append(claudeUser("2026-11-17T04:40:03.000Z", "status?"))
	f.w.stampAnswers(context.Background())
	if s, _ := f.status(id); s != "delivered" {
		t.Fatalf("taken but not answered = %s, want delivered", s)
	}
	f.append(claudeReply("2026-11-17T04:40:09.000Z", "t1 is in review"))
	f.w.stampAnswers(context.Background())
	if s, at := f.status(id); s != "answered" || at != "2026-11-17T04:40:09.000Z" {
		t.Fatalf("after the reply = %s at %q", s, at)
	}
	events, err := f.st.RecentEventsOf(context.Background(), []string{"supervisor.answered"}, 10)
	if err != nil || len(events) != 1 || events[0].Detail != "i1" {
		t.Fatalf("events = %+v, %v", events, err)
	}
	f.w.stampAnswers(context.Background())
	if events, _ = f.st.RecentEventsOf(context.Background(), []string{"supervisor.answered"}, 10); len(events) != 1 {
		t.Fatalf("a second pass recorded another answer: %+v", events)
	}
}

func TestAReplyToTheTurnThatWasRunningDoesNotAnswerAMessageTypedIntoIt(t *testing.T) {
	f := newAnswerFixture(t)
	id := f.send("status?")
	f.append(claudeReply("2026-11-17T04:40:02.000Z", "the earlier turn is done"))
	f.w.stampAnswers(context.Background())
	if s, _ := f.status(id); s != "delivered" {
		t.Fatalf("the earlier turn's reply answered it: %s", s)
	}
	f.append(claudeUser("2026-11-17T04:40:05.000Z", "status?"), claudeReply("2026-11-17T04:40:12.000Z", "now answering"))
	f.w.stampAnswers(context.Background())
	if s, at := f.status(id); s != "answered" || at != "2026-11-17T04:40:12.000Z" {
		t.Fatalf("after its own turn = %s at %q", s, at)
	}
}

func TestTwoMessagesDeliveredBeforeOneReplyAreBothAnswered(t *testing.T) {
	f := newAnswerFixture(t)
	one, two := f.send("one"), f.send("two")
	f.append(claudeUser("2026-11-17T04:40:03.000Z", "one"), claudeUser("2026-11-17T04:40:04.000Z", "two"), claudeReply("2026-11-17T04:40:10.000Z", "both read"))
	f.w.stampAnswers(context.Background())
	for _, id := range []int64{one, two} {
		if s, at := f.status(id); s != "answered" || at != "2026-11-17T04:40:10.000Z" {
			t.Fatalf("input %d = %s at %q", id, s, at)
		}
	}
}
