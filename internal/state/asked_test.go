package state

import (
	"context"
	"testing"
)

func TestModelNoteNamesTheModelAskedForWhenTheRunningOneDiffers(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	task := activeTask(t, s)
	launch := func(h, model, effort string) string {
		a, err := s.AddAttempt(ctx, AttemptSpec{TaskID: task.ID, Harness: h, Model: model, Effort: effort, Argv: []string{"/bin/x", "p"}}, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.EndAttempt(ctx, a.ID, AttemptFailed, "x"); err != nil {
			t.Fatal(err)
		}
		return AttemptRef(a.ID)
	}
	cases := []struct {
		name, model, effort, ranModel, ranEffort, want string
	}{
		{"same", "p/m", "", "p/m", "", ""},
		{"variant kept", "p/m", "fast", "p/m", "fast", ""},
		{"other model", "p/m", "", "q/n", "", "Asked for p/m, running q/n."},
		{"variant reset", "p/m", "bogus", "p/m", "", "Asked for p/m bogus, running p/m default."},
		{"other model and variant", "p/m", "fast", "q/n", "slow", "Asked for p/m fast, running q/n slow."},
		{"default asked", "p/m", "default", "p/m", "", ""},
		{"variant not asked", "p/m", "", "p/m", "fast", ""},
		{"not read back yet", "p/m", "", "", "", ""},
	}
	for _, c := range cases {
		ref := launch("opencode", c.model, c.effort)
		got, err := s.ModelNote(ctx, ref, c.ranModel, c.ranEffort)
		if err != nil || got != c.want {
			t.Errorf("%s: note = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	if got, err := s.ModelNote(ctx, launch("opencode", "", ""), "q/n", ""); err != nil || got != "" {
		t.Errorf("no model asked: note = %q, %v", got, err)
	}
	if got, err := s.ModelNote(ctx, launch("claude", "sonnet", "low"), "opus", "low"); err != nil || got != "" {
		t.Errorf("claude: note = %q, %v", got, err)
	}
}

func TestModelNoteFollowsTheSupervisorRow(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	spec := supervisorSpec
	spec.Harness, spec.Model, spec.Effort = "opencode", "p/m", ""
	sup, err := s.AddSupervisor(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ModelNote(ctx, SupervisorRef(sup.ID), "q/n", ""); err != nil || got != "Asked for p/m, running q/n." {
		t.Fatalf("note = %q, %v", got, err)
	}
	if got, err := s.ModelNote(ctx, SupervisorRef(sup.ID), "p/m", ""); err != nil || got != "" {
		t.Fatalf("note after it returns = %q, %v", got, err)
	}
	if got, err := s.ModelNote(ctx, "a1", "q/n", ""); err != nil || got != "" {
		t.Fatalf("other ref note = %q, %v", got, err)
	}
}

func TestAttemptSwitchedMovesTheRequestOnlyWhenTheSwitchWasRequested(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	task := activeTask(t, s)
	a, err := s.AddAttempt(ctx, AttemptSpec{TaskID: task.ID, Harness: "opencode", Model: "p/m", Argv: []string{"/bin/x", "p"}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref := AttemptRef(a.ID)
	first := Terminal{ServerGeneration: "g", TerminalID: "t1", PaneID: "p1", PID: 1, StartMarker: "m"}
	second := Terminal{ServerGeneration: "g", TerminalID: "t2", PaneID: "p2", PID: 2, StartMarker: "m"}
	if _, err := s.AttemptRunning(ctx, a.ID, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttemptSwitched(ctx, a.ID, second, "back on p/m", "p/m", "", false); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ModelNote(ctx, ref, "q/n", ""); err != nil || got != "Asked for p/m, running q/n." {
		t.Fatalf("after a switch that kept the request: note = %q, %v", got, err)
	}
	if _, err := s.AttemptSwitched(ctx, a.ID, first, "switched to q/n fast", "q/n", "fast", true); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ModelNote(ctx, ref, "q/n", "fast"); err != nil || got != "" {
		t.Fatalf("after a requested switch: note = %q, %v", got, err)
	}
	if got, err := s.ModelNote(ctx, ref, "p/m", ""); err != nil || got != "Asked for q/n fast, running p/m default." {
		t.Fatalf("running the old model: note = %q, %v", got, err)
	}
}
