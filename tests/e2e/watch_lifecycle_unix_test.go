//go:build e2e && !windows

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
)

func TestWatchConnectInterruptionWhileHerdrIsBlocked(t *testing.T) {
	home := newHome(t)
	callLog := filepath.Join(t.TempDir(), "herdr-calls")
	faketool.Herdr{Hang: []string{"workspace list"}, Log: callLog, LogCommands: []string{"workspace list"}}.Install(t, binDir(t))

	watch := startHandBackground(t, home, "watch", "--poll", "30ms")
	waitForInvocation(t, callLog, "herdr workspace list", 10*time.Second)
	got := watch.interrupt(t, 10*time.Second)
	if got.code != 8 || !strings.Contains(got.stderr, "watch-interrupted") || strings.TrimSpace(got.stdout) != "" {
		t.Fatalf("blocked-connect interruption: exit %d stdout %q stderr %q, want exit 8/watch-interrupted and empty stdout", got.code, got.stdout, got.stderr)
	}
}

func TestWatchArmProbeInterruptionWhileHerdrIsBlocked(t *testing.T) {
	home := seedOneTaskHome(t)
	callLog := filepath.Join(t.TempDir(), "herdr-calls")
	faketool.Herdr{Hang: []string{"pane get"}, Log: callLog, LogCommands: []string{"pane get"}, AllowUnknownPane: true}.Install(t, binDir(t))

	watch := startHandBackground(t, home, "watch", "--until-event", "--poll", "30ms", "--timeout", "60s")
	waitForInvocation(t, callLog, "herdr pane get", 10*time.Second)
	got := watch.interrupt(t, 10*time.Second)
	if got.code != 8 || strings.Contains(got.stderr, "arm-failed") || strings.TrimSpace(got.stdout) != "" {
		t.Fatalf("blocked-arm interruption: exit %d stdout %q stderr %q, want exit 8 without arm-failed or stdout", got.code, got.stdout, got.stderr)
	}
}
