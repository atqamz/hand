package store

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
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

func TestReadHoldReadOnlySupportsSchemaBeforeInferred(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	db := &DB{sql: sqlDB, home: home}
	legacySchema := strings.Replace(schema,
		"\tset_at     TEXT NOT NULL DEFAULT '',\n\tinferred   INTEGER NOT NULL DEFAULT 0",
		"\tset_at     TEXT NOT NULL DEFAULT ''", 1)
	if _, err := db.sql.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`INSERT INTO hold (id, kind, reason, blocked_on, set_at) VALUES (?, ?, ?, ?, ?)`,
		"legacy", HoldKindOperator, "old hold", "", "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec("PRAGMA user_version = " + strconv.Itoa(holdInferredVersion)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	hold, found, err := ReadHoldReadOnly(home, "legacy")
	if err != nil || !found {
		t.Fatalf("ReadHoldReadOnly = %+v, %t, %v", hold, found, err)
	}
	if hold.Inferred {
		t.Fatal("ReadHoldReadOnly inferred = true, want false for a pre-inferred schema")
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

// INV-HOLD-1: a hold is a standalone row with no foreign key to a task, and survives the
// task's teardown when human-authored. Generalizes TestHoldSurvivesWithNoTaskRowBehindIt over
// arbitrary field values and every order of task existence, creation, and teardown.
func TestHumanAuthoredHoldSurvivesAnyTaskLifecycleAroundIt(t *testing.T) {
	db, _ := openTemp(t)

	rapid.Check(t, func(t *rapid.T) {
		id := "task-" + rapid.StringMatching(`[a-z0-9]{1,16}`).Draw(t, "id")
		_ = db.ClearHold(id)
		_ = db.DeleteTask(id)

		hold := Hold{
			ID:        id,
			Kind:      rapid.SampledFrom([]string{HoldKindOperator, HoldKindBlocked}).Draw(t, "kind"),
			Reason:    rapid.String().Draw(t, "reason"),
			BlockedOn: rapid.String().Draw(t, "blocked-on"),
			SetAt:     rapid.String().Draw(t, "set-at"),
			Inferred:  rapid.Bool().Draw(t, "inferred"),
		}

		// Three shapes: no task ever exists, a task exists and outlives the hold, or a task
		// exists and is torn down (deleted) after the hold is set - all three must leave the
		// hold exactly as it was set.
		switch rapid.SampledFrom([]string{"no-task", "task-survives", "task-torn-down"}).Draw(t, "shape") {
		case "no-task":
			if err := db.SetHold(hold); err != nil {
				t.Fatal(err)
			}
		case "task-survives":
			if err := db.CreateTask(Task{ID: id}); err != nil {
				t.Fatal(err)
			}
			if err := db.SetHold(hold); err != nil {
				t.Fatal(err)
			}
		case "task-torn-down":
			if err := db.CreateTask(Task{ID: id}); err != nil {
				t.Fatal(err)
			}
			if err := db.SetHold(hold); err != nil {
				t.Fatal(err)
			}
			if err := db.DeleteTask(id); err != nil {
				t.Fatal(err)
			}
		}

		got, found, err := db.ReadHold(id)
		if err != nil || !found {
			t.Fatalf("ReadHold(%q) = %v, %v, want the hold to survive", id, found, err)
		}
		if got != hold {
			t.Fatalf("hold after task lifecycle = %+v, want unchanged %+v", got, hold)
		}
	})
}

// INV-HOLD-2: a machine-authored hold is cleared, or overwritten by a would-be replacement,
// only after its kind is checked. Generalizes TestConditionalMachineHoldCannotOverwriteOperatorHold
// and TestConditionalMachineClearCannotDeleteOperatorReplacement over every kind pair.
func TestConditionalHoldOperationsCheckKindBeforeActing(t *testing.T) {
	db, _ := openTemp(t)
	kinds := []string{HoldKindOperator, HoldKindBlocked, HoldKindLimit, "external-kind"}

	rapid.Check(t, func(t *rapid.T) {
		id := "hold-" + rapid.StringMatching(`[a-z0-9]{1,16}`).Draw(t, "id")
		_ = db.ClearHold(id)

		existing := Hold{
			ID:        id,
			Kind:      rapid.SampledFrom(kinds).Draw(t, "existing-kind"),
			Reason:    rapid.String().Draw(t, "existing-reason"),
			BlockedOn: rapid.String().Draw(t, "existing-blocked-on"),
			SetAt:     rapid.String().Draw(t, "existing-set-at"),
			Inferred:  rapid.Bool().Draw(t, "existing-inferred"),
		}
		if err := db.SetHold(existing); err != nil {
			t.Fatal(err)
		}

		attemptKind := rapid.SampledFrom(kinds).Draw(t, "attempt-kind")
		sameKind := attemptKind == existing.Kind

		if rapid.Bool().Draw(t, "op-is-clear") {
			cleared, err := db.ClearHoldIfKind(id, attemptKind)
			if err != nil {
				t.Fatal(err)
			}
			if cleared != sameKind {
				t.Fatalf("ClearHoldIfKind(%q) against existing kind %q = %v, want %v", attemptKind, existing.Kind, cleared, sameKind)
			}
			got, found, err := db.ReadHold(id)
			if err != nil {
				t.Fatal(err)
			}
			if sameKind {
				if found {
					t.Fatalf("hold still present after a same-kind clear")
				}
			} else if !found || got != existing {
				t.Fatalf("hold after a different-kind clear attempt = %+v, %v, want untouched %+v", got, found, existing)
			}
		} else {
			replacement := Hold{
				ID:        id,
				Kind:      attemptKind,
				Reason:    rapid.String().Draw(t, "replacement-reason"),
				BlockedOn: rapid.String().Draw(t, "replacement-blocked-on"),
				SetAt:     rapid.String().Draw(t, "replacement-set-at"),
				Inferred:  rapid.Bool().Draw(t, "replacement-inferred"),
			}
			written, err := db.SetHoldIfNotOtherKind(replacement)
			if err != nil {
				t.Fatal(err)
			}
			if written != sameKind {
				t.Fatalf("SetHoldIfNotOtherKind(kind=%q) against existing kind %q = %v, want %v", attemptKind, existing.Kind, written, sameKind)
			}
			got, found, err := db.ReadHold(id)
			if err != nil || !found {
				t.Fatalf("ReadHold after SetHoldIfNotOtherKind = %v, %v", found, err)
			}
			want := existing
			if sameKind {
				want = replacement
			}
			if got != want {
				t.Fatalf("hold after SetHoldIfNotOtherKind = %+v, want %+v", got, want)
			}
		}
	})
}
