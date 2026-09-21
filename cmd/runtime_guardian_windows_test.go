//go:build windows

package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRuntimeGuardianJobHelper(t *testing.T) {
	if os.Getenv("HAND_RUNTIME_GUARDIAN_HELPER") != "1" {
		return
	}
	if err := protectRuntimeGuardian(); err != nil {
		os.Exit(2)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestRuntimeGuardianJobChild$")
	child.Env = append(os.Environ(), "HAND_RUNTIME_GUARDIAN_CHILD=1")
	if err := child.Start(); err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(os.Getenv("HAND_RUNTIME_GUARDIAN_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		os.Exit(4)
	}
	_ = child.Wait()
}

func TestRuntimeGuardianJobChild(t *testing.T) {
	if os.Getenv("HAND_RUNTIME_GUARDIAN_CHILD") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("HAND_RUNTIME_GUARDIAN_READY"), nil, 0o600); err != nil {
		os.Exit(2)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestWindowsRuntimeGuardianDeathTerminatesManagedChild(t *testing.T) {
	root := t.TempDir()
	pidPath := filepath.Join(root, "pid")
	readyPath := filepath.Join(root, "ready")
	guardian := exec.Command(os.Args[0], "-test.run=^TestRuntimeGuardianJobHelper$")
	guardian.Env = append(os.Environ(),
		"HAND_RUNTIME_GUARDIAN_HELPER=1",
		"HAND_RUNTIME_GUARDIAN_PID="+pidPath,
		"HAND_RUNTIME_GUARDIAN_READY="+readyPath,
	)
	if err := guardian.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = guardian.Process.Kill()
			_ = guardian.Wait()
		}
	})
	waitForGuardianFile(t, readyPath)
	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.ParseUint(string(pidData), 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(child)
	if err := guardian.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := guardian.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatal(err)
		}
	}
	waited = true
	result, err := windows.WaitForSingleObject(child, 5000)
	if err != nil || result != windows.WAIT_OBJECT_0 {
		t.Fatalf("managed child survived guardian death: wait=%d, err=%v", result, err)
	}
}

func waitForGuardianFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
