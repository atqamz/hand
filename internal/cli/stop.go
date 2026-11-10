package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/update"
)

const (
	stoppedNow = "stopped"
	notRunning = "not running"
)

func stopPIDFile(name, path string) (string, bool, error) {
	recorded, _ := os.ReadFile(path)
	pid, marker, exe, live := update.LivePID(path)
	switch {
	case pid == 0:
		return notRunning, false, nil
	case !live:
		if host, foreign := update.Foreign(path); foreign && host != "" && !sameHost(host) {
			return notRunning, false, nil
		}
		if err := removeIfSame(path, recorded); err != nil {
			return "", false, err
		}
		return notRunning, true, nil
	}
	self, err := os.Executable()
	if err == nil {
		self, err = update.Target(self)
	}
	if err != nil {
		return "", false, err
	}
	if exe != "" && exe != self {
		return "", false, fmt.Errorf("%w: the %s (process %d) runs %s, not this hand (%s), so it was left running; stop it with that binary", state.ErrConflict, name, pid, exe, self)
	}
	if err := stopOne(pid, marker); err != nil {
		return "", false, err
	}
	if err := removeIfSame(path, recorded); err != nil {
		return "", false, err
	}
	return stoppedNow, false, nil
}

func sameHost(name string) bool {
	self, _ := hostIdentity()
	return strings.EqualFold(name, self)
}

func removeIfSame(path string, recorded []byte) error {
	if now, err := os.ReadFile(path); err != nil || !bytes.Equal(now, recorded) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (r *runner) stopReport(name, path string, help ...string) error {
	host, foreign := update.Foreign(path)
	got, stale, err := stopPIDFile(name, path)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field(name, got)
	switch {
	case foreign && !stale && got == notRunning:
		d.Help("Left " + filepath.Base(path) + ": it names a process of host " + host + ", which this command never signals; stop it there")
	case stale && foreign:
		d.Help("Removed " + filepath.Base(path) + ": it names a process of another host or boot, which this command never signals")
	case stale:
		d.Help("Removed " + filepath.Base(path) + ": it named a process that is gone")
	}
	if got == stoppedNow {
		d.Help(help...)
	}
	return r.print(&d)
}

func cmdBoardStop(r *runner, args []string) error {
	if _, err := parse(flags("board stop"), args, 0); err != nil {
		return err
	}
	root, err := r.rootDir()
	if err != nil {
		return err
	}
	return r.stopReport("board", filepath.Join(root, "board.pid"),
		"Fleet watchers keep running; stop one with `hand watch stop` in its fleet",
		"Start the board again with `hand open` from a fleet, or `hand board --iroh`. `hand board` alone clears the recorded phone access. A systemd unit or LaunchAgent does not restart a board that stopped cleanly")
}

func cmdWatchStop(r *runner, args []string) error {
	if _, err := parse(flags("watch stop"), args, 0); err != nil {
		return err
	}
	if err := r.needHome(); err != nil {
		return err
	}
	root, err := r.rootDir()
	if err != nil {
		return err
	}
	_, _, _, board := update.LivePID(filepath.Join(root, "board.pid"))
	help := "Start it again with `hand watch`, or `hand supervisor start`"
	if board {
		help = "The running board starts this watcher again within 30 seconds when the fleet has a live supervisor, or `autoresume` set and an ended one. To keep it down, run `hand board stop` first, then `hand watch stop`"
	}
	return r.stopReport("watcher", filepath.Join(r.home, "watch.pid"), help)
}
