package luvus

import (
	"fmt"
	"io/fs"
	"math"

	"golang.org/x/sys/unix"
)

func ProcStartMarker(pid int) (string, error) {
	if pid <= 0 || pid > math.MaxInt32 {
		return "", fmt.Errorf("process %d: %w", pid, fs.ErrNotExist)
	}
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("process %d: %w (%v)", pid, fs.ErrNotExist, err)
	}
	if k.Proc.P_pid != int32(pid) {
		return "", fmt.Errorf("process %d: %w", pid, fs.ErrNotExist)
	}
	return fmt.Sprintf("%d.%06d", k.Proc.P_starttime.Sec, k.Proc.P_starttime.Usec), nil
}
