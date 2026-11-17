package board_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

func apiEntryOf(t *testing.T, fx *fixture, input int64) board.APIEntry {
	t.Helper()
	_, tl := apiInto[board.APITimeline](t, fx.handler(), "/api/v1/timeline")
	for _, e := range tl.Entries {
		if e.Input == input {
			return e
		}
	}
	t.Fatalf("no entry for input %d in %+v", input, tl.Entries)
	return board.APIEntry{}
}

func TestTheAPICarriesTheStatusAndTheReasonOfEachMessage(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	ctx := context.Background()
	id := sendInput(t, fx, "hello")
	if e := apiEntryOf(t, fx, id); e.Status != "queued" || e.Reason != "" || e.ReasonKeys != nil {
		t.Fatalf("fresh = %+v", e)
	}
	if err := fx.st.HoldSupervisorInputs(ctx, state.ReasonBlocked, "Esc to cancel"); err != nil {
		t.Fatal(err)
	}
	e := apiEntryOf(t, fx, id)
	if e.Status != "queued" || e.Delivery != "queued" || e.Reason != "blocked" || e.ReasonDetail != "Esc to cancel" || !slices.Equal(e.ReasonKeys, state.SupervisorKeys) {
		t.Fatalf("blocked = %+v", e)
	}
	if err := fx.st.DeliverSupervisorInput(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	claudeLog(t, fx, []string{rec("user", ago(20), "hello", 0)})
	e = apiEntryOf(t, fx, id)
	if e.Status != "delivered" || e.Reason != "" || e.ReasonDetail != "" || e.ReasonKeys != nil || e.AnsweredAt != "" {
		t.Fatalf("delivered = %+v", e)
	}
	if err := fx.st.AnswerSupervisorInput(ctx, id, "2026-11-01T04:59:00Z"); err != nil {
		t.Fatal(err)
	}
	e = apiEntryOf(t, fx, id)
	if e.Status != "answered" || e.AnsweredAt != "2026-11-01T04:59:00Z" || e.Delivery != "delivered" {
		t.Fatalf("answered = %+v", e)
	}
}

func TestTheAPIDropsTheDirectoryOfAPathInAReasonDetail(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	id := sendInput(t, fx, "hello")
	if err := fx.st.HoldSupervisorInputs(context.Background(), state.ReasonBlocked, "Allow edits to /home/someone/work/secret/plan.md?"); err != nil {
		t.Fatal(err)
	}
	if e := apiEntryOf(t, fx, id); e.ReasonDetail != "Allow edits to plan.md?" {
		t.Fatalf("detail = %q", e.ReasonDetail)
	}
}

func TestAPIMessageStatesGolden(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	ctx := context.Background()
	answered := sendInput(t, fx, "hello")
	claudeLog(t, fx, []string{
		rec("user", ago(20), "status?", 0),
		rec("assistant", ago(19), "All good.", 0),
		rec("user", ago(18), "hello", 0),
		rec("assistant", ago(17), "Hello.", 0),
	})
	if err := fx.st.DeliverSupervisorInput(ctx, answered, false); err != nil {
		t.Fatal(err)
	}
	if err := fx.st.AnswerSupervisorInput(ctx, answered, frozen.Add(-17*time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	sendInput(t, fx, "and then?")
	if err := fx.st.HoldSupervisorInputs(ctx, state.ReasonBlocked, "Do you want to proceed?"); err != nil {
		t.Fatal(err)
	}
	checkAPIGolden(t, fx.handler(), "message-states", "/api/v1/timeline")
}
