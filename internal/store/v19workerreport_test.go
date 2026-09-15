package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
)

func TestIngestCanonicalV19WorkerReportPersistsInitialExactWitness(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	witness := canonicalV19WorkerReportWitness(t, attemptID, "", "working: started\n", "2026-09-15T09:00:00Z", nil)

	created, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "worker-report-a0bb720b482c22fc6c5f39c87c95eeaa05213e5064db6aaa7f812f1d9dedf82b" ||
		created.AttemptID != attemptID || created.ExecutorBindingID != "" ||
		created.SourcePrefixDigest != "a08cdaa2b00afba9c0a8bad16fff47f67e35c2b3ab773e3483509b446106b5dd" ||
		created.SourceEndOffset != 17 || created.ReportState != "working" || created.Note != "started" ||
		created.CreatedAt != witness.CreatedAt {
		t.Fatalf("WorkerReport = %#v", created)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var persisted CanonicalV19WorkerReport
	var executorBindingID sql.NullString
	if err := db.sql.QueryRow(`SELECT id,attempt_id,executor_binding_id,source_prefix_digest,
		source_end_offset,report_state,note,created_at FROM worker_report WHERE id=?`, created.ID).Scan(
		&persisted.ID, &persisted.AttemptID, &executorBindingID, &persisted.SourcePrefixDigest,
		&persisted.SourceEndOffset, &persisted.ReportState, &persisted.Note, &persisted.CreatedAt,
	); err != nil {
		t.Fatal(err)
	}
	if executorBindingID.Valid || persisted != created {
		t.Fatalf("persisted WorkerReport = %#v executor=%#v, want %#v with NULL executor", persisted, executorBindingID, created)
	}
	var acknowledgements int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM worker_report_acknowledgement`).Scan(&acknowledgements); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 0 {
		t.Fatalf("implicit WorkerReport acknowledgements = %d, want 0", acknowledgements)
	}
}

func TestIngestCanonicalV19WorkerReportExactReplayConverges(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	witness := canonicalV19WorkerReportWitness(t, attemptID, "", "paused: waiting\n", "2026-09-15T09:01:00Z", nil)
	first, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness)
	if err != nil {
		t.Fatal(err)
	}
	second, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("replayed WorkerReport = %#v, want %#v", second, first)
	}
	if count := canonicalV19WorkerReportCount(t, fixture.Home, attemptID); count != 1 {
		t.Fatalf("WorkerReport rows after replay = %d, want 1", count)
	}
}

func TestIngestCanonicalV19WorkerReportFreshObservationPreservesCreatedAt(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	witness := canonicalV19WorkerReportWitness(t, launch.AttemptID, launch.BindingID,
		"working: observed\n", "2026-09-15T09:01:00Z", nil)
	first, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness)
	if err != nil {
		t.Fatal(err)
	}
	witness.CreatedAt = "2026-09-15T09:02:00Z"
	canonicalV19WorkerReportRefreshAttestation(&witness)
	replayed, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != first {
		t.Fatalf("freshly observed WorkerReport = %#v, want persisted %#v", replayed, first)
	}

	witness.ExecutorBindingID = ""
	canonicalV19WorkerReportRefreshAttestation(&witness)
	if _, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness); !errors.Is(err, ErrCanonicalV19WorkerReportConflict) {
		t.Fatalf("changed binding replay error = %v, want %v", err, ErrCanonicalV19WorkerReportConflict)
	}
}

func TestIngestCanonicalV19WorkerReportRefusesUnprovenAppendWitness(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	witness := canonicalV19WorkerReportWitness(t, attemptID, "", "working: unattested\n", "2026-09-15T09:01:30Z", nil)
	witness.sourceAttestation = nil
	if _, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness); !errors.Is(err, ErrCanonicalV19WorkerReportWitnessUnproven) {
		t.Fatalf("unproven append witness error = %v, want %v", err, ErrCanonicalV19WorkerReportWitnessUnproven)
	}
	if count := canonicalV19WorkerReportCount(t, fixture.Home, attemptID); count != 0 {
		t.Fatalf("WorkerReport rows after unproven witness = %d, want 0", count)
	}
}

func TestIngestCanonicalV19WorkerReportAppendsExactVocabularyRecords(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	states := []string{"working", "paused", "blocked", "needs-decision", "done", "failed"}
	var predecessor *CanonicalV19WorkerReport
	for i, state := range states {
		witness := canonicalV19WorkerReportWitness(t, launch.AttemptID, launch.BindingID,
			fmt.Sprintf("%s: note %d\n", state, i), fmt.Sprintf("2026-09-15T09:%02d:00Z", i+2), predecessor)
		created, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness)
		if err != nil {
			t.Fatal(err)
		}
		if created.ReportState != state || created.ExecutorBindingID != launch.BindingID {
			t.Fatalf("WorkerReport %d = %#v", i, created)
		}
		predecessor = &created
	}
	if count := canonicalV19WorkerReportCount(t, fixture.Home, launch.AttemptID); count != len(states) {
		t.Fatalf("WorkerReport rows = %d, want %d", count, len(states))
	}
}

func TestIngestCanonicalV19WorkerReportLeavesPartialRecordUningested(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	witness := canonicalV19WorkerReportWitness(t, attemptID, "", "working: partial", "2026-09-15T09:08:00Z", nil)
	if _, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness); !errors.Is(err, ErrCanonicalV19WorkerReportIncomplete) {
		t.Fatalf("partial WorkerReport error = %v, want %v", err, ErrCanonicalV19WorkerReportIncomplete)
	}
	if count := canonicalV19WorkerReportCount(t, fixture.Home, attemptID); count != 0 {
		t.Fatalf("WorkerReport rows after partial record = %d, want 0", count)
	}
}

func TestIngestCanonicalV19WorkerReportRejectsMalformedOrCombinedRecord(t *testing.T) {
	for name, record := range map[string]string{
		"unknown state":    "thinking: maybe\n",
		"missing colon":    "working without colon\n",
		"multiple records": "working: one\ndone: two\n",
	} {
		t.Run(name, func(t *testing.T) {
			fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
			witness := canonicalV19WorkerReportWitness(t, attemptID, "", record, "2026-09-15T09:09:00Z", nil)
			if _, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness); !errors.Is(err, ErrCanonicalV19WorkerReportConflict) {
				t.Fatalf("invalid WorkerReport error = %v, want %v", err, ErrCanonicalV19WorkerReportConflict)
			}
			if count := canonicalV19WorkerReportCount(t, fixture.Home, attemptID); count != 0 {
				t.Fatalf("WorkerReport rows after invalid record = %d, want 0", count)
			}
		})
	}
}

func TestIngestCanonicalV19WorkerReportRejectsContradictoryPredecessor(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	first, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home,
		canonicalV19WorkerReportWitness(t, attemptID, "", "working: first\n", "2026-09-15T09:10:00Z", nil))
	if err != nil {
		t.Fatal(err)
	}
	witness := canonicalV19WorkerReportWitness(t, attemptID, "", "done: second\n", "2026-09-15T09:11:00Z", &first)
	witness.Predecessor.SourcePrefixDigest = "changed-historical-prefix"
	canonicalV19WorkerReportRefreshAttestation(&witness)
	if _, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness); !errors.Is(err, ErrCanonicalV19WorkerReportConflict) {
		t.Fatalf("contradictory predecessor error = %v, want %v", err, ErrCanonicalV19WorkerReportConflict)
	}
	if count := canonicalV19WorkerReportCount(t, fixture.Home, attemptID); count != 1 {
		t.Fatalf("WorkerReport rows after predecessor contradiction = %d, want 1", count)
	}
}

func TestIngestCanonicalV19WorkerReportRejectsValidNonTailPredecessor(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	first, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home,
		canonicalV19WorkerReportWitness(t, attemptID, "", "working: first\n", "2026-09-15T09:11:00Z", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home,
		canonicalV19WorkerReportWitness(t, attemptID, "", "working: second\n", "2026-09-15T09:12:00Z", &first)); err != nil {
		t.Fatal(err)
	}
	fork := canonicalV19WorkerReportWitness(t, attemptID, "", "failed: fork\n", "2026-09-15T09:13:00Z", &first)
	if _, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, fork); !errors.Is(err, ErrCanonicalV19WorkerReportConflict) {
		t.Fatalf("non-tail predecessor error = %v, want %v", err, ErrCanonicalV19WorkerReportConflict)
	}
	if count := canonicalV19WorkerReportCount(t, fixture.Home, attemptID); count != 2 {
		t.Fatalf("WorkerReport rows after rejected fork = %d, want 2", count)
	}
}

func TestIngestCanonicalV19WorkerReportRejectsTruncatedBoundary(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	first, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home,
		canonicalV19WorkerReportWitness(t, attemptID, "", "working: first\n", "2026-09-15T09:12:00Z", nil))
	if err != nil {
		t.Fatal(err)
	}
	witness := canonicalV19WorkerReportWitness(t, attemptID, "", "done: second\n", "2026-09-15T09:13:00Z", &first)
	witness.SourceEndOffset--
	canonicalV19WorkerReportRefreshAttestation(&witness)
	if _, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness); !errors.Is(err, ErrCanonicalV19WorkerReportConflict) {
		t.Fatalf("truncated boundary error = %v, want %v", err, ErrCanonicalV19WorkerReportConflict)
	}
}

func TestReplayCanonicalV19WorkerReportsRecoversAfterCheckpointLoss(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	witnesses := canonicalV19WorkerReportWitnessChain(t, attemptID, "",
		[]string{"working: first\n", "blocked: second\n", "done: third\n"})
	first, err := ReplayCanonicalV19WorkerReports(context.Background(), fixture.Home, attemptID, witnesses)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayCanonicalV19WorkerReports(context.Background(), fixture.Home, attemptID, witnesses)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != len(first) {
		t.Fatalf("replayed WorkerReports = %d, want %d", len(second), len(first))
	}
	for i := range first {
		if second[i] != first[i] {
			t.Fatalf("replayed WorkerReport %d = %#v, want %#v", i, second[i], first[i])
		}
	}
	if count := canonicalV19WorkerReportCount(t, fixture.Home, attemptID); count != len(witnesses) {
		t.Fatalf("WorkerReport rows after full replay = %d, want %d", count, len(witnesses))
	}
}

func TestReplayCanonicalV19WorkerReportsFreshObservationsPreserveCreatedAt(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	witnesses := canonicalV19WorkerReportWitnessChain(t, attemptID, "",
		[]string{"working: first\n", "done: second\n"})
	first, err := ReplayCanonicalV19WorkerReports(context.Background(), fixture.Home, attemptID, witnesses)
	if err != nil {
		t.Fatal(err)
	}
	for i := range witnesses {
		witnesses[i].CreatedAt = fmt.Sprintf("2026-09-16T12:%02d:00Z", i)
		canonicalV19WorkerReportRefreshAttestation(&witnesses[i])
	}
	replayed, err := ReplayCanonicalV19WorkerReports(context.Background(), fixture.Home, attemptID, witnesses)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if replayed[i] != first[i] {
			t.Fatalf("freshly observed WorkerReport %d = %#v, want persisted %#v", i, replayed[i], first[i])
		}
	}
}

func TestReplayCanonicalV19WorkerReportsRejectsTruncationAndHistoricalRewrite(t *testing.T) {
	for name, replacement := range map[string][]string{
		"truncation": {"working: first\n"},
		"rewrite":    {"working: first\n", "failed: changed history\n"},
	} {
		t.Run(name, func(t *testing.T) {
			fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
			original := canonicalV19WorkerReportWitnessChain(t, attemptID, "",
				[]string{"working: first\n", "done: second\n"})
			if _, err := ReplayCanonicalV19WorkerReports(context.Background(), fixture.Home, attemptID, original); err != nil {
				t.Fatal(err)
			}
			changed := canonicalV19WorkerReportWitnessChain(t, attemptID, "", replacement)
			if _, err := ReplayCanonicalV19WorkerReports(context.Background(), fixture.Home, attemptID, changed); !errors.Is(err, ErrCanonicalV19WorkerReportConflict) {
				t.Fatalf("%s replay error = %v, want %v", name, err, ErrCanonicalV19WorkerReportConflict)
			}
			if count := canonicalV19WorkerReportCount(t, fixture.Home, attemptID); count != len(original) {
				t.Fatalf("WorkerReport rows after %s = %d, want %d", name, count, len(original))
			}
		})
	}
}

func TestIngestCanonicalV19WorkerReportSmallAppendAfterLargeReplayUsesOnlyWitnessMaterial(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	records := make([]string, 128)
	for i := range records {
		records[i] = fmt.Sprintf("working: history %04d\n", i)
	}
	witnesses := canonicalV19WorkerReportWitnessChain(t, attemptID, "", records)
	history, err := ReplayCanonicalV19WorkerReports(context.Background(), fixture.Home, attemptID, witnesses)
	if err != nil {
		t.Fatal(err)
	}
	appendedRecord := "done: bounded append\n"
	witness := canonicalV19WorkerReportWitness(t, attemptID, "", appendedRecord, "2026-09-15T10:00:00Z", &history[len(history)-1])
	created, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness)
	if err != nil {
		t.Fatal(err)
	}
	if len(witness.AppendedRecord) != len(appendedRecord) || created.SourceEndOffset != history[len(history)-1].SourceEndOffset+int64(len(appendedRecord)) {
		t.Fatalf("bounded append = witness bytes %d offset %d, predecessor offset %d",
			len(witness.AppendedRecord), created.SourceEndOffset, history[len(history)-1].SourceEndOffset)
	}
	if count := canonicalV19WorkerReportCount(t, fixture.Home, attemptID); count != len(records)+1 {
		t.Fatalf("WorkerReport rows after bounded append = %d, want %d", count, len(records)+1)
	}
}

func TestIngestCanonicalV19WorkerReportRefusesStaleAttempt(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID: attemptID, Lifecycle: "failed", TerminalAt: "2026-09-15T10:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	witness := canonicalV19WorkerReportWitness(t, attemptID, "", "failed: late\n", "2026-09-15T10:02:00Z", nil)
	if _, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness); !errors.Is(err, ErrCanonicalV19WorkerReportNotCurrent) {
		t.Fatalf("stale Attempt WorkerReport error = %v, want %v", err, ErrCanonicalV19WorkerReportNotCurrent)
	}
}

func TestIngestCanonicalV19WorkerReportRefusesStaleExecutor(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	interrupt := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-worker-report")
	request, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, interrupt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), fixture.Home, request.OperationID,
		"2026-09-15T10:03:00Z", "interrupt-submitted-worker-report"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19ExecutorInterruptedEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-15T10:04:00Z", EvidenceDigest: "executor-ceased-worker-report",
	}); err != nil {
		t.Fatal(err)
	}
	witness := canonicalV19WorkerReportWitness(t, launch.AttemptID, launch.BindingID,
		"failed: late executor\n", "2026-09-15T10:05:00Z", nil)
	if _, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home, witness); !errors.Is(err, ErrCanonicalV19WorkerReportNotCurrent) {
		t.Fatalf("stale Executor WorkerReport error = %v, want %v", err, ErrCanonicalV19WorkerReportNotCurrent)
	}
}

func TestCreateCanonicalV19WorkerReportAcknowledgementPersistsExactDistinctEvidence(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	report, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home,
		canonicalV19WorkerReportWitness(t, attemptID, "", "done: ready\n", "2026-09-15T10:06:00Z", nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID: attemptID, Lifecycle: "completed", TerminalAt: "2026-09-15T10:07:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	input := CanonicalV19WorkerReportAcknowledgementCreateInput{
		WorkerReportID: report.ID, ActorKind: "supervisor",
		AcknowledgedAt: "2026-09-15T10:08:00Z", EvidenceDigest: "acknowledged-exact-worker-report",
	}
	first, err := CreateCanonicalV19WorkerReportAcknowledgement(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateCanonicalV19WorkerReportAcknowledgement(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.WorkerReportID != report.ID || first.ActorKind != input.ActorKind ||
		first.AcknowledgedAt != input.AcknowledgedAt || first.EvidenceDigest != input.EvidenceDigest {
		t.Fatalf("WorkerReportAcknowledgement = %#v replay=%#v", first, second)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var workerReportAcknowledgements, workerInputAcknowledgements int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM worker_report_acknowledgement WHERE worker_report_id=?`, report.ID).
		Scan(&workerReportAcknowledgements); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM worker_input_acknowledgement`).Scan(&workerInputAcknowledgements); err != nil {
		t.Fatal(err)
	}
	if workerReportAcknowledgements != 1 || workerInputAcknowledgements != 0 {
		t.Fatalf("acknowledgements WorkerReport/WorkerInput = %d/%d, want 1/0",
			workerReportAcknowledgements, workerInputAcknowledgements)
	}
}

func TestCreateCanonicalV19WorkerReportAcknowledgementRefusesConflictingReplay(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	report, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home,
		canonicalV19WorkerReportWitness(t, attemptID, "", "blocked: review\n", "2026-09-15T10:09:00Z", nil))
	if err != nil {
		t.Fatal(err)
	}
	input := CanonicalV19WorkerReportAcknowledgementCreateInput{
		WorkerReportID: report.ID, ActorKind: "operator",
		AcknowledgedAt: "2026-09-15T10:10:00Z", EvidenceDigest: "operator-read-report",
	}
	if _, err := CreateCanonicalV19WorkerReportAcknowledgement(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
	input.EvidenceDigest = "different-evidence"
	if _, err := CreateCanonicalV19WorkerReportAcknowledgement(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerReportAcknowledgementConflict) {
		t.Fatalf("conflicting WorkerReport acknowledgement error = %v, want %v",
			err, ErrCanonicalV19WorkerReportAcknowledgementConflict)
	}
}

func canonicalV19WorkerReportAttemptFixture(t *testing.T) (canonicalV19AttemptWriterTestFixture, string) {
	t.Helper()
	fixture := canonicalV19AttemptWriterFixture(t)
	attempt := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, attempt); err != nil {
		t.Fatal(err)
	}
	return fixture, attempt.ID
}

func canonicalV19WorkerReportWitness(
	t *testing.T,
	attemptID string,
	executorBindingID string,
	record string,
	createdAt string,
	predecessor *CanonicalV19WorkerReport,
) CanonicalV19WorkerReportAppendWitness {
	t.Helper()
	recordDigest := sha256.Sum256([]byte(record))
	witness := CanonicalV19WorkerReportAppendWitness{
		AttemptID: attemptID, ExecutorBindingID: executorBindingID,
		AppendedRecord: []byte(record), AppendedRecordDigest: hex.EncodeToString(recordDigest[:]),
		SourceEndOffset: int64(len(record)), CreatedAt: createdAt,
	}
	if predecessor != nil {
		witness.Predecessor = &CanonicalV19WorkerReportPredecessor{
			WorkerReportID: predecessor.ID, SourcePrefixDigest: predecessor.SourcePrefixDigest,
			SourceEndOffset: predecessor.SourceEndOffset,
		}
		witness.SourceEndOffset += predecessor.SourceEndOffset
	}
	canonicalV19WorkerReportRefreshAttestation(&witness)
	return witness
}

func canonicalV19WorkerReportRefreshAttestation(witness *CanonicalV19WorkerReportAppendWitness) {
	witness.sourceAttestation = &canonicalV19WorkerReportSourceAttestation{
		witnessDigest: canonicalV19WorkerReportAppendWitnessDigest(*witness),
	}
}

func canonicalV19WorkerReportWitnessChain(
	t *testing.T,
	attemptID string,
	executorBindingID string,
	records []string,
) []CanonicalV19WorkerReportAppendWitness {
	t.Helper()
	witnesses := make([]CanonicalV19WorkerReportAppendWitness, 0, len(records))
	var predecessor *CanonicalV19WorkerReport
	for i, record := range records {
		witness := canonicalV19WorkerReportWitness(t, attemptID, executorBindingID, record,
			fmt.Sprintf("2026-09-15T11:%02d:00Z", i%60), predecessor)
		report, err := canonicalV19WorkerReportFromAppendWitness(witness)
		if err != nil {
			t.Fatal(err)
		}
		witnesses = append(witnesses, witness)
		predecessor = &report
	}
	return witnesses
}

func canonicalV19WorkerReportCount(t *testing.T, homeDir, attemptID string) int {
	t.Helper()
	db, err := openReadOnly(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM worker_report WHERE attempt_id=?`, attemptID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
