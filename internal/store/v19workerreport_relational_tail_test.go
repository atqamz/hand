package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestIngestCanonicalV19WorkerReportRefusesCheckpointRollbackFork(t *testing.T) {
	for _, test := range []struct {
		name  string
		delta int
	}{
		{name: "before canonical tail", delta: -1},
		{name: "at canonical tail", delta: 0},
		{name: "after canonical tail", delta: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
			secondRecord := "working: second\n"
			witnesses := canonicalV19WorkerReportWitnessChain(t, attemptID, "",
				[]string{"working: first\n", secondRecord})
			reports, err := ReplayCanonicalV19WorkerReports(ctx, fixture.Home, attemptID, witnesses)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeCanonicalV19WorkerReportCheckpointTail(fixture.Home, reports[0]); err != nil {
				t.Fatal(err)
			}
			record := "failed: " + strings.Repeat("x", len(secondRecord)-len("failed: \n")+test.delta) + "\n"
			fork := canonicalV19WorkerReportWitness(t, attemptID, "", record,
				"2026-09-21T04:00:00Z", &reports[0])
			if _, err := IngestCanonicalV19WorkerReport(ctx, fixture.Home, fork); !errors.Is(err, ErrCanonicalV19WorkerReportConflict) {
				t.Fatalf("rollback fork error = %v, want %v", err, ErrCanonicalV19WorkerReportConflict)
			}
			if count := canonicalV19WorkerReportCount(t, fixture.Home, attemptID); count != 2 {
				t.Fatalf("WorkerReport rows after refused rollback fork = %d, want 2", count)
			}
			checkpoint, found, err := readCanonicalV19WorkerReportCheckpoint(fixture.Home, attemptID)
			if err != nil || !found || checkpoint.WorkerReportID != reports[0].ID ||
				checkpoint.SourcePrefixDigest != reports[0].SourcePrefixDigest ||
				checkpoint.SourceEndOffset != reports[0].SourceEndOffset {
				t.Fatalf("refused fork mutated checkpoint: %#v, found=%v, err=%v", checkpoint, found, err)
			}

			replayed, err := ReplayCanonicalV19WorkerReports(ctx, fixture.Home, attemptID, witnesses)
			if err != nil {
				t.Fatal(err)
			}
			if len(replayed) != len(reports) {
				t.Fatalf("repaired history length = %d, want %d", len(replayed), len(reports))
			}
			for i := range reports {
				if replayed[i] != reports[i] {
					t.Fatalf("repair changed immutable report %d: %#v, want %#v", i, replayed[i], reports[i])
				}
			}
			next := canonicalV19WorkerReportWitness(t, attemptID, "", "done: third\n",
				"2026-09-21T04:01:00Z", &reports[1])
			if _, err := IngestCanonicalV19WorkerReport(ctx, fixture.Home, next); err != nil {
				t.Fatalf("append after full replay repair: %v", err)
			}
			if count := canonicalV19WorkerReportCount(t, fixture.Home, attemptID); count != 3 {
				t.Fatalf("WorkerReport rows after repaired append = %d, want 3", count)
			}
		})
	}
}

func TestIngestCanonicalV19WorkerReportHistoricalReplayPreservesTail(t *testing.T) {
	ctx := context.Background()
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	witnesses := canonicalV19WorkerReportWitnessChain(t, attemptID, "",
		[]string{"working: first\n", "working: second\n"})
	reports, err := ReplayCanonicalV19WorkerReports(ctx, fixture.Home, attemptID, witnesses)
	if err != nil {
		t.Fatal(err)
	}
	witnesses[0].CreatedAt = "2026-09-21T04:02:00Z"
	canonicalV19WorkerReportRefreshAttestation(&witnesses[0])
	replayed, err := IngestCanonicalV19WorkerReport(ctx, fixture.Home, witnesses[0])
	if err != nil || replayed != reports[0] {
		t.Fatalf("historical replay = %#v, err=%v, want %#v", replayed, err, reports[0])
	}
	checkpoint, found, err := readCanonicalV19WorkerReportCheckpoint(fixture.Home, attemptID)
	if err != nil || !found || checkpoint.WorkerReportID != reports[1].ID ||
		checkpoint.SourcePrefixDigest != reports[1].SourcePrefixDigest ||
		checkpoint.SourceEndOffset != reports[1].SourceEndOffset {
		t.Fatalf("historical replay changed tail: %#v, found=%v, err=%v", checkpoint, found, err)
	}
	if count := canonicalV19WorkerReportCount(t, fixture.Home, attemptID); count != 2 {
		t.Fatalf("WorkerReport rows after historical replay = %d, want 2", count)
	}
}
