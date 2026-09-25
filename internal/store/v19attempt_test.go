package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCreateCanonicalV19AttemptPersistsExactResolvedProvenance(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	input := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	ordinal, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 {
		t.Fatalf("Attempt ordinal = %d, want 1", ordinal)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var got CanonicalV19AttemptCreateInput
	var gotOrdinal int64
	var lifecycle, terminalAt string
	var profileOverride, harnessOverride, modelOverride, effortOverride sql.NullString
	if err := db.sql.QueryRow(`SELECT id,plan_id,ordinal,worker_harness_ref,worker_harness_version,
		worker_profile_ref,model_ref,effort_ref,session_adapter_ref,lifecycle,created_at,terminal_at,
		profile_override,harness_override,model_override,effort_override
		FROM attempt WHERE id=?`, input.ID).Scan(
		&got.ID, &got.PlanID, &gotOrdinal, &got.WorkerHarnessRef, &got.WorkerHarnessVersion,
		&got.WorkerProfileRef, &got.ModelRef, &got.EffortRef, &got.SessionAdapterRef,
		&lifecycle, &got.CreatedAt, &terminalAt,
		&profileOverride, &harnessOverride, &modelOverride, &effortOverride,
	); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, input) || gotOrdinal != 1 || lifecycle != "active" || terminalAt != "" ||
		profileOverride.Valid || harnessOverride.Valid || modelOverride.Valid || effortOverride.Valid {
		t.Fatalf("persisted Attempt = %#v ordinal=%d lifecycle=%q terminal_at=%q", got, gotOrdinal, lifecycle, terminalAt)
	}
}

func TestCreateCanonicalV19AttemptAllowsEmptyOptionalResolvedProvenance(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	input := canonicalV19AttemptWriterInput("attempt-optional", "plan-root")
	input.WorkerHarnessVersion = ""
	input.WorkerProfileRef = ""
	input.ModelRef = ""
	input.EffortRef = ""
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
}

func TestCreateCanonicalV19AttemptPersistsRequestedOverridesSeparatelyFromResolvedValues(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	input := canonicalV19AttemptWriterInput("attempt-overrides", "plan-root")
	profile := "profile/requested"
	harness := "worker-harness/requested"
	model := ""
	effort := "high"
	input.ProfileOverride = &profile
	input.HarnessOverride = &harness
	input.ModelOverride = &model
	input.EffortOverride = &effort
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var gotProfile, gotHarness, gotModel, gotEffort sql.NullString
	var finalProfile, finalHarness, finalModel, finalEffort string
	if err := db.sql.QueryRow(`SELECT profile_override,harness_override,model_override,effort_override,
		worker_profile_ref,worker_harness_ref,model_ref,effort_ref FROM attempt WHERE id=?`, input.ID).Scan(
		&gotProfile, &gotHarness, &gotModel, &gotEffort,
		&finalProfile, &finalHarness, &finalModel, &finalEffort,
	); err != nil {
		t.Fatal(err)
	}
	if gotProfile != (sql.NullString{String: profile, Valid: true}) ||
		gotHarness != (sql.NullString{String: harness, Valid: true}) ||
		gotModel != (sql.NullString{String: model, Valid: true}) ||
		gotEffort != (sql.NullString{String: effort, Valid: true}) {
		t.Fatalf("persisted overrides = %#v %#v %#v %#v", gotProfile, gotHarness, gotModel, gotEffort)
	}
	if finalProfile != input.WorkerProfileRef || finalHarness != input.WorkerHarnessRef ||
		finalModel != input.ModelRef || finalEffort != input.EffortRef {
		t.Fatalf("final provenance = %q %q %q %q", finalProfile, finalHarness, finalModel, finalEffort)
	}
}

func TestCreateCanonicalV19AttemptRejectsInvalidRequestedOverrides(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value string
	}{
		{"empty profile", "profile", ""},
		{"empty harness", "harness", ""},
		{"nul profile", "profile", "p\x00q"},
		{"nul harness", "harness", "h\x00i"},
		{"nul model", "model", "m\x00n"},
		{"nul effort", "effort", "e\x00f"},
		{"long profile", "profile", strings.Repeat("p", 513)},
		{"long harness", "harness", strings.Repeat("h", 513)},
		{"long model", "model", strings.Repeat("m", 513)},
		{"long effort", "effort", strings.Repeat("e", 513)},
		{"invalid utf8 model", "model", string([]byte{0xff})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := canonicalV19AttemptWriterFixture(t)
			input := canonicalV19AttemptWriterInput("attempt-invalid", "plan-root")
			switch tc.field {
			case "profile":
				input.ProfileOverride = &tc.value
			case "harness":
				input.HarnessOverride = &tc.value
			case "model":
				input.ModelOverride = &tc.value
			case "effort":
				input.EffortOverride = &tc.value
			}
			if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, input); err == nil {
				t.Fatal("invalid override accepted")
			}
			if got := canonicalV19AttemptWriterCount(t, fixture.Home); got != 0 {
				t.Fatalf("Attempt rows after refusal = %d, want 0", got)
			}
		})
	}
}

func TestCreateCanonicalV19AttemptRefusesSecondActiveAttempt(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	_, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-2", "plan-root"))
	if !errors.Is(err, ErrCanonicalV19AttemptConflict) {
		t.Fatalf("second active Attempt error = %v, want %v", err, ErrCanonicalV19AttemptConflict)
	}
	if got := canonicalV19AttemptWriterCount(t, fixture.Home); got != 1 {
		t.Fatalf("Attempt rows after active conflict = %d, want 1", got)
	}
}

func TestCreateCanonicalV19AttemptRetriesSamePlanAfterTerminalAttempt(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	first := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	canonicalV19AttemptWriterTerminalize(t, fixture.Home, first.ID, "failed", "2026-09-04T09:01:00Z")

	second := canonicalV19AttemptRetryInput("attempt-2", "plan-root", first.ID)
	second.CreatedAt = "2026-09-04T09:02:00Z"
	ordinal, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, second)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 2 {
		t.Fatalf("retry Attempt ordinal = %d, want 2", ordinal)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var firstLifecycle, firstTerminal, secondLifecycle, secondTerminal string
	if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM attempt WHERE id=?`, first.ID).Scan(&firstLifecycle, &firstTerminal); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM attempt WHERE id=?`, second.ID).Scan(&secondLifecycle, &secondTerminal); err != nil {
		t.Fatal(err)
	}
	if firstLifecycle != "failed" || firstTerminal != "2026-09-04T09:01:00Z" || secondLifecycle != "active" || secondTerminal != "" {
		t.Fatalf("retry state = first %q/%q second %q/%q", firstLifecycle, firstTerminal, secondLifecycle, secondTerminal)
	}
}

func TestCreateCanonicalV19AttemptDuplicateIdentityAfterTerminalRefusesWithoutNewRow(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	first := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	canonicalV19AttemptWriterTerminalize(t, fixture.Home, first.ID, "failed", "2026-09-04T09:01:00Z")

	_, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptRetryInput(first.ID, "plan-root", first.ID))
	if !errors.Is(err, ErrCanonicalV19AttemptConflict) {
		t.Fatalf("duplicate Attempt identity error = %v, want %v", err, ErrCanonicalV19AttemptConflict)
	}
	if got := canonicalV19AttemptWriterCount(t, fixture.Home); got != 1 {
		t.Fatalf("Attempt rows after duplicate identity = %d, want 1", got)
	}
}

func TestCreateCanonicalV19AttemptRefusesStaleRetryPredecessorWithoutRow(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	canonicalV19AttemptWriterTerminalize(t, fixture.Home, "attempt-1", "failed", "2026-09-04T09:01:00Z")
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-fresh", "plan-root")); !errors.Is(err, ErrCanonicalV19AttemptNotCurrent) {
		t.Fatalf("create after Attempt history error = %v, want %v", err, ErrCanonicalV19AttemptNotCurrent)
	}
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptRetryInput("attempt-2", "plan-root", "attempt-1")); err != nil {
		t.Fatal(err)
	}
	for _, successor := range []struct {
		lifecycle string
		want      error
	}{{"active", ErrCanonicalV19AttemptConflict}, {"failed", ErrCanonicalV19AttemptNotCurrent}} {
		if successor.lifecycle != "active" {
			canonicalV19AttemptWriterTerminalize(t, fixture.Home, "attempt-2", successor.lifecycle, "2026-09-04T09:03:00Z")
		}
		_, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptRetryInput("attempt-3", "plan-root", "attempt-1"))
		if !errors.Is(err, successor.want) {
			t.Fatalf("stale retry with %s successor error = %v, want %v", successor.lifecycle, err, successor.want)
		}
		if got := canonicalV19AttemptWriterCount(t, fixture.Home); got != 2 {
			t.Fatalf("Attempt rows after stale retry with %s successor = %d, want 2", successor.lifecycle, got)
		}
	}
}

func TestCreateCanonicalV19AttemptRefusesStalePlanWithoutRetarget(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	successor := canonicalV19PlanWriterInput("plan-replan")
	successor.CreatedAt = "2026-09-04T09:03:00Z"
	if _, err := ReplanCanonicalV19Plan(context.Background(), fixture.Home, CanonicalV19PlanReplanInput{
		PredecessorPlanID: "plan-root",
		Successor:         successor,
		SupersededAt:      "2026-09-04T09:02:59Z",
	}); err != nil {
		t.Fatal(err)
	}

	_, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-stale", "plan-root"))
	if !errors.Is(err, ErrCanonicalV19AttemptNotCurrent) {
		t.Fatalf("stale Plan Attempt error = %v, want %v", err, ErrCanonicalV19AttemptNotCurrent)
	}
	if got := canonicalV19AttemptWriterCount(t, fixture.Home); got != 0 {
		t.Fatalf("Attempt rows after stale Plan refusal = %d, want 0", got)
	}
}

func TestReplanCanonicalV19PlanRefusesActiveAttempt(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	successor := canonicalV19PlanWriterInput("plan-replan")
	successor.CreatedAt = "2026-09-04T09:03:00Z"
	_, err := ReplanCanonicalV19Plan(context.Background(), fixture.Home, CanonicalV19PlanReplanInput{
		PredecessorPlanID: "plan-root",
		Successor:         successor,
		SupersededAt:      "2026-09-04T09:02:59Z",
	})
	if !errors.Is(err, ErrCanonicalV19PlanNotCurrent) {
		t.Fatalf("replan with active Attempt error = %v, want %v", err, ErrCanonicalV19PlanNotCurrent)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var lifecycle, terminalAt string
	if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM plan WHERE id='plan-root'`).Scan(&lifecycle, &terminalAt); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "active" || terminalAt != "" {
		t.Fatalf("predecessor changed despite active Attempt: lifecycle=%q terminal_at=%q", lifecycle, terminalAt)
	}
	var successorCount int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM plan WHERE id=?`, successor.ID).Scan(&successorCount); err != nil {
		t.Fatal(err)
	}
	if successorCount != 0 {
		t.Fatalf("successor Plan rows = %d, want 0", successorCount)
	}
}

func TestCreateCanonicalV19AttemptConcurrentRetriesHaveOneWinner(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	canonicalV19AttemptWriterTerminalize(t, fixture.Home, "attempt-1", "failed", "2026-09-04T09:01:00Z")
	retry := func(id string) func() error {
		return func() error {
			_, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptRetryInput(id, "plan-root", "attempt-1"))
			return err
		}
	}
	wins := 0
	for _, err := range canonicalV19RaceWriters(retry("attempt-2"), retry("attempt-3")) {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrCanonicalV19AttemptConflict) {
			t.Fatalf("concurrent retry = %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent retry winners = %d, want 1", wins)
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM attempt`, 2)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM attempt WHERE ordinal=2 AND lifecycle='active'`, 1)
}

func TestCanonicalV19RetryAndReplanRaceHasOneWinnerWithoutRetarget(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	canonicalV19AttemptWriterTerminalize(t, fixture.Home, "attempt-1", "failed", "2026-09-04T09:01:00Z")
	successor := canonicalV19PlanWriterInput("plan-replan")
	successor.CreatedAt = "2026-09-04T09:03:00Z"
	errs := canonicalV19RaceWriters(
		func() error {
			_, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptRetryInput("attempt-2", "plan-root", "attempt-1"))
			return err
		},
		func() error {
			_, err := ReplanCanonicalV19Plan(context.Background(), fixture.Home, CanonicalV19PlanReplanInput{
				PredecessorPlanID: "plan-root", Successor: successor, SupersededAt: "2026-09-04T09:02:59Z",
			})
			return err
		},
	)
	retryErr, replanErr := errs[0], errs[1]
	switch {
	case retryErr == nil && errors.Is(replanErr, ErrCanonicalV19PlanNotCurrent):
		canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM plan`, 1)
		canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM plan WHERE id='plan-root' AND lifecycle='active'`, 1)
		canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM attempt WHERE id='attempt-2' AND plan_id='plan-root' AND lifecycle='active'`, 1)
	case replanErr == nil && errors.Is(retryErr, ErrCanonicalV19AttemptNotCurrent):
		canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM plan WHERE id='plan-root' AND lifecycle='superseded'`, 1)
		canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM plan WHERE id='plan-replan' AND lifecycle='active'`, 1)
		canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM attempt`, 1)
	default:
		t.Fatalf("retry/replan race = %v / %v, want exactly one winner", retryErr, replanErr)
	}
}

func canonicalV19RaceWriters(first, second func() error) [2]error {
	start := make(chan struct{})
	results := [2]chan error{make(chan error, 1), make(chan error, 1)}
	for i, writer := range []func() error{first, second} {
		go func() {
			<-start
			results[i] <- writer()
		}()
	}
	close(start)
	return [2]error{<-results[0], <-results[1]}
}

type canonicalV19AttemptWriterTestFixture struct {
	Home string
}

func canonicalV19AttemptWriterFixture(t *testing.T) canonicalV19AttemptWriterTestFixture {
	t.Helper()
	fixture := canonicalV19PlanWriterFixture(t, "")
	if _, err := CreateCanonicalV19RootPlan(context.Background(), fixture.Home, canonicalV19PlanWriterInput("plan-root")); err != nil {
		t.Fatal(err)
	}
	return canonicalV19AttemptWriterTestFixture{Home: fixture.Home}
}

func canonicalV19AttemptWriterInput(id, planID string) CanonicalV19AttemptCreateInput {
	return CanonicalV19AttemptCreateInput{
		ID:                   id,
		PlanID:               planID,
		WorkerHarnessRef:     "worker-harness/codex",
		WorkerHarnessVersion: "1.0.0",
		WorkerProfileRef:     "profile/default",
		ModelRef:             "model/example",
		EffortRef:            "medium",
		SessionAdapterRef:    "builtin/session",
		CreatedAt:            "2026-09-04T09:00:00Z",
	}
}

func canonicalV19AttemptRetryInput(id, planID, predecessorID string) CanonicalV19AttemptCreateInput {
	input := canonicalV19AttemptWriterInput(id, planID)
	input.PredecessorAttemptID = predecessorID
	return input
}

func canonicalV19AttemptWriterTerminalize(t *testing.T, home, attemptID, lifecycle, terminalAt string) {
	t.Helper()
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE attempt SET lifecycle=?,terminal_at=? WHERE id=?`, lifecycle, terminalAt, attemptID); err != nil {
		t.Fatal(err)
	}
}

func canonicalV19AttemptWriterCount(t *testing.T, home string) int {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM attempt`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
