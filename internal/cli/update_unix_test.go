//go:build unix

package cli

import (
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

func sleeper(t *testing.T, script string) (int, string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	marker, err := luvus.ProcStartMarker(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	return cmd.Process.Pid, marker
}

func TestStopOneTerminatesAProcess(t *testing.T) {
	pid, marker := sleeper(t, "exec sleep 60")
	if err := stopOne(pid, marker); err != nil {
		t.Fatal(err)
	}
	if rootAlive(pid, marker) {
		t.Fatalf("process %d survived", pid)
	}
}

func TestStopOneGivesUpOnAProcessThatIgnoresTerm(t *testing.T) {
	old := updateGrace
	updateGrace = 300 * time.Millisecond
	t.Cleanup(func() { updateGrace = old })
	pid, marker := sleeper(t, "trap '' TERM; while :; do sleep 1; done")
	if err := stopOne(pid, marker); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if !rootAlive(pid, marker) {
		t.Fatalf("process %d died although it ignores SIGTERM", pid)
	}
}

func TestStopOneNeverSignalsARecycledPID(t *testing.T) {
	pid, marker := sleeper(t, "exec sleep 60")
	if err := stopOne(pid, "another marker"); err != nil {
		t.Fatal(err)
	}
	if !rootAlive(pid, marker) {
		t.Fatalf("process %d was signalled although its start marker differs", pid)
	}
}
