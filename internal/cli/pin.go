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
	if Channel == "source" {
		return "hand update --channel stable"
	}
	return "hand update"
}

func newerPin(p fleet.VersionPin) bool {
	return update.Newer(p.Version, Version) || p.Schema > state.SchemaVersion
}

func (r *runner) pinRefusal() error {
	p, found, err := fleet.ReadVersion(r.home)
	if err != nil {
		fmt.Fprintf(r.env.Stderr, "warning: cannot read %s: %v; treating it as absent\n", filepath.Join(r.home, fleet.VersionFile), err)
		return nil
	}
	if !found || !newerPin(p) {
		return nil
	}
	return fmt.Errorf("%w: this fleet was last opened by %s, newer than this hand, %s; nothing was changed. Run `%s` to get the newer hand, then try again", state.ErrInvalid, handLabel(p), handLabel(selfPin()), updateCommand())
}

func (r *runner) preflight() (*fleet.HostMarker, error) {
	if err := r.pinRefusal(); err != nil {
		return nil, err
	}
	return r.judgeHost()
}

func (r *runner) writePin() {
	self := selfPin()
	if p, found, _ := fleet.ReadVersion(r.home); found && p == self {
		return
	}
	if err := fleet.WriteVersion(r.home, self); err != nil {
		fmt.Fprintf(r.env.Stderr, "warning: cannot write %s: %v\n", filepath.Join(r.home, fleet.VersionFile), err)
	}
}

func (r *runner) doctorVersion(c *checks) {
	p, found, err := fleet.ReadVersion(r.home)
	switch {
	case err != nil:
		c.add("hand.version", "warn", err.Error())
	case !found:
		c.add("hand.version", "info", "no pin yet; the next hand command that opens this fleet writes "+fleet.VersionFile)
	case newerPin(p):
		c.add("hand.version", "fail", fmt.Sprintf("this fleet was last opened by %s, newer than this hand, %s; run `%s`", handLabel(p), handLabel(selfPin()), updateCommand()))
	case p != selfPin():
		c.add("hand.version", "info", fmt.Sprintf("this fleet was last opened by %s; the next hand command that opens it migrates it and pins %s", handLabel(p), handLabel(selfPin())))
	default:
		c.add("hand.version", "ok", "")
	}
}
