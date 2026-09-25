//go:build !windows

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCanonicalV19WorktreePhysicalIdentityOmitsDeviceNumber(t *testing.T) {
	dir := t.TempDir()
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := CanonicalV19WorktreePhysicalIdentity(dir, info)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalV19WorktreePhysicalIdentity(dir, info)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("physical identity first=%q second=%q, want stable", first, second)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if want := fmt.Sprintf("unix-v2:ino=%016x:btime=", uint64(stat.Ino)); !strings.HasPrefix(first, want) {
		t.Fatalf("physical identity = %q, want prefix %q", first, want)
	}
	if strings.Contains(first, "dev=") {
		t.Fatalf("physical identity = %q, want no mount-assigned device number", first)
	}
	if legacy := canonicalV19WorktreeUnixV1PhysicalIdentity(t, dir); legacy == first {
		t.Fatalf("unix-v1 identity %q matches unix-v2 identity", legacy)
	}
}

func TestCanonicalV19WorktreePhysicalIdentityDistinguishesReplacedDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worktree")
	seen := make(map[string]uint64)
	reused := 0
	for range 8 {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		identity, err := CanonicalV19WorktreePhysicalIdentity(path, info)
		if err != nil {
			t.Fatal(err)
		}
		ino := uint64(info.Sys().(*syscall.Stat_t).Ino)
		for _, previous := range seen {
			if previous == ino {
				reused++
			}
		}
		if _, duplicate := seen[identity]; duplicate {
			t.Fatalf("replacement directory reused physical identity %q", identity)
		}
		seen[identity] = ino
		time.Sleep(20 * time.Millisecond)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if reused == 0 {
		t.Skipf("no inode reuse observed across 8 replacement directories (%d distinct identities)", len(seen))
	}
}

func TestCanonicalV19WorktreePhysicalIdentityRejectsSwappedPath(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	other := filepath.Join(dir, "other")
	for _, path := range []string{original, other} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Lstat(original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalV19WorktreePhysicalIdentity(other, info); err == nil {
		t.Fatal("physical identity accepted metadata from a different directory")
	}
}

func TestCanonicalV19PlanWriterRefusesStoredUnixV1PhysicalIdentity(t *testing.T) {
	fixture := canonicalV19PlanWriterFixture(t, "")
	repository := filepath.Join(fixture.Home, "projects", "demo")
	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE workspace_binding SET superseded_at='2026-09-04T08:04:00Z' WHERE id='workspace-1'`)
	if err == nil {
		_, err = db.Exec(`INSERT INTO workspace_binding(
			id,project_id,ordinal,repository_locator,repository_identity_digest,common_git_dir,physical_identity_digest,revision,established_at,superseded_at
		) VALUES('workspace-2','project-1',2,'projects/demo',?,'projects/demo/.git',?,?,'2026-09-04T08:04:00Z','')`,
			legacyV18CutoverManifestIdentitySHA256(legacyV18CutoverRepositoryIdentityDomain, canonicalV19WorktreeUnixV1PhysicalIdentity(t, repository)),
			legacyV18CutoverManifestIdentitySHA256(legacyV18CutoverCommonGitDirIdentityDomain, canonicalV19WorktreeUnixV1PhysicalIdentity(t, filepath.Join(repository, ".git"))),
			fixture.Revision)
	}
	if closeErr := db.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}

	input := canonicalV19PlanWriterInput("plan-unix-v1")
	input.WorkspaceBindingID = "workspace-2"
	_, err = CreateCanonicalV19RootPlan(context.Background(), fixture.Home, input)
	if !errors.Is(err, ErrCanonicalV19PlanGitBasis) {
		t.Fatalf("stored unix-v1 identity error = %v, want %v", err, ErrCanonicalV19PlanGitBasis)
	}
	if got := canonicalV19PlanWriterCount(t, fixture.Home); got != 0 {
		t.Fatalf("Plan rows after unix-v1 identity refusal = %d, want 0", got)
	}
}

func TestCanonicalV19WorktreeRemoveRefusesStoredUnixV1PhysicalIdentity(t *testing.T) {
	fixture, binding := canonicalV19NativeWorktreeRemoveFixture(t, "native-remove-unix-v1")
	input := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-unix-v1")
	input.CreatedAt = "2026-09-07T00:00:00Z"
	request, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if observed := observeCanonicalV19GitWorktreeRemove(fixture.Home, request); observed.State != canonicalV19GitWorktreeExact {
		t.Fatalf("unix-v2 binding observation = %q (%s), want exact", observed.State, observed.Reason)
	}

	request.ExpectedPhysicalIdentityDigest = canonicalV19WorktreePhysicalIdentityDigest(canonicalV19WorktreeUnixV1PhysicalIdentity(t, request.Path))
	observed := observeCanonicalV19GitWorktreeRemove(fixture.Home, request)
	if observed.State != canonicalV19GitWorktreeMismatch || !strings.Contains(observed.Reason, "physical identity") {
		t.Fatalf("unix-v1 binding observation = %q (%s), want physical identity mismatch", observed.State, observed.Reason)
	}
}

func canonicalV19WorktreeUnixV1PhysicalIdentity(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("unix-v1:dev=%016x:ino=%016x", uint64(stat.Dev), uint64(stat.Ino))
}
