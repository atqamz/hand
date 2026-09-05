package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateCanonicalV19AttemptBackoffPersistsExactEvidenceAndAllocatesOrdinal(t *testing.T) {
	fixture := canonicalV19AttemptBackoffWriterFixture(t)
	first := canonicalV19AttemptBackoffWriterInput("backoff-1", "attempt-1")
	ordinal, err := CreateCanonicalV19AttemptBackoff(context.Background(), fixture.Home, first)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 {
		t.Fatalf("Backoff ordinal = %d, want 1", ordinal)
	}

	if err := ResolveCanonicalV19AttemptBackoff(context.Background(), fixture.Home,
		CanonicalV19AttemptBackoffResolveInput{
			BackoffID:      first.ID,
			Resolution:     "resumed",
			ResolvedAt:     "2026-09-05T02:01:00Z",
			EvidenceDigest: "digest-resolution-1",
		}); err != nil {
		t.Fatal(err)
	}

	second := canonicalV19AttemptBackoffWriterInput("backoff-2", "attempt-1")
	second.Reason = "provider-transient"
	second.NotBefore = "2026-09-05T02:10:00Z"
	second.EvidenceDigest = "digest-backoff-2"
	second.CreatedAt = "2026-09-05T02:02:00Z"
	ordinal, err = CreateCanonicalV19AttemptBackoff(context.Background(), fixture.Home, second)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 2 {
		t.Fatalf("second Backoff ordinal = %d, want 2", ordinal)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var got CanonicalV19AttemptBackoffCreateInput
	var gotOrdinal int64
	if err := db.sql.QueryRow(`SELECT id,attempt_id,ordinal,reason,not_before,evidence_digest,created_at
		FROM attempt_backoff WHERE id=?`, second.ID).Scan(
		&got.ID, &got.AttemptID, &gotOrdinal, &got.Reason, &got.NotBefore,
		&got.EvidenceDigest, &got.CreatedAt,
	); err != nil {
		t.Fatal(err)
	}
	if got != second || gotOrdinal != 2 {
		t.Fatalf("persisted Backoff = %#v ordinal=%d, want %#v ordinal=2", got, gotOrdinal, second)
	}

	var resolution, resolvedAt, evidenceDigest string
	if err := db.sql.QueryRow(`SELECT resolution,resolved_at,evidence_digest
		FROM attempt_backoff_resolution WHERE backoff_id=?`, first.ID).Scan(
		&resolution, &resolvedAt, &evidenceDigest,
	); err != nil {
		t.Fatal(err)
	}
	if resolution != "resumed" || resolvedAt != "2026-09-05T02:01:00Z" || evidenceDigest != "digest-resolution-1" {
		t.Fatalf("Backoff resolution = %q/%q/%q", resolution, resolvedAt, evidenceDigest)
	}
}

func TestCreateCanonicalV19AttemptBackoffRefusesSecondUnresolvedEpisode(t *testing.T) {
	fixture := canonicalV19AttemptBackoffWriterFixture(t)
	if _, err := CreateCanonicalV19AttemptBackoff(context.Background(), fixture.Home,
		canonicalV19AttemptBackoffWriterInput("backoff-1", "attempt-1")); err != nil {
		t.Fatal(err)
	}
	_, err := CreateCanonicalV19AttemptBackoff(context.Background(), fixture.Home,
		canonicalV19AttemptBackoffWriterInput("backoff-2", "attempt-1"))
	if !errors.Is(err, ErrCanonicalV19AttemptBackoffConflict) {
		t.Fatalf("second unresolved Backoff error = %v, want %v", err, ErrCanonicalV19AttemptBackoffConflict)
	}
	if got := canonicalV19AttemptBackoffWriterCount(t, fixture.Home); got != 1 {
		t.Fatalf("Backoff rows after unresolved conflict = %d, want 1", got)
	}
}

func TestCreateCanonicalV19AttemptBackoffRefusesStaleAttemptWithoutRetarget(t *testing.T) {
	fixture := canonicalV19AttemptBackoffWriterFixture(t)
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home,
		CanonicalV19AttemptTerminalizeInput{
			AttemptID:  "attempt-1",
			Lifecycle:  "failed",
			TerminalAt: "2026-09-05T02:03:00Z",
		}); err != nil {
		t.Fatal(err)
	}

	_, err := CreateCanonicalV19AttemptBackoff(context.Background(), fixture.Home,
		canonicalV19AttemptBackoffWriterInput("backoff-stale", "attempt-1"))
	if !errors.Is(err, ErrCanonicalV19AttemptBackoffNotCurrent) {
		t.Fatalf("stale Attempt Backoff error = %v, want %v", err, ErrCanonicalV19AttemptBackoffNotCurrent)
	}
	if got := canonicalV19AttemptBackoffWriterCount(t, fixture.Home); got != 0 {
		t.Fatalf("Backoff rows after stale Attempt refusal = %d, want 0", got)
	}
}

func TestResolveCanonicalV19AttemptBackoffRefusesReplay(t *testing.T) {
	fixture := canonicalV19AttemptBackoffWriterFixture(t)
	input := canonicalV19AttemptBackoffWriterInput("backoff-1", "attempt-1")
	if _, err := CreateCanonicalV19AttemptBackoff(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
	first := CanonicalV19AttemptBackoffResolveInput{
		BackoffID:      input.ID,
		Resolution:     "cancelled",
		ResolvedAt:     "2026-09-05T02:04:00Z",
		EvidenceDigest: "digest-resolution-first",
	}
	if err := ResolveCanonicalV19AttemptBackoff(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Resolution = "resumed"
	second.ResolvedAt = "2026-09-05T02:05:00Z"
	second.EvidenceDigest = "digest-resolution-second"
	if err := ResolveCanonicalV19AttemptBackoff(context.Background(), fixture.Home, second); !errors.Is(err, ErrCanonicalV19AttemptBackoffNotCurrent) {
		t.Fatalf("replayed Backoff resolution error = %v, want %v", err, ErrCanonicalV19AttemptBackoffNotCurrent)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var resolution, resolvedAt, evidenceDigest string
	if err := db.sql.QueryRow(`SELECT resolution,resolved_at,evidence_digest
		FROM attempt_backoff_resolution WHERE backoff_id=?`, input.ID).Scan(
		&resolution, &resolvedAt, &evidenceDigest,
	); err != nil {
		t.Fatal(err)
	}
	if resolution != first.Resolution || resolvedAt != first.ResolvedAt || evidenceDigest != first.EvidenceDigest {
		t.Fatalf("replayed resolution mutated row = %q/%q/%q", resolution, resolvedAt, evidenceDigest)
	}
}

func TestTerminalizeCanonicalV19AttemptRequiresBackoffResolution(t *testing.T) {
	fixture := canonicalV19AttemptBackoffWriterFixture(t)
	backoff := canonicalV19AttemptBackoffWriterInput("backoff-1", "attempt-1")
	if _, err := CreateCanonicalV19AttemptBackoff(context.Background(), fixture.Home, backoff); err != nil {
		t.Fatal(err)
	}
	terminal := CanonicalV19AttemptTerminalizeInput{
		AttemptID:  "attempt-1",
		Lifecycle:  "failed",
		TerminalAt: "2026-09-05T02:06:00Z",
	}
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, terminal); !errors.Is(err, ErrCanonicalV19AttemptConflict) {
		t.Fatalf("terminalize with unresolved Backoff error = %v, want %v", err, ErrCanonicalV19AttemptConflict)
	}

	if err := ResolveCanonicalV19AttemptBackoff(context.Background(), fixture.Home,
		CanonicalV19AttemptBackoffResolveInput{
			BackoffID:      backoff.ID,
			Resolution:     "superseded",
			ResolvedAt:     "2026-09-05T02:05:59Z",
			EvidenceDigest: "digest-resolution-terminal",
		}); err != nil {
		t.Fatal(err)
	}
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, terminal); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalV19AttemptBackoffWritersRejectInvalidEnumsWithoutMutation(t *testing.T) {
	fixture := canonicalV19AttemptBackoffWriterFixture(t)
	invalid := canonicalV19AttemptBackoffWriterInput("backoff-invalid", "attempt-1")
	invalid.Reason = "unknown"
	if _, err := CreateCanonicalV19AttemptBackoff(context.Background(), fixture.Home, invalid); err == nil {
		t.Fatal("invalid Backoff reason unexpectedly accepted")
	}
	if got := canonicalV19AttemptBackoffWriterCount(t, fixture.Home); got != 0 {
		t.Fatalf("Backoff rows after invalid reason = %d, want 0", got)
	}

	valid := canonicalV19AttemptBackoffWriterInput("backoff-1", "attempt-1")
	if _, err := CreateCanonicalV19AttemptBackoff(context.Background(), fixture.Home, valid); err != nil {
		t.Fatal(err)
	}
	if err := ResolveCanonicalV19AttemptBackoff(context.Background(), fixture.Home,
		CanonicalV19AttemptBackoffResolveInput{
			BackoffID:      valid.ID,
			Resolution:     "unknown",
			ResolvedAt:     "2026-09-05T02:07:00Z",
			EvidenceDigest: "digest-invalid-resolution",
		}); err == nil {
		t.Fatal("invalid Backoff resolution unexpectedly accepted")
	}
	if got := canonicalV19AttemptBackoffResolutionCount(t, fixture.Home); got != 0 {
		t.Fatalf("Backoff resolution rows after invalid enum = %d, want 0", got)
	}
}

type canonicalV19AttemptBackoffWriterTestFixture struct {
	Home string
}

func canonicalV19AttemptBackoffWriterFixture(t *testing.T) canonicalV19AttemptBackoffWriterTestFixture {
	t.Helper()
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home,
		canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	return canonicalV19AttemptBackoffWriterTestFixture{Home: fixture.Home}
}

func canonicalV19AttemptBackoffWriterInput(id, attemptID string) CanonicalV19AttemptBackoffCreateInput {
	return CanonicalV19AttemptBackoffCreateInput{
		ID:             id,
		AttemptID:      attemptID,
		Reason:         "rate-limit",
		NotBefore:      "2026-09-05T02:08:00Z",
		EvidenceDigest: "digest-backoff-1",
		CreatedAt:      "2026-09-05T02:00:00Z",
	}
}

func canonicalV19AttemptBackoffWriterCount(t *testing.T, home string) int {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM attempt_backoff`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func canonicalV19AttemptBackoffResolutionCount(t *testing.T, home string) int {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM attempt_backoff_resolution`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
