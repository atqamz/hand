//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

// #348 revision 4 "Unknowns": an installed v0.7.2 run against a published canonical home must fail without writing it.
// Opt-in: HAND_TEST_V072_BINARY names an exact v0.7.2 build, which CI does not ship.
func TestCutoverStaleV072BinaryLeavesPublishedCanonicalHomeUnchanged(t *testing.T) {
	v072 := os.Getenv("HAND_TEST_V072_BINARY")
	if v072 == "" {
		t.Skip("set HAND_TEST_V072_BINARY to an exact v0.7.2 build")
	}
	home := offlineCutoverHome(t)
	cutoverBootEnv(t, false)
	if got := runHand(t, home, "cutover", "offline", home); got.code != 0 {
		t.Fatalf("freeze run = %+v", got)
	}
	cutoverBootEnv(t, true)
	if got := runHand(t, home, "cutover", "offline", home); got.code != 0 || !strings.Contains(got.stdout, "disposition: canonical-authority") {
		t.Fatalf("completion = %+v", got)
	}
	published, err := os.ReadFile(store.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	user := t.TempDir()
	for _, args := range [][]string{{"status"}, {"project", "sync"}, {"init"}, {"session", "start"}} {
		cmd := exec.Command(v072, args...)
		cmd.Dir = home
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + user, "HAND_HOME=" + home, "SECONDHAND_HOME=" + filepath.Join(user, ".secondhand")}
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("v0.7.2 %v did not run to a failing exit: %v: %s", args, err, out)
		}
		t.Logf("v0.7.2 %v: %s", args, bytes.TrimSpace(out))
		if err == nil {
			t.Fatalf("v0.7.2 %v succeeded on a canonical home: %s", args, out)
		}
		if after, readErr := os.ReadFile(store.Path(home)); readErr != nil || !bytes.Equal(after, published) {
			t.Fatalf("v0.7.2 %v changed the published canonical DB (%v): %s", args, readErr, out)
		}
		for _, sidecar := range []string{"-journal", "-wal"} {
			if _, err := os.Lstat(store.Path(home) + sidecar); !os.IsNotExist(err) {
				t.Fatalf("v0.7.2 %v left %s beside the canonical DB: %v", args, sidecar, err)
			}
		}
	}
	if got := runHand(t, home, "cutover", "inspect", home); got.code != 0 || !strings.Contains(got.stdout, "disposition: canonical-authority") {
		t.Fatalf("inspect after v0.7.2 runs = %+v", got)
	}
}
