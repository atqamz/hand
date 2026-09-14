package store

import (
	"errors"
	"fmt"
)

// ErrCanonicalV19HerdrCapabilityUnsupported marks a canonical operation that
// the selected managed Herdr provider cannot prove safely.
var ErrCanonicalV19HerdrCapabilityUnsupported = errors.New("canonical v19 Herdr capability is unsupported")

func canonicalV19HerdrCapabilityUnsupported(operation, proof string) error {
	return fmt.Errorf("%w: %s requires provider-backed %s", ErrCanonicalV19HerdrCapabilityUnsupported, operation, proof)
}

// Only provider proof that the caller belongs to the exact ExecutorBinding may
// create this token. No selected Herdr provider can create one yet.
type canonicalV19WorkerInputCallerAttestation struct {
	executorBindingID string
}

func requireCanonicalV19WorkerInputCallerAttestation(
	executorBindingID string,
	attestation *canonicalV19WorkerInputCallerAttestation,
) error {
	if attestation == nil || attestation.executorBindingID != executorBindingID {
		return canonicalV19HerdrCapabilityUnsupported("WorkerInput protocol", "exact caller-to-ExecutorBinding attestation")
	}
	return nil
}
