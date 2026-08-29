package worktree

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/state"
)

func testGet(clonePath, leaseHolder string) (Lease, error) {
	return Get(clonePath, leaseHolder)
}

func testObserveLease(worktreePath, expectedLeaseID string) LeaseObservation {
	return ObserveLease(filepath.Dir(worktreePath), worktreePath, expectedLeaseID)
}

func testReturn(worktreePath string, force bool) error {
	return Return(filepath.Dir(worktreePath), worktreePath, force)
}

func testReturnLease(worktreePath, leaseID string, force bool) error {
	return ReturnLease(filepath.Dir(worktreePath), worktreePath, leaseID, force)
}

func writeFakeTreehouse(t *testing.T, response faketool.TreehouseResponse) {
	t.Helper()
	writeFakeTreehouseAt(t, faketool.Bin(t), response)
}

// faketool.Bin repoints SECONDHAND_HOME, and poolRoot reads it, so a test asserting the resolved
// --root has to take the bin directory first and compute the expected root after it.
func writeFakeTreehouseAt(t *testing.T, bin string, response faketool.TreehouseResponse) {
	t.Helper()
	faketool.Treehouse{Responses: []faketool.TreehouseResponse{response}}.Install(t, bin)
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	slot := filepath.Join(t.TempDir(), "slot")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", slot)
	faketool.Treehouse{Held: []string{slot}, LeaseIDs: map[string]string{slot: "5fe5412a4aabdeb85a148d6d73eb42d8"}, Responses: []faketool.TreehouseResponse{{Command: "get", Stdout: mustJSON(t, map[string]string{"path": slot, "lease_id": "5fe5412a4aabdeb85a148d6d73eb42d8"})}}}.Install(t, faketool.Bin(t))
	got, err := testGet(clone, "hand:task-1")
	if err != nil {
		t.Fatal(err)
	}
	want := Lease{Path: slot, ID: "5fe5412a4aabdeb85a148d6d73eb42d8"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// A treehouse older than v2.1.0 reports no identity at all, and that has to
// stay a usable lease rather than an error - CheckCollision falls back to the
// path for it.
func TestGetAcceptsAPayloadWithoutALeaseIdentity(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	slot := filepath.Join(t.TempDir(), "slot")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", slot)
	faketool.Treehouse{Held: []string{slot}, NoLeaseIdentity: true, Responses: []faketool.TreehouseResponse{
		{Command: "get", Stdout: mustJSON(t, map[string]string{"path": slot})},
	}}.Install(t, faketool.Bin(t))
	got, err := testGet(clone, "hand:task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != (Lease{Path: slot}) {
		t.Fatalf("got %+v, want path-only lease", got)
	}
}

func TestGetRejectsAMissingAcquiredWorktree(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	missing := filepath.Join(t.TempDir(), "missing")
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "get", Stdout: mustJSON(t, map[string]string{"path": missing, "lease_id": "lease-1"})})

	if _, err := Get(clone, "hand:task-1"); err == nil || !strings.Contains(err.Error(), "unsound worktree") {
		t.Fatalf("Get() error = %v, want missing acquired worktree rejection", err)
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
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	slot := filepath.Join(t.TempDir(), "slot")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", slot)
	writeFakeTreehouse(t, faketool.TreehouseResponse{
		Command: "get", Args: []string{"--lease", "--json", "--lease-holder", "hand:task-1"},
		Stdout: mustJSON(t, map[string]string{"path": slot}),
	})
	if _, err := testGet(clone, "hand:task-1"); err == nil {
		t.Fatal("Get() unexpectedly accepted an unreported lease")
	}
}

func TestPoolStatusListsEverySlotFromThePoolOutsideEveryFleetHome(t *testing.T) {
	bin := faketool.Bin(t)
	home := t.TempDir()
	clone := filepath.Join(home, "projects", "demo")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := poolRoot(clone, "")
	if err != nil {
		t.Fatal(err)
	}
	if withinPath(home, root) {
		t.Fatalf("pool root %s is inside fleet home %s, which is what atqamz/hand#427 fixes", root, home)
	}
	writeFakeTreehouseAt(t, bin, faketool.TreehouseResponse{
		Command: "--root", Args: []string{root, "status", "--json"},
		Stdout: mustJSON(t, []map[string]string{
			{"path": "/pool/4", "status": "available"},
			{"path": "/pool/5", "status": "leased", "lease_id": "43247d2cb7424bac", "lease_holder": "hand:f_abc123:340-scout"},
		}),
	})
	entries, err := PoolStatus(clone)
	if err != nil {
		t.Fatal(err)
	}
	want := []PoolEntry{
		{Path: "/pool/4", Status: "available"},
		{Path: "/pool/5", Status: "leased", LeaseID: "43247d2cb7424bac", LeaseHolder: "hand:f_abc123:340-scout"},
	}
	if len(entries) != len(want) || entries[0] != want[0] || entries[1] != want[1] {
		t.Fatalf("PoolStatus() = %#v, want %#v", entries, want)
	}
}

func TestParseLeaseHolder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		holder  string
		fleetID string
		taskID  string
		ok      bool
	}{
		{name: "well formed", holder: "hand:f_167a403f6e12d103a5d310cc10fecedc:340-scout", fleetID: "f_167a403f6e12d103a5d310cc10fecedc", taskID: "340-scout", ok: true},
		{name: "non-Hand lease", holder: "codex-196", ok: false},
		{name: "empty", holder: "", ok: false},
		{name: "missing task id", holder: "hand:f_abc:", ok: false},
		{name: "missing fleet id", holder: "hand::340-scout", ok: false},
		{name: "wrong prefix", holder: "other:f_abc:340-scout", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleetID, taskID, ok := ParseLeaseHolder(tc.holder)
			if ok != tc.ok || (ok && (fleetID != tc.fleetID || taskID != tc.taskID)) {
				t.Fatalf("ParseLeaseHolder(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.holder, fleetID, taskID, ok, tc.fleetID, tc.taskID, tc.ok)
			}
		})
	}
}

func TestGetAcquiresFromAPoolOutsideEveryFleetHome(t *testing.T) {
	bin := faketool.Bin(t)
	home := t.TempDir()
	clone := filepath.Join(home, "projects", "demo")
	faketool.InitRepo(t, clone)
	slot := filepath.Join(t.TempDir(), "slot")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", slot)
	root, err := poolRoot(clone, "")
	if err != nil {
		t.Fatal(err)
	}
	if withinPath(home, root) {
		t.Fatalf("pool root %s is inside fleet home %s, which is what atqamz/hand#427 fixes", root, home)
	}
	writeFakeTreehouseAt(t, bin, faketool.TreehouseResponse{
		Command: "--root", Args: []string{root, "get", "--lease", "--json", "--lease-holder", "hand:task-1"},
		Stdout: mustJSON(t, map[string]string{"path": slot}),
	})
	if _, err := Get(clone, "hand:task-1"); err == nil {
		t.Fatal("Get() unexpectedly accepted an unreported lease")
	}
}

func TestPoolRootDiffersPerClone(t *testing.T) {
	first, err := poolRoot(filepath.Join(t.TempDir(), "projects", "demo"), "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := poolRoot(filepath.Join(t.TempDir(), "projects", "demo"), "")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("two clones share pool root %s, so one fleet's worktrees can alias another's", first)
	}
}

func TestPoolRootKeepsALegacyWorktreeOnItsOwnClone(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "projects", "demo")
	legacy := filepath.Join(clone, ".treehouse", "demo-123", "1", "demo")
	got, err := poolRoot(clone, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if got != clone {
		t.Fatalf("poolRoot() = %s, want the clone %s so a lease taken before atqamz/hand#427 stays returnable", got, clone)
	}
}

func TestGetRejectsAWorktreeFromAnotherRegisteredClone(t *testing.T) {
	home := t.TempDir()
	clone := filepath.Join(home, "projects", "demo")
	foreign := filepath.Join(t.TempDir(), "demo")
	faketool.InitRepo(t, clone)
	faketool.InitRepo(t, foreign)
	faketool.Treehouse{Responses: []faketool.TreehouseResponse{
		{Command: "get", Stdout: mustJSON(t, map[string]string{"path": foreign, "lease_id": "lease-1"})},
		{Command: "return", Args: []string{"--force", "--if-lease-id", "lease-1", foreign}},
	}}.Install(t, faketool.Bin(t))
	if _, err := Get(clone, "hand:task-1"); err == nil || !strings.Contains(err.Error(), "another Git repository") {
		t.Fatalf("Get() error = %v, want a registered-clone mismatch", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign worktree was mutated or removed: %v", err)
	}
}

func TestGetRejectsAnAliasedStaleSlotWithoutReleasingItsLease(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	live := filepath.Join(t.TempDir(), "live")
	stale := filepath.Join(t.TempDir(), "stale")
	log := filepath.Join(t.TempDir(), "treehouse.log")
	runGit(t, clone, "worktree", "add", "-q", "-b", "live", live)
	runGit(t, clone, "worktree", "add", "-q", "-b", "stale", stale)
	metadata, err := os.ReadFile(filepath.Join(live, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, ".git"), metadata, 0o644); err != nil {
		t.Fatal(err)
	}
	faketool.Treehouse{
		Log:   log,
		Slots: []string{stale},
		Responses: []faketool.TreehouseResponse{
			{Command: "get", Stdout: mustJSON(t, map[string]string{"path": stale, "lease_id": "lease-1"})},
			{Command: "status", Stdout: mustJSON(t, []map[string]string{{"path": stale, "status": "leased", "lease_id": "lease-1"}})},
		},
	}.Install(t, faketool.Bin(t))

	if _, err := Get(clone, "hand:task-1"); err == nil {
		t.Fatal("Get() accepted a stale slot whose metadata points at another worktree")
	}
	entries, err := PoolStatus(clone)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Status != "leased" {
		t.Fatalf("PoolStatus() = %#v, want the aliased stale slot left leased", entries)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), " return ") {
		t.Fatalf("treehouse calls = %q, want no recovery mutation", calls)
	}
}

func TestCheckSoundnessReportsMissingAndAliasedMetadata(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	live := filepath.Join(t.TempDir(), "live")
	stale := filepath.Join(t.TempDir(), "stale")
	runGit(t, clone, "worktree", "add", "-q", "-b", "live", live)
	runGit(t, clone, "worktree", "add", "-q", "-b", "stale", stale)
	if got := CheckSoundness(clone, live); !got.Sound {
		t.Fatalf("CheckSoundness() = %+v, want a sound linked worktree", got)
	}
	metadata, err := os.ReadFile(filepath.Join(live, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, ".git"), metadata, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CheckSoundness(clone, stale); got.Sound || !containsFailure(got, "does not point back") {
		t.Fatalf("CheckSoundness() = %+v, want an aliased metadata failure", got)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(missing, ".git"), []byte("gitdir: "+filepath.Join(missing, "gone")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CheckSoundness(clone, missing); got.Sound || !containsFailure(got, "metadata directory does not exist") {
		t.Fatalf("CheckSoundness() = %+v, want a missing metadata failure", got)
	}
}

func TestDiscoverPoolSlotsFindsMetadataAliasesAcrossPoolRoots(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	firstRoot := filepath.Join(clone, ".treehouse")
	secondRoot := filepath.Join(t.TempDir(), ".treehouse")
	first := filepath.Join(firstRoot, "pool-a", "1", "demo")
	second := filepath.Join(secondRoot, "pool-b", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "first", first)
	runGit(t, clone, "worktree", "add", "-q", "-b", "second", second)
	metadata, err := os.ReadFile(filepath.Join(first, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, ".git"), metadata, 0o644); err != nil {
		t.Fatal(err)
	}

	slots, err := DiscoverPoolSlots(clone, firstRoot, secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 2 {
		t.Fatalf("DiscoverPoolSlots() = %+v, want both pool slots", slots)
	}
	collisions := PoolSlotCollisions(slots)
	if len(collisions) != 1 || len(collisions[0]) != 2 {
		t.Fatalf("PoolSlotCollisions() = %+v, want one two-slot collision", collisions)
	}
}

func TestDiscoverPoolSlotsFindsAnArbitraryMissingMetadataTarget(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	root := filepath.Join(t.TempDir(), ".treehouse")
	worktree := filepath.Join(root, "pool-a", "1", "demo")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "unrelated", "missing")
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+missing+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	slots, err := DiscoverPoolSlots(clone, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 || slots[0].Path != worktree || slots[0].Soundness.Sound {
		t.Fatalf("DiscoverPoolSlots() = %+v, want the unsound slot retained for reporting", slots)
	}
}

func TestDiscoverPoolSlotsFindsTheCurrentPerClonePoolLayout(t *testing.T) {
	fleetHome := t.TempDir()
	secondhand := filepath.Join(fleetHome, "secondhand")
	t.Setenv("HOME", fleetHome)
	t.Setenv("SECONDHAND_HOME", secondhand)
	clone := filepath.Join(fleetHome, "projects", "demo")
	faketool.InitRepo(t, clone)
	pool, err := poolRoot(clone, "")
	if err != nil {
		t.Fatal(err)
	}
	slot := filepath.Join(pool, ".treehouse", "pool", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", slot)

	slots, err := DiscoverPoolSlots(clone, PoolSearchRoots(fleetHome, clone)...)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 || slots[0].Path != slot || !slots[0].Soundness.Sound {
		t.Fatalf("DiscoverPoolSlots() = %+v, want the current per-clone pool slot", slots)
	}
}

func containsFailure(result SlotSoundness, want string) bool {
	for _, failure := range result.Failures {
		if strings.Contains(failure, want) {
			return true
		}
	}
	return false
}

func TestGetRejectsAnAcquiredSlotWhenStatusOmitsIt(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	slot := filepath.Join(t.TempDir(), "slot")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", slot)
	log := filepath.Join(t.TempDir(), "treehouse.log")
	faketool.Treehouse{
		Log: log,
		Responses: []faketool.TreehouseResponse{
			{Command: "get", Stdout: mustJSON(t, map[string]string{"path": slot, "lease_id": "lease-1"})},
			{Command: "status", Stdout: "[]"},
			{Command: "return", Args: []string{"--if-lease-id", "lease-1", slot}},
		},
	}.Install(t, faketool.Bin(t))

	if _, err := Get(clone, "hand:task-1"); err == nil || !strings.Contains(err.Error(), "status omitted") {
		t.Fatalf("Get() error = %v, want status omission", err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "return --if-lease-id lease-1") {
		t.Fatalf("treehouse calls = %q, want conditional lease recovery", calls)
	}
}

func TestGetReturnsASoundSlotWhenLegacyStatusOmitsItsLease(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	slot := filepath.Join(t.TempDir(), "slot")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", slot)
	log := filepath.Join(t.TempDir(), "treehouse.log")
	faketool.Treehouse{
		Log: log,
		Responses: []faketool.TreehouseResponse{
			{Command: "get", Stdout: mustJSON(t, map[string]string{"path": slot})},
			{Command: "status", Stdout: "[]"},
			{Command: "return", Args: []string{slot}},
		},
	}.Install(t, faketool.Bin(t))

	if _, err := Get(clone, "hand:task-1"); err == nil || !strings.Contains(err.Error(), "status omitted") {
		t.Fatalf("Get() error = %v, want status omission", err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "return "+slot) {
		t.Fatalf("treehouse calls = %q, want path-only legacy recovery", calls)
	}
}

func TestGetRefusesAnUnprovableSlotWithoutMutatingIt(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	foreign := filepath.Join(t.TempDir(), "foreign")
	faketool.InitRepo(t, foreign)
	faketool.Treehouse{Slots: []string{foreign}}.Install(t, faketool.Bin(t))

	if _, err := Get(clone, "hand:task-1"); err == nil {
		t.Fatal("Get() accepted a repository-root slot with unprovable ownership")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("unprovable worktree was mutated or removed: %v", err)
	}
	entries, err := PoolStatus(clone)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Status != "leased" {
		t.Fatalf("PoolStatus() = %#v, want the unprovable lease left untouched", entries)
	}
}

func TestGetFailsOnNonZeroExit(t *testing.T) {
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "get", Stderr: "pool exhausted\n", Exit: 1})
	if _, err := testGet(t.TempDir(), ""); err == nil || !strings.Contains(err.Error(), "pool exhausted") {
		t.Fatalf("got err %v, want pool exhausted failure", err)
	}
}

func TestGetFailsOnMissingPath(t *testing.T) {
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "get", Stdout: `{}`})
	if _, err := testGet(t.TempDir(), ""); err == nil {
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
			got := testObserveLease(path, test.leaseID)
			if got.State != test.want {
				t.Fatalf("testObserveLease() = %+v, want %s", got, test.want)
			}
			if got.Probe != (LeaseProbe{}) {
				t.Fatalf("testObserveLease() probe = %+v, want no probe on an observed pool", got.Probe)
			}
		})
	}
}

func TestObserveLeaseReportsAbsentAfterReturn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-1"}}.Install(t, faketool.Bin(t))
	if err := testReturnLease(path, "lease-1", true); err != nil {
		t.Fatal(err)
	}
	if got := testObserveLease(path, "lease-1"); got.State != LeaseAbsent {
		t.Fatalf("testObserveLease() = %+v, want absent", got)
	}
}

func TestObserveLeaseReportsAbsentWhenTheRecordedPathIsGone(t *testing.T) {
	if got := testObserveLease(filepath.Join(t.TempDir(), "gone"), "lease-1"); got.State != LeaseAbsent {
		t.Fatalf("testObserveLease() = %+v, want absent", got)
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
			got := testObserveLease(path, "lease-1")
			if got.State != LeaseUnknown {
				t.Fatalf("testObserveLease() = %+v, want %s", got, LeaseUnknown)
			}
			if got.Probe.Command != "treehouse status --json" || got.Probe.WorkingDir != path {
				t.Fatalf("testObserveLease() probe = %+v, want the command and working directory that selected the pool", got.Probe)
			}
			if !strings.Contains(got.Probe.Reason, test.wantReason) {
				t.Fatalf("testObserveLease() probe reason = %q, want it to contain %q", got.Probe.Reason, test.wantReason)
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
	mismatch := testObserveLease(path, "lease-1")
	if mismatch.State != LeaseMismatch || mismatch.LeaseID != "lease-2" {
		t.Fatalf("testObserveLease() = %+v, want a mismatch naming the observed lease", mismatch)
	}

	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "status", Stdout: "[]"})
	unknown := testObserveLease(path, "lease-1")
	if unknown.State != LeaseUnknown {
		t.Fatalf("testObserveLease() = %+v, want %s", unknown, LeaseUnknown)
	}
	if unknown.State == mismatch.State {
		t.Fatal("an unobservable pool is reported with the same state as a lease held by another owner")
	}
	if unknown.LeaseID != "" {
		t.Fatalf("testObserveLease() = %+v, want no observed lease identity when nothing was observed", unknown)
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
	if got := testObserveLease(path, "lease-1"); got.State != LeaseUnknown {
		t.Fatalf("testObserveLease() = %+v, want %s", got, LeaseUnknown)
	}
	faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-1"}}.Install(t, faketool.Bin(t))
	if got := testObserveLease(path, "lease-1"); got.State != LeaseExact {
		t.Fatalf("testObserveLease() = %+v, want %s once the pool answers again", got, LeaseExact)
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
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	runGit(t, clone, "worktree", "add", "-q", "-b", "worktree", worktreePath)
	faketool.Treehouse{Slots: []string{worktreePath}}.Install(t, faketool.Bin(t))

	lease, err := testGet(clone, "hand:task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := ObserveLease(clone, worktreePath, lease.ID); got.State != LeaseExact {
		t.Fatalf("testObserveLease() = %+v, want the lease visible from the worktree repository", got)
	}
}

func TestObserveLeaseRejectsARecycledOrMissingIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-2"}}.Install(t, faketool.Bin(t))
	if got := testObserveLease(path, "lease-1"); got.State != LeaseMismatch {
		t.Fatalf("testObserveLease() = %+v, want a recycled lease identity reported as %s", got, LeaseMismatch)
	}
	if got := testObserveLease(path, ""); got.State != LeaseUnprovable {
		t.Fatalf("testObserveLease() = %+v, want an empty expected identity reported as %s", got, LeaseUnprovable)
	}
}

func TestObserveLeaseRejectsAnAvailableStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	faketool.Treehouse{Slots: []string{path}}.Install(t, faketool.Bin(t))
	if got := testObserveLease(path, "lease-1"); got.State != LeaseAbsent {
		t.Fatalf("testObserveLease() = %+v, want an available entry reported as %s", got, LeaseAbsent)
	}
}

func TestReturnPassesForceFlag(t *testing.T) {
	wt := t.TempDir()
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "return", Args: []string{wt, "--force"}})
	if err := testReturn(wt, true); err != nil {
		t.Fatal(err)
	}
}

func TestReturnUsesThePoolOutsideEveryFleetHome(t *testing.T) {
	bin := faketool.Bin(t)
	home := t.TempDir()
	clone := filepath.Join(home, "projects", "demo")
	wt := filepath.Join(home, ".treehouse", "demo-123", "1", "demo")
	root, err := poolRoot(clone, wt)
	if err != nil {
		t.Fatal(err)
	}
	if withinPath(home, root) {
		t.Fatalf("pool root %s is inside fleet home %s", root, home)
	}
	writeFakeTreehouseAt(t, bin, faketool.TreehouseResponse{
		Command: "--root", Args: []string{root, "return", wt, "--force"},
	})
	if err := Return(clone, wt, true); err != nil {
		t.Fatal(err)
	}
}

func TestReturnNeverFollowsAForeignRecordedPathToItsOwnPool(t *testing.T) {
	bin := faketool.Bin(t)
	currentClone := filepath.Join(t.TempDir(), "projects", "demo")
	foreignHome := t.TempDir()
	wt := filepath.Join(foreignHome, ".treehouse", "demo-123", "1", "demo")
	root, err := poolRoot(currentClone, wt)
	if err != nil {
		t.Fatal(err)
	}
	if withinPath(foreignHome, root) {
		t.Fatalf("pool root %s follows the foreign recorded path into %s", root, foreignHome)
	}
	writeFakeTreehouseAt(t, bin, faketool.TreehouseResponse{
		Command: "--root", Args: []string{root, "return", wt, "--force"},
	})
	if err := Return(currentClone, wt, true); err != nil {
		t.Fatal(err)
	}
}

func TestReturnKeepsALegacyWorktreeOnItsOwnClone(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "projects", "demo")
	wt := filepath.Join(clone, ".treehouse", "demo-123", "1", "demo")
	writeFakeTreehouse(t, faketool.TreehouseResponse{
		Command: "--root", Args: []string{clone, "return", wt, "--force"},
	})
	if err := Return(clone, wt, true); err != nil {
		t.Fatal(err)
	}
}

func TestReturnLeasePassesConditionalLeaseIdentity(t *testing.T) {
	wt := t.TempDir()
	writeFakeTreehouse(t, faketool.TreehouseResponse{
		Command: "return", Args: []string{"--force", "--if-lease-id", "lease-1", wt},
	})
	if err := testReturnLease(wt, "lease-1", true); err != nil {
		t.Fatal(err)
	}
}

func TestObserveLeaseStatusUsesThisClonesPool(t *testing.T) {
	bin := faketool.Bin(t)
	home := t.TempDir()
	clone := filepath.Join(home, "projects", "demo")
	path := filepath.Join(home, ".treehouse", "demo-123", "1", "demo")
	faketool.InitRepo(t, path)
	root, err := poolRoot(clone, path)
	if err != nil {
		t.Fatal(err)
	}
	writeFakeTreehouseAt(t, bin, faketool.TreehouseResponse{
		Command: "--root", Args: []string{root, "status", "--json"},
		Stdout: mustJSON(t, []map[string]string{{"path": path, "status": "leased", "lease_id": "lease-1"}}),
	})
	if got := ObserveLease(clone, path, "lease-1"); got.State != LeaseExact {
		t.Fatalf("testObserveLease() = %+v, want the scoped pool lease", got)
	}
}

func TestReturnLeaseRejectsMismatchedIdentityAndKeepsLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-2"}}.Install(t, faketool.Bin(t))

	if err := testReturnLease(path, "lease-1", true); err == nil {
		t.Fatal("testReturnLease() succeeded for a mismatched lease identity")
	}
	if _, err := testGet(path, "hand:still-held"); err == nil {
		t.Fatal("mismatched testReturnLease() released the current lease")
	}
}

func TestReturnLeaseProtectsAReusedPathFromAStaleLease(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	path := filepath.Join(t.TempDir(), "wt-1")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", path)
	faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-original"}}.Install(t, faketool.Bin(t))

	if err := ReturnLease(clone, path, "lease-original", true); err != nil {
		t.Fatal(err)
	}
	l2, err := testGet(clone, "hand:l2")
	if err != nil {
		t.Fatal(err)
	}
	if err := ReturnLease(clone, path, "lease-original", true); err == nil {
		t.Fatal("stale testReturnLease() released the reused path")
	}
	if _, err := testGet(clone, "hand:still-held"); err == nil {
		t.Fatalf("stale testReturnLease() released current lease %q", l2.ID)
	}
}

func TestReturnLeaseFallsBackForAnEmptyLeaseIdentity(t *testing.T) {
	wt := t.TempDir()
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "return", Args: []string{wt, "--force"}})
	if err := testReturnLease(wt, "", true); err != nil {
		t.Fatal(err)
	}
}

func TestReturnFailsOnNonZeroExit(t *testing.T) {
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "return", Stderr: "worktree busy\n", Exit: 1})
	if err := testReturn(t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "worktree busy") {
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

	if err := testReturn(wt, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree path gone after return: %v", err)
	}
	if err := testReturn(wt, false); err != nil {
		t.Fatalf("got err %v, want a repeated return to succeed", err)
	}
}

func TestReturnFailsOnAWorktreeNoPoolManages(t *testing.T) {
	faketool.Treehouse{Slots: []string{t.TempDir()}}.Install(t, faketool.Bin(t))
	err := testReturn(filepath.Join(t.TempDir(), "gone"), false)
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

	err := testReturn(wt, false)
	if err == nil || !strings.Contains(err.Error(), "still leased") {
		t.Fatalf("got err %v, want the aborted return reported as a failure", err)
	}
	if !errors.Is(err, ErrReturnAborted) {
		t.Fatalf("got err %v, want ErrReturnAborted", err)
	}
	if out, gitErr := exec.Command("git", "-C", wt, "status", "--porcelain").Output(); gitErr != nil || len(out) == 0 {
		t.Fatalf("worktree cleaned by an aborted return: %q %v", out, gitErr)
	}
	if err := testReturn(wt, true); err != nil {
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

// A stale task row can still name path P while treehouse has freed P and handed it to the next
// task under a lease of its own. That is not a collision, and refusing the spawn over it was the
// bug.
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
