//go:build windows

package osfacts

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

var procGetTickCount64 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

// Incarnation names one Windows process for the lifetime of one boot: a PID alone
// is reused, but the process creation time the kernel reports through a handle to
// it is not, within a boot.
type Incarnation struct {
	PID          uint32
	CreationTime int64
}

// ReadIncarnation reads pid's current process creation time.
func ReadIncarnation(pid uint32) (Incarnation, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return Incarnation{}, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	creation, err := processCreationTime(handle)
	if err != nil {
		return Incarnation{}, err
	}
	return Incarnation{PID: pid, CreationTime: creation}, nil
}

// Observe re-opens incarnation.PID and classifies it against the recorded creation
// time, per the contract's Live(G) rule: OpenProcess succeeds, the creation time
// read through that handle is equal, and the handle is not signaled.
func Observe(incarnation Incarnation) Observation {
	if incarnation.PID == 0 {
		return Unknown
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, incarnation.PID)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return Absent
		}
		return Unknown
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	creation, err := processCreationTime(handle)
	if err != nil {
		return Unknown
	}
	if creation != incarnation.CreationTime {
		return Absent
	}
	event, err := windows.WaitForSingleObject(handle, 0)
	switch {
	case err != nil:
		return Unknown
	case event == windows.WAIT_OBJECT_0:
		return Absent
	case event == uint32(windows.WAIT_TIMEOUT):
		return Alive
	default:
		return Unknown
	}
}

func processCreationTime(handle windows.Handle) (int64, error) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return 0, fmt.Errorf("get process times: %w", err)
	}
	return creation.Nanoseconds(), nil
}

// TickCount reads the milliseconds since boot GetTickCount64 reports. It cannot
// fabricate a reboot, and it misses a Fast Startup shutdown, per the contract's
// Windows boot-change witness.
func TickCount() uint64 {
	r0, _, _ := procGetTickCount64.Call()
	return uint64(r0)
}

// BootTickChanged reports the contract's positive Windows boot-change witness:
// the current tick count is lower than the value recorded at an earlier read.
func BootTickChanged(recorded uint64) bool {
	return bootTickChanged(TickCount(), recorded)
}

func bootTickChanged(current, recorded uint64) bool {
	return current < recorded
}
