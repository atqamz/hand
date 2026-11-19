package uninstall

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/desktopasset"
	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/fsx"
	"github.com/atqamz/hand/internal/launcher"
	"github.com/atqamz/hand/internal/sidecar"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/update"
)

const wizardUninstaller = "unins000.exe"

type Options struct {
	Target, Root, OS string
	Getenv           func(string) string
	DryRun           bool

	Owned          func(func(string) string) ([]string, string, error)
	RemoveLauncher func(func(string) string) (string, error)
	UserPath       func(dir string, apply bool) (bool, error)
}

type Item struct{ Path, Note string }

type Report struct {
	DryRun  bool
	Blocked []string
	Removed []Item
	Kept    []Item
	Help    []string
}

type step struct {
	items []Item
	run   func() error
}

func (o *Options) defaults() {
	if o.Owned == nil {
		o.Owned = launcher.Owned
	}
	if o.RemoveLauncher == nil {
		o.RemoveLauncher = launcher.Remove
	}
	if o.UserPath == nil {
		o.UserPath = userPath
	}
}

func withExt(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

func draftDir(getenv func(string) string, goos string) string {
	if dir := strings.TrimSpace(getenv("XDG_STATE_HOME")); dir != "" {
		return filepath.Join(dir, "hand-desktop", "drafts")
	}
	if goos == "windows" {
		if dir := strings.TrimSpace(getenv("LOCALAPPDATA")); dir != "" {
			return filepath.Join(dir, "hand-desktop", "drafts")
		}
	}
	home := strings.TrimSpace(getenv("HOME"))
	if home == "" {
		home = strings.TrimSpace(getenv("USERPROFILE"))
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "hand-desktop", "drafts")
}

func Run(o Options) (Report, error) {
	o.defaults()
	rep := Report{DryRun: o.DryRun}
	bin := filepath.Dir(o.Target)
	win := o.OS == "windows"

	if win {
		if un := filepath.Join(filepath.Dir(bin), wizardUninstaller); isFile(un) {
			rep.Blocked = []string{"this install was made by the setup wizard, so it is removed from Windows Settings, Apps, Hand, or by running " + un + "; nothing was changed"}
			return rep.refuse(o.DryRun)
		}
	}

	rep.Blocked = o.running()

	var steps []step
	add := func(run func() error, items ...Item) { steps = append(steps, step{items, run}) }
	planned := func(path string) bool {
		for _, s := range steps {
			for _, it := range s.items {
				if it.Path == path || win && strings.EqualFold(it.Path, path) {
					return true
				}
			}
		}
		return false
	}
	file := func(path, what string) {
		fi, err := os.Lstat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist) || planned(path):
		case err != nil:
			rep.Kept = append(rep.Kept, Item{path, "not checked: " + err.Error()})
		case !fi.Mode().IsRegular():
			rep.Kept = append(rep.Kept, Item{path, "not a file that Hand placed: it is a symlink or a folder"})
		default:
			add(func() error { return remove(path) }, Item{path, what})
		}
	}

	if o.OS != "darwin" {
		file(filepath.Join(bin, withExt(desktopasset.Name, o.OS)), "window")
	}
	file(filepath.Join(bin, withExt(sidecar.Name, o.OS)), "hand-iroh")
	if win {
		file(filepath.Join(bin, "hand.ico"), "icon")
	}

	found, foreign, err := o.Owned(o.Getenv)
	if err != nil {
		rep.Help = append(rep.Help, fmt.Sprintf("the launcher entry was not checked (%v)", err))
	}
	if foreign != "" {
		rep.Kept = append(rep.Kept, Item{foreign, "not Hand's launcher entry (" + launcher.Marker + ")"})
	}
	var entries []Item
	for _, p := range found {
		if !planned(p) {
			entries = append(entries, Item{p, "launcher entry"})
		}
	}
	if len(entries) > 0 {
		add(func() error {
			skipped, err := o.RemoveLauncher(o.Getenv)
			if err == nil && skipped != "" {
				err = fmt.Errorf("%s is not Hand's launcher entry", skipped)
			}
			return err
		}, entries...)
	}

	if win {
		for _, p := range update.OldFiles(o.Target) {
			file(p, "old file")
		}
		onPath, err := o.UserPath(bin, false)
		if err != nil {
			rep.Help = append(rep.Help, fmt.Sprintf("the user PATH was not checked (%v)", err))
		}
		if onPath {
			add(func() error { _, err := o.UserPath(bin, true); return err }, Item{bin, "user PATH entry"})
		}
	}

	var last *step
	if fi, err := os.Lstat(o.Target); err == nil && fi.Mode().IsRegular() {
		what := "hand"
		run := func() error { return remove(o.Target) }
		if win {
			old := o.Target + ".old"
			what = "hand (renamed to " + filepath.Base(old) + ")"
			run = func() error {
				if err := fsx.Rename(o.Target, old); err != nil {
					return err
				}
				rep.Help = append(rep.Help, "A running program cannot delete its own file, so it was renamed to "+old+". Once this command has exited, delete it: `Remove-Item -LiteralPath "+"'"+strings.ReplaceAll(old, "'", "''")+"'"+"`")
				return nil
			}
		}
		last = &step{[]Item{{o.Target, what}}, run}
	}

	if len(rep.Blocked) > 0 && !o.DryRun {
		return rep.refuse(false)
	}

	var failed []string
	for _, s := range steps {
		if o.DryRun {
			rep.Removed = append(rep.Removed, s.items...)
			continue
		}
		if err := s.run(); err != nil {
			failed = append(failed, err.Error())
			for _, it := range s.items {
				rep.Kept = append(rep.Kept, Item{it.Path, "not removed: " + err.Error()})
			}
			continue
		}
		rep.Removed = append(rep.Removed, s.items...)
	}
	switch {
	case last == nil:
	case o.DryRun:
		rep.Removed = append(rep.Removed, last.items...)
	case len(failed) > 0:
		rep.Kept = append(rep.Kept, Item{o.Target, "kept so that you can run `hand uninstall` again"})
	default:
		if err := last.run(); err != nil {
			failed = append(failed, err.Error())
			rep.Kept = append(rep.Kept, Item{o.Target, "not removed: " + err.Error()})
		} else {
			rep.Removed = append(rep.Removed, last.items...)
		}
	}

	rep.keep(o)
	if len(failed) > 0 {
		return rep, fmt.Errorf("uninstall: %d step(s) failed: %s", len(failed), strings.Join(failed, "; "))
	}
	return rep, nil
}

func (rep Report) refuse(dry bool) (Report, error) {
	if dry {
		return rep, nil
	}
	return rep, fmt.Errorf("%w: uninstall removed nothing: %s", state.ErrConflict, strings.Join(rep.Blocked, "; "))
}

func remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func isFile(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular()
}

func (o Options) running() []string {
	var out []string
	live := func(path, what, stop string) {
		if pid, _, from, ok := update.LivePID(path); ok && (from == "" || from == o.Target) {
			out = append(out, fmt.Sprintf("%s is running (process %d); %s", what, pid, stop))
		}
	}
	live(filepath.Join(o.Root, "board.pid"), "the board", "stop it with `hand board stop`")
	homes, _ := fleet.Homes(o.Root)
	for _, home := range homes {
		live(filepath.Join(home, "watch.pid"), "the watcher in "+home, "run `hand watch stop` in that folder")
	}
	return out
}

func (rep *Report) keep(o Options) {
	homes, err := fleet.Homes(o.Root)
	if err != nil {
		rep.Help = append(rep.Help, fmt.Sprintf("the fleets were not listed (%v)", err))
	}
	for _, h := range homes {
		rep.keepIf(h, "fleet")
	}
	rep.keepIf(o.Root, "Hand's state: fleet index, worktrees, backups")
	if d := draftDir(o.Getenv, o.OS); d != "" {
		rep.keepIf(filepath.Dir(d), "the window's composer drafts and preferences")
	}
}

func (rep *Report) keepIf(path, why string) {
	if _, err := os.Lstat(path); err == nil {
		rep.Kept = append(rep.Kept, Item{path, why})
	}
}

func Render(rep Report) *toon.Doc {
	var d toon.Doc
	status := "removed"
	switch {
	case len(rep.Blocked) > 0 && rep.DryRun:
		status = "would refuse"
	case rep.DryRun:
		status = "would remove"
	case len(rep.Removed) == 0:
		status = "nothing to remove"
	}
	d.Field("status", status)
	if len(rep.Blocked) > 0 {
		d.List("blocked", rep.Blocked)
	}
	rows := func(items []Item) [][]string {
		out := [][]string{}
		for _, it := range items {
			out = append(out, []string{it.Path, it.Note})
		}
		return out
	}
	d.Rows("removed", []string{"path", "what"}, rows(rep.Removed))
	d.Rows("kept", []string{"path", "why"}, rows(rep.Kept))
	d.Help(rep.Help...)
	return &d
}
