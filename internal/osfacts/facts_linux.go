//go:build linux

package osfacts

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const procRoot = "/proc"

// Incarnation names one Linux process for the lifetime of one boot: a PID alone is
// reused, but the kernel start time of the process holding it is not, within a boot.
type Incarnation struct {
	BootID     string
	PID        int
	StartTicks uint64
}

// ReadIncarnation reads pid's current boot identity and kernel start time.
func ReadIncarnation(pid int) (Incarnation, error) {
	boot, err := readBootID()
	if err != nil {
		return Incarnation{}, err
	}
	ticks, err := readStartTicks(pid)
	if err != nil {
		return Incarnation{}, err
	}
	return Incarnation{BootID: boot, PID: pid, StartTicks: ticks}, nil
}

// Observe re-reads incarnation.PID from the OS and classifies it against the
// recorded boot and start time, per the contract's Live(G) rule: boot_id equal,
// starttime equal, and the process state neither Z (zombie) nor X (dead).
func Observe(incarnation Incarnation) Observation {
	if incarnation.BootID == "" || incarnation.PID <= 0 {
		return Unknown
	}
	boot, err := readBootID()
	if err != nil {
		return Unknown
	}
	if boot != incarnation.BootID {
		return Ceased
	}
	stat, err := readStat(incarnation.PID)
	if errors.Is(err, fs.ErrNotExist) {
		return Ceased
	}
	if err != nil {
		return Unknown
	}
	ticks, err := parseStartTicks(stat)
	if err != nil {
		return Unknown
	}
	if ticks != incarnation.StartTicks {
		return Ceased
	}
	state, err := parseState(stat)
	if err != nil {
		return Unknown
	}
	if state == 'Z' || state == 'X' {
		return Ceased
	}
	return Alive
}

// ControllingTerminal reads pid's tty_nr, the device number of its controlling
// terminal (0 when it has none).
func ControllingTerminal(pid int) (uint64, error) {
	stat, err := readStat(pid)
	if err != nil {
		return 0, err
	}
	field, err := statField(stat, 7)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(field, 10, 64)
}

// ForegroundGroup reads pid's tpgid, the process group holding the foreground of
// its controlling terminal.
func ForegroundGroup(pid int) (int, error) {
	stat, err := readStat(pid)
	if err != nil {
		return 0, err
	}
	field, err := statField(stat, 8)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(field)
}

// PIDNamespace reads the inode identifying pid's PID namespace, from /proc/<pid>/ns/pid.
// Hand calls it with its own pid to get the value the contract names /proc/self/ns/pid.
func PIDNamespace(pid int) (uint64, error) {
	target, err := os.Readlink(filepath.Join(procRoot, strconv.Itoa(pid), "ns", "pid"))
	if err != nil {
		return 0, err
	}
	lb := strings.IndexByte(target, '[')
	rb := strings.IndexByte(target, ']')
	if lb < 0 || rb < lb {
		return 0, fmt.Errorf("pid namespace link %q has no inode", target)
	}
	return strconv.ParseUint(target[lb+1:rb], 10, 64)
}

// RealUID reads pid's real uid from /proc/<pid>/status, the first of the four
// uid fields the kernel reports (real, effective, saved-set, filesystem).
func RealUID(pid int) (uint32, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "status"))
	if err != nil {
		return 0, err
	}
	for line := range strings.Lines(string(data)) {
		rest, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			break
		}
		uid, err := strconv.ParseUint(fields[0], 10, 32)
		if err != nil {
			return 0, err
		}
		return uint32(uid), nil
	}
	return 0, fmt.Errorf("process %d status has no Uid line", pid)
}

// Evidence is one candidate's environment and working directory, read for the
// attested-repair absence check. Unreadable is set, with the other fields left
// zero, when either read failed: the contract requires that case to stay unknown.
type Evidence struct {
	Environ    []string
	Cwd        string
	Unreadable bool
}

// ReadEvidence reads pid's environ and cwd for the absence-check candidate scan.
func ReadEvidence(pid int) Evidence {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "environ"))
	if err != nil {
		return Evidence{Unreadable: true}
	}
	cwd, err := os.Readlink(filepath.Join(procRoot, strconv.Itoa(pid), "cwd"))
	if err != nil {
		return Evidence{Unreadable: true}
	}
	return Evidence{Environ: splitEnviron(data), Cwd: cwd}
}

func splitEnviron(data []byte) []string {
	trimmed := bytes.TrimRight(data, "\x00")
	if len(trimmed) == 0 {
		return nil
	}
	return strings.Split(string(trimmed), "\x00")
}

// ScanCandidates lists the pids in Hand's PID namespace, owned by uid, and started
// at or after minStartTicks: the absence-check candidate set the contract defines
// for a Linux repair. A pid this call cannot read is simply not a candidate.
func ScanCandidates(uid uint32, pidNS uint64, minStartTicks uint64) ([]int, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	var candidates []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if isCandidate(pid, uid, pidNS, minStartTicks) {
			candidates = append(candidates, pid)
		}
	}
	return candidates, nil
}

func isCandidate(pid int, uid uint32, pidNS uint64, minStartTicks uint64) bool {
	stat, err := readStat(pid)
	if err != nil {
		return false
	}
	ticks, err := parseStartTicks(stat)
	if err != nil || ticks < minStartTicks {
		return false
	}
	gotUID, err := RealUID(pid)
	if err != nil || gotUID != uid {
		return false
	}
	gotNS, err := PIDNamespace(pid)
	if err != nil || gotNS != pidNS {
		return false
	}
	return true
}

func readBootID() (string, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, "sys", "kernel", "random", "boot_id"))
	if err != nil {
		return "", err
	}
	boot := strings.TrimSpace(string(data))
	if boot == "" {
		return "", errors.New("boot id is empty")
	}
	return boot, nil
}

func readStartTicks(pid int) (uint64, error) {
	stat, err := readStat(pid)
	if err != nil {
		return 0, err
	}
	return parseStartTicks(stat)
}

func readStat(pid int) ([]byte, error) {
	return os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
}

func parseStartTicks(stat []byte) (uint64, error) {
	field, err := statField(stat, 22)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(field, 10, 64)
}

func parseState(stat []byte) (byte, error) {
	field, err := statField(stat, 3)
	if err != nil || len(field) == 0 {
		return 0, fmt.Errorf("process stat has no state field")
	}
	return field[0], nil
}

// proc(5) numbers stat fields from 1, and field 2 (comm) may itself contain spaces
// and ')', so every field from the state onward is counted from the line's last ')'.
func statField(stat []byte, field int) (string, error) {
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return "", errors.New("process stat has no command terminator")
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) <= field-3 {
		return "", fmt.Errorf("process stat lacks field %d", field)
	}
	return fields[field-3], nil
}
