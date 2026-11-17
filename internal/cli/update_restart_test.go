package cli_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

func TestUpdateRestartsAWatchAfterTheNewBinaryPinnedTheFleet(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	if err := fleet.WriteVersion(h.home, fleet.VersionPin{Version: "0.14.1", Commit: "0123456789ab", Schema: state.SchemaVersion + 1}); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "hand")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	if err := os.WriteFile(target, []byte("old hand\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldTarget, oldWatch := cli.UpdateHooks.Target, *cli.WatchExe
	cli.UpdateHooks.Target, *cli.WatchExe = target, os.Executable
	t.Cleanup(func() {
		cli.UpdateHooks.Target, *cli.WatchExe = oldTarget, oldWatch
		_ = os.Remove(filepath.Join(h.home, "watch.started"))
		if b, err := os.ReadFile(filepath.Join(h.home, "watch.child")); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Kill()
				}
			}
		}
	})

	watch := exec.Command(exe, "watch")
	watch.Dir = h.home
	watch.Env = append(h.env(nil, nil).Environ(), "HAND_HOME="+h.home)
	if err := watch.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = watch.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = watch.Process.Kill()
		<-done
	})

	marker, err := luvus.ProcStartMarker(watch.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.home, "watch.pid"), []byte(fmt.Sprintf("%d %s %s\n", watch.Process.Pid, marker, target)), 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(h.home, "watch.started"))
		return err == nil
	})

	h.vars["HAND_INSTALL_BASE"] = handReleaseSchema(t, "0.14.1", "edge", "0123456789ab", state.SchemaVersion+1, "")
	out, errOut, code := h.run("update", "--channel", "edge")
	if code != 0 || strings.Contains(out+errOut, "newer than this hand") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if !strings.Contains(out, "status: updated\n") {
		t.Fatalf("stdout=%q", out)
	}
}
