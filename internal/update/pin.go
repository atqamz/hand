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
	hint := "Run `hand update --channel edge` to get the newer hand."
	if to.Channel == "edge" {
		hint = "No release of this channel is that new yet."
	}
	return fmt.Errorf("%w: update: this update would install %s %s (schema %d), older than the hand that last opened %s; nothing was changed. %s To go back on purpose, restore a backup of hand.db from the fleet's backups/ and delete its hand.version, then run `hand update` again; see docs/update.md", state.ErrInvalid, to.Channel, to.Version, to.Schema, strings.Join(newer, ", "), hint)
}
