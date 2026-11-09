package cli

import (
	"context"
	"fmt"
	"runtime"

	"github.com/atqamz/hand/internal/desktopasset"
	"github.com/atqamz/hand/internal/toon"
)

func (r *runner) doctorDesktop(ctx context.Context, d *toon.Doc, c *checks, path string) {
	if !desktopasset.Supported(runtime.GOOS, runtime.GOARCH) {
		c.add("desktop", "info", desktopasset.Unsupported(runtime.GOOS, runtime.GOARCH))
		return
	}
	bin, err := desktopasset.Locate(selfPath(), path)
	if err != nil {
		c.add("desktop", "info", "not installed; run `hand update` to put hand-desktop next to hand")
		return
	}
	version, gotCommit, err := desktopasset.BinaryVersion(ctx, bin)
	if err != nil {
		c.add("desktop", "warn", err.Error())
		return
	}
	d.Field("desktop", version+" "+gotCommit+" "+bin)
	if version != Version || gotCommit != commit() {
		c.add("desktop", "warn", fmt.Sprintf("hand-desktop %s %s does not match this hand's %s %s; run `hand update`", version, gotCommit, Version, commit()))
		return
	}
	c.add("desktop", "ok", "")
}
