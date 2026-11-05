package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/sidecar"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/update"
)

func irohPath(root string) string { return filepath.Join(root, "board.iroh") }

func recordedIroh(root string) bool {
	_, err := os.Stat(irohPath(root))
	return err == nil
}

func recordIroh(root string, on bool) error {
	if on {
		return writeIroh(root, sidecar.Status{State: sidecar.Starting})
	}
	if err := os.Remove(irohPath(root)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func writeIroh(root string, st sidecar.Status) error {
	line := st.State
	switch {
	case st.State == sidecar.Ready:
		line += " " + st.ID + " " + st.Relay
	case st.Reason != "":
		line += " " + st.Reason
	}
	return writeFile(root, irohPath(root), line+"\n")
}

func (r *runner) doctorIroh(ctx context.Context, d *toon.Doc, c *checks, root, path string) {
	on := recordedIroh(root)
	bin, err := sidecar.Locate(selfPath(), path)
	if err != nil {
		if on {
			c.add("iroh", "warn", err.Error()+"; the board runs without it: install a release that carries it, as docs/phone.md says")
		} else {
			c.add("iroh", "info", "not installed; phone access over iroh needs hand-iroh: install a release that carries it, as docs/phone.md says")
		}
		return
	}
	version, err := sidecar.BinaryVersion(ctx, bin)
	if err != nil {
		c.add("iroh", "warn", err.Error())
		return
	}
	d.Field("iroh", version+" "+bin)
	_, _, _, up := update.LivePID(filepath.Join(root, "board.pid"))
	st, _ := readIroh(root)
	switch {
	case version != sidecar.Version:
		c.add("iroh", "warn", fmt.Sprintf("hand-iroh %s does not match the %s this hand needs; run `hand update`, or install the hand-iroh of the same release as this hand", version, sidecar.Version))
	case !on:
		c.add("iroh", "info", "installed, and off; turn it on by starting the board with `hand board --iroh`")
	case !up:
		c.add("iroh", "info", "on, but no board is running; the sidecar starts with the board")
	case st.State == sidecar.Ready:
		d.Field("iroh_id", st.ID)
		if st.Relay == "none" {
			c.add("iroh", "warn", "running, but it reached no relay within 10 seconds, so a phone elsewhere cannot dial it; check that this machine can reach the internet, then restart the board")
			return
		}
		c.add("iroh", "ok", "")
	case st.State == sidecar.Failed:
		c.add("iroh", "warn", st.Reason+"; restart the board to try again")
	default:
		c.add("iroh", "info", "starting; check again in a few seconds")
	}
}

func readIroh(root string) (sidecar.Status, bool) {
	b, err := os.ReadFile(irohPath(root))
	if err != nil {
		return sidecar.Status{}, false
	}
	state, rest, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
	st := sidecar.Status{State: state}
	if state == sidecar.Ready {
		st.ID, st.Relay, _ = strings.Cut(rest, " ")
	} else {
		st.Reason = rest
	}
	return st, true
}

func selfPath() string {
	exe, err := os.Executable()
	if err == nil {
		exe, err = update.Target(exe)
	}
	if err != nil {
		return ""
	}
	return exe
}

func (r *runner) newSidecar(root, peer string) *sidecar.Sidecar {
	return sidecar.New(sidecar.Options{
		Resolve:    sidecar.Resolve(selfPath(), r.env.Getenv("PATH")),
		Board:      peer,
		SecretFile: filepath.Join(root, "board.peer"),
		KeyDir:     filepath.Join(root, "iroh"),
		Dir:        root,
		OnChange: func(st sidecar.Status) {
			_ = writeIroh(root, st)
			switch {
			case st.State == sidecar.Ready:
				fmt.Fprintln(r.env.Stderr, "board: iroh: ready, endpoint ID", st.ID)
			case st.State == sidecar.Failed:
				fmt.Fprintln(r.env.Stderr, "board: iroh: failed:", st.Reason)
			case st.State == sidecar.Starting && st.Reason != "":
				fmt.Fprintln(r.env.Stderr, "board: iroh: restarting after:", st.Reason)
			}
		},
	})
}
