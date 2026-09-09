package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateCanonicalV19WorkerInputAcknowledgementPersistsExactEvidence(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-ack-1", "instruction", "digest-input-ack-1")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput); err != nil {
		t.Fatal(err)
	}
	input := canonicalV19WorkerInputAcknowledgementCreateInput(workerInput.ID, launch.BindingID)
	created, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkerInputID != workerInput.ID || created.ExecutorBindingID != launch.BindingID ||
		created.ActorKind != "worker" || created.ObservedAt != input.ObservedAt || created.EvidenceDigest != input.EvidenceDigest {
		t.Fatalf("WorkerInputAcknowledgement = %#v", created)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var workerInputID, executorBindingID, actorKind, observedAt, evidenceDigest string
	if err := db.sql.QueryRow(`SELECT worker_input_id,executor_binding_id,actor_kind,observed_at,evidence_digest
		FROM worker_input_acknowledgement WHERE worker_input_id=?`, workerInput.ID).Scan(
		&workerInputID, &executorBindingID, &actorKind, &observedAt, &evidenceDigest,
	); err != nil {
		t.Fatal(err)
	}
	if workerInputID != workerInput.ID || executorBindingID != launch.BindingID || actorKind != "worker" ||
		observedAt != input.ObservedAt || evidenceDigest != input.EvidenceDigest {
		t.Fatalf("persisted WorkerInputAcknowledgement = %q/%q/%q/%q/%q",
			workerInputID, executorBindingID, actorKind, observedAt, evidenceDigest)
	}
	var wakes int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM worker_wake_operation`).Scan(&wakes); err != nil {
		t.Fatal(err)
	}
	if wakes != 0 {
		t.Fatalf("WorkerWake rows after acknowledgement = %d, want 0", wakes)
	}
}

func TestCreateCanonicalV19WorkerInputAcknowledgementExactReplayConverges(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-ack-replay", "instruction", "digest-input-ack-replay")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput); err != nil {
		t.Fatal(err)
	}
	input := canonicalV19WorkerInputAcknowledgementCreateInput(workerInput.ID, launch.BindingID)
	first, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("replayed WorkerInputAcknowledgement = %#v, want %#v", second, first)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM worker_input_acknowledgement WHERE worker_input_id=?`, workerInput.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("WorkerInputAcknowledgement rows after replay = %d, want 1", count)
	}
}

func TestCreateCanonicalV19WorkerInputAcknowledgementEvidenceDriftConflicts(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-ack-drift", "instruction", "digest-input-ack-drift")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput); err != nil {
		t.Fatal(err)
	}
	input := canonicalV19WorkerInputAcknowledgementCreateInput(workerInput.ID, launch.BindingID)
	if _, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
	input.EvidenceDigest = "different-ack-evidence"
	if _, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerInputAcknowledgementConflict) {
		t.Fatalf("acknowledgement evidence drift error = %v, want %v",
			err, ErrCanonicalV19WorkerInputAcknowledgementConflict)
	}
}

func TestCreateCanonicalV19WorkerInputAcknowledgementRejectsExecutorMismatch(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-ack-mismatch", "instruction", "digest-input-ack-mismatch")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput); err != nil {
		t.Fatal(err)
	}
	input := canonicalV19WorkerInputAcknowledgementCreateInput(workerInput.ID, "different-executor-binding")
	if _, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerInputAcknowledgementConflict) {
		t.Fatalf("acknowledgement executor mismatch error = %v, want %v",
			err, ErrCanonicalV19WorkerInputAcknowledgementConflict)
	}
}

func TestCreateCanonicalV19WorkerInputAcknowledgementAllowsExactHistoricalAttachment(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-ack-historical", "instruction", "digest-input-ack-historical")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput); err != nil {
		t.Fatal(err)
	}

	interrupt := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-worker-input-ack")
	request, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, interrupt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), fixture.Home, request.OperationID,
		"2026-09-09T02:20:00Z", "interrupt-submitted-worker-input-ack"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19ExecutorInterruptedEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-09T02:21:00Z", EvidenceDigest: "executor-ceased-worker-input-ack",
	}); err != nil {
		t.Fatal(err)
	}

	input := canonicalV19WorkerInputAcknowledgementCreateInput(workerInput.ID, launch.BindingID)
	input.ObservedAt = "2026-09-09T02:22:00Z"
	created, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkerInputID != workerInput.ID || created.ExecutorBindingID != launch.BindingID {
		t.Fatalf("historical WorkerInputAcknowledgement = %#v", created)
	}
}

func canonicalV19WorkerInputAcknowledgementCreateInput(
	workerInputID string,
	executorBindingID string,
) CanonicalV19WorkerInputAcknowledgementCreateInput {
	return CanonicalV19WorkerInputAcknowledgementCreateInput{
		WorkerInputID: workerInputID, ExecutorBindingID: executorBindingID,
		ObservedAt: "2026-09-09T02:19:00Z", EvidenceDigest: "worker-observed-input",
	}
}
