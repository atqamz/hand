package store

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
)

func TestCreateCanonicalV19WorkerInputPersistsExactSemanticInputAndOrdinal(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	first := canonicalV19WorkerInputCreateInput(launch, "worker-input-1", "first instruction", "digest-first")
	created, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, first)
	if err != nil {
		t.Fatal(err)
	}
	if created.Ordinal != 1 || created.ID != first.ID || created.AttemptID != launch.AttemptID ||
		created.ExecutorBindingID != launch.BindingID || created.Payload != first.Payload ||
		created.PayloadDigest != first.PayloadDigest || created.OriginKind != "operator" ||
		created.CreatedAt != first.CreatedAt {
		t.Fatalf("first WorkerInput = %#v", created)
	}

	second := canonicalV19WorkerInputCreateInput(launch, "worker-input-2", "second instruction", "digest-second")
	second.CreatedAt = "2026-09-08T06:01:00Z"
	createdSecond, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, second)
	if err != nil {
		t.Fatal(err)
	}
	if createdSecond.Ordinal != 2 {
		t.Fatalf("second WorkerInput ordinal = %d, want 2", createdSecond.Ordinal)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.sql.Query(`SELECT id,attempt_id,executor_binding_id,ordinal,payload,payload_digest,origin_kind,created_at
		FROM worker_input WHERE executor_binding_id=? ORDER BY ordinal`, launch.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []CanonicalV19WorkerInput
	for rows.Next() {
		var input CanonicalV19WorkerInput
		var payload []byte
		if err := rows.Scan(&input.ID, &input.AttemptID, &input.ExecutorBindingID, &input.Ordinal,
			&payload, &input.PayloadDigest, &input.OriginKind, &input.CreatedAt); err != nil {
			t.Fatal(err)
		}
		input.Payload = string(payload)
		got = append(got, input)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "worker-input-1" || got[0].Ordinal != 1 ||
		got[0].Payload != "first instruction" || got[1].ID != "worker-input-2" || got[1].Ordinal != 2 ||
		got[1].Payload != "second instruction" {
		t.Fatalf("persisted WorkerInputs = %#v", got)
	}
	var payloadType string
	if err := db.sql.QueryRow(`SELECT typeof(payload) FROM worker_input WHERE id=?`, first.ID).Scan(&payloadType); err != nil {
		t.Fatal(err)
	}
	if payloadType != "blob" {
		t.Fatalf("WorkerInput payload storage class = %q, want blob", payloadType)
	}
}

func TestCreateCanonicalV19WorkerInputExactReplayConverges(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	input := canonicalV19WorkerInputCreateInput(launch, "worker-input-replay", "same instruction", "digest-same")
	first, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("replayed WorkerInput = %#v, want %#v", second, first)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM worker_input WHERE id=?`, input.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("WorkerInput rows after replay = %d, want 1", count)
	}
}

func TestCreateCanonicalV19WorkerInputIdentityDriftConflicts(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	input := canonicalV19WorkerInputCreateInput(launch, "worker-input-drift", "original instruction", "digest-original")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
	input.Payload = "different instruction"
	input.PayloadDigest = "digest-different"
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerInputConflict) {
		t.Fatalf("WorkerInput identity drift error = %v, want %v", err, ErrCanonicalV19WorkerInputConflict)
	}
}

func TestCreateCanonicalV19WorkerInputRefusesTerminatedExecutor(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	interrupt := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-worker-input")
	request, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, interrupt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), fixture.Home, request.OperationID,
		"2026-09-08T06:00:00Z", "interrupt-submitted-worker-input"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19ExecutorInterruptedEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-08T06:01:00Z", EvidenceDigest: "executor-ceased-worker-input",
	}); err != nil {
		t.Fatal(err)
	}

	input := canonicalV19WorkerInputCreateInput(launch, "worker-input-after-interrupt", "stale instruction", "digest-stale")
	input.CreatedAt = "2026-09-08T06:02:00Z"
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerInputNotCurrent) {
		t.Fatalf("WorkerInput after executor termination error = %v, want %v", err, ErrCanonicalV19WorkerInputNotCurrent)
	}
}

func TestConcurrentCanonicalV19WorkerInputsAllocateDistinctOrdinals(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	inputs := []CanonicalV19WorkerInputCreateInput{
		canonicalV19WorkerInputCreateInput(launch, "worker-input-concurrent-a", "instruction a", "digest-a"),
		canonicalV19WorkerInputCreateInput(launch, "worker-input-concurrent-b", "instruction b", "digest-b"),
	}
	inputs[1].CreatedAt = "2026-09-08T06:01:00Z"
	ordinals := make(chan int64, len(inputs))
	errs := make(chan error, len(inputs))
	var wg sync.WaitGroup
	for _, input := range inputs {
		input := input
		wg.Add(1)
		go func() {
			defer wg.Done()
			created, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, input)
			if err != nil {
				errs <- err
				return
			}
			ordinals <- created.Ordinal
		}()
	}
	wg.Wait()
	close(errs)
	close(ordinals)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got := make([]int, 0, len(inputs))
	for ordinal := range ordinals {
		got = append(got, int(ordinal))
	}
	sort.Ints(got)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("concurrent WorkerInput ordinals = %v, want [1 2]", got)
	}
}

func canonicalV19WorkerInputCreateInput(
	launch CanonicalV19LaunchRequest,
	id string,
	payload string,
	payloadDigest string,
) CanonicalV19WorkerInputCreateInput {
	return CanonicalV19WorkerInputCreateInput{
		ID: id, AttemptID: launch.AttemptID, ExecutorBindingID: launch.BindingID,
		Payload: payload, PayloadDigest: payloadDigest, OriginKind: "operator", CreatedAt: "2026-09-08T06:00:00Z",
	}
}
