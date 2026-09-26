package cli_test

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
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

func TestBoardStopsServingAMovedHome(t *testing.T) {
	h := initWithProject(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		_, _, code := h.runCtx(ctx, "board", "--addr", addr)
		done <- code
	}()
	get := func() int {
		resp, err := http.Get("http://" + addr + "/")
		if err != nil {
			return 0
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	eventually(t, func() bool { c := get(); return c != 0 && c != http.StatusServiceUnavailable })
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(h.home, moved); err != nil {
		t.Fatal(err)
	}
	adopt := *h
	adopt.home = moved
	adopt.ok("init")
	if c := get(); c != http.StatusServiceUnavailable {
		t.Fatalf("board after a move answered %d, want 503", c)
	}
	select {
	case code := <-done:
		if code != 3 {
			t.Fatalf("board exit = %d, want 3", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the board kept its port after its home moved")
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
