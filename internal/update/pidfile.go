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
	line := fmt.Sprintf("%d %s\n", pid, marker)
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		return nil, err
	}
	return func() {
		if b, err := os.ReadFile(path); err == nil && string(b) == line {
			_ = os.Remove(path)
		}
	}, nil
}

func livePID(path string) (int, string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, "", false
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return 0, "", false
	}
	pid, err := strconv.Atoi(f[0])
	if err != nil {
		return 0, "", false
	}
	m, err := luvus.ProcStartMarker(pid)
	return pid, f[1], err == nil && m == f[1]
}
