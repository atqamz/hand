//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

// #348 revision 4 C4-1: until the offline witness and drift gate run, recover never completes a frozen cutover.
func TestCutoverRecoveryCLIRefusesToCompleteFrozenCutover(t *testing.T) {
	home, _ := frozenCutoverFixture(t)
	before := snapshotTree(t, home)
	inspected := runHand(t, home, "cutover", "inspect", home)
	if inspected.code != 0 || !strings.Contains(inspected.stdout, "disposition: rebuild-canonical-temp") {
		t.Fatalf("inspect frozen cutover = %+v", inspected)
	}
	assertTreeUnchanged(t, home, before)

	got := runHandEnv(t, home, []string{"HAND_ROLE=worker"}, "cutover", "recover", home)
	if got.code != 3 || !strings.Contains(got.stderr, "HAND_ROLE=worker") {
		t.Fatalf("worker recovery = %+v", got)
	}
	assertTreeUnchanged(t, home, before)
	got = runHand(t, home, "cutover", "recover", home)
	if got.code == 0 || !strings.Contains(got.stderr, "does not run yet") {
		t.Fatalf("recover frozen cutover = %+v, want refusal", got)
	}
	assertTreeUnchanged(t, home, before)

	archives, err := filepath.Glob(filepath.Join(home, "state", "v19-cutover-*", "hand.db"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("original archive paths = %v, %v", archives, err)
	}
	if err := os.Rename(store.Path(home), filepath.Join(filepath.Dir(archives[0]), "frozen-bridge.db")); err != nil {
		t.Fatal(err)
	}
	before = snapshotTree(t, home)
	got = runHand(t, home, "cutover", "recover", home)
	if got.code == 0 || !strings.Contains(got.stderr, "absent after a freeze") {
		t.Fatalf("recover with absent active DB = %+v, want refusal", got)
	}
	assertTreeUnchanged(t, home, before)

	if err := os.WriteFile(archives[0], []byte("corrupt evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	before = snapshotTree(t, home)
	got = runHand(t, home, "cutover", "recover", home)
	if got.code == 0 || !strings.Contains(got.stderr, "recovery disposition=refuse") {
		t.Fatalf("corrupt evidence recovery = %+v", got)
	}
	assertTreeUnchanged(t, home, before)
}

func TestCutoverRecoveryCLILeavesFreshLegacyAndEmptyHomesUnchanged(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		home := t.TempDir()
		want := "no-state"
		if legacy {
			createCutoverLegacyFixture(t, home)
			want = "legacy-source"
		}
		seedPrivateRuntime(t, home)
		before := snapshotTree(t, home)
		got := runHand(t, home, "cutover", "recover", home)
		if got.code != 0 || !strings.Contains(got.stdout, "disposition: "+want) {
			t.Fatalf("recovery with legacy=%t = %+v", legacy, got)
		}
		assertTreeUnchanged(t, home, before)
	}
}

func frozenCutoverFixture(t *testing.T) (string, string) {
	t.Helper()
	token, machine := "11111111-2222-4333-8444-555555555555", "abcdefab-cdef-4abc-8def-abcdefabcdef"
	switch runtime.GOOS {
	case "linux":
		machine = "0123456789abcdef0123456789abcdef"
	case "windows":
		token = "7200000"
	}
	t.Setenv("HAND_TEST_CUTOVER_FILESYSTEM", "local")
	t.Setenv("HAND_TEST_CUTOVER_BOOT_TOKEN", token)
	t.Setenv("HAND_TEST_CUTOVER_MACHINE_ID", machine)
	home := t.TempDir()
	createCutoverLegacyFixture(t, home)
	guard, err := store.AcquireLegacyV18CutoverGuardFixture(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	plan, err := guard.ObservationPlan()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Projects)+len(plan.Worktrees)+len(plan.Herdr) != 0 {
		t.Fatal("empty fixture unexpectedly needs external provider observations")
	}
	if err := guard.Freeze(context.Background(), home, store.LegacyV18CutoverManifestInput{
		FleetID: plan.FleetID, ImportedAt: "2026-09-23T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	seedPrivateRuntime(t, home)
	return home, plan.FleetID
}

func createCutoverLegacyFixture(t *testing.T, home string) {
	t.Helper()
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	execFleetFixtureSQL(t, home, `PRAGMA journal_mode=DELETE`)
}
