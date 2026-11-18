package state

import (
	"context"
	"slices"
	"testing"
)

func sendDelivered(t *testing.T, s *Store, n int) []int64 {
	t.Helper()
	var ids []int64
	for range n {
		in, err := s.AddSupervisorInput(context.Background(), "hello")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.DeliverSupervisorInput(context.Background(), in.ID, false); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, in.ID)
	}
	return ids
}

func TestWaitingIsOneEventPerChangeOfWhyAndLeavesQueuedAndAnsweredInputsAlone(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	ids := sendDelivered(t, s, 3)
	queued, err := s.AddSupervisorInput(ctx, "not typed yet")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AnswerSupervisorInput(ctx, ids[2], "2026-11-17T04:42:00Z"); err != nil {
		t.Fatal(err)
	}
	all := append(slices.Clone(ids), queued.ID)
	if err := s.WaitInputs(ctx, all, WaitNotWait); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitInputs(ctx, all, WaitNotWait); err != nil {
		t.Fatal(err)
	}
	if ev := inputEvents(t, s, "input.waited"); !slices.Equal(ev, []string{"i1, i2: not_wait"}) {
		t.Fatalf("waited events = %q", ev)
	}
	for _, id := range ids[:2] {
		if in, _ := s.SupervisorInput(ctx, id); in.Reason != ReasonWaited || in.ReasonDetail != WaitNotWait || in.Status() != "delivered" {
			t.Fatalf("input %d = %+v", id, in)
		}
	}
	for id, want := range map[int64]string{ids[2]: "", queued.ID: ""} {
		if in, _ := s.SupervisorInput(ctx, id); in.Reason != want {
			t.Fatalf("input %d took reason %q", id, in.Reason)
		}
	}
	if err := s.WaitInputs(ctx, ids[:2], WaitTyped); err != nil {
		t.Fatal(err)
	}
	if ev := inputEvents(t, s, "input.waited"); !slices.Equal(ev, []string{"i1, i2: not_wait", "i1, i2: typed"}) {
		t.Fatalf("a new why records one more event: %q", ev)
	}
}

func TestUnknownAndNotWaitDoNotTakeTurnsAsEvents(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	ids := sendDelivered(t, s, 1)
	for _, why := range []string{WaitUnknown, WaitNotWait, WaitUnknown, WaitNotWait} {
		if err := s.WaitInputs(ctx, ids, why); err != nil {
			t.Fatal(err)
		}
	}
	if ev := inputEvents(t, s, "input.waited"); !slices.Equal(ev, []string{"i1: unknown"}) {
		t.Fatalf("waited events = %q, want the first reason only", ev)
	}
	if err := s.WaitInputs(ctx, ids, WaitOff); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitInputs(ctx, ids, WaitNotWait); err != nil {
		t.Fatal(err)
	}
	if ev := inputEvents(t, s, "input.waited"); !slices.Equal(ev, []string{"i1: unknown", "i1: off", "i1: not_wait"}) {
		t.Fatalf("waited events = %q, want off and a later not_wait kept", ev)
	}
}

func TestAnInterruptIsRecordedOnceAndNeverUndoneByAWait(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	ids := sendDelivered(t, s, 2)
	if err := s.WaitInputs(ctx, ids, WaitNotWait); err != nil {
		t.Fatal(err)
	}
	if err := s.InterruptInputs(ctx, ids[:1], ""); err != nil {
		t.Fatal(err)
	}
	if err := s.InterruptInputs(ctx, ids[:1], "again"); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitInputs(ctx, ids, WaitOff); err != nil {
		t.Fatal(err)
	}
	if ev := inputEvents(t, s, "input.interrupted"); !slices.Equal(ev, []string{"i1"}) {
		t.Fatalf("interrupted events = %q", ev)
	}
	if in, _ := s.SupervisorInput(ctx, ids[0]); in.Reason != ReasonInterrupted || in.ReasonDetail != "" {
		t.Fatalf("interrupted input = %+v", in)
	}
	if in, _ := s.SupervisorInput(ctx, ids[1]); in.Reason != ReasonWaited || in.ReasonDetail != WaitOff {
		t.Fatalf("waiting input = %+v", in)
	}
	if err := s.AnswerSupervisorInput(ctx, ids[0], "2026-11-17T04:42:00Z"); err != nil {
		t.Fatal(err)
	}
	if in, _ := s.SupervisorInput(ctx, ids[0]); in.Status() != "answered" || in.Reason != ReasonInterrupted {
		t.Fatalf("the receipt is lost at the answer: %+v", in)
	}
}

func TestTheReasonTextNamesWhatHappened(t *testing.T) {
	for _, c := range []struct{ code, detail, want string }{
		{ReasonWaited, WaitOff, "waited: interrupt is off"},
		{ReasonWaited, WaitUnknown, "waited: Hand cannot tell what the supervisor runs"},
		{ReasonWaited, WaitNotWait, "waited: the supervisor is not in a known wait"},
		{ReasonWaited, WaitTyped, "waited: someone typed in the pane"},
		{ReasonWaited, "", "waited: Hand cannot tell what the supervisor runs"},
		{ReasonInterrupted, "", "interrupted, sent again"},
		{ReasonInterrupted, "not sent again: the prompt was not empty", "interrupted: not sent again: the prompt was not empty"},
	} {
		if got := ReasonText(c.code, c.detail); got != c.want {
			t.Errorf("ReasonText(%q, %q) = %q, want %q", c.code, c.detail, got, c.want)
		}
	}
}
