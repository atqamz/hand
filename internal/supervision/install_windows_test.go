//go:build windows

package supervision

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("HAND_CODEX_WINDOWS_HELPER") == "1" {
		_ = os.WriteFile(os.Getenv("HAND_CODEX_WINDOWS_MARKER"), []byte(strings.Join(os.Args[1:], " ")), 0o600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCodexWindowsCommandExecutesPathWithSpaces(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "managed Hand", "hand.exe")
	marker := filepath.Join(root, "argv.txt")
	copyWindowsTestExecutable(t, exe)
	runCodexWindowsCommand(t, exe, marker)
}

func TestCodexWindowsCommandExecutesLiteralPercentPath(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "managed %TEAM%", "hand.exe")
	marker := filepath.Join(root, "argv.txt")
	t.Setenv("TEAM", "expanded-away")
	copyWindowsTestExecutable(t, exe)
	runCodexWindowsCommand(t, exe, marker)
}

func runCodexWindowsCommand(t *testing.T, exe, marker string) {
	t.Helper()
	commandLine := codexStopHandler(exe)["commandWindows"].(string)
	command := exec.Command("cmd.exe")
	command.Env = append(os.Environ(),
		"HAND_CODEX_WINDOWS_HELPER=1",
		"HAND_CODEX_WINDOWS_MARKER="+marker,
	)
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

func copyWindowsTestExecutable(t *testing.T, destination string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		t.Fatal(err)
	}
}
