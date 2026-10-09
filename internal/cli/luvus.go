package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/update"
)

var luvusCommands = map[string]handler{
	"pin":  cmdLuvusPin,
	"show": cmdLuvusShow,
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
	if err == nil && ok {
		err = pin.Verify()
	}
	switch {
	case err != nil:
		return "", err
	case !ok:
		return harness.LookPath("luvus", r.env.Getenv("PATH"))
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

func (r *runner) fetchLuvus(root string) (luvus.Pin, error) {
	store := filepath.Join(root, "luvus")
	if err := os.MkdirAll(store, 0o755); err != nil {
		return luvus.Pin{}, err
	}
	tmp, err := os.MkdirTemp(store, ".fetch-")
	if err != nil {
		return luvus.Pin{}, err
	}
	defer os.RemoveAll(tmp)
	bin, err := update.FetchLuvus(r.ctx(), r.luvusBase(), luvus.Tested, runtime.GOOS, runtime.GOARCH, tmp)
	if err != nil {
		return luvus.Pin{}, err
	}
	return r.pinLuvus(root, bin)
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
	prev, had, err := luvus.LoadPin(root)
	previous := pinLabel(prev, had)
	switch {
	case errors.Is(err, luvus.ErrPinChanged):
		previous = "unreadable"
	case err != nil:
		return err
	}
	bin, fetch, lookErr := fs.Arg(0), false, error(nil)
	if bin == "" {
		if bin, lookErr = harness.LookPath("luvus", r.env.Getenv("PATH")); lookErr != nil {
			if had || err != nil {
				return lookErr
			}
			fetch = true
		}
	}
	if !fetch {
		if bin, err = filepath.Abs(bin); err != nil {
			return err
		}
	}
	session, err := r.fleetSession()
	if err != nil {
		return err
	}
	if had && prev.Path == bin {
		return fmt.Errorf("%w: %s is already the pinned copy; pin another binary", state.ErrInvalid, bin)
	}
	var pin luvus.Pin
	if fetch {
		if pin, err = r.fetchLuvus(root); err != nil {
			return fmt.Errorf("%w (download failed: %v)", lookErr, err)
		}
	} else if pin, err = r.pinLuvus(root, bin); err != nil {
		return err
	}
	var d toon.Doc
	d.Field("version", pin.Version)
	d.Field("path", pin.Path)
	d.Field("sha256", pin.SHA256)
	d.Field("source", pin.Source)
	d.Field("previous", previous)
	help := []string{"The running server keeps its binary until it restarts, and `hand attach` refuses a server of another version until the server restarts; check it with `hand luvus show`"}
	d.Help(append(help, r.serverFields(&d, session, pin, true)...)...)
	return r.print(&d)
}

func cmdLuvusShow(r *runner, args []string) error {
	if _, err := parse(flags("luvus show"), args, 0); err != nil {
		return err
	}
	root, err := r.rootDir()
	if err != nil {
		return err
	}
	pin, had, err := luvus.LoadPin(root)
	if err != nil {
		return err
	}
	session, err := r.fleetSession()
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("pin", pinLabel(pin, had))
	d.Help(r.serverFields(&d, session, pin, had)...)
	return r.print(&d)
}

func (r *runner) fleetSession() (string, error) {
	if r.home == "" && r.homeErr == nil {
		return "", nil
	}
	st, err := r.store()
	if err != nil {
		return "", err
	}
	_ = st.Close()
	return fleet.Session(r.fleet.ID), nil
}

func (r *runner) serverFields(d *toon.Doc, session string, pin luvus.Pin, pinned bool) []string {
	if session == "" {
		return nil
	}
	version, err := r.client(session).Version(r.ctx())
	server, match := serverMatch(pin, pinned, version, err)
	d.Field("server", server)
	d.Field("match", match)
	if match != "no" {
		return nil
	}
	return []string{"At a quiet time run `hand update` from a plain terminal; it stops the server of a quiet fleet, and the next hand command starts the pinned copy"}
}

func pinLabel(p luvus.Pin, ok bool) string {
	if !ok {
		return "none"
	}
	return p.Version + " " + p.Path
}

func serverMatch(pin luvus.Pin, pinned bool, version string, err error) (server, match string) {
	switch {
	case errors.Is(err, luvus.ErrUnreachable):
		return "not running", "not running"
	case err != nil || version == "":
		return "unknown", "unknown"
	case !pinned:
		return version, "unknown"
	case version == pin.Version:
		return version, "yes"
	}
	return version, "no"
}
