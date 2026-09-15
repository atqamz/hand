//go:build windows

package supervision

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCodexWindowsCommandExecutesPathWithSpaces(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "managed Hand", "hand.cmd")
	marker := filepath.Join(root, "argv.txt")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("@echo off\necho %%* > \"%s\"\n", marker)
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	commandLine := codexStopHandler(exe)["commandWindows"].(string)
	command := exec.Command("cmd.exe")
	// Codex 0.154.0 passes one outer-quoted raw argument after /C. A normal
	// exec.Command argument would apply the incompatible CommandLineToArgvW escaping.
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /C "` + commandLine + `"`}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Codex cmd.exe command %q: %v: %s", commandLine, err, output)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(data)), "supervision codex-stop"; got != want {
		t.Fatalf("managed command argv = %q, want %q", got, want)
	}
}
