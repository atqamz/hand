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
