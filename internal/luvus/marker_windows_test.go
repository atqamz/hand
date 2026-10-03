package luvus

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
	"golang.org/x/sys/windows"
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

func TestProcStartMarkerIsLuvusFiletime(t *testing.T) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	want := "windows:" + strconv.FormatUint(uint64(created.HighDateTime)<<32|uint64(created.LowDateTime), 10)
	if got, err := ProcStartMarker(os.Getpid()); err != nil || got != want {
		t.Fatalf("marker = %q, %v; want %q", got, err, want)
	}
}

func TestProcStartMarkerSeesAProcessThatExitedWithStillActive(t *testing.T) {
	cmd := exec.Command(fakebin.Install(t, t.TempDir(), "exits", "luvus", map[string]string{"exit": "259"}))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	var exit *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exit) || exit.ExitCode() != 259 {
		t.Fatalf("exit = %v", err)
	}
	if m, err := ProcStartMarker(cmd.Process.Pid); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("marker %q, %v for a process that exited with 259; want fs.ErrNotExist", m, err)
	}
}
