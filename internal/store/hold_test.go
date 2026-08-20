package store

import (
	"errors"
	"testing"
)

func sampleHold() Hold {
	return Hold{
		ID: "fix-login", Kind: HoldKindOperator, Reason: "race condition needs a call",
		SetAt: "2026-07-24T10:00:00Z", Inferred: true,
	}
}

func TestSetReadHoldPreservesEveryField(t *testing.T) {
	db, _ := openTemp(t)
	want := sampleHold()
	if err := db.SetHold(want); err != nil {
		t.Fatal(err)
	}

	got, found, err := db.ReadHold(want.ID)
	if err != nil || !found {
		t.Fatalf("ReadHold = %v, %v", found, err)
	}
	if got != want {
		t.Fatalf("round trip lost a field:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestSetHoldUpsertsInPlace(t *testing.T) {
	db, _ := openTemp(t)
	hold := sampleHold()
	if err := db.SetHold(hold); err != nil {
		t.Fatal(err)
	}
	hold.Reason = "actually two ways to fix this, need a call"
	if err := db.SetHold(hold); err != nil {
		t.Fatal(err)
	}

	holds, err := db.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 1 || holds[0].Reason != hold.Reason {
		t.Fatalf("ListHolds = %+v", holds)
	}
}

func TestReadHoldReportsAMissingHoldWithoutAnError(t *testing.T) {
	db, _ := openTemp(t)
	_, found, err := db.ReadHold("nope")
	if err != nil || found {
		t.Fatalf("ReadHold = %v, %v", found, err)
	}
}

// Pins the decision that makes a hold cover the motivating case: its own row keyed by an
// arbitrary id, not a foreign key into task, so a hold set on a task torn down with its
// question open still answers "what needs the operator" after DeleteTask.
func TestHoldSurvivesWithNoTaskRowBehindIt(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "fix-login"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetHold(sampleHold()); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteTask("fix-login"); err != nil {
		t.Fatal(err)
	}

	_, found, err := db.ReadHold("fix-login")
	if err != nil || !found {
		t.Fatalf("ReadHold after task teardown = %v, %v, want found", found, err)
	}
}

func TestClearHoldLeavesNoResidue(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.SetHold(sampleHold()); err != nil {
		t.Fatal(err)
	}
	if err := db.ClearHold("fix-login"); err != nil {
		t.Fatal(err)
	}

	_, found, err := db.ReadHold("fix-login")
	if err != nil || found {
		t.Fatalf("ReadHold after clear = %v, %v, want gone", found, err)
	}
	holds, err := db.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 0 {
		t.Fatalf("ListHolds after clear = %+v, want empty", holds)
	}
}

func TestClearHoldMissing(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.ClearHold("nope"); !errors.Is(err, ErrHoldNotFound) {
		t.Fatalf("ClearHold error = %v, want ErrHoldNotFound", err)
	}
}

func TestListHoldsSortedAndEmpty(t *testing.T) {
	db, _ := openTemp(t)
	holds, err := db.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 0 {
		t.Fatalf("got %+v, want empty", holds)
	}

	for _, id := range []string{"zebra", "apple", "mango"} {
		if err := db.SetHold(Hold{ID: id, Kind: HoldKindOperator}); err != nil {
			t.Fatal(err)
		}
	}

	holds, err = db.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"apple", "mango", "zebra"}
	for i, id := range want {
		if holds[i].ID != id {
			t.Errorf("holds[%d].ID = %q, want %q", i, holds[i].ID, id)
		}
	}
}

// An inconsistent row from an external write must still come back from ListHolds,
// or "what is held" silently drops the row most worth seeing.
func TestListHoldsSurfacesEveryRowRegardlessOfKind(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.SetHold(Hold{ID: "weird", Kind: "not-a-real-kind", Reason: "who knows"}); err != nil {
		t.Fatal(err)
	}

	holds, err := db.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 1 || holds[0].Kind != "not-a-real-kind" {
		t.Fatalf("ListHolds = %+v, want the inconsistent row untouched", holds)
	}
}

func TestConditionalMachineHoldCannotOverwriteOperatorHold(t *testing.T) {
	db, _ := openTemp(t)
	operator := sampleHold()
	if err := db.SetHold(operator); err != nil {
		t.Fatal(err)
	}

	written, err := db.SetHoldIfNotOtherKind(Hold{ID: operator.ID, Kind: HoldKindLimit, Reason: "quota"})
	if err != nil {
		t.Fatal(err)
	}
	if written {
		t.Fatal("SetHoldIfNotOtherKind = true, want operator hold to win")
	}
	got, found, err := db.ReadHold(operator.ID)
	if err != nil || !found {
		t.Fatalf("ReadHold = %+v, %v, want operator hold", got, err)
	}
	if got != operator {
		t.Fatalf("hold = %+v, want %+v", got, operator)
	}
}

func TestConditionalMachineClearCannotDeleteOperatorReplacement(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.SetHold(Hold{ID: "fix-login", Kind: HoldKindLimit, Reason: "quota"}); err != nil {
		t.Fatal(err)
	}
	operator := sampleHold()
	if err := db.SetHold(operator); err != nil {
		t.Fatal(err)
	}

	cleared, err := db.ClearHoldIfKind(operator.ID, HoldKindLimit)
	if err != nil {
		t.Fatal(err)
	}
	if cleared {
		t.Fatal("ClearHoldIfKind = true, want operator replacement to remain")
	}
	got, found, err := db.ReadHold(operator.ID)
	if err != nil || !found || got != operator {
		t.Fatalf("ReadHold = %+v, %v, want %+v", got, found, operator)
	}
}
