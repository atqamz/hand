//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

func TestCutoverRecoveryCLIRebuildsPublishesAndSurvivesRestart(t *testing.T) {
	home, fleetID := frozenCutoverFixture(t)
	before := snapshotTree(t, home)
	inspected := runHand(t, home, "cutover", "inspect", home)
	if inspected.code != 0 || !strings.Contains(inspected.stdout, "disposition: rebuild-canonical-temp") {
		t.Fatalf("inspect frozen cutover = %+v", inspected)
	}
	assertTreeUnchanged(t, home, before)

	frozen, err := os.ReadFile(store.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	recovered := runHand(t, home, "cutover", "recover", home)
	if recovered.code != 0 || !strings.Contains(recovered.stdout, "disposition: canonical-authority") || !strings.Contains(recovered.stdout, fleetID) {
		t.Fatalf("recover frozen cutover = %+v", recovered)
	}
	if got, err := store.FleetIDReadOnly(home); err != nil || got != fleetID {
		t.Fatalf("recovered Fleet identity = %q, %v; want %q", got, err, fleetID)
	}
	retired, err := filepath.Glob(filepath.Join(home, "state", "v19-cutover-*", "frozen-bridge.db"))
	if err != nil || len(retired) != 1 {
		t.Fatalf("retired bridge paths = %v, %v", retired, err)
	}
	retained, err := os.ReadFile(retired[0])
	if err != nil || !bytes.Equal(retained, frozen) {
		t.Fatalf("retired bridge bytes differ: %v", err)
	}
	after := snapshotTree(t, home)
	evidenceFiles := 0
	for path, original := range before {
		if strings.HasPrefix(path, "state/v19-cutover-") && (strings.HasSuffix(path, "/hand.db") || strings.HasSuffix(path, "/manifest.json")) {
			evidenceFiles++
			if got := after[path]; got != original {
				t.Fatalf("recovery changed authoritative evidence %s", path)
			}
		}
	}
	if evidenceFiles != 2 {
		t.Fatalf("preserved evidence files = %d, want original archive and manifest", evidenceFiles)
	}
	for _, action := range []string{"recover", "inspect"} {
		got := runHand(t, home, "cutover", action, home)
		if got.code != 0 || !strings.Contains(got.stdout, "disposition: canonical-authority") {
			t.Fatalf("%s after restart = %+v", action, got)
		}
		assertTreeUnchanged(t, home, after)
	}
	execFleetFixtureSQL(t, home, `CREATE TABLE unexpected(id TEXT)`)
	after = snapshotTree(t, home)
	got := runHand(t, home, "cutover", "recover", home)
	if got.code == 0 || !strings.Contains(got.stderr, "cannot fall back to legacy evidence") {
		t.Fatalf("corrupt canonical authority recovery = %+v", got)
	}
	assertTreeUnchanged(t, home, after)
}

func TestCutoverRecoveryCLIRefusesContentionAndCorruptEvidence(t *testing.T) {
	home, _ := frozenCutoverFixture(t)
	release, err := store.Lock(home, store.MigrationLock, true)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, home)
	got := runHand(t, home, "cutover", "recover", home)
	release()
	if got.code == 0 || !strings.Contains(got.stderr, "MigrationLock") {
		t.Fatalf("contended recovery = %+v", got)
	}
	assertTreeUnchanged(t, home, before)

	archives, err := filepath.Glob(filepath.Join(home, "state", "v19-cutover-*", "hand.db"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("original archive paths = %v, %v", archives, err)
	}
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

func TestCutoverRecoveryCLIWithMissingActiveDatabase(t *testing.T) {
	home, fleetID := frozenCutoverFixture(t)
	archives, err := filepath.Glob(filepath.Join(home, "state", "v19-cutover-*", "hand.db"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("original archive paths = %v, %v", archives, err)
	}
	if err := os.Rename(store.Path(home), filepath.Join(filepath.Dir(archives[0]), "frozen-bridge.db")); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, home)
	got := runHandEnv(t, home, []string{"HAND_ROLE=worker"}, "cutover", "recover", home)
	if got.code != 3 || !strings.Contains(got.stderr, "HAND_ROLE=worker") {
		t.Fatalf("worker recovery = %+v", got)
	}
	assertTreeUnchanged(t, home, before)
	got = runHand(t, home, "cutover", "recover", home)
	if got.code != 0 || !strings.Contains(got.stdout, "disposition: canonical-authority") || !strings.Contains(got.stdout, fleetID) {
		t.Fatalf("recover with absent active DB = %+v", got)
	}
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
