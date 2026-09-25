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

	"golang.org/x/sys/unix"
)

const procRoot = "/proc"

// Incarnation names one Linux process for one boot: a PID alone is reused, but the
// kernel start time of the process holding it is not. The json tags match the
// guard's own identity record (internal/execguard), so a rename can't change the wire format.
type Incarnation struct {
	BootID     string `json:"boot_id"`
	PID        int    `json:"pid"`
	StartTicks uint64 `json:"start_ticks"`
}

// ReadIncarnation reads pid's current boot identity and kernel start time.
func ReadIncarnation(pid int) (Incarnation, error) {
	if !properProcMount() {
		return Incarnation{}, errProcMount
	}
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

// Observe re-reads incarnation.PID and classifies it against the recorded boot and
// start time, per the contract's Live(G) rule: boot_id equal, starttime equal, and
// state neither Z nor X (see classifyState for the zombie/thread-count decision).
func Observe(incarnation Incarnation) Observation {
	if incarnation.BootID == "" || incarnation.PID <= 0 {
		return Unknown
	}
	if !properProcMount() {
		return Unknown
	}
	boot, err := readBootID()
	if err != nil {
		return Unknown
	}
	if boot != incarnation.BootID {
		return BootChanged
	}
	stat, err := readStat(incarnation.PID)
	switch {
	case isGone(err):
		return Absent
	case err != nil:
		return Unknown
	}
	ticks, err := parseStartTicks(stat)
	if err != nil {
		return Unknown
	}
	if ticks != incarnation.StartTicks {
		return Absent
	}
	state, err := parseState(stat)
	if err != nil {
		return Unknown
	}
	threads, err := parseNumThreads(stat)
	if err != nil {
		return Unknown
	}
	return classifyState(state, threads)
}

// A zombie or dead main thread with other threads still counted is not proof of
// death: SYS_exit on one thread of a multithreaded process shows Z while the
// process runs on, so that case is Unknown rather than a false Absent.
func classifyState(state byte, threads int) Observation {
	if state != 'Z' && state != 'X' {
		return Alive
	}
	if threads > 1 {
		return Unknown
	}
	return Absent
}

// ControllingTerminal reads pid's tty_nr, the device number of its controlling
// terminal (0 when it has none).
func ControllingTerminal(pid int) (uint64, error) {
	if !properProcMount() {
		return 0, errProcMount
	}
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
	if !properProcMount() {
		return 0, errProcMount
	}
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
func PIDNamespace(pid int) (uint64, error) {
	if !properProcMount() {
		return 0, errProcMount
	}
	return readPIDNamespace(filepath.Join(procRoot, strconv.Itoa(pid), "ns", "pid"))
}

// SelfPIDNamespace reads Hand's own PID-namespace inode from /proc/self/ns/pid,
// the path the contract names, rather than a path built from os.Getpid(), which
// can resolve to an unrelated process under a foreign /proc mount.
func SelfPIDNamespace() (uint64, error) {
	if !properProcMount() {
		return 0, errProcMount
	}
	return readPIDNamespace(filepath.Join(procRoot, "self", "ns", "pid"))
}

func readPIDNamespace(path string) (uint64, error) {
	target, err := os.Readlink(path)
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
	if !properProcMount() {
		return 0, errProcMount
	}
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

// Evidence is one candidate's environment and cwd for the absence check. Gone means
// the candidate was confirmed to no longer exist. Unreadable means it still exists
// but a read failed, which the contract requires to stay unknown, not pass the check.
type Evidence struct {
	Environ    []string
	Cwd        string
	Gone       bool
	Unreadable bool
}

// ReadEvidence reads pid's environ and cwd for the absence-check candidate scan.
// A failed read is Gone only once a fresh stat confirms the process is gone; a
// same-uid non-dumpable process that blocks the read otherwise stays Unreadable.
func ReadEvidence(pid int) Evidence {
	if !properProcMount() {
		return Evidence{Unreadable: true}
	}
	environ, environErr := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "environ"))
	cwd, cwdErr := os.Readlink(filepath.Join(procRoot, strconv.Itoa(pid), "cwd"))
	if environErr == nil && cwdErr == nil {
		return Evidence{Environ: splitEnviron(environ), Cwd: cwd}
	}
	if isGone(environErr) || isGone(cwdErr) {
		if _, statErr := readStat(pid); isGone(statErr) {
			return Evidence{Gone: true}
		}
	}
	return Evidence{Unreadable: true}
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
// for a Linux repair. See isCandidate for what excludes a pid versus keeps it.
func ScanCandidates(uid uint32, pidNS uint64, minStartTicks uint64) ([]int, error) {
	if !properProcMount() {
		return nil, errProcMount
	}
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

// A pid is excluded only on a definitive fact: gone, started before minStartTicks,
// or a successful read reporting a different uid or namespace. Any other read
// error (EACCES on a non-dumpable process's ns link, say) keeps it a candidate.
func isCandidate(pid int, uid uint32, pidNS uint64, minStartTicks uint64) bool {
	stat, err := readStat(pid)
	switch {
	case isGone(err):
		return false
	case err != nil:
		return true
	}
	ticks, err := parseStartTicks(stat)
	if err != nil {
		return true
	}
	if ticks < minStartTicks {
		return false
	}
	gotUID, err := RealUID(pid)
	switch {
	case isGone(err):
		return false
	case err != nil:
		return true
	}
	if gotUID != uid {
		return false
	}
	gotNS, err := PIDNamespace(pid)
	switch {
	case isGone(err):
		return false
	case err != nil:
		return true
	}
	return gotNS == pidNS
}

var errProcMount = errors.New("proc mount does not resolve /proc/self to this process")

// Guards every namespace-relative pid read against a /proc not mounted for Hand's
// own PID namespace (a host /proc bind-mounted into `unshare -pf` without
// --mount-proc, say): there, os.Getpid() and a /proc/<pid> entry can name two processes.
func properProcMount() bool {
	self, err := os.Readlink(filepath.Join(procRoot, "self"))
	return err == nil && self == strconv.Itoa(os.Getpid())
}

func isGone(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH)
}

// BootID reads the kernel's per-boot UUID, which changes on every boot and survives hibernation.
func BootID() (string, error) {
	return readBootID()
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

func parseNumThreads(stat []byte) (int, error) {
	field, err := statField(stat, 20)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(field)
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
