package update

import (
	"fmt"
	"strings"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
)

func PinNewer(p fleet.VersionPin, version string, schema int) bool {
	if p.Schema > schema {
		return true
	}
	_, pinOK := semver(p.Version)
	_, selfOK := semver(version)
	return pinOK && selfOK && Newer(p.Version, version)
}

func refuseOlderTarget(to Build, fleets []fleet.Entry) error {
	var newer []string
	for _, e := range fleets {
		if e.State != "ok" {
			continue
		}
		if p, found, err := fleet.ReadVersion(e.Home); err == nil && found && PinNewer(p, to.Version, to.Schema) {
			newer = append(newer, fmt.Sprintf("%s (hand %s, schema %d; %s)", e.Name, p.Version, p.Schema, e.Home))
		}
	}
	if len(newer) == 0 {
		return nil
	}
	return fmt.Errorf("%w: update: this update would install %s %s (schema %d), older than the hand that last opened %s; nothing was changed. %s To go back on purpose, stop the supervisor and board first, delete the fleet's hand.version (and restore a backup of its hand.db from backups/ only if the older hand refuses the schema), then run `hand update --channel %s`; see docs/update.md", state.ErrInvalid, to.Channel, to.Version, to.Schema, strings.Join(newer, ", "), newerHint(to), to.Channel)
}

func newerHint(to Build) string {
	if to.Channel == "edge" {
		return "No release of this channel is that new yet."
	}
	return "Run `hand update --channel edge` to get the newer hand."
}
