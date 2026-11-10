package update

import "github.com/atqamz/hand/internal/fleet"

func PinNewer(p fleet.VersionPin, version string, schema int) bool {
	if p.Schema > schema {
		return true
	}
	_, pinOK := semver(p.Version)
	_, selfOK := semver(version)
	return pinOK && selfOK && Newer(p.Version, version)
}
