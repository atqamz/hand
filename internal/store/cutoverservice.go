package store

// CanonicalV19CutoverRecovery exposes the bounded recovery classification,
// without treating archive paths or advisory markers as caller authority.
type CanonicalV19CutoverRecovery struct {
	Disposition string
	Reason      string
	MigrationID string
	FleetID     string
}

// InspectCanonicalV19Cutover classifies existing recovery evidence read-only.
func InspectCanonicalV19Cutover(homeDir string) (CanonicalV19CutoverRecovery, error) {
	state, err := inspectLegacyV18CutoverRecovery(homeDir)
	return canonicalV19CutoverRecoverySummary(state), err
}

// RecoverCanonicalV19Cutover resumes only an already-frozen cutover with exact
// preserved evidence. Each mutation revalidates authority under MigrationLock.
func RecoverCanonicalV19Cutover(homeDir string) (CanonicalV19CutoverRecovery, error) {
	state, err := recoverCanonicalV19Cutover(homeDir)
	return canonicalV19CutoverRecoverySummary(state), err
}

func canonicalV19CutoverRecoverySummary(state legacyV18CutoverRecoveryState) CanonicalV19CutoverRecovery {
	return CanonicalV19CutoverRecovery{
		Disposition: string(state.Disposition),
		Reason:      state.Reason,
		MigrationID: state.MigrationID,
		FleetID:     state.FleetID,
	}
}
