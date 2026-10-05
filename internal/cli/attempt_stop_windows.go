package cli

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

type victim struct {
	pid    uint32
	marker string
}

func descendants(root uint32) ([]victim, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	children := map[uint32][]uint32{}
	markers := map[uint32]string{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err := windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		children[e.ParentProcessID] = append(children[e.ParentProcessID], e.ProcessID)
	}
	var out []victim
	seen := map[uint32]bool{root: true}
	for queue := []uint32{root}; len(queue) > 0; queue = queue[1:] {
		parent, parentKnown := startedAt(queue[0], markers)
		for _, c := range children[queue[0]] {
			born, known := startedAt(c, markers)
			if seen[c] || !known || parentKnown && born < parent {
				continue
			}
			seen[c] = true
			out = append(out, victim{c, markers[c]})
			queue = append(queue, c)
		}
	}
	return out, nil
}

func startedAt(pid uint32, markers map[uint32]string) (int64, bool) {
	m, ok := markers[pid]
	if !ok {
		m, _ = luvus.ProcStartMarker(int(pid))
		markers[pid] = m
	}
	t, err := strconv.ParseInt(strings.TrimPrefix(m, "windows:"), 10, 64)
	return t, m != "" && err == nil
}

func terminate(pid uint32, marker string) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	if m, err := luvus.ProcStartMarker(int(pid)); err != nil || m != marker {
		return
	}
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
		terminate(below[i].pid, below[i].marker)
	}
	terminate(uint32(pid), marker)
	if waitGone(pid, marker, killGrace) {
		return nil
	}
	return fmt.Errorf("%w: root process %d is still alive after TerminateProcess", state.ErrConflict, pid)
}
