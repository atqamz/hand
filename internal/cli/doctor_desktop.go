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
	if !desktopasset.Supported(runtime.GOOS, runtime.GOARCH) {
		c.add("desktop", "info", desktopasset.Unsupported(runtime.GOOS, runtime.GOARCH))
		return
	}
	bin, err := desktopasset.Locate(selfPath(), path, runtime.GOOS)
	if err != nil {
		if state, where := launcher.Check(r.env.Getenv, ""); state == launcher.Elsewhere {
			c.add("desktop entry", "warn", where+" is left over and hand-desktop is not installed; run `hand update` to remove it")
		}
		c.add("desktop", "info", "not installed; run `hand update` to put hand-desktop next to hand")
		return
	}
	switch state, where := launcher.Check(r.env.Getenv, bin); state {
	case launcher.OK:
		c.add("desktop entry", "ok", "")
	case launcher.Missing:
		c.add("desktop entry", "warn", where+" is missing; run `hand update` to write the launcher entry")
	case launcher.Foreign:
		c.add("desktop entry", "warn", where+" is not Hand's launcher entry (no X-Hand-Managed=true line); hand update leaves it alone")
	default:
		c.add("desktop entry", "warn", where+" does not point at "+bin+"; run `hand update` to rewrite it")
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
