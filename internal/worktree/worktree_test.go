package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/state"
)

func writeFakeTreehouse(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	path := filepath.Join(bin, "treehouse")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGetParsesLeasePath(t *testing.T) {
	writeFakeTreehouse(t, `printf '{"path":"/tmp/wt-1"}'`)
	got, err := Get(t.TempDir(), "hand:task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/wt-1" {
		t.Fatalf("got %q, want /tmp/wt-1", got)
	}
}

func TestGetPassesLeaseHolder(t *testing.T) {
	writeFakeTreehouse(t, `
if [ "$4" != "--lease-holder" ] || [ "$5" != "hand:task-1" ]; then
	echo "unexpected args: $@" >&2
	exit 1
fi
printf '{"path":"/tmp/wt-1"}'
`)
	if _, err := Get(t.TempDir(), "hand:task-1"); err != nil {
		t.Fatal(err)
	}
}

func TestGetFailsOnNonZeroExit(t *testing.T) {
	writeFakeTreehouse(t, `echo "pool exhausted" >&2; exit 1`)
	if _, err := Get(t.TempDir(), ""); err == nil || !strings.Contains(err.Error(), "pool exhausted") {
		t.Fatalf("got err %v, want pool exhausted failure", err)
	}
}

func TestGetFailsOnMissingPath(t *testing.T) {
	writeFakeTreehouse(t, `printf '{}'`)
	if _, err := Get(t.TempDir(), ""); err == nil {
		t.Fatal("expected error for missing path in lease response")
	}
}

func TestReturnPassesForceFlag(t *testing.T) {
	writeFakeTreehouse(t, `
if [ "$1" != "return" ] || [ "$2" != "/tmp/wt-1" ] || [ "$3" != "--force" ]; then
	echo "unexpected args: $@" >&2
	exit 1
fi
`)
	if err := Return("/tmp/wt-1", true); err != nil {
		t.Fatal(err)
	}
}

func TestReturnFailsOnNonZeroExit(t *testing.T) {
	writeFakeTreehouse(t, `echo "worktree busy" >&2; exit 1`)
	if err := Return("/tmp/wt-1", false); err == nil || !strings.Contains(err.Error(), "worktree busy") {
		t.Fatalf("got err %v, want worktree busy failure", err)
	}
}

func TestCheckCollisionDetectsConflict(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.Task{ID: "other-task", Worktree: "/tmp/wt-shared"}); err != nil {
		t.Fatal(err)
	}

	conflict, err := CheckCollision(home, "/tmp/wt-shared", "new-task")
	if err != nil {
		t.Fatal(err)
	}
	if conflict != "other-task" {
		t.Fatalf("got conflict %q, want other-task", conflict)
	}
}

func TestCheckCollisionExcludesOwnTask(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.Task{ID: "same-task", Worktree: "/tmp/wt-shared"}); err != nil {
		t.Fatal(err)
	}

	conflict, err := CheckCollision(home, "/tmp/wt-shared", "same-task")
	if err != nil {
		t.Fatal(err)
	}
	if conflict != "" {
		t.Fatalf("got conflict %q, want none", conflict)
	}
}

func TestCheckCollisionNoConflict(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.Task{ID: "other-task", Worktree: "/tmp/wt-other"}); err != nil {
		t.Fatal(err)
	}

	conflict, err := CheckCollision(home, "/tmp/wt-shared", "new-task")
	if err != nil {
		t.Fatal(err)
	}
	if conflict != "" {
		t.Fatalf("got conflict %q, want none", conflict)
	}
}

func TestCheckCollisionEmptyStateDir(t *testing.T) {
	conflict, err := CheckCollision(t.TempDir(), "/tmp/wt-shared", "new-task")
	if err != nil {
		t.Fatal(err)
	}
	if conflict != "" {
		t.Fatalf("got conflict %q, want none", conflict)
	}
}
