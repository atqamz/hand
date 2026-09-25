//go:build linux

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/osfacts"
)

// The interrupt-request record is the only control Hand exerts, written after Tx B while
// it reads G live; no signal ever goes by PID. Success comes only from B's termination.
func reconcileCanonicalV19HerdrGuardInterrupt(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrInterruptCurrent,
	deps canonicalV19HerdrInterruptDeps,
) (string, bool, error) {
	request := current.Current.Request
	if _, err := parseCanonicalV19ExecGuardKey(request.ProviderExecutorKey); err != nil {
		return "", false, nil
	}
	fail := func(err error) (string, bool, error) {
		return current.Current.State, true, fmt.Errorf("reconcile canonical v19 Herdr Interrupt: %w", err)
	}
	binding, err := readCanonicalV19ExecGuardBinding(ctx, homeDir, request.ExecutorBindingID)
	if err != nil {
		return fail(err)
	}
	kind, liveness, err := ceaseCanonicalV19ExecGuardBinding(ctx, homeDir, binding, deps.now)
	if err != nil {
		return fail(err)
	}
	if kind != "" {
		state, err := reconcileCanonicalV19HerdrInterrupt(ctx, homeDir, request.OperationID, deps)
		return state, true, err
	}
	transition := CanonicalV19InterruptTransitionInput{
		OperationID: request.OperationID,
		ObservedAt:  canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt),
	}
	switch {
	case current.Current.State == "prepared":
		transition.State = "no-effect"
		transition.EvidenceDigest = canonicalV19ExecGuardInterruptEvidence(request, transition.State, liveness)
		err := classifyCanonicalV19InterruptPreparedNoEffect(ctx, homeDir, transition)
		if errors.Is(err, ErrCanonicalV19InterruptTransition) {
			state, err := reconcileCanonicalV19HerdrInterrupt(ctx, homeDir, request.OperationID, deps)
			return state, true, err
		}
		if err != nil {
			return fail(err)
		}
		return "no-effect", true, nil
	case liveness == osfacts.Alive:
		if err := execguard.RequestInterrupt(binding.Dir, *binding.Parsed.Guard, request.OperationID); err != nil {
			return fail(fmt.Errorf("write the interrupt request: %w", err))
		}
		return fail(errors.New("the live exec guard has not recorded cessation yet"))
	case current.Current.State == "submitted":
		transition.State = "uncertain"
		transition.EvidenceDigest = canonicalV19ExecGuardInterruptEvidence(request, transition.State, liveness)
		if err := ClassifyCanonicalV19Interrupt(ctx, homeDir, transition); err != nil {
			return fail(err)
		}
	}
	return "uncertain", true, fmt.Errorf("reconcile canonical v19 Herdr Interrupt: the exec guard is %s without a ceased record", liveness)
}

func canonicalV19ExecGuardInterruptEvidence(request CanonicalV19InterruptRequest, state string, liveness osfacts.Observation) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:herdr-exec-guard-interrupt-observation:v1")
	writeCanonicalV19DigestField(hash, "operation_id", request.OperationID)
	writeCanonicalV19DigestField(hash, "request_digest", request.RequestDigest)
	writeCanonicalV19DigestField(hash, "state", state)
	writeCanonicalV19DigestField(hash, "guard", string(liveness))
	return hex.EncodeToString(hash.Sum(nil))
}
