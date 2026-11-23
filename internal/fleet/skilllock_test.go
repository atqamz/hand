package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/state"
)

func lockFree(t *testing.T, s Skills) bool {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(s.metaDir(), lockFileMu), os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ok, err := flock.Lock(f, false)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestSkillsLockAcquireAndRelease(t *testing.T) {
	t.Parallel()
	s := Skills{Meta: t.TempDir()}
	unlock, err := s.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if lockFree(t, s) {
		t.Fatal("lock not held after Lock")
	}
	unlock()
	if !lockFree(t, s) {
		t.Fatal("lock still held after unlock")
	}
}

func TestSkillsLockCancelledContextDoesNotAcquire(t *testing.T) {
	t.Parallel()
	s := Skills{Meta: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Lock(ctx); !errors.Is(err, context.Canceled) || !errors.Is(err, state.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if !lockFree(t, s) {
		t.Fatal("cancelled Lock left the lock held")
	}
}

func TestSkillsLockWaiterCancelAndDeadline(t *testing.T) {
	t.Parallel()
	s := Skills{Meta: t.TempDir()}
	unlock, err := s.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.Lock(ctx); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("waiter returned while the lock was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, state.ErrConflict) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter ignored cancel")
	}
	dctx, dcancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer dcancel()
	if _, err := s.Lock(dctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if lockFree(t, s) {
		t.Fatal("holder lost its lock to a timed-out waiter")
	}
	unlock()
	if !lockFree(t, s) {
		t.Fatal("timed-out waiter left the lock held")
	}
	again, err := s.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	again()
}

func TestSkillsLockSerializes(t *testing.T) {
	t.Parallel()
	s := Skills{Meta: t.TempDir()}
	first, err := s.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan func(), 1)
	go func() {
		u, err := s.Lock(context.Background())
		if err != nil {
			t.Error(err)
			return
		}
		got <- u
	}()
	select {
	case <-got:
		t.Fatal("second holder entered while the first held the lock")
	case <-time.After(100 * time.Millisecond):
	}
	first()
	select {
	case u := <-got:
		u()
	case <-time.After(5 * time.Second):
		t.Fatal("second holder never acquired after release")
	}
}
