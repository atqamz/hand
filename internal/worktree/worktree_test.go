package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/state"
)

// writeFakeTreehouse fakes "treehouse get"/"return". Get's doc comment above
// notes the real stderr banner ahead of JSON; these fakes write straight to
// stdout on success since Get reads stdout alone (cmd.Output()), which is
// exactly what that separation is for - no banner needed to exercise it
// faithfully. Failure fakes below write to stderr, matching both Get's
// separate-stderr-buffer error path and Return's CombinedOutput one.
//
// fakeTreehousePool models what real treehouse does to a returned worktree,
// checked against the tool itself: return leaves the pool slot's directory in
// place and flips the slot from leased to available, so a second return of the
// same path succeeds again (exit 0, with and without --force), while a path in no
// pool exits 1 with "is not managed by treehouse". A fake that answers a
// state-changing command identically before and after that command cannot test
// anything about the state change, which is why this one is keyed on the state
// return actually leaves behind.
const fakeTreehousePool = `
case "$1" in
return)
	if [ -d "$2" ]; then
		echo "Worktree returned to pool."
		exit 0
	fi
	echo "worktree $2 is not managed by treehouse" >&2
	exit 1
	;;
esac
echo "unexpected args: $@" >&2
exit 1
`

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
	wt := t.TempDir()
	writeFakeTreehouse(t, `
if [ "$1" != "return" ] || [ "$2" != "`+wt+`" ] || [ "$3" != "--force" ]; then
	echo "unexpected args: $@" >&2
	exit 1
fi
`)
	if err := Return(wt, true); err != nil {
		t.Fatal(err)
	}
}

func TestReturnFailsOnNonZeroExit(t *testing.T) {
	writeFakeTreehouse(t, `echo "worktree busy" >&2; exit 1`)
	if err := Return(t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "worktree busy") {
		t.Fatalf("got err %v, want worktree busy failure", err)
	}
}

// Returning a worktree that is already back in the pool succeeds, so teardown can
// run its cleanup a second time after a later step faulted. The path surviving the
// first return is the point: it is why nothing here may infer "already returned"
// from the path being gone.
func TestReturnIsIdempotentOnAnAlreadyReturnedWorktree(t *testing.T) {
	wt := t.TempDir()
	writeFakeTreehouse(t, fakeTreehousePool)
	if err := Return(wt, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree path gone after return: %v", err)
	}
	if err := Return(wt, false); err != nil {
		t.Fatalf("got err %v, want a repeated return to succeed", err)
	}
}

func TestReturnFailsOnAWorktreeNoPoolManages(t *testing.T) {
	writeFakeTreehouse(t, fakeTreehousePool)
	err := Return(filepath.Join(t.TempDir(), "gone"), false)
	if err == nil || !strings.Contains(err.Error(), "not managed by treehouse") {
		t.Fatalf("got err %v, want an unmanaged worktree reported", err)
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
