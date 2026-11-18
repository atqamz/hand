package update

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

func TestChildRunsInItsOwnProcessGroupOnWindows(t *testing.T) {
	cmd := childCmd(context.Background(), Options{Target: "hand.exe"}, t.TempDir(), "init")
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("SysProcAttr = %+v, want CREATE_NEW_PROCESS_GROUP", cmd.SysProcAttr)
	}
}
