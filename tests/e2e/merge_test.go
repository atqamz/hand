//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/state"
)

// TestMergePR drives `hand merge` through a faked gh, no real remote: a task
// whose PR checks are all green merges cleanly, and one whose checks include
// a failing bucket is refused before gh pr merge is ever invoked.
func TestMergePR(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	now := time.Now().UTC().Format(time.RFC3339)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, PR: "1", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-2", Project: "demo", Kind: state.KindShip, PR: "2", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	dir := binDir(t)
	invocationLog := filepath.Join(t.TempDir(), "gh-invocations.log")
	writeFakeDispatch(t, dir, "gh", invocationLog, "$1 $2", `  "pr checks")
    case "$3" in
      1) echo '[{"bucket":"pass"}]' ;;
      2) echo '[{"bucket":"fail"}]' ;;
      *) echo "unexpected pr checks arg: $3" >&2; exit 1 ;;
    esac
    ;;
  "pr merge") echo ok ;;`)

	clean := runHand(t, home, "merge", "task-1")
	if clean.code != 0 {
		t.Fatalf("merge task-1: exit %d, stderr %q", clean.code, clean.stderr)
	}
	task1, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !task1.Merged || task1.MergedAt == "" {
		t.Fatalf("task-1 state = %+v, want Merged=true and MergedAt set", task1)
	}

	refused := runHand(t, home, "merge", "task-2")
	assertInvocation(t, refused, 3, "not green")
	task2, err := state.Read(home, "task-2")
	if err != nil {
		t.Fatal(err)
	}
	if task2.Merged {
		t.Fatalf("task-2 state = %+v, want Merged=false after refused merge (red checks must never reach gh pr merge)", task2)
	}

	logData, err := os.ReadFile(invocationLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "pr merge 2") {
		t.Fatalf("gh invocation log = %q, want red checks to short-circuit before gh pr merge is ever called for task-2", logData)
	}
}
