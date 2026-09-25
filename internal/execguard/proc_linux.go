//go:build linux

package execguard

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const procRoot = "/proc"

type procStat struct {
	state      byte
	ppid       int
	startTicks uint64
}

func gone(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH)
}

func readStat(pid int) (procStat, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return procStat{}, err
	}
	return parseStat(data)
}

// proc(5) numbers stat fields from 1, and field 2, the command, may itself hold
// spaces and ')', so every later field is counted from the last ')'.
func parseStat(data []byte) (procStat, error) {
	end := bytes.LastIndexByte(data, ')')
	if end < 0 {
		return procStat{}, errors.New("process stat has no command terminator")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 || len(fields[0]) != 1 {
		return procStat{}, errors.New("process stat is truncated")
	}
	ppid, err := strconv.Atoi(fields[4-3])
	if err != nil {
		return procStat{}, fmt.Errorf("process stat parent: %w", err)
	}
	start, err := strconv.ParseUint(fields[22-3], 10, 64)
	if err != nil {
		return procStat{}, fmt.Errorf("process stat start time: %w", err)
	}
	return procStat{state: fields[0][0], ppid: ppid, startTicks: start}, nil
}

var procChildrenSupported = sync.OnceValue(func() bool {
	_, err := os.Stat(filepath.Join(procRoot, "thread-self", "children"))
	return err == nil
})

func childrenOf(parent int) ([]int, error) {
	if !procChildrenSupported() {
		return childrenByScan(parent)
	}
	task := filepath.Join(procRoot, strconv.Itoa(parent), "task")
	threads, err := os.ReadDir(task)
	if gone(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, thread := range threads {
		data, err := os.ReadFile(filepath.Join(task, thread.Name(), "children"))
		if gone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, field := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(field)
			if err != nil {
				return nil, fmt.Errorf("process children list: %w", err)
			}
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func childrenByScan(parent int) ([]int, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := readStat(pid)
		if gone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if stat.ppid == parent {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

type descendant struct {
	pid    int
	parent int
}

func descendants(root int) ([]descendant, error) {
	var found []descendant
	queue := []int{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		pids, err := childrenOf(parent)
		if err != nil {
			return nil, err
		}
		for _, pid := range pids {
			found = append(found, descendant{pid: pid, parent: parent})
			queue = append(queue, pid)
		}
	}
	return found, nil
}
