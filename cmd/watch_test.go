package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeHerdrWatchScript = `#!/bin/sh
case "$1 $2" in
"workspace list")
	printf '{"id":"cli:1","result":{"workspaces":[]}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`

func setupWatchHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Chdir(home)

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(fakeHerdrWatchScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home
}

func TestWatchRejectsInvalidPollFlag(t *testing.T) {
	setupWatchHome(t)

	cmd := newWatchCmd()
	cmd.SetArgs([]string{"--poll", "nonsense"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "invalid poll interval") {
		t.Fatalf("got err %v, want invalid poll interval", err)
	}
}

func TestWatchRejectsInvalidStaleThresholdConfig(t *testing.T) {
	home := setupWatchHome(t)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "stale-threshold"), []byte("not-a-number"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newWatchCmd()
	cmd.SetArgs([]string{"--poll", "1h"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "invalid config/stale-threshold") {
		t.Fatalf("got err %v, want invalid config/stale-threshold", err)
	}
}

func TestWatchExitsCleanlyOnContextCancel(t *testing.T) {
	setupWatchHome(t)

	cmd := newWatchCmd()
	cmd.SetArgs([]string{"--poll", "1h"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- cmd.ExecuteContext(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not exit after context cancellation")
	}
}
