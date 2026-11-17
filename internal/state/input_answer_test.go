package state

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

func inputEvents(t *testing.T, s *Store, kind string) []string {
	t.Helper()
	events, err := s.RecentEventsOf(context.Background(), []string{kind}, 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, e.Detail)
	}
	return out
}

func TestAVersion23DatabaseMigratesWithItsInputsReadingAsBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range append(append([]string{schema}, migrations[:7]...),
		`PRAGMA user_version = 23`,
		`INSERT INTO supervisor_input(id, body, created_at, delivered_at) VALUES (1, 'old queued', '2026-11-01T00:00:00Z', '')`,
		`INSERT INTO supervisor_input(id, body, created_at, delivered_at) VALUES (2, 'old delivered', '2026-11-01T00:00:00Z', '2026-11-01T00:00:05Z')`,
	) {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for id, want := range map[int64]string{1: "queued", 2: "delivered"} {
		in, err := s.SupervisorInput(ctx, id)
		if err != nil || in.Status() != want || in.AnsweredAt != "" || in.Reason != "" || in.ReasonDetail != "" {
			t.Fatalf("input %d = %+v, %v; want %s with no reason", id, in, err, want)
		}
	}
	if err := s.HoldSupervisorInputs(ctx, ReasonBlocked, "Esc to cancel"); err != nil {
		t.Fatal(err)
	}
	if in, _ := s.SupervisorInput(ctx, 1); in.Reason != ReasonBlocked {
		t.Fatalf("migrated queued input = %+v", in)
	}
}

func TestAnInputMovesFromQueuedToDeliveredToAnsweredAndTheReasonLeavesWithDelivery(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	in, err := s.AddSupervisorInput(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SupervisorInput(ctx, in.ID); got.Status() != "queued" || got.Reason != "" {
		t.Fatalf("fresh = %+v", got)
	}
	if err := s.AnswerSupervisorInput(ctx, in.ID, "2026-11-17T04:42:00Z"); !errors.Is(err, ErrConflict) {
		t.Fatalf("answering a queued input = %v, want ErrConflict", err)
	}
	if err := s.HoldSupervisorInputs(ctx, ReasonBlocked, "Esc to cancel"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.SupervisorInput(ctx, in.ID)
	if got.Status() != "queued" || got.Reason != ReasonBlocked || got.ReasonDetail != "Esc to cancel" || !slices.Equal(got.Keys(), SupervisorKeys) {
		t.Fatalf("held = %+v keys %v", got, got.Keys())
	}
	if err := s.DeliverSupervisorInput(ctx, in.ID, false); err != nil {
		t.Fatal(err)
	}
	got, _ = s.SupervisorInput(ctx, in.ID)
	if got.Status() != "delivered" || got.Reason != "" || got.ReasonDetail != "" || got.Keys() != nil {
		t.Fatalf("delivered = %+v", got)
	}
	if err := s.AnswerSupervisorInput(ctx, in.ID, "2026-11-17T04:42:00.5Z"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.SupervisorInput(ctx, in.ID)
	if got.Status() != "answered" || got.AnsweredAt != "2026-11-17T04:42:00.5Z" {
		t.Fatalf("answered = %+v", got)
	}
	if err := s.AnswerSupervisorInput(ctx, in.ID, "2026-11-17T05:00:00Z"); !errors.Is(err, ErrConflict) {
		t.Fatalf("answering twice = %v, want ErrConflict", err)
	}
	if got, _ = s.SupervisorInput(ctx, in.ID); got.AnsweredAt != "2026-11-17T04:42:00.5Z" {
		t.Fatalf("the first answer was overwritten: %+v", got)
	}
	if ev := inputEvents(t, s, "supervisor.answered"); !slices.Equal(ev, []string{"i1"}) {
		t.Fatalf("answered events = %q", ev)
	}
}

func TestHoldingRecordsOneEventPerChangeAndLeavesDeliveredInputsAlone(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for _, body := range []string{"one", "two", "three"} {
		if _, err := s.AddSupervisorInput(ctx, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeliverSupervisorInput(ctx, 1, false); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := s.HoldSupervisorInputs(ctx, ReasonLimited, "resets 6:10am"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.HoldSupervisorInputs(ctx, ReasonNoSupervisor, ""); err != nil {
		t.Fatal(err)
	}
	if ev := inputEvents(t, s, "supervisor.held"); !slices.Equal(ev, []string{"i2, i3: limited: resets 6:10am", "i2, i3: no_supervisor"}) {
		t.Fatalf("held events = %q", ev)
	}
	if in, _ := s.SupervisorInput(ctx, 1); in.Reason != "" || in.Status() != "delivered" {
		t.Fatalf("a delivered input took a reason: %+v", in)
	}
	if err := s.HoldSupervisorInputs(ctx, "bored", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown reason = %v, want ErrInvalid", err)
	}
	pending, err := s.PendingSupervisorInputs(ctx)
	if err != nil || len(pending) != 2 || pending[0].Reason != ReasonNoSupervisor {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
}

func TestUnansweredInputsAreTheDeliveredOnesOldestFirstAndCapped(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for _, body := range []string{"a", "b", "c"} {
		in, err := s.AddSupervisorInput(ctx, body)
		if err != nil {
			t.Fatal(err)
		}
		if body != "c" {
			if err := s.DeliverSupervisorInput(ctx, in.ID, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.AnswerSupervisorInput(ctx, 1, "2026-11-17T04:42:00Z"); err != nil {
		t.Fatal(err)
	}
	got, err := s.UnansweredSupervisorInputs(ctx, 10)
	if err != nil || len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("unanswered = %+v, %v", got, err)
	}
	if err := s.DeliverSupervisorInput(ctx, 3, false); err != nil {
		t.Fatal(err)
	}
	got, _ = s.UnansweredSupervisorInputs(ctx, 1)
	if len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("capped = %+v", got)
	}
}

func TestATurnStartsAtTheLastDeliveryOfTheRunningSupervisor(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	sup := runningSupervisor(t, s)
	if at, err := s.TurnSince(ctx, sup); err != nil || at != sup.CreatedAt {
		t.Fatalf("before any delivery = %q, %v; want the supervisor's start %q", at, err, sup.CreatedAt)
	}
	in, _ := s.AddSupervisorInput(ctx, "go")
	if err := s.DeliverSupervisorInput(ctx, in.ID, false); err != nil {
		t.Fatal(err)
	}
	e, _, _ := s.LatestEvent(ctx, "supervisor.delivered")
	if at, err := s.TurnSince(ctx, sup); err != nil || at != e.At {
		t.Fatalf("after a delivery = %q, %v; want %q", at, err, e.At)
	}
}
