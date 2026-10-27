package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

type leftover struct {
	pid    int
	marker string
}

func attemptDir(dir string) bool {
	fleetDir := filepath.Dir(dir)
	return filepath.IsAbs(dir) && filepath.Clean(dir) == dir &&
		worktreeName.MatchString(filepath.Base(dir)) &&
		state.FleetID.MatchString(filepath.Base(fleetDir)) &&
		filepath.Base(filepath.Dir(fleetDir)) == "worktrees"
}

func sweepWorktree(dir string) {
	if attemptDir(dir) {
		roots := realRoots(dir)
		endLeftovers(roots, leftovers(roots))
	}
}

func endLeftovers(roots []string, victims []leftover) {
	signalLeftovers(victims, syscall.SIGTERM)
	deadline := time.Now().Add(killGrace)
	for _, v := range victims {
		waitGone(v.pid, v.marker, time.Until(deadline))
	}
	signalLeftovers(append(victims, leftovers(roots)...), syscall.SIGKILL)
}

func signalLeftovers(victims []leftover, sig syscall.Signal) {
	for _, v := range victims {
		if rootAlive(v.pid, v.marker) {
			_ = syscall.Kill(v.pid, sig)
		}
	}
}

func leftovers(roots []string) []leftover {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self, uid := ancestry(os.Getpid()), uint32(os.Getuid())
	var out []leftover
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || self[pid] {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Sys().(*syscall.Stat_t).Uid != uid {
			continue
		}
		marker, err := luvus.ProcStartMarker(pid)
		if err != nil {
			continue
		}
		cwd, err := os.Readlink("/proc/" + e.Name() + "/cwd")
		if err == nil && under(roots, cwd) {
			out = append(out, leftover{pid, marker})
		}
	}
	return out
}

func ancestry(pid int) map[int]bool {
	chain := map[int]bool{}
	for pid > 1 && !chain[pid] {
		chain[pid] = true
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			break
		}
		s := string(b)
		f := strings.Fields(s[strings.LastIndex(s, ")")+1:])
		if len(f) < 2 {
			break
		}
		pid, _ = strconv.Atoi(f[1])
	}
	return chain
}
