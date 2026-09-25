package store

import (
	"errors"
	"fmt"
)

var errLegacyV18CutoverRecoveryExecutionUnsafe = errors.New("v19 cutover recovery execution is not mechanically safe")

// LegacyV18CutoverDriftGate re-observes the frozen baseline after a positive boot witness.
// Any verdict other than "pass" refuses with that verdict as the disposition.
type LegacyV18CutoverDriftGate func(homeDir string, frozen LegacyV18CutoverFrozenEvidence) (verdict, detail string)

// Executes only recovery work already authorized by the read-only classifier, in one MigrationLock
// hold. Before building or publishing from a frozen bridge it requires the boot witness and the drift gate.
func recoverCanonicalV19Cutover(homeDir string, driftGate LegacyV18CutoverDriftGate) (legacyV18CutoverRecoveryState, error) {
	const maxInspections = 3 // rebuild -> publish -> canonical authority

	initial, err := inspectLegacyV18CutoverRecovery(homeDir)
	if err != nil {
		return legacyV18CutoverRecoveryState{}, fmt.Errorf("recover canonical v19 cutover: inspect recovery authority: %w", err)
	}
	if initial.Disposition != legacyV18CutoverRecoveryRebuildCanonicalTemp && initial.Disposition != legacyV18CutoverRecoveryPublishCanonicalTemp {
		if initial.Disposition == legacyV18CutoverRecoveryRefuse {
			return initial, fmt.Errorf("%w: recovery disposition=%s: %s", errLegacyV18CutoverRecoveryExecutionUnsafe, initial.Disposition, initial.Reason)
		}
		return initial, nil
	}
	releaseMigration, err := Lock(homeDir, MigrationLock, true)
	if err != nil {
		return initial, fmt.Errorf("recover canonical v19 cutover: acquire MigrationLock: %w", err)
	}
	defer releaseMigration()

	gated := false
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
		case legacyV18CutoverRecoveryRebuildCanonicalTemp, legacyV18CutoverRecoveryPublishCanonicalTemp:
			if !gated {
				if refusal, refused := checkLegacyV18CutoverCompletion(homeDir, state, driftGate); refused {
					return refusal, fmt.Errorf("%w: recovery disposition=%s: %s", errLegacyV18CutoverRecoveryExecutionUnsafe, refusal.Disposition, refusal.Reason)
				}
				gated = true
			}
			if state.Disposition == legacyV18CutoverRecoveryRebuildCanonicalTemp {
				if _, err := rebuildCanonicalV19CutoverTempLocked(homeDir); err != nil {
					return state, fmt.Errorf("recover canonical v19 cutover: rebuild canonical temp: %w", err)
				}
			} else if _, err := publishCanonicalV19CutoverLocked(homeDir); err != nil {
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

// #348 revision 4 completion steps 1, 4, and 5; every refusal leaves every file unchanged.
func checkLegacyV18CutoverCompletion(homeDir string, state legacyV18CutoverRecoveryState, driftGate LegacyV18CutoverDriftGate) (legacyV18CutoverRecoveryState, bool) {
	refuse := func(disposition legacyV18CutoverRecoveryDisposition, reason string) (legacyV18CutoverRecoveryState, bool) {
		state.Disposition, state.Reason = disposition, reason
		return state, true
	}
	platform := legacyV18CutoverPlatform()
	if err := checkLegacyV18CutoverFilesystem(Dir(homeDir), platform); err != nil {
		return refuse(legacyV18CutoverRecoveryRefuse, err.Error())
	}
	bridge, err := discoverLegacyV18CutoverFrozenBridge(homeDir)
	if err != nil {
		return refuse(legacyV18CutoverRecoveryRefuse, fmt.Sprintf("active frozen bridge is not exact recovery evidence: %v", err))
	}
	witness, reason := legacyV18CutoverWitnessUnknown, ""
	if current, err := readLegacyV18CutoverBootEvidence(platform); err != nil {
		reason = err.Error()
	} else {
		witness, reason = evaluateLegacyV18CutoverBootWitness(bridge.Evidence, current)
	}
	if witness != legacyV18CutoverWitnessPositive {
		return refuse(legacyV18CutoverRecoveryDisposition(witness), reason)
	}
	frozen, err := InspectLegacyV18CutoverFrozenEvidence(homeDir)
	if err != nil {
		return refuse(legacyV18CutoverRecoveryRefuse, err.Error())
	}
	if driftGate == nil {
		return refuse(legacyV18CutoverRecoveryRefuse, "no drift gate was supplied")
	}
	if verdict, detail := driftGate(homeDir, frozen); verdict != "pass" {
		return refuse(legacyV18CutoverRecoveryDisposition(verdict), detail)
	}
	return state, false
}
