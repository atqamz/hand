package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/store"
)

func TestWorkerInputProtocolRequiresWorkerRole(t *testing.T) {
	cmd := newWorkerInputCmdWithDeps(workerInputCommandDeps{
		role: func() string { return "" },
	})
	cmd.SetArgs([]string{"drain", "attempt-1", "executor-1"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	_, err := cmd.ExecuteC()
	if err == nil {
		t.Fatal("worker-input drain unexpectedly accepted non-worker role")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("worker-input role error = %v, want precondition exit 3", err)
	}
	if !strings.Contains(err.Error(), "HAND_ROLE=worker") {
		t.Fatalf("worker-input role error = %q", err)
	}
}

func TestWorkerInputDrainRendersExactPendingInputs(t *testing.T) {
	fixed := time.Date(2026, 9, 9, 9, 30, 0, 123, time.UTC)
	cmd := newWorkerInputCmdWithDeps(workerInputCommandDeps{
		role:        func() string { return harness.WorkerRole },
		resolveHome: func() (string, error) { return "/fleet", nil },
		now:         func() time.Time { return fixed },
		drain: func(_ context.Context, home string, input store.CanonicalV19WorkerInputDrainInput) ([]store.CanonicalV19WorkerInput, error) {
			if home != "/fleet" || input.AttemptID != "attempt-1" || input.ExecutorBindingID != "executor-1" {
				t.Fatalf("drain target = %q %#v", home, input)
			}
			return []store.CanonicalV19WorkerInput{
				{ID: "input-1", AttemptID: "attempt-1", ExecutorBindingID: "executor-1", Ordinal: 1,
					Payload: "first\nmessage", PayloadDigest: "digest-1", OriginKind: "operator", CreatedAt: "2026-09-09T09:00:00Z"},
				{ID: "input-2", AttemptID: "attempt-1", ExecutorBindingID: "executor-1", Ordinal: 2,
					Payload: "second", PayloadDigest: "digest-2", OriginKind: "supervisor", CreatedAt: "2026-09-09T09:01:00Z"},
			}, nil
		},
	})
	cmd.SetArgs([]string{"drain", "attempt-1", "executor-1"})
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(new(strings.Builder))
	if _, err := cmd.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	var got workerInputDrainOutput
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("decode drain output %q: %v", out.String(), err)
	}
	if got.AttemptID != "attempt-1" || got.ExecutorBindingID != "executor-1" ||
		got.DrainedAt != fixed.Format(time.RFC3339Nano) || len(got.Inputs) != 2 {
		t.Fatalf("drain output = %#v", got)
	}
	if got.Inputs[0].ID != "input-1" || got.Inputs[0].Ordinal != 1 || got.Inputs[0].Payload != "first\nmessage" ||
		got.Inputs[1].ID != "input-2" || got.Inputs[1].Ordinal != 2 {
		t.Fatalf("drain inputs = %#v", got.Inputs)
	}
}

func TestWorkerInputAcknowledgeCreatesAndReplaysExactEvidence(t *testing.T) {
	fixed := time.Date(2026, 9, 9, 9, 31, 0, 0, time.UTC)
	var created store.CanonicalV19WorkerInputAcknowledgementCreateInput
	cmd := newWorkerInputCmdWithDeps(workerInputCommandDeps{
		role:        func() string { return harness.WorkerRole },
		resolveHome: func() (string, error) { return "/fleet", nil },
		now:         func() time.Time { return fixed },
		readAck: func(context.Context, string, string) (store.CanonicalV19WorkerInputAcknowledgement, bool, error) {
			return store.CanonicalV19WorkerInputAcknowledgement{}, false, nil
		},
		createAck: func(_ context.Context, home string, input store.CanonicalV19WorkerInputAcknowledgementCreateInput) (store.CanonicalV19WorkerInputAcknowledgement, error) {
			if home != "/fleet" {
				t.Fatalf("ack home = %q", home)
			}
			created = input
			return store.CanonicalV19WorkerInputAcknowledgement{
				WorkerInputID: input.WorkerInputID, ExecutorBindingID: input.ExecutorBindingID,
				ActorKind: "worker", ObservedAt: input.ObservedAt, EvidenceDigest: input.EvidenceDigest,
			}, nil
		},
	})
	cmd.SetArgs([]string{"acknowledge", "input-1", "executor-1"})
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(new(strings.Builder))
	if _, err := cmd.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	if created.WorkerInputID != "input-1" || created.ExecutorBindingID != "executor-1" ||
		created.ObservedAt != fixed.Format(time.RFC3339Nano) || len(created.EvidenceDigest) != 64 {
		t.Fatalf("created acknowledgement = %#v", created)
	}
	var first workerInputAcknowledgementOutput
	if err := json.Unmarshal([]byte(out.String()), &first); err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.EvidenceDigest != created.EvidenceDigest {
		t.Fatalf("first acknowledgement output = %#v", first)
	}

	existing := store.CanonicalV19WorkerInputAcknowledgement{
		WorkerInputID: "input-1", ExecutorBindingID: "executor-1", ActorKind: "worker",
		ObservedAt: created.ObservedAt, EvidenceDigest: created.EvidenceDigest,
	}
	replay := newWorkerInputCmdWithDeps(workerInputCommandDeps{
		role:        func() string { return harness.WorkerRole },
		resolveHome: func() (string, error) { return "/fleet", nil },
		now:         func() time.Time { t.Fatal("replay unexpectedly requested a new timestamp"); return time.Time{} },
		readAck: func(context.Context, string, string) (store.CanonicalV19WorkerInputAcknowledgement, bool, error) {
			return existing, true, nil
		},
		createAck: func(context.Context, string, store.CanonicalV19WorkerInputAcknowledgementCreateInput) (store.CanonicalV19WorkerInputAcknowledgement, error) {
			t.Fatal("replay unexpectedly created acknowledgement")
			return store.CanonicalV19WorkerInputAcknowledgement{}, nil
		},
	})
	replay.SetArgs([]string{"acknowledge", "input-1", "executor-1"})
	out.Reset()
	replay.SetOut(&out)
	replay.SetErr(new(strings.Builder))
	if _, err := replay.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	var second workerInputAcknowledgementOutput
	if err := json.Unmarshal([]byte(out.String()), &second); err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.EvidenceDigest != existing.EvidenceDigest || second.ObservedAt != existing.ObservedAt {
		t.Fatalf("replayed acknowledgement output = %#v", second)
	}
}
