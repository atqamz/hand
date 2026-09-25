//go:build linux

package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/execguard"
)

// One real exec-guard ExecutorBinding, established the same way the Launch adapter tests
// do (newExecGuardLaunchTest, prepareCanonicalV19ExecGuardLaunch), so the WorkerInput
// caller-attestation tests exercise the real V_B committed at Tx A.
type workerInputCallerAttestationFixture struct {
	home       string
	attemptID  string
	bindingID  string
	credential string
}

func newWorkerInputCallerAttestationFixture(t *testing.T) workerInputCallerAttestationFixture {
	t.Helper()
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	current, err := prepareCanonicalV19ExecGuardLaunch(context.Background(), l.home, l.input)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(execguard.Locator(l.dir))
	if err != nil {
		t.Fatal(err)
	}
	var handoff execguard.Handoff
	if err := json.Unmarshal(data, &handoff); err != nil {
		t.Fatal(err)
	}
	key, err := encodeCanonicalV19ExecGuardKey(canonicalV19ExecGuardKey{
		Session: l.key, Assoc: "unobserved", ObjectClass: canonicalV19ExecGuardUnknown,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := current.Current.Request
	if err := EstablishCanonicalV19ExecutorBinding(context.Background(), l.home, CanonicalV19ExecutorBindingEvidence{
		OperationID: request.OperationID, ProviderExecutorKey: key,
		EstablishedAt: "2026-09-10T00:00:00Z", EvidenceDigest: "executor-established-attestation-fixture",
	}); err != nil {
		t.Fatal(err)
	}
	return workerInputCallerAttestationFixture{
		home: l.home, attemptID: request.AttemptID, bindingID: request.BindingID,
		credential: handoff.Values[execguard.CredentialEnv],
	}
}

func (f workerInputCallerAttestationFixture) terminate(t *testing.T) {
	t.Helper()
	interrupt := canonicalV19InterruptPrepareInput(CanonicalV19LaunchRequest{AttemptID: f.attemptID, BindingID: f.bindingID}, "operation-interrupt-attestation")
	request, err := PrepareCanonicalV19Interrupt(context.Background(), f.home, interrupt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), f.home, request.OperationID,
		"2026-09-10T00:01:00Z", "interrupt-submitted-attestation"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19Interrupt(context.Background(), f.home, CanonicalV19ExecutorInterruptedEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-10T00:02:00Z", EvidenceDigest: "executor-ceased-attestation",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerInputCallerAttestationAcceptsExactCredential(t *testing.T) {
	f := newWorkerInputCallerAttestationFixture(t)
	created, err := CreateCanonicalV19WorkerInput(context.Background(), f.home, CanonicalV19WorkerInputCreateInput{
		ID: "worker-input-attested-drain", AttemptID: f.attemptID, ExecutorBindingID: f.bindingID,
		Payload: "instruction", PayloadDigest: "digest-attested-drain", OriginKind: "operator", CreatedAt: "2026-09-10T00:00:30Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := DrainCanonicalV19WorkerInputs(context.Background(), f.home, CanonicalV19WorkerInputDrainInput{
		AttemptID: f.attemptID, ExecutorBindingID: f.bindingID, Credential: f.credential,
	})
	if err != nil {
		t.Fatalf("drain with the exact credential: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != created.ID {
		t.Fatalf("pending WorkerInputs = %#v, want exactly the recorded input (EG-11)", pending)
	}
}

func TestWorkerInputCallerAttestationAcceptsExactCredentialForAcknowledge(t *testing.T) {
	f := newWorkerInputCallerAttestationFixture(t)
	created, err := CreateCanonicalV19WorkerInput(context.Background(), f.home, CanonicalV19WorkerInputCreateInput{
		ID: "worker-input-attested-ack", AttemptID: f.attemptID, ExecutorBindingID: f.bindingID,
		Payload: "instruction", PayloadDigest: "digest-attested-ack", OriginKind: "operator", CreatedAt: "2026-09-10T00:00:30Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	acknowledgement, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), f.home,
		CanonicalV19WorkerInputAcknowledgementCreateInput{
			WorkerInputID: created.ID, ExecutorBindingID: f.bindingID,
			ObservedAt: "2026-09-10T00:00:45Z", EvidenceDigest: "worker-observed-attested-ack",
			Credential: f.credential,
		})
	if err != nil {
		t.Fatalf("acknowledge with the exact credential: %v", err)
	}
	if acknowledgement.WorkerInputID != created.ID || acknowledgement.ExecutorBindingID != f.bindingID {
		t.Fatalf("acknowledgement = %#v, want exactly the presented WorkerInput and ExecutorBinding (EG-11)", acknowledgement)
	}
}

func TestWorkerInputCallerAttestationRefusesMissingCredential(t *testing.T) {
	f := newWorkerInputCallerAttestationFixture(t)
	_, err := DrainCanonicalV19WorkerInputs(context.Background(), f.home, CanonicalV19WorkerInputDrainInput{
		AttemptID: f.attemptID, ExecutorBindingID: f.bindingID,
	})
	if !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("drain with no credential error = %v, want attestation refusal", err)
	}
}

func TestWorkerInputCallerAttestationRefusesMalformedCredential(t *testing.T) {
	f := newWorkerInputCallerAttestationFixture(t)
	_, err := DrainCanonicalV19WorkerInputs(context.Background(), f.home, CanonicalV19WorkerInputDrainInput{
		AttemptID: f.attemptID, ExecutorBindingID: f.bindingID, Credential: "not-base64url-32-bytes",
	})
	if !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("drain with malformed credential error = %v, want attestation refusal", err)
	}
}

func TestWorkerInputCallerAttestationRefusesWrongCredential(t *testing.T) {
	f := newWorkerInputCallerAttestationFixture(t)
	wrong := make([]byte, 32)
	if f.credential == base64.RawURLEncoding.EncodeToString(wrong) {
		wrong[0] ^= 0xFF
	}
	_, err := DrainCanonicalV19WorkerInputs(context.Background(), f.home, CanonicalV19WorkerInputDrainInput{
		AttemptID: f.attemptID, ExecutorBindingID: f.bindingID, Credential: base64.RawURLEncoding.EncodeToString(wrong),
	})
	if !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("drain with the wrong credential error = %v, want attestation refusal (counterexample 6)", err)
	}
}

func TestWorkerInputCallerAttestationRefusesCredentialFromAnotherFleet(t *testing.T) {
	f1 := newWorkerInputCallerAttestationFixture(t)
	f2 := newWorkerInputCallerAttestationFixture(t)
	if f1.bindingID != f2.bindingID {
		t.Fatalf("fixtures must share one literal ExecutorBinding ID to exercise cross-Fleet contamination, got %q and %q", f1.bindingID, f2.bindingID)
	}
	_, err := DrainCanonicalV19WorkerInputs(context.Background(), f2.home, CanonicalV19WorkerInputDrainInput{
		AttemptID: f2.attemptID, ExecutorBindingID: f2.bindingID, Credential: f1.credential,
	})
	if !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("drain against another Fleet's DB with this Fleet's B but the other Fleet's S_B error = %v, want attestation refusal (counterexample 7)", err)
	}
}

func TestWorkerInputCallerAttestationRefusesTerminatedBinding(t *testing.T) {
	f := newWorkerInputCallerAttestationFixture(t)
	f.terminate(t)
	_, err := DrainCanonicalV19WorkerInputs(context.Background(), f.home, CanonicalV19WorkerInputDrainInput{
		AttemptID: f.attemptID, ExecutorBindingID: f.bindingID, Credential: f.credential,
	})
	if !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("drain after termination error = %v, want attestation refusal (counterexample 6)", err)
	}
}

func TestWorkerInputCallerAttestationRefusesBindingFromAnotherAttempt(t *testing.T) {
	f := newWorkerInputCallerAttestationFixture(t)
	_, err := DrainCanonicalV19WorkerInputs(context.Background(), f.home, CanonicalV19WorkerInputDrainInput{
		AttemptID: "attempt-not-owning-" + f.bindingID, ExecutorBindingID: f.bindingID, Credential: f.credential,
	})
	if !errors.Is(err, ErrCanonicalV19WorkerInputDrainNotCurrent) {
		t.Fatalf("drain naming another Attempt for this exact B error = %v, want %v", err, ErrCanonicalV19WorkerInputDrainNotCurrent)
	}
}

func TestWorkerInputCallerAttestationNeverEchoesTheCredential(t *testing.T) {
	f := newWorkerInputCallerAttestationFixture(t)
	wrong := make([]byte, 32)
	copy(wrong, []byte(f.credential))
	wrong[0] ^= 0xFF
	_, err := DrainCanonicalV19WorkerInputs(context.Background(), f.home, CanonicalV19WorkerInputDrainInput{
		AttemptID: f.attemptID, ExecutorBindingID: f.bindingID, Credential: base64.RawURLEncoding.EncodeToString(wrong),
	})
	if err == nil {
		t.Fatal("wrong credential unexpectedly accepted")
	}
	if strings.Contains(err.Error(), f.credential) {
		t.Fatalf("refusal error echoed S_B: %v", err)
	}
}
