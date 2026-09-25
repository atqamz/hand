package store

// CanonicalV19CutoverRecovery exposes the bounded recovery classification,
// without treating archive paths or advisory markers as caller authority.
type CanonicalV19CutoverRecovery struct {
	Disposition string
	Reason      string
	MigrationID string
	FleetID     string
}

// CanonicalV19CutoverLegacySource is the disposition of an exact, unfrozen v0.7.2 source.
const CanonicalV19CutoverLegacySource = string(legacyV18CutoverRecoveryLegacySource)

// InspectCanonicalV19Cutover classifies existing recovery evidence read-only.
func InspectCanonicalV19Cutover(homeDir string) (CanonicalV19CutoverRecovery, error) {
	state, err := inspectLegacyV18CutoverRecovery(homeDir)
	return canonicalV19CutoverRecoverySummary(state), err
}

// RecoverCanonicalV19Cutover resumes only an already-frozen cutover with exact preserved
// evidence, after the boot witness and the drift gate, in one MigrationLock hold.
func RecoverCanonicalV19Cutover(homeDir string, driftGate LegacyV18CutoverDriftGate) (CanonicalV19CutoverRecovery, error) {
	state, err := recoverCanonicalV19Cutover(homeDir, driftGate)
	return canonicalV19CutoverRecoverySummary(state), err
}

// AbortCanonicalV19Cutover returns a frozen, unpublished home to its exact pre-freeze legacy DB.
func AbortCanonicalV19Cutover(homeDir string) (CanonicalV19CutoverRecovery, error) {
	aborted, err := abortLegacyV18CutoverFreeze(homeDir)
	return CanonicalV19CutoverRecovery{Disposition: aborted.Disposition, MigrationID: aborted.MigrationID, Reason: aborted.Record}, err
}

func canonicalV19CutoverRecoverySummary(state legacyV18CutoverRecoveryState) CanonicalV19CutoverRecovery {
	return CanonicalV19CutoverRecovery{
		Disposition: string(state.Disposition),
		Reason:      state.Reason,
		MigrationID: state.MigrationID,
		FleetID:     state.FleetID,
	}
}
