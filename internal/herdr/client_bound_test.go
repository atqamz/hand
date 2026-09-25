package herdr

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
)

func TestHerdrWakeReadsEndAtTheCallerDeadline(t *testing.T) {
	faketool.Herdr{Hang: []string{"tab list", "pane process-info"}}.Install(t, faketool.Bin(t))
	for name, read := range map[string]func(context.Context) error{
		"tab list":          func(ctx context.Context) error { _, err := NewClient().TabListContext(ctx, "w1"); return err },
		"pane process-info": func(ctx context.Context) error { _, err := NewClient().PaneProcessInfoContext(ctx, "p1"); return err },
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		start := time.Now()
		err := read(ctx)
		cancel()
		if err == nil || time.Since(start) > 10*time.Second {
			t.Fatalf("%s against a hung herdr = %v after %s, want an error at the caller's deadline", name, err, time.Since(start))
		}
	}
}

func TestHerdrCallIsNotHeldOpenByAChildOfHerdr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the orphan is started by a POSIX shell script")
	}
	for _, test := range []struct {
		name, tail string
		ok         bool
	}{
		{"hung past the deadline", "exec sleep 60", false},
		{"exited successfully", `echo '{"id":"cli:tab:list","result":{"tabs":[]}}'`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "orphan")
			script := filepath.Join(dir, "herdr")
			if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 60 &\necho $! > "+pidFile+"\n"+test.tail+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				data, _ := os.ReadFile(pidFile)
				if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					if orphan, err := os.FindProcess(pid); err == nil {
						_ = orphan.Kill()
					}
				}
			})
			client, err := NewClientAt(script, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err = client.TabListContext(ctx, "w1")
			if (err == nil) != test.ok || time.Since(start) > 10*time.Second {
				t.Fatalf("tab list = %v after %s while an orphan holds herdr's stdout, want ok=%t within WaitDelay of the deadline or exit", err, time.Since(start), test.ok)
			}
		})
	}
}
