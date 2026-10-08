package proc

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestNoWindowAddsToExistingFlags(t *testing.T) {
	cmd := exec.Command("cmd")
	NewGroup(cmd)
	NoWindow(cmd)
	if got, want := cmd.SysProcAttr.CreationFlags, uint32(windows.CREATE_NEW_PROCESS_GROUP|windows.CREATE_NO_WINDOW); got != want {
		t.Fatalf("flags = %#x, want %#x", got, want)
	}
	bare := exec.Command("cmd")
	NoWindow(bare)
	if got := bare.SysProcAttr.CreationFlags; got != windows.CREATE_NO_WINDOW {
		t.Fatalf("flags = %#x", got)
	}
}
