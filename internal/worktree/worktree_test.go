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
	gitrepo "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/state"
)

func testGet(clonePath, leaseHolder string) (Lease, error) {
	args := []string{"get", "--lease", "--json"}
	if leaseHolder != "" {
		args = append(args, "--lease-holder", leaseHolder)
	}
	return getLease(clonePath, withTreehouseRoot(clonePath, args...))
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
	var payload struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal([]byte(response.Stdout), &payload)
	var slots []string
	if payload.Path != "" {
		slots = []string{payload.Path}
	}
	faketool.Treehouse{Slots: slots, Responses: []faketool.TreehouseResponse{response}}.Install(t, faketool.Bin(t))
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
	path := filepath.Join(clone, ".treehouse", "pool", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", path)
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "get", Stdout: mustJSON(t, map[string]string{"path": path, "lease_id": "5fe5412a4aabdeb85a148d6d73eb42d8"})})
	got, err := Get(clone, "hand:task-1")
	if err != nil {
		t.Fatal(err)
	}
	want := Lease{Path: path, ID: "5fe5412a4aabdeb85a148d6d73eb42d8"}
	if got != (Lease{Path: path, ID: "5fe5412a4aabdeb85a148d6d73eb42d8"}) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// A treehouse older than v2.1.0 reports no identity at all, and that has to
// stay a usable lease rather than an error - CheckCollision falls back to the
// path for it.
func TestGetAcceptsAPayloadWithoutALeaseIdentity(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	path := filepath.Join(clone, ".treehouse", "pool", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", path)
	writeFakeTreehouse(t, faketool.TreehouseResponse{Command: "get", Stdout: mustJSON(t, map[string]string{"path": path})})
	got, err := Get(clone, "hand:task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != (Lease{Path: path}) {
		t.Fatalf("got %+v, want path-only lease", got)
	}
}

func TestCheckSoundnessAcceptsALinkedWorktreeOwnedByTheRegisteredClone(t *testing.T) {
	home := t.TempDir()
	clone := filepath.Join(home, "projects", "demo")
	faketool.InitRepo(t, clone)
	path := filepath.Join(clone, ".treehouse", "pool", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", path)

	got := CheckSoundness(clone, path)
	if !got.Sound {
		t.Fatalf("CheckSoundness() = %+v, want sound", got)
	}
}

func TestCheckSoundnessReportsAnAliasedMetadataDirectory(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	runGit(t, clone, "worktree", "add", "-q", "-b", "first", first)
	runGit(t, clone, "worktree", "add", "-q", "-b", "second", second)
	metadata, err := ReadMetadataDir(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(second, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, ".git"), []byte("gitdir: "+metadata+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := CheckSoundness(clone, second)
	if got.Sound || !containsFailure(got, "metadata gitdir does not point back to this slot") {
		t.Fatalf("CheckSoundness() = %+v, want aliased metadata failure", got)
	}
}

func TestDiscoverPoolSlotsFindsAliasesAcrossPoolRoots(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	first := filepath.Join(clone, ".treehouse", "pool-a", "1", "demo")
	secondRoot := filepath.Join(t.TempDir(), ".treehouse")
	second := filepath.Join(secondRoot, "pool-b", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "first", first)
	runGit(t, clone, "worktree", "add", "-q", "-b", "second", second)
	metadata, err := ReadMetadataDir(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(second, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, ".git"), []byte("gitdir: "+metadata+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := DiscoverPoolSlots(clone, filepath.Join(clone, ".treehouse"), secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("DiscoverPoolSlots() = %+v, want both pool slots", got)
	}
	collisions := PoolSlotCollisions(got)
	if len(collisions) != 1 || len(collisions[0]) != 2 {
		t.Fatalf("PoolSlotCollisions() = %+v, want one two-slot collision", collisions)
	}
	for _, slot := range got {
		if !gitrepo.SamePath(slot.MetadataDir, metadata) {
			t.Fatalf("slot %+v has metadata target %q, want %q", slot, slot.MetadataDir, metadata)
		}
	}
}

func TestDiscoverPoolSlotsFindsCloneOwnedSlotWithExternalMetadata(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	root := filepath.Join(t.TempDir(), ".treehouse")
	path := filepath.Join(root, "pool", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", path)
	metadata, err := ReadMetadataDir(path)
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "metadata")
	if err := os.Rename(metadata, external); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir: "+external+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "gitdir"), []byte(filepath.Join(path, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "commondir"), []byte(filepath.Join(clone, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := DiscoverPoolSlots(clone, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !gitrepo.SamePath(got[0].Path, path) || !gitrepo.SamePath(got[0].MetadataDir, external) {
		t.Fatalf("DiscoverPoolSlots() = %+v, want the clone-owned external-metadata slot", got)
	}
}

func TestCheckSoundnessReportsMissingMetadataAndForeignCommonDirectory(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	foreign := filepath.Join(t.TempDir(), "foreign")
	faketool.InitRepo(t, clone)
	faketool.InitRepo(t, foreign)
	missing := filepath.Join(t.TempDir(), "missing")
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(missing, ".git"), []byte("gitdir: "+filepath.Join(t.TempDir(), "gone")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	foreignSlot := filepath.Join(t.TempDir(), "foreign-slot")
	runGit(t, foreign, "worktree", "add", "-q", "-b", "foreign-slot", foreignSlot)

	missingGot := CheckSoundness(clone, missing)
	if missingGot.Sound || !containsFailure(missingGot, "metadata directory does not exist") {
		t.Fatalf("missing metadata CheckSoundness() = %+v", missingGot)
	}
	foreignGot := CheckSoundness(clone, foreignSlot)
	if foreignGot.Sound || !containsFailure(foreignGot, "common directory is") {
		t.Fatalf("foreign common directory CheckSoundness() = %+v", foreignGot)
	}
}

func TestRemoveMetadataRemovesTheLinkedWorktreeMetadataDirectory(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	path := filepath.Join(t.TempDir(), "slot")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", path)
	metadata, err := ReadMetadataDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveMetadata(clone, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metadata); !os.IsNotExist(err) {
		t.Fatalf("metadata directory still exists: %v", err)
	}
}

func TestRemoveMetadataKeepsExternalLinkedWorktreeMetadata(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	path := filepath.Join(t.TempDir(), "slot")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", path)
	metadata, err := ReadMetadataDir(path)
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "metadata")
	if err := os.Rename(metadata, external); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir: "+external+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "gitdir"), []byte(filepath.Join(path, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "commondir"), []byte(filepath.Join(clone, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveMetadata(clone, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(external); err != nil {
		t.Fatalf("external metadata was removed: %v", err)
	}
}

func TestGetRetiresAnUnsoundLeaseAndAcquiresTheNextSoundSlot(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	bad := filepath.Join(clone, ".treehouse", "pool", "1", "demo")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, ".git"), []byte("broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sound := filepath.Join(clone, ".treehouse", "pool", "2", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "sound", sound)
	faketool.Treehouse{Slots: []string{bad, sound}}.Install(t, faketool.Bin(t))

	got, err := Get(clone, "hand:task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != sound {
		t.Fatalf("Get() path = %q, want sound slot %q", got.Path, sound)
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatalf("unsound slot still exists: %v", err)
	}
}

func TestGetFailsWithThePoolNamedWhenEverySlotIsUnsound(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	bad := filepath.Join(clone, ".treehouse", "pool", "1", "demo")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, ".git"), []byte("broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	faketool.Treehouse{Slots: []string{bad}}.Install(t, faketool.Bin(t))

	if _, err := Get(clone, "hand:task-1"); err == nil || !strings.Contains(err.Error(), "treehouse pool for "+clone+" has no sound free slot") {
		t.Fatalf("Get() error = %v, want a named exhausted pool", err)
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
	path := filepath.Join(clone, ".treehouse", "pool", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", path)
	writeFakeTreehouse(t, faketool.TreehouseResponse{
		Command: "get", Args: []string{"--lease", "--json", "--lease-holder", "hand:task-1"},
		Stdout: mustJSON(t, map[string]string{"path": path}),
	})
	if _, err := Get(clone, "hand:task-1"); err != nil {
		t.Fatal(err)
	}
}

func TestGetScopesPoolToTheFleetHome(t *testing.T) {
	home := t.TempDir()
	clone := filepath.Join(home, "projects", "demo")
	faketool.InitRepo(t, clone)
	path := filepath.Join(clone, ".treehouse", "pool", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", path)
	writeFakeTreehouse(t, faketool.TreehouseResponse{
		Command: "--root", Args: []string{clone, "get", "--lease", "--json", "--lease-holder", "hand:task-1"},
		Stdout: mustJSON(t, map[string]string{"path": path}),
	})
	if _, err := Get(clone, "hand:task-1"); err != nil {
		t.Fatal(err)
	}
}

func TestGetRejectsAWorktreeFromAnotherRegisteredClone(t *testing.T) {
	home := t.TempDir()
	clone := filepath.Join(home, "projects", "demo")
	foreign := filepath.Join(t.TempDir(), "demo")
	faketool.InitRepo(t, clone)
	faketool.InitRepo(t, foreign)
	foreignSlot := filepath.Join(foreign, ".treehouse", "pool", "1", "demo")
	runGit(t, foreign, "worktree", "add", "-q", "-b", "foreign-slot", foreignSlot)
	faketool.Treehouse{Responses: []faketool.TreehouseResponse{
		{Command: "get", Stdout: mustJSON(t, map[string]string{"path": foreignSlot, "lease_id": "lease-1"})},
		{Command: "return", Args: []string{"--force", "--if-lease-id", "lease-1", foreignSlot}},
	}, Slots: []string{foreignSlot}}.Install(t, faketool.Bin(t))
	if _, err := Get(clone, "hand:task-1"); err == nil || !strings.Contains(err.Error(), "foreign Git registration") {
		t.Fatalf("Get() error = %v, want a foreign registration report", err)
	}
	if _, err := os.Stat(foreignSlot); !os.IsNotExist(err) {
		t.Fatalf("foreign slot was not retired: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign repository was removed: %v", err)
	}
}

func TestGetRejectsAWorktreeOutsideTheReportedPool(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	allowed := filepath.Join(clone, ".treehouse", "pool", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "allowed", allowed)
	outside := filepath.Join(t.TempDir(), "outside")
	faketool.InitRepo(t, outside)
	faketool.Treehouse{Slots: []string{allowed}, Responses: []faketool.TreehouseResponse{
		{Command: "get", Stdout: mustJSON(t, map[string]string{"path": outside, "lease_id": "lease-1"})},
	}}.Install(t, faketool.Bin(t))
	if _, err := Get(clone, "hand:task-1"); err == nil || !strings.Contains(err.Error(), "outside its reported pool") {
		t.Fatalf("Get() error = %v, want an outside-pool refusal", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside path was mutated: %v", err)
	}
}

func TestGetReleasesACloneOwnedLeaseWhenStatusOmitsIt(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	path := filepath.Join(clone, ".treehouse", "pool", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", path)
	log := filepath.Join(t.TempDir(), "treehouse.log")
	faketool.Treehouse{Log: log, Slots: []string{path}, Responses: []faketool.TreehouseResponse{
		{Command: "status", Stdout: "[]\n"},
		{Command: "return", Args: []string{"--force", "--if-lease-id", "lease-1", path}},
	}}.Install(t, faketool.Bin(t))

	if _, err := Get(clone, "hand:task-1"); err == nil || !strings.Contains(err.Error(), "status omitted it; lease released") {
		t.Fatalf("Get() error = %v, want acquisition and release report", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), " get --lease --json --lease-holder hand:task-1") || !strings.Contains(string(data), " status --json") || !strings.Contains(string(data), " return --force --if-lease-id lease-1 "+path) {
		t.Fatalf("treehouse invocations = %q, want acquisition, omitted status, and conditional release", data)
	}
}

func TestGetRejectsStatusThatDoesNotProveTheAcquiredLease(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		want   string
	}{
		{name: "available", status: "available", want: "not leased"},
		{name: "different lease", status: "leased", want: "with lease lease-other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clone := filepath.Join(t.TempDir(), "clone")
			faketool.InitRepo(t, clone)
			path := filepath.Join(clone, ".treehouse", "pool", "1", "demo")
			runGit(t, clone, "worktree", "add", "-q", "-b", "slot", path)
			log := filepath.Join(t.TempDir(), "treehouse.log")
			status := mustJSON(t, []map[string]string{{"path": path, "status": test.status, "lease_id": "lease-other"}})
			faketool.Treehouse{Log: log, Slots: []string{path}, Responses: []faketool.TreehouseResponse{
				{Command: "status", Stdout: status},
				{Command: "return", Args: []string{"--force", "--if-lease-id", "lease-1", path}},
			}}.Install(t, faketool.Bin(t))

			if _, err := Get(clone, "hand:task-1"); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Get() error = %v, want status ownership failure", err)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), " return --force --if-lease-id lease-1 "+path) {
				t.Fatalf("treehouse invocations = %q, want conditional release", data)
			}
		})
	}
}

func TestGetReturnsLeaseWhenPoolStatusFails(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	faketool.InitRepo(t, clone)
	path := filepath.Join(clone, ".treehouse", "pool", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", path)
	log := filepath.Join(t.TempDir(), "treehouse.log")
	faketool.Treehouse{Log: log, Slots: []string{path}, Responses: []faketool.TreehouseResponse{
		{Command: "status", Stderr: "status unavailable\n", Exit: 1},
		{Command: "get", Stdout: mustJSON(t, map[string]string{"path": path, "lease_id": "lease-1"})},
		{Command: "return", Args: []string{"--force", "--if-lease-id", "lease-1", path}},
	}}.Install(t, faketool.Bin(t))
	if _, err := Get(clone, "hand:task-1"); err == nil || !strings.Contains(err.Error(), "status unavailable") {
		t.Fatalf("Get() error = %v, want pool status failure", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), " return --force --if-lease-id lease-1 "+path) {
		t.Fatalf("treehouse invocations = %q, want the held lease returned", data)
	}
}

func TestGetDoesNotReturnAnUnverifiedForeignLeaseWhenPoolStatusFails(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "clone")
	foreign := filepath.Join(t.TempDir(), "foreign")
	faketool.InitRepo(t, clone)
	faketool.InitRepo(t, foreign)
	path := filepath.Join(foreign, ".treehouse", "pool", "1", "demo")
	runGit(t, foreign, "worktree", "add", "-q", "-b", "slot", path)
	log := filepath.Join(t.TempDir(), "treehouse.log")
	faketool.Treehouse{Log: log, Slots: []string{path}, Responses: []faketool.TreehouseResponse{
		{Command: "status", Stderr: "status unavailable\n", Exit: 1},
		{Command: "get", Stdout: mustJSON(t, map[string]string{"path": path, "lease_id": "lease-1"})},
		{Command: "return", Args: []string{"--force", "--if-lease-id", "lease-1", path}},
	}}.Install(t, faketool.Bin(t))
	if _, err := Get(clone, "hand:task-1"); err == nil || !strings.Contains(err.Error(), "refusing to return an unverified lease path") {
		t.Fatalf("Get() error = %v, want an unverified foreign lease refusal", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), " return ") {
		t.Fatalf("treehouse invocations = %q, want no return against the foreign repository", data)
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
	worktreePath := filepath.Join(clone, ".treehouse", "pool", "1", "demo")
	runGit(t, clone, "worktree", "add", "-q", "-b", "slot", worktreePath)
	faketool.Treehouse{Slots: []string{worktreePath}}.Install(t, faketool.Bin(t))

	lease, err := Get(clone, "hand:task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := testObserveLease(worktreePath, lease.ID); got.State != LeaseExact {
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

func TestReturnScopesPoolToTheFleetHome(t *testing.T) {
	home := t.TempDir()
	clone := filepath.Join(home, "projects", "demo")
	wt := filepath.Join(home, ".treehouse", "demo-123", "1", "demo")
	writeFakeTreehouse(t, faketool.TreehouseResponse{
		Command: "--root", Args: []string{clone, "return", wt, "--force"},
	})
	if err := Return(clone, wt, true); err != nil {
		t.Fatal(err)
	}
}

func TestReturnUsesTheCurrentFleetHomeForAForeignRecordedPath(t *testing.T) {
	currentHome := t.TempDir()
	currentClone := filepath.Join(currentHome, "projects", "demo")
	foreignHome := t.TempDir()
	wt := filepath.Join(foreignHome, ".treehouse", "demo-123", "1", "demo")
	writeFakeTreehouse(t, faketool.TreehouseResponse{
		Command: "--root", Args: []string{currentClone, "return", wt, "--force"},
	})
	if err := Return(currentClone, wt, true); err != nil {
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

func TestObserveLeaseScopesStatusToTheFleetHome(t *testing.T) {
	home := t.TempDir()
	clone := filepath.Join(home, "projects", "demo")
	path := filepath.Join(home, ".treehouse", "demo-123", "1", "demo")
	faketool.InitRepo(t, path)
	writeFakeTreehouse(t, faketool.TreehouseResponse{
		Command: "--root", Args: []string{clone, "status", "--json"},
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
	path := filepath.Join(t.TempDir(), "wt-1")
	faketool.InitRepo(t, path)
	faketool.Treehouse{Slots: []string{path}, Held: []string{path}, LeaseIDs: map[string]string{path: "lease-original"}}.Install(t, faketool.Bin(t))

	if err := testReturnLease(path, "lease-original", true); err != nil {
		t.Fatal(err)
	}
	l2, err := testGet(path, "hand:l2")
	if err != nil {
		t.Fatal(err)
	}
	if err := testReturnLease(path, "lease-original", true); err == nil {
		t.Fatal("stale testReturnLease() released the reused path")
	}
	if _, err := testGet(path, "hand:still-held"); err == nil {
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
