package state

import (
	"context"
	"errors"
	"testing"
)

func TestAnAttemptRecordsItsAccess(t *testing.T) {
	s, _ := openTest(t)
	task := activeTask(t, s)
	ctx := context.Background()
	spec := claudeSpec
	spec.TaskID = task.ID
	spec.Access = AccessReadonly
	a, err := s.AddAttempt(ctx, spec, t.TempDir())
	if err != nil || a.Access != AccessReadonly {
		t.Fatalf("add = %+v, %v", a, err)
	}
	if got, err := s.Attempt(ctx, a.ID); err != nil || got.Access != AccessReadonly {
		t.Fatalf("read = %+v, %v", got, err)
	}
	if _, err := s.AttemptRunning(ctx, a.ID, Terminal{TerminalID: "t", PaneID: "p"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndAttempt(ctx, a.ID, AttemptStopped, "x"); err != nil {
		t.Fatal(err)
	}
	spec.Access = ""
	b, err := s.AddAttempt(ctx, spec, t.TempDir())
	if err != nil || b.Access != AccessOpen {
		t.Fatalf("default = %+v, %v", b, err)
	}
	if _, err := s.AttemptRunning(ctx, b.ID, Terminal{TerminalID: "t2", PaneID: "p2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndAttempt(ctx, b.ID, AttemptStopped, "x"); err != nil {
		t.Fatal(err)
	}
	spec.Access = "write"
	if _, err := s.AddAttempt(ctx, spec, t.TempDir()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad access err = %v", err)
	}
}
