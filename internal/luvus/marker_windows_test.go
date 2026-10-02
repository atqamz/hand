package luvus

import (
	"errors"
	"io/fs"
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

func TestProcStartMarkerReportsNotExistForAnExitedProcess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=NONE")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if m, err := ProcStartMarker(cmd.Process.Pid); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("marker %q, %v for an exited process; want fs.ErrNotExist", m, err)
	}
}
