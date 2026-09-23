//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/store"
)

// Discovery failure happens after canonical publication. A process restart must
// repair only the projection, preserving the already-published Fleet identity.
func TestCanonicalInitRepairsRegistryAfterPublicationFailure(t *testing.T) {
	parent := t.TempDir()
	fleet := filepath.Join(parent, "fleet")
	seedPrivateRuntime(t, parent)
	registryPath := filepath.Join(parent, ".secondhand", "registry.db")
	if err := os.Mkdir(registryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	failed := runHand(t, parent, "init", "--canonical", fleet)
	if failed.code == 0 || canonicalOutputField(t, failed, "registry") != "failed" ||
		!strings.Contains(failed.stderr, "registry discovery update failed") {
		t.Fatalf("registry failure was not reported after publication: %+v", failed)
	}
	id, err := store.FleetIDReadOnly(fleet)
	if err != nil || id != canonicalOutputField(t, failed, "fleet_id") {
		t.Fatalf("published canonical identity was lost: %s %v", id, err)
	}
	before := snapshotTree(t, fleet)
	if err := os.Remove(registryPath); err != nil {
		t.Fatal(err)
	}
	repaired := runHand(t, parent, "init", "--canonical", fleet)
	if repaired.code != 0 || canonicalOutputField(t, repaired, "fleet_id") != id ||
		canonicalOutputField(t, repaired, "registry") != "registered" {
		t.Fatalf("discovery repair changed identity or failed: %+v", repaired)
	}
	assertTreeUnchanged(t, fleet, before)
	listed := runHand(t, parent, "fleet")
	if listed.code != 0 || !strings.Contains(listed.stdout, id+",") || !strings.Contains(listed.stdout, ",ready,") {
		t.Fatalf("repaired Fleet not discoverable: %+v", listed)
	}
	assertTreeUnchanged(t, fleet, before)
}

// A copied canonical DB keeps the same Fleet identity. Bypassing legacy startup
// must not also bypass the existing duplicate-Fleet write guard.
func TestCanonicalMutationRejectsCopiedFleet(t *testing.T) {
	parent := t.TempDir()
	original := filepath.Join(parent, "original")
	created := runHand(t, parent, "init", "--canonical", original)
	if created.code != 0 {
		t.Fatal(created)
	}
	fleetID := canonicalOutputField(t, created, "fleet_id")
	initGitRepo(t, filepath.Join(original, "projects", "sample"))
	registered := runHand(t, original, "project", "register", "sample")
	if registered.code != 0 {
		t.Fatal(registered)
	}
	projectID := canonicalOutputField(t, registered, "project_id")
	if got := runHand(t, original, "task", "create", "t_original", "--project-id", projectID, "--goal", "Original goal"); got.code != 0 {
		t.Fatal(got)
	}
	if got := runHand(t, original, "decision", "create", "d_original", "--task-id", "t_original",
		"--scope", "task", "--question", "Preserve original question?", "--created-at", "2026-09-23T12:00:00Z"); got.code != 0 {
		t.Fatal(got)
	}
	if got := runHand(t, original, "task", "hold", "create", "h_original", "--task-id", "t_original", "--kind", "operator",
		"--reason", "Fixture deferral", "--evidence-digest", strings.Repeat("a", 64), "--created-at", "2026-09-23T12:00:00Z"); got.code != 0 {
		t.Fatal(got)
	}
	clone := filepath.Join(parent, "copy")
	if err := os.CopyFS(clone, os.DirFS(original)); err != nil {
		t.Fatal(err)
	}
	// Each existing E2E home uses its own isolated infrastructure. Register the
	// positively valid other locator in the copy's projection to expose the conflict.
	r, err := registry.OpenAt(filepath.Join(clone, ".secondhand", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register(original, fleetID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	beforeOriginal, beforeClone := snapshotTree(t, original), snapshotTree(t, clone)
	for _, args := range [][]string{
		{"task", "create", "t_duplicate", "--project-id", projectID, "--goal", "Must refuse"},
		{"decision", "answer", "d_original", "--answer-id", "a_duplicate", "--answer", "yes",
			"--operator-ref", "operator:fixture", "--operator-answer", "--answered-at", "2026-09-23T12:01:00Z"},
		{"task", "hold", "resolve", "h_original", "--resolution", "released",
			"--evidence-digest", strings.Repeat("a", 64), "--resolved-at", "2026-09-23T12:01:00Z"},
	} {
		got := runHand(t, clone, args...)
		if got.code != 1 || !strings.Contains(got.stderr, "also valid at") {
			t.Fatalf("copied canonical Fleet mutation did not refuse: %+v", got)
		}
		assertTreeUnchanged(t, original, beforeOriginal)
		assertTreeUnchanged(t, clone, beforeClone)
	}
	// Exact historical evidence remains inspectable without registry repair.
	if got := runHand(t, clone, "decision", "show", "d_original"); got.code != 0 {
		t.Fatalf("duplicate projection hid immutable Decision history: %+v", got)
	}
	if got := runHand(t, clone, "task", "hold", "show", "h_original"); got.code != 0 || canonicalOutputField(t, got, "unresolved") != "true" {
		t.Fatalf("duplicate projection hid or resolved TaskHold history: %+v", got)
	}
	assertTreeUnchanged(t, original, beforeOriginal)
	assertTreeUnchanged(t, clone, beforeClone)
}
