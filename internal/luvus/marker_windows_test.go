package luvus

import (
	"os"
	"os/exec"
	"testing"
)

func TestProcStartMarkerIsStable(t *testing.T) {
	a, err := ProcStartMarker(os.Getpid())
	if err != nil || a == "" {
		t.Fatalf("marker = %q, %v", a, err)
	}
	if b, _ := ProcStartMarker(os.Getpid()); a != b {
		t.Fatalf("marker changed: %q then %q", a, b)
	}
}

func TestProcStartMarkerFailsForAnExitedProcessWithAnOpenHandle(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=NONE")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if m, err := ProcStartMarker(cmd.Process.Pid); err == nil {
		t.Fatalf("marker %q for an exited process", m)
	}
}
