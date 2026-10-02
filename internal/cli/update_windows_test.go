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
	var kids []uint32
	deadline := time.Now().Add(10 * time.Second)
	for len(kids) == 0 && time.Now().Before(deadline) {
		kids, _ = descendants(uint32(pid))
		time.Sleep(50 * time.Millisecond)
	}
	if len(kids) == 0 {
		t.Fatal("no child process started")
	}
	t.Cleanup(func() { terminate(kids[0]) })
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
	if _, err := luvus.ProcStartMarker(int(kids[0])); err != nil {
		t.Fatalf("child %s died with its parent: %v", strconv.Itoa(int(kids[0])), err)
	}
}
