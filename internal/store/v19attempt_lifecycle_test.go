package store

import (
	"context"
	"errors"
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
