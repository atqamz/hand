//go:build !linux && !windows

package osfacts

import (
	"errors"
	"fmt"
	"runtime"
)

// Incarnation is unpopulated: no platform in this build reads process incarnation.
type Incarnation struct{}

// ReadIncarnation always refuses: macOS keeps the revision-1 refusals until a later
// revision qualifies its own proof (docs/architecture/v19-contracts/346-capability-adapters-v2.md).
func ReadIncarnation(int) (Incarnation, error) {
	return Incarnation{}, fmt.Errorf("os facts are not read on %s: %w", runtime.GOOS, errors.ErrUnsupported)
}

// Observe always reports Unknown: a zero-value Incarnation is never Absent.
func Observe(Incarnation) Observation {
	return Unknown
}
