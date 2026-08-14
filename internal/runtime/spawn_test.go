package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
)

func TestSpawnFailureAfterAttemptCreationLeavesProvisioningEvidence(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "brief.md"), []byte("brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://example.com/demo.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	phaseErr := errors.New("stop after attempt creation")
	deps := defaultDependencies()
	deps.phase = func(phase lifecyclePhase) error {
		if phase == phaseAttemptCreated {
			return phaseErr
		}
		return nil
	}

	_, err := (&Runtime{deps: deps}).Spawn(context.Background(), SpawnRequest{
		Home: home, ID: "task-1", Project: "demo", Kind: state.KindShip, Harness: "claude",
	})
	if !errors.Is(err, phaseErr) {
		t.Fatalf("Spawn() = %v, want %v", err, phaseErr)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil {
		t.Fatal("Spawn() did not leave an active provisioning attempt")
	}
	got := *history.ActiveAttempt
	if got.Lifecycle != state.AttemptProvisioning || got.Worktree != "" || got.LeaseID != "" || got.Herdr.PaneID != "" || got.LaunchSubmittedAt != "" || got.LaunchConfirmedAt != "" {
		t.Fatalf("attempt after creation-phase failure = %+v, want provisioning-only evidence", got)
	}
}
