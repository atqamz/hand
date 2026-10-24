//go:build unix

package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestStartedServiceIsReapedWhenItExits(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	enableWatcher(t, h.home)
	startClaudeSupervisor(h)
	eventually(t, func() bool { return watchSpawns(h.home) == 1 })
	b, err := os.ReadFile(filepath.Join(h.home, "watch.child"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.home, "watch.started")); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) })
}
