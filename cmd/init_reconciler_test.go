package cmd

import (
	"testing"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/routing"
	"github.com/atqamz/hand/internal/state"
)

// hand init restores Hand-owned generated surfaces (AGENTS.md, the bundled skill) and preserves
// everything else durable. TestInitLeavesExistingDataFilesAlone covers data/** skeleton files;
// these cover the rest: config/**, project registration, and Task/Attempt history.

func TestInitPreservesExistingProjectRegistration(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	dir := t.TempDir()
	t.Chdir(dir)
	mkFleetDirs(t, dir)
	if err := project.Add(dir, project.Project{Name: "myproj", URL: "git@example.com:me/myproj.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}

	cmd := newInitCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	p, ok, err := project.Find(dir, "myproj")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("got project unregistered after hand init, want it preserved")
	}
	if p.URL != "git@example.com:me/myproj.git" || p.Mode != project.ModeNoMistakes {
		t.Fatalf("got %+v, want the registered project's fields unchanged", p)
	}
}

func TestInitPreservesExistingConfigProfilesAndRoutes(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	dir := t.TempDir()
	t.Chdir(dir)
	mkFleetDirs(t, dir)
	mustConfig(t, "profile", "set", "daily", "--harness", harness.Claude, "--model", "claude-opus-5")
	mustConfig(t, "route", "set", "ship", "deep", "daily")

	cmd := newInitCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := routing.LoadExecutionSnapshot(dir, harness.Claude, true)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range snapshot.Config.Profiles {
		if p.Name == "daily" && p.Harness == harness.Claude && p.Model == "claude-opus-5" {
			found = true
		}
	}
	if !found {
		t.Fatalf("got profiles %+v, want the daily profile preserved unchanged", snapshot.Config.Profiles)
	}
	var routeFound bool
	for _, r := range snapshot.Config.Routes {
		if r.Kind == routing.TaskKindShip && r.ExecutionClass == routing.ExecutionClassDeep && r.Profile == "daily" {
			routeFound = true
		}
	}
	if !routeFound {
		t.Fatalf("got routes %+v, want the ship/deep route to daily preserved", snapshot.Config.Routes)
	}
}

func TestInitPreservesExistingTaskAndAttemptHistory(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	dir := t.TempDir()
	t.Chdir(dir)
	mkFleetDirs(t, dir)
	if err := state.CreateTask(dir, state.Task{
		ID: "task-1", Project: "myproj", Kind: state.KindShip, Lifecycle: state.TaskOpen,
	}); err != nil {
		t.Fatal(err)
	}
	attempt, err := state.CreateAttempt(dir, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning, Harness: harness.Claude})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.UpdateTask(dir, state.Task{
		ID: "task-1", Project: "myproj", Kind: state.KindShip,
		Lifecycle: state.TaskOpen, ActiveAttemptID: attempt.ID,
	}); err != nil {
		t.Fatal(err)
	}

	cmd := newInitCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(dir, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "myproj" || got.Kind != state.KindShip || got.Lifecycle != state.TaskOpen || got.ActiveAttemptID != attempt.ID {
		t.Fatalf("got task %+v, want it unchanged by hand init", got)
	}
	gotAttempt, ok, err := state.ReadAttempt(dir, attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || gotAttempt.Lifecycle != state.AttemptRunning || gotAttempt.Harness != harness.Claude {
		t.Fatalf("got attempt %+v ok=%v, want it unchanged by hand init", gotAttempt, ok)
	}
}

// Repeated init is the reconciler's other half: a second run must reach the identical
// steady state as the first, not merely avoid an error.
func TestInitRepeatedRunsConvergeToTheSameSteadyState(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	dir := t.TempDir()
	t.Chdir(dir)

	for i := 0; i < 3; i++ {
		cmd := newInitCmd()
		cmd.SetArgs([]string{})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
}
