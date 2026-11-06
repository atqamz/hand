//go:build !windows

package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestFleetMoveResumeNamesThePreMoveDatabase(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores folder permissions")
	}
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	dest := filepath.Join(t.TempDir(), "moved")
	st, err := state.Open(filepath.Join(home, "hand.db"), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.MoveHome(context.Background(), home, dest); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(home, "hand.db.pre-move-20260101T000000")
	if err := os.WriteFile(backup, []byte("pre-move"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(dest)
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parent, 0o755)
	_, errOut, code := h.run("fleet", "move", "--yes", dest)
	if code != 3 || !strings.Contains(errOut, "the folder did not move") || !strings.Contains(errOut, backup) || !strings.Contains(errOut, "undo it by copying") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("the failed resume removed the home: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("the failed resume created %s: %v", dest, err)
	}
	if out := h.ok("fleet", "move", "--dry-run", dest); !strings.Contains(out, "move: pending") {
		t.Fatalf("dry run after the failed resume = %q", out)
	}
}
