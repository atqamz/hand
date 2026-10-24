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
	"time"
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
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var ws syscall.WaitStatus
		switch got, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil); {
		case errors.Is(err, syscall.ECHILD):
			return
		case err != nil:
			t.Fatal(err)
		case got == pid:
			t.Fatalf("watcher %d stayed a zombie until the test reaped it", pid)
		}
	}
	t.Fatalf("watcher %d never exited", pid)
}
