package state

import (
	"context"
	"testing"
)

func TestAnAttemptRowWithTheRemovedApproveAndAccessColumnsStillReads(t *testing.T) {
	s, path := openTest(t)
	task := activeTask(t, s)
	ctx := context.Background()
	spec := claudeSpec
	spec.TaskID = task.ID
	a, err := s.AddAttempt(ctx, spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE attempt SET approve = 'ask', access = 'readonly' WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got, err := again.Attempt(ctx, a.ID); err != nil || got.ID != a.ID {
		t.Fatalf("attempt = %+v, %v", got, err)
	}
	if list, err := again.Attempts(ctx, task.ID, 10); err != nil || len(list) != 1 {
		t.Fatalf("attempts = %+v, %v", list, err)
	}
}
