package herdr

func SessionName(fleetID string) string {
	return "hand-" + fleetID
}

func WorkspaceLabel(fleetID, projectName string) string {
	return "hand:" + fleetID + ":" + projectName
}

func LegacyWorkspaceLabel(projectName string) string {
	return "hand:" + projectName
}
