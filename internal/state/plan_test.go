package state

import (
	"context"
	"errors"
	"testing"
)

func TestSetPlanAppendsRevisionsAndKeepsHistory(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	task, _ := s.AddTask(ctx, "hand", "Fix login", "")
	if _, _, err := s.CurrentPlan(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	p1, err := s.SetPlan(ctx, task.ID, "reproduce, then fix")
	if err != nil || p1.Revision != 1 {
		t.Fatalf("p1 = %+v, %v", p1, err)
	}
	p2, err := s.SetPlan(ctx, task.ID, "fix the session cookie")
	if err != nil || p2.Revision != 2 {
		t.Fatalf("p2 = %+v, %v", p2, err)
	}
	cur, ok, err := s.CurrentPlan(ctx, task.ID)
	if err != nil || !ok || cur.Revision != 2 || cur.Body != "fix the session cookie" {
		t.Fatalf("current = %+v, %v, %v", cur, ok, err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM plan WHERE task_id = ?`, task.ID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("plan rows = %d, %v; want history kept", n, err)
	}
}

func TestSetPlanRefusesEmptyBodyAndClosedTask(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	task, _ := s.AddTask(ctx, "hand", "Fix login", "")
	if _, err := s.SetPlan(ctx, task.ID, "   "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty body err = %v", err)
	}
	if _, err := s.SetPlan(ctx, 99, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task err = %v", err)
	}
	_, _ = s.Transition(ctx, task.ID, StatusAbandoned)
	if _, err := s.SetPlan(ctx, task.ID, "x"); !errors.Is(err, ErrConflict) {
		t.Fatalf("closed task err = %v", err)
	}
}
