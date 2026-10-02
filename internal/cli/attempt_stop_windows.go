package cli

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/atqamz/hand/internal/state"
)

func descendants(root uint32) ([]uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	children := map[uint32][]uint32{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err := windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		children[e.ParentProcessID] = append(children[e.ParentProcessID], e.ProcessID)
	}
	var out []uint32
	for queue := []uint32{root}; len(queue) > 0; queue = queue[1:] {
		out = append(out, children[queue[0]]...)
		queue = append(queue, children[queue[0]]...)
	}
	return out, nil
}

func terminate(pid uint32) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.TerminateProcess(h, 1)
}

func stopRoot(pid int, marker string) error {
	if !rootAlive(pid, marker) {
		return nil
	}
	below, err := descendants(uint32(pid))
	if err != nil {
		return err
	}
	for i := len(below) - 1; i >= 0; i-- {
		terminate(below[i])
	}
	terminate(uint32(pid))
	if waitGone(pid, marker, killGrace) {
		return nil
	}
	return fmt.Errorf("%w: root process %d is still alive after TerminateProcess", state.ErrConflict, pid)
}
