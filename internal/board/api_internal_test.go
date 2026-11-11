package board

import (
	"context"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestADeviceNameCannotSpoofTheActorLabel(t *testing.T) {
	ctx := context.WithValue(context.Background(), sessionKey{}, session{Device: &state.Device{Name: "Pi\u202exel) (operator"}})
	if got, want := actor(ctx, "operator (desktop)"), "phone (Pixel operator)"; got != want {
		t.Fatalf("actor = %q, want %q", got, want)
	}
	if got := actor(context.Background(), "operator (desktop)"); got != "operator (desktop)" {
		t.Fatalf("laptop actor = %q", got)
	}
}

func TestAStalledNeedLeavesOutTheIdleTime(t *testing.T) {
	got := apiNeed(waiting{Kind: "stalled", Title: "Supervisor idle for 1h12m with 2 active tasks and no running attempt"}).Title
	if want := "Supervisor idle with 2 active tasks and no running attempt"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

func TestAPanicWhileReadingTheVersionIsAnError(t *testing.T) {
	b := &Board{}
	if _, err := b.sharedVersion(context.Background(), false); err == nil {
		t.Fatal("a version read without a store did not fail")
	}
}
