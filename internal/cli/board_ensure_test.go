package cli_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
)

func fakeBoard(root string) {
	marker := filepath.Join(root, "board.started")
	started, err := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fakeExit(1, err.Error())
	}
	_, _ = started.WriteString("started\n")
	_ = started.Close()
	running := filepath.Join(root, "board.running")
	if err := os.WriteFile(running, nil, 0o600); err != nil {
		fakeExit(1, err.Error())
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			if _, err := os.Stat(marker); err != nil {
				cancel()
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	code := cli.Run([]string{"board", "--addr", "127.0.0.1:0"}, cli.Env{Stdout: os.Stdout, Stderr: os.Stderr, Getenv: cli.HostGetenv, Environ: os.Environ, Now: time.Now, Getwd: os.Getwd, Context: ctx})
	_ = os.Remove(running)
	os.Exit(code)
}

func enableBoard(t *testing.T, h *harness) {
	t.Helper()
	root := h.vars["SECONDHAND_HOME"]
	*cli.WatchExe = os.Executable
	t.Cleanup(func() {
		*cli.WatchExe = func() (string, error) { return "", errors.New("watcher start disabled in tests") }
		if err := os.Remove(filepath.Join(root, "board.started")); err != nil {
			return
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(root, "board.running")); errors.Is(err, os.ErrNotExist) {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("board did not exit before cleanup deadline")
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
}

func boardSpawns(h *harness) int {
	b, _ := os.ReadFile(filepath.Join(h.vars["SECONDHAND_HOME"], "board.started"))
	return strings.Count(string(b), "\n")
}

func openWithoutBoard(t *testing.T) *openFixture {
	t.Helper()
	fx := newOpenFixture(t)
	fx.stop()
	enableBoard(t, fx.h)
	return fx
}

func TestOpenStartsNothingWhenABoardRuns(t *testing.T) {
	fx := newOpenFixture(t)
	enableBoard(t, fx.h)
	fx.h.ok("open")
	fx.h.ok("open", "--print")
	if n := boardSpawns(fx.h); n != 0 {
		t.Fatalf("boards started = %d, want 0", n)
	}
}

func TestOpenStartsABoardDetached(t *testing.T) {
	fx := openWithoutBoard(t)
	out := fx.h.ok("open", "--print", "t1")
	if !strings.Contains(out, "/"+fx.id+"/task/t1?token="+fx.token(t)) {
		t.Fatalf("open --print = %q", out)
	}
	fx.h.ok("open", "t1")
	if n := boardSpawns(fx.h); n != 1 {
		t.Fatalf("boards started = %d, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.log")); err != nil {
		t.Fatalf("no board.log: %v", err)
	}
	if got := fx.opened(t); len(got) != 1 || !strings.Contains(got[0], "/"+fx.id+"/task/t1?token=") {
		t.Fatalf("opened %q", got)
	}
}

func TestOpenTimesOutWhenTheBoardNeverAnswers(t *testing.T) {
	fx := newOpenFixture(t)
	fx.stop()
	dead := fakebin.Install(t, t.TempDir(), "dead", "die", nil)
	*cli.WatchExe = func() (string, error) { return dead, nil }
	wait := *cli.BoardWait
	*cli.BoardWait = 300 * time.Millisecond
	t.Cleanup(func() {
		*cli.BoardWait = wait
		*cli.WatchExe = func() (string, error) { return "", errors.New("watcher start disabled in tests") }
	})
	_, errOut, code := fx.h.run("open", "--print")
	log := filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.log")
	if code != 3 || !strings.Contains(errOut, "the board did not answer within 300ms") || !strings.Contains(errOut, "read "+log) || !strings.Contains(errOut, "hand board") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), "board cannot start") {
		t.Fatalf("board.log = %q", b)
	}
}

func TestConcurrentOpensStartOneBoard(t *testing.T) {
	fx := openWithoutBoard(t)
	var wg sync.WaitGroup
	outs := make(chan string, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, errOut, code := fx.h.run("open", "--print")
			if code != 0 || errOut != "" {
				outs <- "code=" + errOut
				return
			}
			outs <- out
		}()
	}
	wg.Wait()
	close(outs)
	for out := range outs {
		if !strings.Contains(out, "url:") {
			t.Fatalf("a concurrent open failed: %s", out)
		}
	}
	if n := boardSpawns(fx.h); n != 1 {
		t.Fatalf("boards started = %d, want 1", n)
	}
}
