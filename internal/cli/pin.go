package cli

import (
	"fmt"
	"path/filepath"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/update"
)

func selfPin() fleet.VersionPin {
	return fleet.VersionPin{Version: Version, Commit: commit(), Schema: state.SchemaVersion}
}

func handLabel(p fleet.VersionPin) string {
	if p.Commit == "" {
		return fmt.Sprintf("hand %s (schema %d)", p.Version, p.Schema)
	}
	return fmt.Sprintf("hand %s, commit %s (schema %d)", p.Version, p.Commit, p.Schema)
}

func updateCommand() string {
	switch Channel {
	case "source":
		return "hand update --channel stable"
	case "edge":
		return "hand update"
	}
	return "hand update --channel edge"
}

func newerPin(p fleet.VersionPin) bool {
	return update.PinNewer(p, Version, state.SchemaVersion)
}

func (r *runner) pinRefusal(home string) error {
	p, found, err := fleet.ReadVersion(home)
	if err != nil {
		fmt.Fprintf(r.env.Stderr, "warning: cannot use %s: %v; leaving it as it is; delete it to let the next open write a new one\n", filepath.Join(home, fleet.VersionFile), err)
		return nil
	}
	if !found || !newerPin(p) {
		return nil
	}
	return fmt.Errorf("%w: this fleet was last opened by %s, newer than this hand, %s; nothing was changed. Run `%s` to get the newer hand, then try again. To go back to this hand on purpose, stop the supervisor and board, delete %s, and restore a backup of hand.db from %s only if this hand then refuses the schema", state.ErrInvalid, handLabel(p), handLabel(selfPin()), updateCommand(), filepath.Join(home, fleet.VersionFile), filepath.Join(home, "backups"))
}

func (r *runner) preflight() (*fleet.HostMarker, error) {
	if err := r.pinRefusal(r.home); err != nil {
		return nil, err
	}
	return r.judgeHost()
}

func (r *runner) writePin(home string) {
	self := selfPin()
	if p, found, err := fleet.ReadVersion(home); err != nil || found && (p == self || newerPin(p)) {
		return
	}
	if err := fleet.WriteVersion(home, self); err != nil {
		fmt.Fprintf(r.env.Stderr, "warning: cannot write %s: %v\n", filepath.Join(home, fleet.VersionFile), err)
	}
}

func (r *runner) doctorVersion(c *checks) {
	p, found, err := fleet.ReadVersion(r.home)
	switch {
	case err != nil:
		c.add("hand.version", "warn", err.Error()+"; delete it to let the next open write a new one")
	case !found:
		c.add("hand.version", "info", "no pin yet; opening this fleet with this hand writes "+fleet.VersionFile)
	case newerPin(p):
		c.add("hand.version", "fail", fmt.Sprintf("this fleet was last opened by %s, newer than this hand, %s; run `%s`", handLabel(p), handLabel(selfPin()), updateCommand()))
	case p != selfPin():
		c.add("hand.version", "info", fmt.Sprintf("this fleet was last opened by %s; opening it with this hand migrates it and pins %s", handLabel(p), handLabel(selfPin())))
	default:
		c.add("hand.version", "ok", "")
	}
}
