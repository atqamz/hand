package board

import (
	"context"
	"testing"
)

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
