package cli

import (
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
)

func TestStopOneLeavesTheChildAlive(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 >nul")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	var kids []victim
	deadline := time.Now().Add(10 * time.Second)
	for len(kids) == 0 && time.Now().Before(deadline) {
		kids, _ = descendants(uint32(pid))
		time.Sleep(50 * time.Millisecond)
	}
	if len(kids) == 0 {
		t.Fatal("no child process started")
	}
	t.Cleanup(func() { terminate(kids[0].pid, kids[0].marker) })
	marker, err := luvus.ProcStartMarker(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := stopOne(pid, marker); err != nil {
		t.Fatal(err)
	}
	if rootAlive(pid, marker) {
		t.Fatalf("process %d survived", pid)
	}
	if _, err := luvus.ProcStartMarker(int(kids[0].pid)); err != nil {
		t.Fatalf("child %s died with its parent: %v", strconv.Itoa(int(kids[0].pid)), err)
	}
}

func TestTerminateSkipsAProcessWithAnotherStartMarker(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 >nul")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	marker, err := luvus.ProcStartMarker(pid)
	if err != nil {
		t.Fatal(err)
	}
	terminate(uint32(pid), "windows:1")
	if !rootAlive(pid, marker) {
		t.Fatal("terminate killed a process whose start marker did not match")
	}
	terminate(uint32(pid), marker)
	if !waitGone(pid, marker, 5*time.Second) {
		t.Fatalf("process %d survived terminate with its own marker", pid)
	}
}
