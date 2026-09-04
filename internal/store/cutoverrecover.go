package store

import (
	"errors"
	"fmt"
)

var errLegacyV18CutoverRecoveryExecutionUnsafe = errors.New("v19 cutover recovery execution is not mechanically safe")

// Executes only recovery work already authorized by the read-only classifier.
// It never starts fresh legacy cutover or treats advisory marker prose as authority.
// Mutating transitions retain their own MigrationLock and exact reclassification.
func recoverCanonicalV19Cutover(homeDir string) (legacyV18CutoverRecoveryState, error) {
	const maxInspections = 3 // rebuild -> publish -> canonical authority

	for inspection := 0; inspection < maxInspections; inspection++ {
		state, err := inspectLegacyV18CutoverRecovery(homeDir)
		if err != nil {
			return legacyV18CutoverRecoveryState{}, fmt.Errorf("recover canonical v19 cutover: inspect recovery authority: %w", err)
		}

		switch state.Disposition {
		case legacyV18CutoverRecoveryCanonicalAuthority,
			legacyV18CutoverRecoveryLegacySource,
			legacyV18CutoverRecoveryNoState:
			return state, nil
		case legacyV18CutoverRecoveryRefuse:
			return state, fmt.Errorf("%w: recovery disposition=%s: %s", errLegacyV18CutoverRecoveryExecutionUnsafe, state.Disposition, state.Reason)
		case legacyV18CutoverRecoveryRebuildCanonicalTemp:
			if _, err := rebuildCanonicalV19CutoverTemp(homeDir); err != nil {
				return state, fmt.Errorf("recover canonical v19 cutover: rebuild canonical temp: %w", err)
			}
		case legacyV18CutoverRecoveryPublishCanonicalTemp:
			if _, err := publishCanonicalV19Cutover(homeDir); err != nil {
				return state, fmt.Errorf("recover canonical v19 cutover: publish canonical temp: %w", err)
			}
		default:
			return state, fmt.Errorf("%w: unsupported recovery disposition=%q", errLegacyV18CutoverRecoveryExecutionUnsafe, state.Disposition)
		}
	}

	state, err := inspectLegacyV18CutoverRecovery(homeDir)
	if err != nil {
		return legacyV18CutoverRecoveryState{}, fmt.Errorf("recover canonical v19 cutover: inspect recovery authority after bounded transitions: %w", err)
	}
	return state, fmt.Errorf("%w: recovery did not converge after %d inspections; disposition=%s: %s", errLegacyV18CutoverRecoveryExecutionUnsafe, maxInspections, state.Disposition, state.Reason)
}
