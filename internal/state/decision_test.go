package state

import (
	"context"
	"errors"
	"testing"
)

func TestDecisionAskAnswerAndRefuseRepeat(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	task, _ := s.AddTask(ctx, "hand", "Fix login", "")
	d, err := s.Ask(ctx, task.ID, "Keep the old cookie name?")
	if err != nil || d.ID != 1 || d.Status != DecisionOpen {
		t.Fatalf("ask = %+v, %v", d, err)
	}
	if n, _ := s.OpenDecisionCount(ctx, 0); n != 1 {
		t.Fatalf("open count = %d", n)
	}
	if _, err := s.Answer(ctx, d.ID, "yes", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("answer without actor err = %v", err)
	}
	a, err := s.Answer(ctx, d.ID, "yes", "operator")
	if err != nil || a.Status != DecisionAnswered || a.Answer != "yes" || a.AnsweredBy != "operator" {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	if _, err := s.Answer(ctx, d.ID, "no", "operator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second answer err = %v", err)
	}
	if _, err := s.Withdraw(ctx, d.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("withdraw answered err = %v", err)
	}
	got, _ := s.OpenDecisions(ctx, 0, 10)
	if len(got) != 0 {
		t.Fatalf("open decisions = %+v", got)
	}
}

func TestAskRefusesClosedTaskAndEmptyQuestion(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	task, _ := s.AddTask(ctx, "hand", "Fix login", "")
	if _, err := s.Ask(ctx, task.ID, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty question err = %v", err)
	}
	_, _ = s.Transition(ctx, task.ID, StatusAbandoned)
	if _, err := s.Ask(ctx, task.ID, "still?"); !errors.Is(err, ErrConflict) {
		t.Fatalf("closed task err = %v", err)
	}
	if _, err := s.Answer(ctx, 42, "x", "operator"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing decision err = %v", err)
	}
}

func TestClosingATaskWithdrawsItsOpenDecisions(t *testing.T) {
	for _, to := range []string{StatusDone, StatusAbandoned} {
		s, _ := openTest(t)
		seedProject(t, s)
		ctx := context.Background()
		task, _ := s.AddTask(ctx, "hand", "Fix login", "")
		if _, err := s.Transition(ctx, task.ID, StatusActive); err != nil {
			t.Fatal(err)
		}
		d, err := s.Ask(ctx, task.ID, "Keep the old cookie name?")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transition(ctx, task.ID, to); err != nil {
			t.Fatal(err)
		}
		if n, _ := s.OpenDecisionCount(ctx, 0); n != 0 {
			t.Fatalf("%s: open decisions = %d, want 0", to, n)
		}
		if _, err := s.Answer(ctx, d.ID, "yes", "operator"); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s: answer after close err = %v, want ErrConflict", to, err)
		}
		events, _ := s.RecentEvents(ctx, 10)
		var withdrawn bool
		for _, e := range events {
			withdrawn = withdrawn || (e.Kind == "decision.withdrawn" && e.TaskID == task.ID && e.Detail == DecisionRef(d.ID))
		}
		if !withdrawn {
			t.Fatalf("%s: no decision.withdrawn event in %+v", to, events)
		}
	}
}

func TestListsRefuseUnknownStatusAndNonPositiveLimit(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for _, tc := range []struct {
		statuses []string
		limit    int
	}{{[]string{"actve"}, 10}, {[]string{""}, 10}, {nil, 0}, {nil, -1}} {
		if _, err := s.Tasks(ctx, tc.statuses, tc.limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Tasks(%q, %d) err = %v, want ErrInvalid", tc.statuses, tc.limit, err)
		}
	}
	for _, limit := range []int{0, -1} {
		if _, err := s.OpenDecisions(ctx, 0, limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("OpenDecisions limit %d err = %v, want ErrInvalid", limit, err)
		}
	}
}
