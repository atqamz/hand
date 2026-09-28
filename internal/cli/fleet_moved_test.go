package cli_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatchExitsWhenItsHomeMoves(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	type result struct {
		errOut string
		code   int
	}
	done := make(chan result, 1)
	go func() {
		_, errOut, code := fx.h.runCtx(context.Background(), "watch", "--every", "50ms", "--notify=false")
		done <- result{errOut, code}
	}()
	eventually(t, func() bool { return fx.rt.srv.Subscribers() == 1 })
	old := fx.h.home
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(old, moved); err != nil {
		t.Fatal(err)
	}
	adopt := *fx.h
	adopt.home = moved
	adopt.ok("init")
	select {
	case r := <-done:
		if r.code != 3 || !strings.Contains(r.errOut, "restart hand watch") {
			t.Fatalf("watch after a move: code=%d stderr=%q", r.code, r.errOut)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch kept running after its home moved")
	}
	if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the watcher recreated the old home: %v", err)
	}
}

func TestWatchStopsOnTheFirstEventAfterAMove(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	done := make(chan int, 1)
	go func() {
		_, _, code := fx.h.runCtx(context.Background(), "watch", "--every", "1h", "--notify=false")
		done <- code
	}()
	eventually(t, func() bool { return fx.rt.srv.Subscribers() == 1 })
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(fx.h.home, moved); err != nil {
		t.Fatal(err)
	}
	adopt := *fx.h
	adopt.home = moved
	adopt.ok("init")
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Do you want to proceed?" })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "blocked", "agent": "claude"})
	select {
	case code := <-done:
		if code != 3 {
			t.Fatalf("watch exit = %d, want 3", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch handled an event after its home moved")
	}
	if strings.Contains(adopt.ok("wait", "--after", "0", "--timeout", "1ms"), ",attempt.blocked,") {
		t.Fatal("the moved watcher still recorded the event")
	}
}
