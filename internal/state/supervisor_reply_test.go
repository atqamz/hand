package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func replyStore(t *testing.T) (*Store, string, Supervisor) {
	t.Helper()
	s, path := openTest(t)
	sup, err := s.AddSupervisor(context.Background(), SupervisorSpec{Harness: "claude", Argv: []string{"claude"}, Session: "sess1"})
	if err != nil {
		t.Fatal(err)
	}
	return s, path, sup
}

func replyRows(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT id, supervisor_id, session, at, n, body FROM supervisor_reply ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, sup, n int64
		var session, at, body string
		if err := rows.Scan(&id, &sup, &session, &at, &n, &body); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("y%d s%d %s %s %d %s", id, sup, session, at, n, body))
	}
	return out
}

func replyCount(t *testing.T, s *Store) int { return len(replyRows(t, s)) }

func TestARepliesBatchIsStoredOnceWhateverHowOftenItIsSent(t *testing.T) {
	s, _, sup := replyStore(t)
	ctx := context.Background()
	batch := []Reply{{"2026-11-18T10:00:00Z", 0, "one"}, {"2026-11-18T10:00:00Z", 1, "two"}, {"2026-11-18T10:00:05Z", 0, "three"}}
	if n, err := s.AddSupervisorReplies(ctx, sup.ID, "sess1", batch); err != nil || n != 3 {
		t.Fatalf("first insert = %d, %v", n, err)
	}
	last, _ := s.LastEventSeq(ctx)
	if n, err := s.AddSupervisorReplies(ctx, sup.ID, "sess1", batch); err != nil || n != 0 {
		t.Fatalf("second insert = %d, %v; want 0", n, err)
	}
	if after, _ := s.LastEventSeq(ctx); after != last {
		t.Fatalf("a repeat wrote an event: %d then %d", last, after)
	}
	want := []string{
		"y1 s1 sess1 2026-11-18T10:00:00Z 0 one",
		"y2 s1 sess1 2026-11-18T10:00:00Z 1 two",
		"y3 s1 sess1 2026-11-18T10:00:05Z 0 three",
	}
	if got := replyRows(t, s); !slices.Equal(got, want) {
		t.Fatalf("rows = %q", got)
	}
}

func TestTheSameSessionAndTimeUnderAnotherSupervisorRowIsNotStoredAgain(t *testing.T) {
	s, _, sup := replyStore(t)
	ctx := context.Background()
	if _, err := s.AddSupervisorReplies(ctx, sup.ID, "sess1", []Reply{{"2026-11-18T10:00:00Z", 0, "one"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SupervisorRunning(ctx, sup.ID, Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "1", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndSupervisor(ctx, sup.ID, AttemptStopped, "stopped"); err != nil {
		t.Fatal(err)
	}
	next, err := s.AddSupervisor(ctx, SupervisorSpec{Harness: "claude", Argv: []string{"claude"}, Session: "sess1"})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.AddSupervisorReplies(ctx, next.ID, "sess1", []Reply{{"2026-11-18T10:00:00Z", 0, "one"}}); err != nil || n != 0 {
		t.Fatalf("a resumed row stored the reply again: %d, %v", n, err)
	}
}

func TestConcurrentBatchesStoreEachReplyOnceAndRecordTheirOwnNewRows(t *testing.T) {
	s, _, sup := replyStore(t)
	ctx := context.Background()
	mk := func(from, to int) []Reply {
		var out []Reply
		for i := from; i < to; i++ {
			out = append(out, Reply{fmt.Sprintf("2026-11-18T10:00:%02dZ", i), 0, fmt.Sprintf("reply %d", i)})
		}
		return out
	}
	var wg sync.WaitGroup
	counts := make(chan int, 4)
	errs := make(chan error, 4)
	for _, b := range [][]Reply{mk(0, 10), mk(5, 15), mk(0, 15), mk(10, 15)} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := s.AddSupervisorReplies(ctx, sup.ID, "sess1", b)
			counts <- n
			errs <- err
		}()
	}
	wg.Wait()
	close(counts)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	total := 0
	for n := range counts {
		total += n
	}
	if total != 15 || replyCount(t, s) != 15 {
		t.Fatalf("inserted %d, rows %d; want 15 each", total, replyCount(t, s))
	}
	var events, refs int
	if err := s.db.QueryRow(`SELECT count(*) FROM event WHERE kind = 'supervisor.replied'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.RecentEventsOf(ctx, []string{"supervisor.replied"}, 10)
	for _, e := range rows {
		if strings.Contains(e.Detail, "..") {
			var a, b, c int
			if _, err := fmt.Sscanf(e.Detail, "s1: y%d..y%d (%d)", &a, &b, &c); err != nil || b-a+1 != c {
				t.Fatalf("detail %q is not an exact range", e.Detail)
			}
			refs += c
		} else {
			refs++
		}
	}
	if refs != 15 {
		t.Fatalf("events name %d replies, want 15: %+v", refs, rows)
	}
}

func TestABodyOverTheCapIsCutOnARuneBoundaryWithTheCutNamed(t *testing.T) {
	s, _, sup := replyStore(t)
	ctx := context.Background()
	for _, r := range []string{"é", "€", "𝄞"} {
		body := strings.Repeat(r, 40000/len(r))
		if _, err := s.AddSupervisorReplies(ctx, sup.ID, "sess1", []Reply{{"at-" + r, 0, body}}); err != nil {
			t.Fatalf("%q: %v", r, err)
		}
		var got string
		if err := s.db.QueryRow(`SELECT body FROM supervisor_reply WHERE at = ?`, "at-"+r).Scan(&got); err != nil {
			t.Fatal(err)
		}
		i := strings.Index(got, "[… ")
		if i < 0 || !utf8.ValidString(got) || len(got) > MaxReplyBody {
			t.Fatalf("%q: %d bytes, valid=%v, marker at %d", r, len(got), utf8.ValidString(got), i)
		}
		var cut int
		if _, err := fmt.Sscanf(got[i:], "[… %d bytes cut …]", &cut); err != nil || cut != len(body)-i || got[i:] != fmt.Sprintf("[… %d bytes cut …]", cut) {
			t.Fatalf("%q: marker %q, kept %d of %d", r, got[i:], i, len(body))
		}
		if strings.Trim(got[:i], r) != "" {
			t.Fatalf("%q: the kept part is not whole runes", r)
		}
	}
	exact := strings.Repeat("a", MaxReplyBody)
	if _, err := s.AddSupervisorReplies(ctx, sup.ID, "sess1", []Reply{{"exact", 0, exact}}); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := s.db.QueryRow(`SELECT body FROM supervisor_reply WHERE at = 'exact'`).Scan(&got); err != nil || got != exact {
		t.Fatalf("a body at the cap changed: %d bytes, %v", len(got), err)
	}
}

func TestABodyIsRedactedBeforeItIsStoredAndTheCapSeesTheRedactedText(t *testing.T) {
	s, _, sup := replyStore(t)
	token := "ghp_" + strings.Repeat("a", 36)
	if _, err := s.AddSupervisorReplies(context.Background(), sup.ID, "sess1", []Reply{{"t1", 0, "use " + token + " now"}}); err != nil {
		t.Fatal(err)
	}
	rows := replyRows(t, s)
	if len(rows) != 1 || strings.Contains(rows[0], token) || !strings.HasSuffix(rows[0], "use [redacted:github-token] now") {
		t.Fatalf("rows = %q", rows)
	}
	var detail string
	if err := s.db.QueryRow(`SELECT detail FROM event WHERE kind = 'supervisor.replied'`).Scan(&detail); err != nil || strings.Contains(detail, "use") {
		t.Fatalf("detail = %q, %v", detail, err)
	}
}

func TestAnEmptyBatchWritesNothingAndRecordsNothing(t *testing.T) {
	s, _, sup := replyStore(t)
	ctx := context.Background()
	last, _ := s.LastEventSeq(ctx)
	for _, b := range [][]Reply{nil, {}, {{"t", 0, ""}}} {
		if n, err := s.AddSupervisorReplies(ctx, sup.ID, "sess1", b); err != nil || n != 0 {
			t.Fatalf("%v: %d, %v", b, n, err)
		}
	}
	if after, _ := s.LastEventSeq(ctx); after != last || replyCount(t, s) != 0 {
		t.Fatalf("events %d to %d, rows %d", last, after, replyCount(t, s))
	}
}

func TestARepliesBatchIsRefusedWhole(t *testing.T) {
	s, _, sup := replyStore(t)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"no session": func() error { _, err := s.AddSupervisorReplies(ctx, sup.ID, "", []Reply{{"t", 0, "x"}}); return err },
		"negative number": func() error {
			_, err := s.AddSupervisorReplies(ctx, sup.ID, "s", []Reply{{"t", 0, "ok"}, {"t", -1, "x"}})
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if _, err := s.AddSupervisorReplies(ctx, 999, "s", []Reply{{"t", 0, "x"}}); err == nil {
		t.Fatal("a reply of a supervisor that does not exist was stored")
	}
	if replyCount(t, s) != 0 {
		t.Fatalf("a refused batch left rows: %q", replyRows(t, s))
	}
}

func TestTheRepliedEventNamesRefsAsOneOrARange(t *testing.T) {
	s, _, sup := replyStore(t)
	ctx := context.Background()
	add := func(rs ...Reply) {
		t.Helper()
		if _, err := s.AddSupervisorReplies(ctx, sup.ID, "sess1", rs); err != nil {
			t.Fatal(err)
		}
	}
	add(Reply{"a", 0, "secret words"})
	add(Reply{"b", 0, "x"}, Reply{"c", 0, "y"}, Reply{"d", 0, "z"})
	add(Reply{"b", 0, "x"}, Reply{"e", 0, "p"}, Reply{"c", 0, "y"}, Reply{"f", 0, "q"})
	add(Reply{"g", 0, "only"}, Reply{"g", 0, "again"})
	events, err := s.RecentEventsOf(ctx, []string{"supervisor.replied"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range events {
		if e.TaskID != 0 {
			t.Fatalf("event of a task: %+v", e)
		}
		got = append(got, e.Detail)
	}
	if want := []string{"s1: y1", "s1: y2..y4 (3)", "s1: y5..y6 (2)", "s1: y7"}; !slices.Equal(got, want) {
		t.Fatalf("details = %q, want %q", got, want)
	}
}

func TestRepliesAreNotAWake(t *testing.T) {
	if slices.Contains(WakeKinds, "supervisor.replied") {
		t.Fatal("supervisor.replied is a wake kind")
	}
	s, _, sup := replyStore(t)
	ctx := context.Background()
	before, _ := s.LastEventSeq(ctx)
	if _, err := s.AddSupervisorReplies(ctx, sup.ID, "sess1", []Reply{{"t", 0, "x"}}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.WakeEventsAfter(ctx, before, WakeKinds, 10); err != nil || len(got) != 0 {
		t.Fatalf("a reply woke the supervisor: %+v, %v", got, err)
	}
}

func TestTheOrientEventViewsSkipRepliedEvents(t *testing.T) {
	s, _, sup := replyStore(t)
	ctx := context.Background()
	if _, err := s.AddProject(ctx, "app", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	cursor, _ := s.LastEventSeq(ctx)
	for i := range 5 {
		if _, err := s.AddSupervisorReplies(ctx, sup.ID, "sess1", []Reply{{fmt.Sprint("t", i), 0, "x"}}); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := s.RecentEvents(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range recent {
		if e.Kind == "supervisor.replied" {
			t.Fatalf("recent shows %+v", e)
		}
	}
	var others int
	if err := s.db.QueryRow(`SELECT count(*) FROM event WHERE kind != 'supervisor.replied'`).Scan(&others); err != nil || others == 0 || len(recent) != min(others, 3) {
		t.Fatalf("recent = %d events of %d others, %v", len(recent), others, err)
	}
	events, total, wakes, err := s.NewestEventsAfter(ctx, cursor, WakeKinds, 10)
	if err != nil || len(events) != 0 || total != 0 || wakes != 0 {
		t.Fatalf("after = %+v, total %d, wakes %d, %v", events, total, wakes, err)
	}
	var stored int
	if err := s.db.QueryRow(`SELECT count(*) FROM event WHERE kind = 'supervisor.replied'`).Scan(&stored); err != nil || stored != 5 {
		t.Fatalf("event rows = %d, %v; they must stay in the log", stored, err)
	}
}

func TestAVersion24DatabaseMigratesToTheReplyTableKeepingItsRows(t *testing.T) {
	s, path, sup := replyStore(t)
	ctx := context.Background()
	if _, err := s.AddProject(ctx, "app", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	in, err := s.AddSupervisorInput(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE supervisor_reply`, `PRAGMA user_version = 24`, `PRAGMA wal_checkpoint(TRUNCATE)`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "hand.db")
	src, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.Create(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	_ = src.Close()
	_ = dst.Close()
	m, err := Open(copyPath, clock)
	if err != nil {
		t.Fatalf("open a version 24 copy: %v", err)
	}
	defer m.Close()
	var v int
	if err := m.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != 25 {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	if got, err := m.Supervisors(ctx); err != nil || len(got) != 1 || got[0].ID != sup.ID {
		t.Fatalf("supervisors = %+v, %v", got, err)
	}
	if got, err := m.SupervisorInput(ctx, in.ID); err != nil || got.Body != "hello" {
		t.Fatalf("input = %+v, %v", got, err)
	}
	if n, err := m.AddSupervisorReplies(ctx, sup.ID, "sess1", []Reply{{"t", 0, "after"}}); err != nil || n != 1 {
		t.Fatalf("insert after migrating = %d, %v", n, err)
	}
	var idx int
	if err := m.db.QueryRow(`SELECT count(*) FROM pragma_index_list('supervisor_reply') WHERE name IN ('supervisor_reply_sup', 'supervisor_reply_at')`).Scan(&idx); err != nil || idx != 2 {
		t.Fatalf("indexes = %d, %v", idx, err)
	}
}

func TestADatabaseAtTheNextSchemaIsRefusedAndTheRefIsY(t *testing.T) {
	if got := ReplyRef(410); got != "y410" {
		t.Fatalf("ReplyRef = %s", got)
	}
	_, path := openTest(t)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, SchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := Open(path, clock); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("newer than this hand (%d)", SchemaVersion)) {
		t.Fatalf("open = %v", err)
	}
}
