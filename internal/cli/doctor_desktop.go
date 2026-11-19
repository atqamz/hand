package cli

import (
	"context"
	"fmt"
	"runtime"

	"github.com/atqamz/hand/internal/buildstamp"
	"github.com/atqamz/hand/internal/desktopasset"
	"github.com/atqamz/hand/internal/launcher"
	"github.com/atqamz/hand/internal/toon"
)

func (r *runner) doctorDesktop(ctx context.Context, d *toon.Doc, c *checks, path string) {
	if !desktopasset.Supported(runtime.GOOS, runtime.GOARCH, Channel) {
		c.add("desktop", "info", desktopasset.Unsupported(runtime.GOOS, runtime.GOARCH))
		return
	}
	bin, err := desktopasset.Locate(selfPath(), path, runtime.GOOS, r.env.Getenv("HOME"))
	if err != nil {
		if state, where := launcher.Check(r.env.Getenv, ""); state == launcher.Elsewhere {
			c.add(entryRow(), "warn", where+" is left over and hand-desktop is not installed; run `hand update` to remove it")
		}
		c.add("desktop", "info", "not installed; run `hand update` to put hand-desktop next to hand")
		return
	}
	switch runtime.GOOS {
	case "windows":
		c.add("desktop window", "info", "Windows runs the window in "+Channel+" builds only; a real window has opened on a CI runner, not yet on a real Windows PC; if it does not start, `hand open` shows the Board in a browser")
	case "darwin":
		c.add("desktop window", "info", "untested on a real Mac: a real window has opened from Hand.app on a CI runner (arm64 only; the amd64 build has never run); a copy that arrives with a quarantine mark is stopped by Gatekeeper, so install it with `hand update`; if it does not start, `hand open` shows the Board in a browser")
	}
	switch state, where := launcher.Check(r.env.Getenv, bin); {
	case state == launcher.OK:
		c.add(entryRow(), "ok", "")
	case state == launcher.Missing:
		c.add(entryRow(), "warn", where+" is missing; run `hand update` to write the launcher entry")
	case state == launcher.Foreign:
		c.add(entryRow(), "warn", where+" is not Hand's launcher entry ("+launcher.Marker+"); hand update leaves it alone")
	default:
		c.add(entryRow(), "warn", where+" does not point at "+bin+"; run `hand update` to rewrite it")
	}
	version, gotCommit, err := desktopasset.BinaryVersion(ctx, bin)
	if err != nil {
		c.add("desktop", "warn", err.Error())
		return
	}
	want := commit()
	d.Field("desktop", version+" "+gotCommit+" "+bin)
	mismatch := fmt.Sprintf("hand-desktop %s %s does not match this hand's %s %s; run `hand update`", version, gotCommit, Version, want)
	switch {
	case version != Version:
		c.add("desktop", "warn", mismatch)
	case !buildstamp.Known(want) || !buildstamp.Known(gotCommit):
		c.add("desktop", "info", fmt.Sprintf("identity unverifiable: hand-desktop %s %s and this hand %s %s; a build without a commit stamp cannot be compared, so run `hand update`", version, gotCommit, Version, want))
	case gotCommit != want:
		c.add("desktop", "warn", mismatch)
	default:
		c.add("desktop", "ok", "")
	}
}

func entryRow() string {
	switch runtime.GOOS {
	case "windows":
		return "start menu shortcut"
	case "darwin":
		return "desktop app"
	}
	return "desktop entry"
}
