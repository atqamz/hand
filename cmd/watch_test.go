package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/watcher"
)

func setupWatchHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	bin := faketool.Bin(t)
	callLog := filepath.Join(t.TempDir(), "herdr-calls")
	t.Setenv("HERDR_CALL_LOG", callLog)
	faketool.Herdr{Responses: []faketool.HerdrResponse{{
		Command: "workspace list",
		Stdout:  "{\"id\":\"cli:1\",\"result\":{\"workspaces\":[]}}",
	}}, Log: callLog}.Install(t, bin)
	return home
}

func TestWatchRejectsInvalidPollFlag(t *testing.T) {
	setupWatchHome(t)

	cmd := newWatchCmd()
	cmd.SetArgs([]string{"--poll", "nonsense"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid poll interval") {
		t.Fatalf("got err %v, want invalid poll interval", err)
	}
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestWatchRejectsInvalidPollConfigAsGeneralError(t *testing.T) {
	home := setupWatchHome(t)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "watch-interval"), []byte("nonsense"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newWatchCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid poll interval") {
		t.Fatalf("got err %v, want invalid poll interval", err)
	}
	if code := exitCodeFor(t, err); code != 1 {
		t.Fatalf("code = %d, want 1 (config value is not a usage error, err = %v)", code, err)
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

func TestWatchUntilEventExitsFourWhenNoEventOccurs(t *testing.T) {
	setupWatchHome(t)

	cmd := newWatchCmd()
	cmd.SetArgs([]string{"--until-event", "--timeout", "100ms", "--poll", "20ms"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no event") {
		t.Fatalf("got err %v, want a no-event error", err)
	}
	if code := exitCodeFor(t, err); code != 4 {
		t.Fatalf("code = %d, want 4 (err = %v)", code, err)
	}
}

func TestWatchRejectsTimeoutWithoutUntilEvent(t *testing.T) {
	setupWatchHome(t)

	cmd := newWatchCmd()
	cmd.SetArgs([]string{"--timeout", "1s"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--timeout requires --until-event") {
		t.Fatalf("got err %v, want --timeout requires --until-event", err)
	}
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestWatchRejectsNonPositiveTimeout(t *testing.T) {
	setupWatchHome(t)

	cmd := newWatchCmd()
	cmd.SetArgs([]string{"--until-event", "--timeout=0"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("got err %v, want a rejected timeout: an explicit 0 asks for a bound and would get none", err)
	}
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestWatchRejectsEventWithoutUntilEvent(t *testing.T) {
	setupWatchHome(t)

	cmd := newWatchCmd()
	cmd.SetArgs([]string{"--event", "blocked"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--event requires --until-event") {
		t.Fatalf("got err %v, want --event requires --until-event", err)
	}
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestWatchRejectsUnknownEventKind(t *testing.T) {
	setupWatchHome(t)

	cmd := newWatchCmd()
	cmd.SetArgs([]string{"--until-event", "--event", "not-a-kind"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `unknown --event "not-a-kind"`) {
		t.Fatalf("got err %v, want unknown --event", err)
	}
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestWatchMapsContextCancelToInterruption(t *testing.T) {
	setupWatchHome(t)

	cmd := newWatchCmd()
	cmd.SetArgs([]string{"--poll", "1h"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- cmd.ExecuteContext(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		calls, err := os.ReadFile(os.Getenv("HERDR_CALL_LOG"))
		if err == nil && bytes.Contains(calls, []byte("herdr workspace list\n")) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if time.Now().After(deadline) {
		t.Fatal("watch did not reach herdr before cancellation")
	}
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("watch returned nil after interruption, want exit 8")
		}
		if code := exitCodeFor(t, err); code != 8 {
			t.Fatalf("code = %d, want 8 (watch-interrupted), err = %v", code, err)
		}
		if !errors.Is(err, watcher.ErrInterrupted) {
			t.Fatalf("err = %v, want it to wrap ErrInterrupted", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not exit after context cancellation")
	}
}

func TestWatchRefusesWhenAWatcherIsAlreadyAttached(t *testing.T) {
	home := setupWatchHome(t)

	// Two flocks from one process genuinely conflict on Linux, so this holds
	// ownership exactly as a separate watcher process would.
	ownership, err := watcher.Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	defer ownership.Release()

	cmd := newWatchCmd()
	cmd.SetArgs([]string{"--poll", "1h"})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "already attached") {
		t.Fatalf("got err %v, want a refusal naming the attached watcher", err)
	}
	if code := exitCodeFor(t, err); code != 3 {
		t.Fatalf("code = %d, want 3 (err = %v)", code, err)
	}
}

// A usage error has to be rejected before ownership is touched, or a mistyped
// flag would take the running watcher's lock away and exit anyway.
func TestWatchRejectsAUsageErrorWithoutContendingForOwnership(t *testing.T) {
	home := setupWatchHome(t)

	ownership, err := watcher.Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	defer ownership.Release()

	cmd := newWatchCmd()
	cmd.SetArgs([]string{"--event", "report-done", "--takeover"})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--event requires --until-event") {
		t.Fatalf("got err %v, want the usage error, not an ownership refusal", err)
	}
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestMapWatchResultClassifiesLifecycleResults(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
	}{
		{"replacement", watcher.ErrReplaced, 9},
		{"interruption", watcher.ErrInterrupted, 8},
		{"no event", watcher.ErrNoEvent, 4},
		{"arm failed", watcher.ErrArmFailed, 5},
		{"general error stays general", errors.New("boom"), 1},
		{"nil stays nil", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapWatchResult(tc.err)
			if code := exitCodeFor(t, got); code != tc.code {
				t.Fatalf("code = %d, want %d (err = %v)", code, tc.code, got)
			}
		})
	}
}

// Wrapping must not defeat classification: the exit taxonomy resolves through
// errors.Is.
func TestMapWatchResultSurvivesWrapping(t *testing.T) {
	got := mapWatchResult(fmt.Errorf("exit early: %w", watcher.ErrReplaced))
	if code := exitCodeFor(t, got); code != 9 {
		t.Fatalf("code = %d, want 9 through a wrapped replacement error", code)
	}
}
