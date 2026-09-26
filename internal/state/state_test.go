package state

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var fixed = time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)

func clock() time.Time { return fixed }

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hand.db")
	s, err := Open(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestOpenCreatesCurrentSchemaAndReopens(t *testing.T) {
	s, path := openTest(t)
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	again, err := Open(path, clock)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_ = again.Close()
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	s, path := openTest(t)
	if _, err := s.db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, clock); !errors.Is(err, ErrInvalid) {
		t.Fatalf("open newer schema err = %v, want ErrInvalid", err)
	}
}

func TestConcurrentFirstOpenCreatesSchemaOnce(t *testing.T) {
	for range 20 {
		path := filepath.Join(t.TempDir(), "hand.db")
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := Open(path, clock)
				if err == nil {
					err = s.Close()
				}
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent open: %v", err)
			}
		}
	}
}

func TestFailedTransactionLeavesNoEvent(t *testing.T) {
	s, _ := openTest(t)
	boom := errors.New("boom")
	err := s.tx(context.Background(), func(tx *sql.Tx) error {
		if err := emit(tx, s.stamp(), "test.event", 0, ""); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("tx err = %v", err)
	}
	events, err := s.RecentEvents(context.Background(), 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("events = %v, %v; want none", events, err)
	}
}

func TestRecentEventsAreAscendingAndLimited(t *testing.T) {
	s, _ := openTest(t)
	for i := range 5 {
		err := s.tx(context.Background(), func(tx *sql.Tx) error {
			return emit(tx, s.stamp(), "test.event", int64(i+1), "")
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.RecentEvents(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Seq != 3 || events[2].Seq != 5 || events[2].TaskID != 5 {
		t.Fatalf("events = %+v", events)
	}
}

func TestOpenMigratesAVersionOneHome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1; INSERT INTO project(name, repo, created_at) VALUES ('hand', '/r', 'x')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := Open(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v, n int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM attempt`).Scan(&n); err != nil {
		t.Fatalf("attempt table missing: %v", err)
	}
	ps, err := s.Projects(context.Background())
	if err != nil || len(ps) != 1 || ps[0].Name != "hand" {
		t.Fatalf("projects after migration = %+v, %v", ps, err)
	}
}

func TestEventsAfterFiltersByCursorAndKind(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if seq, err := s.LastEventSeq(ctx); err != nil || seq != 0 {
		t.Fatalf("empty last seq = %d, %v", seq, err)
	}
	seedProject(t, s)
	task, _ := s.AddTask(ctx, "hand", "Fix login", "")
	_, _ = s.Transition(ctx, task.ID, StatusActive)
	d, _ := s.Ask(ctx, task.ID, "Keep it?")
	_, _ = s.Answer(ctx, d.ID, "yes", "operator")
	last, err := s.LastEventSeq(ctx)
	if err != nil || last != 5 {
		t.Fatalf("last seq = %d, %v", last, err)
	}
	all, err := s.EventsAfter(ctx, 2, nil, 10)
	if err != nil || len(all) != 3 || all[0].Seq != 3 {
		t.Fatalf("after 2 = %+v, %v", all, err)
	}
	wake, err := s.EventsAfter(ctx, 0, []string{"decision.answered"}, 10)
	if err != nil || len(wake) != 1 || wake[0].Kind != "decision.answered" || wake[0].TaskID != task.ID || wake[0].Detail != "d1" {
		t.Fatalf("wake = %+v, %v", wake, err)
	}
	if _, err := s.EventsAfter(ctx, 0, nil, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("limit 0 err = %v", err)
	}
}
