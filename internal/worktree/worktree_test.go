package worktree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/state"
)

func writeFakeTreehouse(t *testing.T, response faketool.TreehouseResponse) {
	t.Helper()
	faketool.Treehouse{Responses: []faketool.TreehouseResponse{response}}.Install(t, faketool.Bin(t))
}

func writeCollisionTask(t *testing.T, home, id, path, leaseID string) {
	t.Helper()
	if err := state.CreateTask(home, state.Task{ID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.CreateAttempt(home, state.Attempt{TaskID: id, Lifecycle: state.AttemptRunning, Worktree: path, LeaseID: leaseID}); err != nil {
		t.Fatal(err)
	}
}

func TestGetParsesLeasePathAndIdentity(t *testing.T) {
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "get", Stdout: `{"path":"/tmp/wt-1","lease_id":"5fe5412a4aabdeb85a148d6d73eb42d8"}`})
	got, err := Get(t.TempDir(), "hand:task-1")
	if err != nil {
		t.Fatal(err)
	}
	want := Lease{Path: "/tmp/wt-1", ID: "5fe5412a4aabdeb85a148d6d73eb42d8"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// A treehouse older than v2.1.0 reports no identity at all, and that has to
// stay a usable lease rather than an error - CheckCollision falls back to the
// path for it.
func TestGetAcceptsAPayloadWithoutALeaseIdentity(t *testing.T) {
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "get", Stdout: `{"path":"/tmp/wt-1"}`})
	got, err := Get(t.TempDir(), "hand:task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != (Lease{Path: "/tmp/wt-1"}) {
		t.Fatalf("got %+v, want path-only lease", got)
	}
}

func TestHeadCommitReturnsTheWorktreeCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worktree")
	faketool.InitRepo(t, path)
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = path
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(output))

	got, err := HeadCommit(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || len(got) != 40 {
		t.Fatalf("HeadCommit() = %q, want %q", got, want)
	}
}

func TestGetPassesLeaseHolder(t *testing.T) {
	writeFakeTreehouse(t, faketool.TreehouseResponse{
		Command: "get", Args: []string{"--lease", "--json", "--lease-holder", "hand:task-1"},
		Stdout: `{"path":"/tmp/wt-1"}`,
	})
	if _, err := Get(t.TempDir(), "hand:task-1"); err != nil {
		t.Fatal(err)
	}
}

func TestGetFailsOnNonZeroExit(t *testing.T) {
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "get", Stderr: "pool exhausted\n", Exit: 1})
	if _, err := Get(t.TempDir(), ""); err == nil || !strings.Contains(err.Error(), "pool exhausted") {
		t.Fatalf("got err %v, want pool exhausted failure", err)
	}
}

func TestGetFailsOnMissingPath(t *testing.T) {
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "get", Stdout: `{}`})
	if _, err := Get(t.TempDir(), ""); err == nil {
		t.Fatal("expected error for missing path in lease response")
	}
}

func TestObserveLeaseDistinguishesExactMissingReusedAndLegacyOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	for _, test := range []struct {
		name    string
		leaseID string
		want    LeaseObservationState
	}{
		{name: "exact", leaseID: "lease-1", want: LeaseExact},
		{name: "reused", leaseID: "lease-old", want: LeaseMismatch},
		{name: "legacy", leaseID: "", want: LeaseUnprovable},
	} {
		t.Run(test.name, func(t *testing.T) {
			faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-1"}}.Install(t, faketool.Bin(t))
			got := ObserveLease(path, test.leaseID)
			if got.State != test.want {
				t.Fatalf("ObserveLease() = %+v, want %s", got, test.want)
			}
			if got.Probe != (LeaseProbe{}) {
				t.Fatalf("ObserveLease() probe = %+v, want no probe on an observed pool", got.Probe)
			}
		})
	}
}

func TestObserveLeaseReportsAbsentAfterReturn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-1"}}.Install(t, faketool.Bin(t))
	if err := ReturnLease(path, "lease-1", true); err != nil {
		t.Fatal(err)
	}
	if got := ObserveLease(path, "lease-1"); got.State != LeaseAbsent {
		t.Fatalf("ObserveLease() = %+v, want absent", got)
	}
}

func TestObserveLeaseReportsAbsentWhenTheRecordedPathIsGone(t *testing.T) {
	if got := ObserveLease(filepath.Join(t.TempDir(), "gone"), "lease-1"); got.State != LeaseAbsent {
		t.Fatalf("ObserveLease() = %+v, want absent", got)
	}
}

// Every cause that stops the pool from being observed is one classification, never absence and
// never a mismatch: the orphaned pool of atqamz/hand#245 is the first case here.
func TestObserveLeaseReportsUnknownForEveryUnobservablePool(t *testing.T) {
	for _, test := range []struct {
		name       string
		install    func(t *testing.T, path string)
		wantReason string
	}{
		{
			name: "pool has no entries",
			install: func(t *testing.T, path string) {
				faketool.Treehouse{}.Install(t, faketool.Bin(t))
			},
			wantReason: "no pool entries",
		},
		{
			name: "pool names other worktrees only",
			install: func(t *testing.T, path string) {
				other := filepath.Join(filepath.Dir(path), "wt-other")
				faketool.Treehouse{Slots: []string{other}}.Install(t, faketool.Bin(t))
			},
			wantReason: "none names this worktree",
		},
		{
			name: "status exits non-zero",
			install: func(t *testing.T, path string) {
				writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "status", Stderr: "pool lock is held\n", Exit: 1})
			},
			wantReason: "pool lock is held",
		},
		{
			name: "treehouse is not installed",
			install: func(t *testing.T, path string) {
				faketool.NoTools(t)
			},
			wantReason: "not executable",
		},
		{
			name: "status prints malformed JSON",
			install: func(t *testing.T, path string) {
				writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "status", Stdout: "{not json"})
			},
			wantReason: "not a JSON array",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wt-1")
			faketool.InitRepo(t, path)
			test.install(t, path)
			got := ObserveLease(path, "lease-1")
			if got.State != LeaseUnknown {
				t.Fatalf("ObserveLease() = %+v, want %s", got, LeaseUnknown)
			}
			if got.Probe.Command != "treehouse status --json" || got.Probe.WorkingDir != path {
				t.Fatalf("ObserveLease() probe = %+v, want the command and working directory that selected the pool", got.Probe)
			}
			if !strings.Contains(got.Probe.Reason, test.wantReason) {
				t.Fatalf("ObserveLease() probe reason = %q, want it to contain %q", got.Probe.Reason, test.wantReason)
			}
		})
	}
}

// An unobservable pool and a lease held by somebody else are different facts, and a caller has to
// be able to tell them apart without reading a message.
func TestUnknownAndMismatchAreDistinguishableWithoutReadingAMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-2"}}.Install(t, faketool.Bin(t))
	mismatch := ObserveLease(path, "lease-1")
	if mismatch.State != LeaseMismatch || mismatch.LeaseID != "lease-2" {
		t.Fatalf("ObserveLease() = %+v, want a mismatch naming the observed lease", mismatch)
	}

	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "status", Stdout: "[]"})
	unknown := ObserveLease(path, "lease-1")
	if unknown.State != LeaseUnknown {
		t.Fatalf("ObserveLease() = %+v, want %s", unknown, LeaseUnknown)
	}
	if unknown.State == mismatch.State {
		t.Fatal("an unobservable pool is reported with the same state as a lease held by another owner")
	}
	if unknown.LeaseID != "" {
		t.Fatalf("ObserveLease() = %+v, want no observed lease identity when nothing was observed", unknown)
	}

	message := (&UnprovenLeaseError{WorktreePath: path, ExpectedLeaseID: "lease-1", Observation: unknown}).Error()
	for _, forbidden := range []string{"does not match", "belongs to another owner", "is held as"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("unobservable diagnostic = %q, want no claim of %q", message, forbidden)
		}
	}
	for _, want := range []string{"could not be observed", "treehouse status --json", path, "could not be proven, not because a lease mismatched"} {
		if !strings.Contains(message, want) {
			t.Fatalf("unobservable diagnostic = %q, want it to contain %q", message, want)
		}
	}
}

// The same recorded ownership that could not be observed once is proven on a later observation,
// with no durable trace of the failed attempt.
func TestObserveLeaseProvesOwnershipAfterATransientUnobservablePool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "status", Stderr: "connection reset\n", Exit: 1})
	if got := ObserveLease(path, "lease-1"); got.State != LeaseUnknown {
		t.Fatalf("ObserveLease() = %+v, want %s", got, LeaseUnknown)
	}
	faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-1"}}.Install(t, faketool.Bin(t))
	if got := ObserveLease(path, "lease-1"); got.State != LeaseExact {
		t.Fatalf("ObserveLease() = %+v, want %s once the pool answers again", got, LeaseExact)
	}
}

func TestObserveCleanlinessIncludesUntrackedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	got, err := ObserveCleanliness(path)
	if err != nil || got != Clean {
		t.Fatalf("clean worktree = %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(path, "untracked"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = ObserveCleanliness(path)
	if err != nil || got != Dirty {
		t.Fatalf("dirty worktree = %q, %v", got, err)
	}
}

func TestObserveLeaseRunsStatusFromTheWorktreeRepository(t *testing.T) {
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	faketool.InitRepo(t, worktreePath)
	faketool.Treehouse{Slots: []string{worktreePath}}.Install(t, faketool.Bin(t))

	lease, err := Get(worktreePath, "hand:task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := ObserveLease(worktreePath, lease.ID); got.State != LeaseExact {
		t.Fatalf("ObserveLease() = %+v, want the lease visible from the worktree repository", got)
	}
}

func TestObserveLeaseRejectsARecycledOrMissingIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-2"}}.Install(t, faketool.Bin(t))
	if got := ObserveLease(path, "lease-1"); got.State != LeaseMismatch {
		t.Fatalf("ObserveLease() = %+v, want a recycled lease identity reported as %s", got, LeaseMismatch)
	}
	if got := ObserveLease(path, ""); got.State != LeaseUnprovable {
		t.Fatalf("ObserveLease() = %+v, want an empty expected identity reported as %s", got, LeaseUnprovable)
	}
}

func TestObserveLeaseRejectsAnAvailableStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	faketool.Treehouse{Slots: []string{path}}.Install(t, faketool.Bin(t))
	if got := ObserveLease(path, "lease-1"); got.State != LeaseAbsent {
		t.Fatalf("ObserveLease() = %+v, want an available entry reported as %s", got, LeaseAbsent)
	}
}

func TestReturnPassesForceFlag(t *testing.T) {
	wt := t.TempDir()
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "return", Args: []string{wt, "--force"}})
	if err := Return(wt, true); err != nil {
		t.Fatal(err)
	}
}

func TestReturnLeasePassesConditionalLeaseIdentity(t *testing.T) {
	wt := t.TempDir()
	writeFakeTreehouse(t, faketool.TreehouseResponse{
		Command: "return", Args: []string{"--force", "--if-lease-id", "lease-1", wt},
	})
	if err := ReturnLease(wt, "lease-1", true); err != nil {
		t.Fatal(err)
	}
}

func TestReturnLeaseRejectsMismatchedIdentityAndKeepsLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-2"}}.Install(t, faketool.Bin(t))

	if err := ReturnLease(path, "lease-1", true); err == nil {
		t.Fatal("ReturnLease() succeeded for a mismatched lease identity")
	}
	if _, err := Get(path, "hand:still-held"); err == nil {
		t.Fatal("mismatched ReturnLease() released the current lease")
	}
}

func TestReturnLeaseProtectsAReusedPathFromAStaleLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-original"}}.Install(t, faketool.Bin(t))

	if err := ReturnLease(path, "lease-original", true); err != nil {
		t.Fatal(err)
	}
	l2, err := Get(path, "hand:l2")
	if err != nil {
		t.Fatal(err)
	}
	if err := ReturnLease(path, "lease-original", true); err == nil {
		t.Fatal("stale ReturnLease() released the reused path")
	}
	if _, err := Get(path, "hand:still-held"); err == nil {
		t.Fatalf("stale ReturnLease() released current lease %q", l2.ID)
	}
}

func TestReturnLeaseFallsBackForAnEmptyLeaseIdentity(t *testing.T) {
	wt := t.TempDir()
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "return", Args: []string{wt, "--force"}})
	if err := ReturnLease(wt, "", true); err != nil {
		t.Fatal(err)
	}
}

func TestReturnFailsOnNonZeroExit(t *testing.T) {
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "return", Stderr: "worktree busy\n", Exit: 1})
	if err := Return(t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "worktree busy") {
		t.Fatalf("got err %v, want worktree busy failure", err)
	}
}

// Teardown reruns its cleanup after a later step faults, so a second return has to
// succeed. The path surviving the first is the point: it is why nothing may infer
// "already returned" from the path being gone.
func TestReturnIsIdempotentOnAnAlreadyReturnedWorktree(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	faketool.InitRepo(t, wt)
	faketool.Treehouse{Held: []string{wt}}.Install(t, faketool.Bin(t))

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
	faketool.Treehouse{Slots: []string{t.TempDir()}}.Install(t, faketool.Bin(t))
	err := Return(filepath.Join(t.TempDir(), "gone"), false)
	if err == nil || !strings.Contains(err.Error(), "not managed by treehouse") {
		t.Fatalf("got err %v, want an unmanaged worktree reported", err)
	}
}

// treehouse exits 0 on the abort, so nothing but the aborted text distinguishes
// this from a return that freed the slot. Reporting it as success leaks the lease:
// the caller deletes the task row that is the only remaining record of the holder.
func TestReturnFailsWhenAnUnforcedDirtyReturnAborts(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	faketool.InitRepo(t, wt)
	faketool.Treehouse{Held: []string{wt}}.Install(t, faketool.Bin(t))
	if err := os.WriteFile(filepath.Join(wt, "dirt.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Return(wt, false)
	if err == nil || !strings.Contains(err.Error(), "still leased") {
		t.Fatalf("got err %v, want the aborted return reported as a failure", err)
	}
	if !errors.Is(err, ErrReturnAborted) {
		t.Fatalf("got err %v, want ErrReturnAborted", err)
	}
	if out, gitErr := exec.Command("git", "-C", wt, "status", "--porcelain").Output(); gitErr != nil || len(out) == 0 {
		t.Fatalf("worktree cleaned by an aborted return: %q %v", out, gitErr)
	}
	if err := Return(wt, true); err != nil {
		t.Fatalf("got err %v, want the forced retry to succeed", err)
	}
}

func TestCheckCollisionDetectsAConflictOnLeaseIdentity(t *testing.T) {
	home := t.TempDir()
	writeCollisionTask(t, home, "other-task", "/tmp/wt-shared", "lease-1")

	conflict, err := CheckCollision(home, Lease{Path: "/tmp/wt-shared", ID: "lease-1"}, "new-task")
	if err != nil {
		t.Fatal(err)
	}
	if conflict != "other-task" {
		t.Fatalf("got conflict %q, want other-task", conflict)
	}
}

// Teardown returns the worktree before state.Delete, so a failed Delete leaves a row still naming
// path P while treehouse has freed P and handed it to the next task under a lease of its own. That
// is not a collision, and refusing the spawn over it was the bug.
func TestCheckCollisionAllowsAReusedPathUnderAFreshLease(t *testing.T) {
	home := t.TempDir()
	writeCollisionTask(t, home, "stale-task", "/tmp/wt-shared", "lease-1")

	conflict, err := CheckCollision(home, Lease{Path: "/tmp/wt-shared", ID: "lease-2"}, "new-task")
	if err != nil {
		t.Fatal(err)
	}
	if conflict != "" {
		t.Fatalf("got conflict %q, want none: a recycled pool slot under a new lease is not a collision", conflict)
	}
}

// A row written before the lease_id column existed has no identity to compare,
// so it keeps being checked by path until its task is torn down and respawned.
func TestCheckCollisionFallsBackToPathForARowWithNoIdentity(t *testing.T) {
	home := t.TempDir()
	writeCollisionTask(t, home, "legacy-task", "/tmp/wt-shared", "")

	conflict, err := CheckCollision(home, Lease{Path: "/tmp/wt-shared", ID: "lease-2"}, "new-task")
	if err != nil {
		t.Fatal(err)
	}
	if conflict != "legacy-task" {
		t.Fatalf("got conflict %q, want legacy-task", conflict)
	}
}

// The mirror case: a treehouse older than v2.1.0 reports no identity, so even
// rows that have one are compared by path.
func TestCheckCollisionFallsBackToPathForALeaseWithNoIdentity(t *testing.T) {
	home := t.TempDir()
	writeCollisionTask(t, home, "other-task", "/tmp/wt-shared", "lease-1")

	conflict, err := CheckCollision(home, Lease{Path: "/tmp/wt-shared"}, "new-task")
	if err != nil {
		t.Fatal(err)
	}
	if conflict != "other-task" {
		t.Fatalf("got conflict %q, want other-task", conflict)
	}
}

func TestCheckCollisionExcludesOwnTask(t *testing.T) {
	home := t.TempDir()
	writeCollisionTask(t, home, "same-task", "/tmp/wt-shared", "lease-1")

	conflict, err := CheckCollision(home, Lease{Path: "/tmp/wt-shared", ID: "lease-1"}, "same-task")
	if err != nil {
		t.Fatal(err)
	}
	if conflict != "" {
		t.Fatalf("got conflict %q, want none", conflict)
	}
}

func TestCheckCollisionNoConflict(t *testing.T) {
	home := t.TempDir()
	writeCollisionTask(t, home, "other-task", "/tmp/wt-other", "lease-1")

	conflict, err := CheckCollision(home, Lease{Path: "/tmp/wt-shared", ID: "lease-2"}, "new-task")
	if err != nil {
		t.Fatal(err)
	}
	if conflict != "" {
		t.Fatalf("got conflict %q, want none", conflict)
	}
}

func TestCheckCollisionEmptyStateDir(t *testing.T) {
	conflict, err := CheckCollision(t.TempDir(), Lease{Path: "/tmp/wt-shared", ID: "lease-1"}, "new-task")
	if err != nil {
		t.Fatal(err)
	}
	if conflict != "" {
		t.Fatalf("got conflict %q, want none", conflict)
	}
}
