package state

import (
	"errors"
	"testing"
)

func TestSetReadClearHoldRoundTrip(t *testing.T) {
	dir := t.TempDir()
	hold := Hold{ID: "fix-login", Kind: HoldKindOperator, Reason: "needs a call", SetAt: "2026-07-24T10:00:00Z"}

	if err := SetHold(dir, hold); err != nil {
		t.Fatal(err)
	}
	got, found, err := ReadHold(dir, "fix-login")
	if err != nil || !found {
		t.Fatalf("ReadHold = %v, %v", found, err)
	}
	if got != hold {
		t.Fatalf("got %+v, want %+v", got, hold)
	}

	if err := ClearHold(dir, "fix-login"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := ReadHold(dir, "fix-login"); err != nil || found {
		t.Fatalf("ReadHold after clear = %v, %v, want gone", found, err)
	}
}

func TestClearHoldMissing(t *testing.T) {
	dir := t.TempDir()
	if err := ClearHold(dir, "nope"); !errors.Is(err, ErrHoldNotFound) {
		t.Fatalf("ClearHold error = %v, want ErrHoldNotFound", err)
	}
}

func TestListHoldsEmpty(t *testing.T) {
	dir := t.TempDir()
	holds, err := ListHolds(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 0 {
		t.Fatalf("got %+v, want empty", holds)
	}
}

func TestHoldRejectsUnsafeIDs(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"../escape", "nested/task", "", "."} {
		if err := SetHold(dir, Hold{ID: id, Kind: HoldKindOperator}); err == nil {
			t.Errorf("SetHold accepted unsafe ID %q", id)
		}
		if _, _, err := ReadHold(dir, id); err == nil {
			t.Errorf("ReadHold accepted unsafe ID %q", id)
		}
		if err := ClearHold(dir, id); err == nil {
			t.Errorf("ClearHold accepted unsafe ID %q", id)
		}
	}
}

func TestSetHoldRejectsUnsafeBlockedOn(t *testing.T) {
	dir := t.TempDir()
	err := SetHold(dir, Hold{ID: "fix-login", Kind: HoldKindBlocked, BlockedOn: "../escape"})
	if err == nil {
		t.Fatal("SetHold accepted unsafe blocked-on id")
	}
}
