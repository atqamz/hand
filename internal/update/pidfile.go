package update

import (
	"fmt"
	"os"
	"strconv"
	"strings"

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
}

func livePID(path string) (pidEntry, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return pidEntry{}, false
	}
	f := strings.SplitN(strings.TrimSuffix(string(b), "\n"), " ", 3)
	if len(f) < 2 {
		return pidEntry{}, false
	}
	pid, err := strconv.Atoi(f[0])
	if err != nil {
		return pidEntry{}, false
	}
	e := pidEntry{pid: pid, marker: f[1]}
	if len(f) == 3 {
		e.exe = f[2]
	}
	m, err := luvus.ProcStartMarker(pid)
	return e, err == nil && m == f[1]
}
