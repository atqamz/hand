package cli

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var luvusCommands = map[string]handler{
	"pin": cmdLuvusPin,
}

func init() {
	commands["luvus"] = func(r *runner, args []string) error { return sub(r, args, "luvus", luvusCommands) }
}

func (r *runner) rootDir() (string, error) {
	if r.root != "" {
		return r.root, nil
	}
	return fleet.Root(r.env.Getenv)
}

func (r *runner) luvusBin() (string, error) {
	root, err := r.rootDir()
	if err != nil {
		return "", err
	}
	pin, ok, err := luvus.LoadPin(root)
	if err != nil {
		return "", err
	}
	if !ok {
		return harness.LookPath("luvus", r.env.Getenv("PATH"))
	}
	if err := pin.Verify(); err != nil {
		return "", fmt.Errorf("%w: %w", state.ErrConflict, err)
	}
	return pin.Path, nil
}

func (r *runner) pinLuvus(root, bin string) (luvus.Pin, error) {
	pin, err := luvus.Keep(r.ctx(), root, bin, r.env.Environ(), r.env.Now())
	if errors.Is(err, luvus.ErrNotLuvus) {
		return pin, fmt.Errorf("%w: %w", state.ErrInvalid, err)
	}
	return pin, err
}

func cmdLuvusPin(r *runner, args []string) error {
	fs := flags("luvus pin")
	if err := fs.Parse(args); err != nil {
		return usageError{fmt.Sprintf("luvus pin: %v", err)}
	}
	if fs.NArg() > 1 {
		return usageError{"usage: hand luvus pin [BIN]"}
	}
	root, err := r.rootDir()
	if err != nil {
		return err
	}
	bin := fs.Arg(0)
	if bin == "" {
		if bin, err = harness.LookPath("luvus", r.env.Getenv("PATH")); err != nil {
			return err
		}
	}
	if bin, err = filepath.Abs(bin); err != nil {
		return err
	}
	prev, had, err := luvus.LoadPin(root)
	if err != nil {
		return err
	}
	if had && prev.Path == bin {
		return fmt.Errorf("%w: %s is already the pinned copy; pin another binary", state.ErrInvalid, bin)
	}
	pin, err := r.pinLuvus(root, bin)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("version", pin.Version)
	d.Field("path", pin.Path)
	d.Field("sha256", pin.SHA256)
	d.Field("source", pin.Source)
	d.Field("previous", pinLabel(prev, had))
	d.Help("The running server keeps its binary until it restarts; check it with `hand luvus show`")
	return r.print(&d)
}

func pinLabel(p luvus.Pin, ok bool) string {
	if !ok {
		return "none"
	}
	return p.Version + " " + p.Path
}
