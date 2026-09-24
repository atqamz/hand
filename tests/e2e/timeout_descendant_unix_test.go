//go:build e2e && (linux || darwin)

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestBackgroundTimeoutStopsDescendantBeforeTempRemoval(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	ready := filepath.Join(base, "ready")
	trigger := filepath.Join(base, "trigger")
	cmd := triggeredDescendantCommand(target, ready, trigger)
	process, err := startBackgroundProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	reaped := false
	t.Cleanup(func() {
		_ = os.WriteFile(trigger, nil, 0o600)
		if !reaped {
			process.stop()
			_ = cmd.Wait()
			if err := process.close(); err != nil {
				t.Error(err)
			}
		}
	})
	waitForDescendantFile(t, ready)
	_, timedOut := process.wait(20 * time.Millisecond)
	reaped = true
	if !timedOut {
		t.Fatal("parent unexpectedly exited before timeout")
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trigger, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(target, "late")); !os.IsNotExist(err) {
		t.Fatalf("descendant wrote after parent cleanup: %v", err)
	}
}

func triggeredDescendantCommand(target, ready, trigger string) *exec.Cmd {
	cmd := exec.Command("/bin/sh", "-c", `(/bin/sh -c 'printf ready > "$READY"; while [ ! -e "$TRIGGER" ]; do /bin/sleep 0.01; done; /bin/mkdir -p "$TARGET"; printf late > "$TARGET/late"') & wait`)
	cmd.Env = append(os.Environ(), "READY="+ready, "TRIGGER="+trigger, "TARGET="+target)
	return cmd
}

func waitForDescendantFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
