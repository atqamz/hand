package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/testtag"
)

// Some tests here (TestPoolRootDiffersPerClone and friends) call poolRoot/PoolsRoot directly rather
// than through faketool.Bin, so the package needs its own isolated SECONDHAND_HOME regardless of
// whether the invoking `go test` was wrapped by `make test` (atqamz/hand#413).
func TestMain(m *testing.M) {
	if !testtag.Present {
		testtag.Refuse()
	}
	root, err := os.MkdirTemp("", "hand-worktree-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("SECONDHAND_HOME", filepath.Join(root, "Secondhand 測試")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(root)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
