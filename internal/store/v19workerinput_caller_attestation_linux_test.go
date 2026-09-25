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

// One real exec-guard ExecutorBinding, established the same way the Launch adapter tests do,
// so these tests exercise the real V_B committed at Tx A end to end. Row shapes the anchored
// schema can never produce are unit-tested in v19workerinput_caller_attestation_test.go.
type workerInputCallerAttestationFixture struct {
	home       string
	attemptID  string
	bindingID  string
	credential string
}

func newWorkerInputCallerAttestationFixture(t *testing.T) workerInputCallerAttestationFixture {
	t.Helper()
	return newWorkerInputCallerAttestationFixtureWithFleetID(t, "fleet-1")
}

// fleetID lets a cross-Fleet test commit V_B under a genuinely distinct Fleet identity;
// fleet is anchored immutable, so it must be chosen at Tx A, not patched in afterward.
func newWorkerInputCallerAttestationFixtureWithFleetID(t *testing.T, fleetID string) workerInputCallerAttestationFixture {
	t.Helper()
	l := newExecGuardLaunchTestWithFleetID(t, fleetID, "/bin/sh", "-c", "exit 0")
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

// A guarded ExecutorBinding closes only from its guard's cessation record or a boot
// change (EG-8); CompleteCanonicalV19Interrupt now refuses the generic path for one. The
// termination row itself, not how it was produced, is what the attestation check reads.
func (f workerInputCallerAttestationFixture) terminate(t *testing.T) {
	t.Helper()
	db, err := open(Path(f.home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO executor_binding_termination(executor_binding_id,terminal_kind,observed_at,evidence_digest)
		VALUES(?,'provider-gone',?,?)`, f.bindingID, "2026-09-10T00:02:00Z", "executor-ceased-attestation"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
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

func TestVerifyCanonicalV19WorkerInputCallerCredentialReturnsExactVerifierOnSuccess(t *testing.T) {
	f := newWorkerInputCallerAttestationFixture(t)
	current, err := readCanonicalV19HerdrLaunchCurrent(context.Background(), f.home, "operation-exec-guard-launch")
	if err != nil {
		t.Fatal(err)
	}
	want := current.Current.Request.Spec.Environment[execguard.CredentialEnv].ValueDigest
	got, err := VerifyCanonicalV19WorkerInputCallerCredential(context.Background(), f.home, f.bindingID, f.credential)
	if err != nil || got != want {
		t.Fatalf("verify = %q, %v, want the committed V_B %q", got, err, want)
	}
}

func TestVerifyCanonicalV19WorkerInputCallerCredentialRefusesWrongCredential(t *testing.T) {
	f := newWorkerInputCallerAttestationFixture(t)
	wrong := make([]byte, 32)
	if _, err := VerifyCanonicalV19WorkerInputCallerCredential(context.Background(), f.home, f.bindingID, base64.RawURLEncoding.EncodeToString(wrong)); !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("verify with the wrong credential error = %v, want attestation refusal", err)
	}
}

// Two real Fleet homes, built through the anchored DDL, share the same literal B
// ("executor-binding-1") but have genuinely distinct fleet_id values. Fleet A's own
// exact credential must be refused against Fleet B's DB (counterexample 7).
func TestWorkerInputCallerAttestationRefusesCredentialFromAnotherFleet(t *testing.T) {
	a := newWorkerInputCallerAttestationFixtureWithFleetID(t, "fleet-attestation-a")
	b := newWorkerInputCallerAttestationFixtureWithFleetID(t, "fleet-attestation-b")
	if a.bindingID != b.bindingID {
		t.Fatalf("fixtures must share one literal ExecutorBinding ID to exercise cross-Fleet contamination, got %q and %q", a.bindingID, b.bindingID)
	}
	if _, err := DrainCanonicalV19WorkerInputs(context.Background(), a.home, CanonicalV19WorkerInputDrainInput{
		AttemptID: a.attemptID, ExecutorBindingID: a.bindingID, Credential: a.credential,
	}); err != nil {
		t.Fatalf("Fleet A's own credential against its own Fleet: %v", err)
	}
	if _, err := DrainCanonicalV19WorkerInputs(context.Background(), b.home, CanonicalV19WorkerInputDrainInput{
		AttemptID: b.attemptID, ExecutorBindingID: b.bindingID, Credential: a.credential,
	}); !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("drain against Fleet B's DB with Fleet A's S_B for the same literal B error = %v, want attestation refusal (counterexample 7)", err)
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

func TestWorkerInputCallerAttestationDerivesAttemptFromBindingWhenArgvOmitsIt(t *testing.T) {
	f := newWorkerInputCallerAttestationFixture(t)
	created, err := CreateCanonicalV19WorkerInput(context.Background(), f.home, CanonicalV19WorkerInputCreateInput{
		ID: "worker-input-attested-derived-attempt", AttemptID: f.attemptID, ExecutorBindingID: f.bindingID,
		Payload: "instruction", PayloadDigest: "digest-attested-derived-attempt", OriginKind: "operator", CreatedAt: "2026-09-10T00:00:30Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := DrainCanonicalV19WorkerInputs(context.Background(), f.home, CanonicalV19WorkerInputDrainInput{
		ExecutorBindingID: f.bindingID, Credential: f.credential,
	})
	if err != nil {
		t.Fatalf("drain with no argv Attempt ID: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != created.ID {
		t.Fatalf("pending WorkerInputs = %#v, want the exact input of B's derived Attempt", pending)
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
