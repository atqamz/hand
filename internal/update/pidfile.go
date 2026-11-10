package update

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/boot"
	"github.com/atqamz/hand/internal/luvus"
)

func WritePID(path string) (func(), error) {
	pid := os.Getpid()
	marker, err := luvus.ProcStartMarker(pid)
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = Target(exe)
	}
	if err != nil {
		return nil, err
	}
	line := fmt.Sprintf("%d %s %s\n", pid, marker, exe)
	if id := boot.Stable(); id != "" {
		host, _ := os.Hostname()
		line += fmt.Sprintf("boot %s %s\n", id, host)
	}
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		return nil, err
	}
	return func() {
		if b, err := os.ReadFile(path); err == nil && string(b) == line {
			_ = os.Remove(path)
		}
	}, nil
}

type pidEntry struct {
	pid         int
	marker, exe string
	host        string
	foreign     bool
}

func livePID(path string) (pidEntry, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return pidEntry{}, false
	}
	first, rest, _ := strings.Cut(string(b), "\n")
	f := strings.SplitN(first, " ", 3)
	if len(f) < 2 {
		return pidEntry{}, false
	}
	pid, err := strconv.Atoi(f[0])
	if err != nil || pid <= 0 {
		return pidEntry{}, false
	}
	e := pidEntry{pid: pid, marker: f[1]}
	if len(f) == 3 {
		e.exe = f[2]
	}
	if tag := strings.Fields(rest); len(tag) >= 2 && tag[0] == "boot" {
		if len(tag) > 2 {
			e.host = tag[2]
		}
		if !boot.Same(tag[1], boot.Stable()) {
			e.foreign = true
			return e, false
		}
	}
	m, err := luvus.ProcStartMarker(pid)
	return e, err == nil && m == f[1]
}

func Foreign(path string) (host string, foreign bool) {
	e, _ := livePID(path)
	return e.host, e.foreign
}

func LivePID(path string) (pid int, marker, exe string, live bool) {
	e, live := livePID(path)
	return e.pid, e.marker, e.exe, live
}
