package store

import (
	"context"
	"errors"
	"testing"
)

func TestDrainCanonicalV19WorkerInputsReturnsPendingInOrdinalOrder(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	first, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home,
		canonicalV19WorkerInputCreateInput(launch, "worker-input-drain-1", "first", "digest-drain-1"))
	if err != nil {
		t.Fatal(err)
	}
	secondInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-drain-2", "second", "digest-drain-2")
	secondInput.CreatedAt = "2026-09-09T06:31:00Z"
	second, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	thirdInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-drain-3", "third", "digest-drain-3")
	thirdInput.CreatedAt = "2026-09-09T06:32:00Z"
	third, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, thirdInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home,
		CanonicalV19WorkerInputAcknowledgementCreateInput{
			WorkerInputID: second.ID, ExecutorBindingID: launch.BindingID,
			ObservedAt: "2026-09-09T06:33:00Z", EvidenceDigest: "worker-drained-second",
		}); err != nil {
		t.Fatal(err)
	}

	pending, err := DrainCanonicalV19WorkerInputs(context.Background(), fixture.Home, CanonicalV19WorkerInputDrainInput{
		AttemptID: launch.AttemptID, ExecutorBindingID: launch.BindingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].ID != first.ID || pending[0].Ordinal != 1 || pending[0].Payload != "first" ||
		pending[1].ID != third.ID || pending[1].Ordinal != 3 || pending[1].Payload != "third" {
		t.Fatalf("pending WorkerInputs = %#v", pending)
	}
}

func TestDrainCanonicalV19WorkerInputsReturnsEmptyAfterAcknowledgement(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	created, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home,
		canonicalV19WorkerInputCreateInput(launch, "worker-input-drain-empty", "instruction", "digest-drain-empty"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home,
		CanonicalV19WorkerInputAcknowledgementCreateInput{
			WorkerInputID: created.ID, ExecutorBindingID: launch.BindingID,
			ObservedAt: "2026-09-09T06:31:00Z", EvidenceDigest: "worker-drained-only-input",
		}); err != nil {
		t.Fatal(err)
	}

	pending, err := DrainCanonicalV19WorkerInputs(context.Background(), fixture.Home, CanonicalV19WorkerInputDrainInput{
		AttemptID: launch.AttemptID, ExecutorBindingID: launch.BindingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending WorkerInputs = %#v, want empty", pending)
	}
}

func TestSuccessfulCanonicalV19WorkerWakeDoesNotAcknowledgeWorkerInput(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	created, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home,
		canonicalV19WorkerInputCreateInput(launch, "worker-input-drain-after-wake", "instruction", "digest-drain-after-wake"))
	if err != nil {
		t.Fatal(err)
	}
	wake := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-drain", created.Ordinal)
	request, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, wake)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19WorkerWake(context.Background(), fixture.Home, request.OperationID,
		"2026-09-09T06:31:00Z", "worker-wake-submitted-drain"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19WorkerWake(context.Background(), fixture.Home, CanonicalV19WorkerWakeSucceededEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-09T06:32:00Z", EvidenceDigest: "worker-wake-accepted-drain",
	}); err != nil {
		t.Fatal(err)
	}

	pending, err := DrainCanonicalV19WorkerInputs(context.Background(), fixture.Home, CanonicalV19WorkerInputDrainInput{
		AttemptID: launch.AttemptID, ExecutorBindingID: launch.BindingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != created.ID {
		t.Fatalf("pending WorkerInputs after successful wake = %#v, want exact unacknowledged input", pending)
	}
}

func TestUnresolvedCanonicalV19WorkerWakeDoesNotBlockWorkerInputDrain(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	created, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home,
		canonicalV19WorkerInputCreateInput(launch, "worker-input-drain-during-wake", "instruction", "digest-drain-during-wake"))
	if err != nil {
		t.Fatal(err)
	}
	wake := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-unresolved-drain", created.Ordinal)
	if _, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, wake); err != nil {
		t.Fatal(err)
	}

	pending, err := DrainCanonicalV19WorkerInputs(context.Background(), fixture.Home, CanonicalV19WorkerInputDrainInput{
		AttemptID: launch.AttemptID, ExecutorBindingID: launch.BindingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != created.ID {
		t.Fatalf("pending WorkerInputs during unresolved wake = %#v, want exact input", pending)
	}
}

func TestDrainCanonicalV19WorkerInputsRefusesTerminatedExecutor(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home,
		canonicalV19WorkerInputCreateInput(launch, "worker-input-drain-after-interrupt", "instruction", "digest-drain-after-interrupt")); err != nil {
		t.Fatal(err)
	}
	interrupt := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-before-worker-input-drain")
	request, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, interrupt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), fixture.Home, request.OperationID,
		"2026-09-09T06:31:00Z", "interrupt-submitted-before-drain"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19ExecutorInterruptedEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-09T06:32:00Z", EvidenceDigest: "executor-ceased-before-drain",
	}); err != nil {
		t.Fatal(err)
	}

	_, err = DrainCanonicalV19WorkerInputs(context.Background(), fixture.Home, CanonicalV19WorkerInputDrainInput{
		AttemptID: launch.AttemptID, ExecutorBindingID: launch.BindingID,
	})
	if !errors.Is(err, ErrCanonicalV19WorkerInputDrainNotCurrent) {
		t.Fatalf("WorkerInput drain after executor termination error = %v, want %v",
			err, ErrCanonicalV19WorkerInputDrainNotCurrent)
	}
}
