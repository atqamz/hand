package store

import (
	"context"
	"testing"
)

func TestReadCanonicalV19WorkerInputAcknowledgementReturnsExactHistoricalEvidence(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-ack-read", "instruction", "digest-worker-input-ack-read")
	createdInput, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := ReadCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home, createdInput.ID); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatal("acknowledgement unexpectedly existed before Worker observation")
	}

	created, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home,
		CanonicalV19WorkerInputAcknowledgementCreateInput{
			WorkerInputID: createdInput.ID, ExecutorBindingID: launch.BindingID,
			ObservedAt: "2026-09-09T09:31:00Z", EvidenceDigest: "worker-observed-ack-read",
		})
	if err != nil {
		t.Fatal(err)
	}
	read, found, err := ReadCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home, createdInput.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || read != created {
		t.Fatalf("read acknowledgement = %#v found=%v, want %#v", read, found, created)
	}
}
