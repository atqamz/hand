package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTerminalizeCanonicalV19AttemptAcceptsExactTerminalStates(t *testing.T) {
	for _, lifecycle := range []string{"completed", "failed", "interrupted"} {
		t.Run(lifecycle, func(t *testing.T) {
			fixture := canonicalV19AttemptWriterFixture(t)
			attempt := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
			if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, attempt); err != nil {
				t.Fatal(err)
			}
			terminalAt := "2026-09-04T10:00:00Z"
			if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
				AttemptID:  attempt.ID,
				Lifecycle:  lifecycle,
				TerminalAt: terminalAt,
			}); err != nil {
				t.Fatal(err)
			}

			db, err := openReadOnly(fixture.Home)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			var gotLifecycle, gotTerminalAt string
			if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM attempt WHERE id=?`, attempt.ID).
				Scan(&gotLifecycle, &gotTerminalAt); err != nil {
				t.Fatal(err)
			}
			if gotLifecycle != lifecycle || gotTerminalAt != terminalAt {
				t.Fatalf("terminal Attempt = lifecycle %q terminal_at %q, want %q/%q", gotLifecycle, gotTerminalAt, lifecycle, terminalAt)
			}
		})
	}
}

func TestTerminalizeCanonicalV19AttemptRejectsNonTerminalStateWithoutMutation(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	attempt := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, attempt); err != nil {
		t.Fatal(err)
	}
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID:  attempt.ID,
		Lifecycle:  "active",
		TerminalAt: "2026-09-04T10:00:00Z",
	}); err == nil {
		t.Fatal("non-terminal lifecycle unexpectedly accepted")
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var lifecycle, terminalAt string
	if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM attempt WHERE id=?`, attempt.ID).
		Scan(&lifecycle, &terminalAt); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "active" || terminalAt != "" {
		t.Fatalf("Attempt mutated after invalid transition: lifecycle=%q terminal_at=%q", lifecycle, terminalAt)
	}
}

func TestTerminalizeCanonicalV19AttemptRefusesReplayWithoutReopen(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	attempt := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, attempt); err != nil {
		t.Fatal(err)
	}
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID:  attempt.ID,
		Lifecycle:  "failed",
		TerminalAt: "2026-09-04T10:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID:  attempt.ID,
		Lifecycle:  "completed",
		TerminalAt: "2026-09-04T10:02:00Z",
	}); !errors.Is(err, ErrCanonicalV19AttemptNotCurrent) {
		t.Fatalf("replayed terminalization error = %v, want %v", err, ErrCanonicalV19AttemptNotCurrent)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var lifecycle, terminalAt string
	if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM attempt WHERE id=?`, attempt.ID).
		Scan(&lifecycle, &terminalAt); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "failed" || terminalAt != "2026-09-04T10:01:00Z" {
		t.Fatalf("terminal Attempt reopened or changed: lifecycle=%q terminal_at=%q", lifecycle, terminalAt)
	}
}

func TestTerminalizeCanonicalV19AttemptEnablesFreshRetry(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	first := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID:  first.ID,
		Lifecycle:  "failed",
		TerminalAt: "2026-09-04T10:03:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	second := canonicalV19AttemptWriterInput("attempt-2", "plan-root")
	second.CreatedAt = "2026-09-04T10:04:00Z"
	ordinal, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, second)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 2 {
		t.Fatalf("retry Attempt ordinal = %d, want 2", ordinal)
	}
}

func TestTerminalizeCanonicalV19AttemptRefusesMissingExactIdentity(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID:  "attempt-missing",
		Lifecycle:  "failed",
		TerminalAt: "2026-09-04T10:05:00Z",
	}); !errors.Is(err, ErrCanonicalV19AttemptNotCurrent) {
		t.Fatalf("missing Attempt error = %v, want %v", err, ErrCanonicalV19AttemptNotCurrent)
	}
}

func TestTerminalizeCanonicalV19AttemptRefusesOpenExecutorOrRepair(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		seed       func(*testing.T) string
	}{
		{"OpenExecutorBinding", "open ExecutorBinding", func(t *testing.T) string {
			fixture, _, _ := canonicalV19ExecutorBindingFixture(t)
			return fixture.Home
		}},
		{"OpenAttemptRepair", "open Repair", func(t *testing.T) string {
			home := canonicalV19AttemptWriterFixture(t).Home
			if _, err := CreateCanonicalV19Attempt(context.Background(), home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
				t.Fatal(err)
			}
			canonicalV19RepairWriterExec(t, home, `BEGIN IMMEDIATE;
				INSERT INTO repair_target(repair_id,attempt_id) VALUES('repair-1','attempt-1');
				INSERT INTO repair(id,repair_code,reason,evidence_digest,created_at)
				VALUES('repair-1','attempt-check','inspect','digest','2026-09-04T09:00:30Z');
				COMMIT`)
			return home
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := tc.seed(t)
			err := TerminalizeCanonicalV19Attempt(context.Background(), home, CanonicalV19AttemptTerminalizeInput{
				AttemptID: "attempt-1", Lifecycle: "failed", TerminalAt: "2026-09-07T00:00:00Z",
			})
			if !errors.Is(err, ErrCanonicalV19AttemptConflict) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("terminalize with %s = %v, want %v naming %q", tc.name, err, ErrCanonicalV19AttemptConflict, tc.want)
			}
			canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM attempt WHERE id='attempt-1' AND lifecycle='active'`, 1)
		})
	}
}
