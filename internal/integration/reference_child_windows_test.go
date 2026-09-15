//go:build windows

package integration

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

func TestPayloadReferenceWindowsGuardianContainsManagedChild(t *testing.T) {
	store, path := installExecutableReferenceFixture(t)
	ready := filepath.Join(t.TempDir(), "ready")
	guardian := exec.Command(os.Args[0], "-test.run=^TestPayloadReferenceWindowsProcessHelper$")
	guardian.Env = append(os.Environ(),
		"HAND_INTEGRATION_WINDOWS_HELPER=guardian",
		"HAND_INTEGRATION_WINDOWS_ROOT="+store.Root,
		"HAND_INTEGRATION_WINDOWS_PAYLOAD="+path,
		"HAND_INTEGRATION_WINDOWS_READY="+ready,
	)
	if output, err := guardian.CombinedOutput(); err != nil {
		t.Fatalf("guardian: %v: %s", err, output)
	}
	data, err := os.ReadFile(ready)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.ParseUint(string(data), 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	waitForWindowsProcessExit(t, uint32(pid))
	recovered, err := store.AcquireReference("github/gh", path, windowsPayloadReferenceRequest())
	if err != nil {
		t.Fatalf("recover reference after guardian death: %v", err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPayloadReferenceWindowsProcessHelper(t *testing.T) {
	switch os.Getenv("HAND_INTEGRATION_WINDOWS_HELPER") {
	case "guardian":
		store := NewStore(os.Getenv("HAND_INTEGRATION_WINDOWS_ROOT"))
		reference, err := store.AcquireReference("github/gh", os.Getenv("HAND_INTEGRATION_WINDOWS_PAYLOAD"), windowsPayloadReferenceRequest())
		if err != nil {
			t.Fatal(err)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestPayloadReferenceWindowsProcessHelper$")
		child.Env = append(os.Environ(), "HAND_INTEGRATION_WINDOWS_HELPER=child")
		if err := reference.StartChild(child); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("HAND_INTEGRATION_WINDOWS_READY"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	case "child":
		time.Sleep(30 * time.Second)
	}
}

func windowsPayloadReferenceRequest() PayloadReferenceRequest {
	return PayloadReferenceRequest{
		ReferenceID: "windows-managed-child", FleetID: integrationTestFleetID,
		Consumer: "integration-process", Evidence: "capability=github/gh",
	}
}

func waitForWindowsProcessExit(t *testing.T, pid uint32) {
	t.Helper()
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	result, err := windows.WaitForSingleObject(process, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if result != windows.WAIT_OBJECT_0 {
		t.Fatalf("managed integration child %d survived guardian death", pid)
	}
}
