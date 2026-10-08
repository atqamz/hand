package luvus

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCommandHasNoConsoleWindow(t *testing.T) {
	cmd := command(context.Background(), nil, "luvus.exe", "session", "list", "--json")
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("luvus children must start with CREATE_NO_WINDOW")
	}
}
