package cli

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func (f *answerFixture) replies() []string {
	f.t.Helper()
	db, err := sql.Open("sqlite", "file:"+f.db+"?mode=ro")
	if err != nil {
		f.t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT supervisor_id, session, at, n, body FROM supervisor_reply ORDER BY id`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sup, n int64
		var session, at, body string
		if err := rows.Scan(&sup, &session, &at, &n, &body); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("s%d %s %s %d %s", sup, session, at, n, body))
	}
	return out
}

func (f *answerFixture) repliedEvents() []string {
	f.t.Helper()
	events, err := f.st.RecentEventsOf(context.Background(), []string{"supervisor.replied"}, 50)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, e.Detail)
	}
	return out
}

func (f *answerFixture) ingest() { f.w.ingestReplies(context.Background()) }

func (f *answerFixture) rewrite(lines ...string) {
	f.t.Helper()
	if err := os.WriteFile(f.file, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *answerFixture) fresh() *watcher {
	return &watcher{r: f.w.r, st: f.st}
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
}

func TestALivePassStoresTheSupervisorsRepliesAndNothingElse(t *testing.T) {
	t.Parallel()
	f := newAnswerFixture(t)
	f.append(
		claudeUser("2026-11-18T10:00:00.000Z", "status?"),
		claudeReply("2026-11-18T10:00:05.000Z", "t1 is in review"),
		claudeReply("2026-11-18T10:00:05.000Z", "and t2 waits"),
		claudeUser("2026-11-18T10:01:00.000Z", "[hand v1 wake]\nattempt.reported"),
		claudeReply("2026-11-18T10:01:30.000Z", "merged"),
	)
	f.ingest()
	eq(t, "rows", f.replies(), []string{
		"s1 sess1 2026-11-18T10:00:05.000Z 0 t1 is in review",
		"s1 sess1 2026-11-18T10:00:05.000Z 1 and t2 waits",
		"s1 sess1 2026-11-18T10:01:30.000Z 0 merged",
	})
	eq(t, "events", f.repliedEvents(), []string{"s1: y1..y3 (3)"})
	last, _ := f.st.LastEventSeq(context.Background())
	f.ingest()
	if after, _ := f.st.LastEventSeq(context.Background()); after != last || len(f.replies()) != 3 {
		t.Fatalf("a second pass wrote: events %d to %d, %d rows", last, after, len(f.replies()))
	}
}

func TestALaterPassStoresOnlyWhatIsNew(t *testing.T) {
	t.Parallel()
	f := newAnswerFixture(t)
	f.append(claudeUser("2026-11-18T10:00:00.000Z", "go"), claudeReply("2026-11-18T10:00:05.000Z", "first"))
	f.ingest()
	f.append(claudeReply("2026-11-18T10:00:05.000Z", "same time"), claudeReply("2026-11-18T10:00:09.000Z", "later"))
	f.ingest()
	eq(t, "rows", f.replies(), []string{
		"s1 sess1 2026-11-18T10:00:05.000Z 0 first",
		"s1 sess1 2026-11-18T10:00:05.000Z 1 same time",
		"s1 sess1 2026-11-18T10:00:09.000Z 0 later",
	})
	eq(t, "events", f.repliedEvents(), []string{"s1: y1", "s1: y2..y3 (2)"})
}

func TestTheWatermarkSkipsWhatItStoredAndARestartRereadsTheWholeTranscriptHarmlessly(t *testing.T) {
	t.Parallel()
	f := newAnswerFixture(t)
	f.append(claudeUser("2026-11-18T10:00:00.000Z", "go"), claudeReply("2026-11-18T10:00:05.000Z", "first"), claudeReply("2026-11-18T10:00:09.000Z", "second"))
	f.ingest()
	db, err := sql.Open("sqlite", "file:"+f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM supervisor_reply`); err != nil {
		t.Fatal(err)
	}
	f.ingest()
	if got := f.replies(); len(got) != 0 {
		t.Fatalf("a pass past the watermark stored %q", got)
	}
	restarted := &answerFixture{t: t, w: f.fresh(), st: f.st, file: f.file, db: f.db, now: f.now}
	restarted.ingest()
	eq(t, "rows after a restart", restarted.replies(), []string{
		"s1 sess1 2026-11-18T10:00:05.000Z 0 first",
		"s1 sess1 2026-11-18T10:00:09.000Z 0 second",
	})
	last, _ := f.st.LastEventSeq(context.Background())
	again := f.fresh()
	again.ingestReplies(context.Background())
	if after, _ := f.st.LastEventSeq(context.Background()); after != last || len(f.replies()) != 2 {
		t.Fatalf("a second restart wrote: events %d to %d, %d rows", last, after, len(f.replies()))
	}
}

func TestAnEndedSupervisorsLastRepliesAreStoredByTheNextSweep(t *testing.T) {
	t.Parallel()
	f := newAnswerFixture(t)
	ctx := context.Background()
	f.append(claudeUser("2026-11-18T10:00:00.000Z", "go"), claudeReply("2026-11-18T10:00:05.000Z", "first"))
	f.ingest()
	f.append(claudeReply("2026-11-18T10:00:20.000Z", "last words"))
	if _, err := f.st.EndSupervisor(ctx, 1, state.AttemptStopped, "stopped"); err != nil {
		t.Fatal(err)
	}
	next, err := f.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Argv: []string{"claude"}, Session: "sess2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.SupervisorRunning(ctx, next.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t2", PaneID: "1", PID: 2, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	f.ingest()
	eq(t, "rows", f.replies(), []string{
		"s1 sess1 2026-11-18T10:00:05.000Z 0 first",
		"s1 sess1 2026-11-18T10:00:20.000Z 0 last words",
	})
	f.ingest()
	if len(f.replies()) != 2 {
		t.Fatalf("the ended pass repeated: %q", f.replies())
	}
}

func TestAWatcherStartedAfterAStopStoresTheLastSupervisorsReplies(t *testing.T) {
	t.Parallel()
	f := newAnswerFixture(t)
	f.append(claudeUser("2026-11-18T10:00:00.000Z", "go"), claudeReply("2026-11-18T10:00:05.000Z", "before the stop"))
	if _, err := f.st.EndSupervisor(context.Background(), 1, state.AttemptStopped, "stopped"); err != nil {
		t.Fatal(err)
	}
	f.ingest()
	eq(t, "rows", f.replies(), []string{"s1 sess1 2026-11-18T10:00:05.000Z 0 before the stop"})
	f.ingest()
	if len(f.replies()) != 1 || len(f.repliedEvents()) != 1 {
		t.Fatalf("repeat: %q, %q", f.replies(), f.repliedEvents())
	}
}

func TestATranscriptThatShrinksAndIsReadAgainStoresNothingTwice(t *testing.T) {
	t.Parallel()
	f := newAnswerFixture(t)
	f.append(claudeUser("2026-11-18T10:00:00.000Z", "go"), claudeReply("2026-11-18T10:00:05.000Z", "first"), claudeReply("2026-11-18T10:00:09.000Z", "second"))
	f.ingest()
	f.rewrite(claudeUser("2026-11-18T10:00:00.000Z", "go"), claudeReply("2026-11-18T10:00:05.000Z", "first"))
	f.ingest()
	if got := f.replies(); len(got) != 2 {
		t.Fatalf("a shrunk transcript changed the rows: %q", got)
	}
	f.append(claudeReply("2026-11-18T10:00:09.000Z", "second"), claudeReply("2026-11-18T10:00:12.000Z", "third"))
	f.ingest()
	eq(t, "rows", f.replies(), []string{
		"s1 sess1 2026-11-18T10:00:05.000Z 0 first",
		"s1 sess1 2026-11-18T10:00:09.000Z 0 second",
		"s1 sess1 2026-11-18T10:00:12.000Z 0 third",
	})
	eq(t, "events", f.repliedEvents(), []string{"s1: y1..y2 (2)", "s1: y3"})
}

func TestAReaderErrorOrAMissingTranscriptIsSilent(t *testing.T) {
	t.Parallel()
	f := newAnswerFixture(t)
	f.rewrite("this is not a transcript", "{nor this")
	f.ingest()
	if len(f.replies()) != 0 || len(f.repliedEvents()) != 0 {
		t.Fatalf("an unreadable transcript stored %q, %q", f.replies(), f.repliedEvents())
	}
	f.append(claudeUser("2026-11-18T10:00:00.000Z", "go"), claudeReply("2026-11-18T10:00:05.000Z", "back"))
	f.ingest()
	eq(t, "rows once it reads", f.replies(), []string{"s1 sess1 2026-11-18T10:00:05.000Z 0 back"})
	gone := newAnswerFixture(t)
	if err := os.Remove(gone.file); err != nil {
		t.Fatal(err)
	}
	gone.ingest()
	if len(gone.replies()) != 0 || len(gone.repliedEvents()) != 0 {
		t.Fatalf("a missing transcript stored %q, %q", gone.replies(), gone.repliedEvents())
	}
}

func TestASupervisorWithoutASessionReadsNothing(t *testing.T) {
	t.Parallel()
	f := newAnswerFixture(t)
	ctx := context.Background()
	if _, err := f.st.EndSupervisor(ctx, 1, state.AttemptStopped, "stopped"); err != nil {
		t.Fatal(err)
	}
	next, err := f.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Argv: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.SupervisorRunning(ctx, next.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t2", PaneID: "1", PID: 2, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	f.append(claudeUser("2026-11-18T10:00:00.000Z", "go"), claudeReply("2026-11-18T10:00:05.000Z", "words"))
	w := f.fresh()
	w.repliesSeen, w.repliesPrev = true, state.Supervisor{ID: next.ID}
	w.ingestReplies(ctx)
	if w.replies != nil || len(f.replies()) != 0 {
		t.Fatalf("read a supervisor with no session: reader %v, rows %q", w.replies != nil, f.replies())
	}
}

func TestAReplyIsRedactedWhenTheWatcherStoresIt(t *testing.T) {
	t.Parallel()
	f := newAnswerFixture(t)
	token := "ghp_" + strings.Repeat("b", 36)
	f.append(claudeUser("2026-11-18T10:00:00.000Z", "go"), claudeReply("2026-11-18T10:00:05.000Z", "the token is "+token))
	f.ingest()
	eq(t, "rows", f.replies(), []string{"s1 sess1 2026-11-18T10:00:05.000Z 0 the token is [redacted:github-token]"})
}

func TestAnOpencodeSupervisorsRepliesAreStoredOnlyOnceItHasEnded(t *testing.T) {
	t.Parallel()
	g := newOpencodeRig(t, true, true)
	g.api = `{"data":[{"id":"ses_1","model":{"id":"gpt-5","providerID":"openai","variant":"high"}},{"id":"ses_2","model":{"id":"gpt-5","providerID":"openai","variant":"default"}}],"cursor":{}}`
	g.install(`{"info":{"id":"ses_1"},"messages":[{"type":"user","time":{"created":1793260115879},"text":"go"},{"type":"assistant","time":{"created":1793260119549},"finish":"stop","content":[{"type":"text","text":"opencode reply"}]}]}`)
	count := func() int {
		db, err := sql.Open("sqlite", "file:"+filepath.Join(g.w.r.home, "hand.db")+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM supervisor_reply WHERE body = 'opencode reply'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	g.sync()
	if g.calls("export") != 0 || count() != 0 {
		t.Fatalf("a live opencode supervisor was exported: %d exports, %d rows", g.calls("export"), count())
	}
	if _, err := g.st.EndSupervisor(context.Background(), 1, state.AttemptStopped, "stopped"); err != nil {
		t.Fatal(err)
	}
	g.sync()
	if g.calls("export") != 1 || count() != 1 {
		t.Fatalf("after the stop: %d exports, %d rows", g.calls("export"), count())
	}
}
