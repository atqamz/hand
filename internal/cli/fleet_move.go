package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/fsx"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/update"
)

func cmdFleetMove(r *runner, args []string) error {
	set := flags("fleet move")
	dry := set.Bool("dry-run", false, "list every cell that would change and move nothing")
	yes := set.Bool("yes", false, "rewrite the database, then move the folder")
	rest, err := parse(set, args, 1)
	if err != nil {
		return err
	}
	if *dry == *yes {
		return usageError{"fleet move: pass --dry-run to list the changes, or --yes to rewrite the database and move the folder"}
	}
	dest, err := canonical(rest[0])
	if err != nil {
		return err
	}
	home, err := resolveHome(r.env, r.home, r.getwd)
	if err != nil {
		return err
	}
	if home == "" {
		return fmt.Errorf("%w: not inside a fleet home; cd into one or pass --home DIR", state.ErrNotFound)
	}
	db := filepath.Join(home, "hand.db")
	if _, err := os.Stat(db); err != nil {
		return fmt.Errorf("%w: no hand home at %s; run `hand init`", state.ErrNotFound, home)
	}
	root, err := fleet.Root(r.env.Getenv)
	if err != nil {
		return err
	}
	var st *state.Store
	if *dry {
		st, err = state.OpenRead(db)
	} else {
		st, err = state.Open(db, r.env.Now)
	}
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := r.ctx()
	f, err := st.Fleet(ctx)
	if err != nil {
		return err
	}
	registered, err := fleet.Home(root, f.ID)
	if err != nil {
		return err
	}
	if registered != home && !samePlace(registered, home) {
		return fmt.Errorf("%w: fleet %s is registered at %s, not %s; move the registered home or delete the copy", state.ErrConflict, f.ID, registered, home)
	}
	empty, err := moveDestination(home, dest)
	if err != nil {
		return err
	}
	blockers, err := moveBlockers(ctx, st, home, root)
	if err != nil {
		return err
	}
	if len(blockers) != 0 {
		return fmt.Errorf("%w: %s", state.ErrConflict, strings.Join(blockers, "; "))
	}
	if *dry {
		rep, err := st.PlanHome(ctx, home, dest)
		if err != nil {
			return err
		}
		var d toon.Doc
		d.Field("move", "dry-run")
		d.Field("fleet", f.Name)
		d.Field("id", f.ID)
		d.Field("from", home)
		d.Field("to", dest)
		d.Field("absolute", strconv.Itoa(rep.Absolute))
		d.Field("tilde", strconv.Itoa(rep.Tilde))
		rows := make([][]string, 0, len(rep.Cells))
		for _, c := range rep.Cells {
			rows = append(rows, []string{c.Table, c.Column, c.Ref, strconv.Itoa(c.Absolute), strconv.Itoa(c.Tilde)})
		}
		d.Rows("cells", []string{"table", "column", "ref", "absolute", "tilde"}, rows)
		d.Help("Run `hand fleet move --yes " + dest + "` to rewrite the database and move the folder")
		return r.print(&d)
	}
	backup := moveBackup(home, r.env.Now().UTC())
	if err := st.Backup(ctx, backup); err != nil {
		return err
	}
	if empty {
		if err := os.Remove(dest); err != nil {
			return err
		}
	}
	rep, err := st.MoveHome(ctx, home, dest)
	if err != nil {
		return fmt.Errorf("%w; the database was not changed, the pre-move copy is %s", err, backup)
	}
	if err := st.Close(); err != nil {
		return fmt.Errorf("%w: the database at %s now names %s, but closing it failed: %v; the pre-move database is %s", state.ErrConflict, home, dest, err, backup)
	}
	if err := fsx.Rename(home, dest); err != nil {
		return fmt.Errorf("%w: the database at %s now names %s, but the folder did not move: %v; the pre-move database is %s; finish the move with `hand fleet move --yes %s`, or undo it by copying %s over %s and removing %s and %s", state.ErrConflict, home, dest, err, backup, dest, backup, db, db+"-wal", db+"-shm")
	}
	if _, err := fleet.Register(root, f.ID, dest); err != nil {
		return fmt.Errorf("%w: the folder moved to %s, but %s still names %s: %v; run `hand init` in %s", state.ErrConflict, dest, filepath.Join(root, "fleets", f.ID), home, err, dest)
	}
	var d toon.Doc
	d.Field("move", "moved")
	d.Field("fleet", f.Name)
	d.Field("id", f.ID)
	d.Field("from", home)
	d.Field("to", dest)
	rows := [][]string{
		{"before", strconv.Itoa(rep.Absolute), strconv.Itoa(rep.Tilde), strconv.Itoa(len(rep.Cells))},
		{"after", strconv.Itoa(rep.AfterAbsolute), strconv.Itoa(rep.AfterTilde), "0"},
	}
	d.Rows("counts", []string{"when", "absolute", "tilde", "cells"}, rows)
	d.Field("verification", "0")
	d.Field("kept", fmt.Sprintf("event e%d (fleet.moved) names the old home as history", rep.EventSeq))
	d.Field("foreign_key_check", strconv.Itoa(rep.ForeignKeys))
	d.Field("integrity_check", rep.Integrity)
	d.Field("rows", "ok")
	d.Field("backup", filepath.Join(dest, filepath.Base(backup)))
	d.Field("home", dest)
	d.Field("registry", filepath.Join(root, "fleets", f.ID))
	d.Help("Run `hand init` in " + dest + " to repair worktrees of projects inside the old home and refresh the skills")
	return r.print(&d)
}

func samePlace(a, b string) bool {
	if samePath(a, b) {
		return true
	}
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(x, y)
}

func moveDestination(home, dest string) (bool, error) {
	if samePlace(home, dest) {
		return false, fmt.Errorf("%w: %s is already this fleet's home", state.ErrConflict, dest)
	}
	if within(dest, home) || within(home, dest) {
		return false, fmt.Errorf("%w: %s and %s are nested; move the fleet to a folder of its own", state.ErrConflict, home, dest)
	}
	parent := filepath.Dir(dest)
	info, err := os.Stat(parent)
	if err != nil {
		return false, fmt.Errorf("%w: the destination's folder %s does not exist: %v", state.ErrConflict, parent, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%w: the destination's folder %s is not a folder", state.ErrConflict, parent)
	}
	empty := false
	probe := parent
	if info, err := os.Stat(dest); err == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("%w: the destination %s is a file", state.ErrConflict, dest)
		}
		entries, err := os.ReadDir(dest)
		if err != nil {
			return false, err
		}
		if len(entries) != 0 {
			return false, fmt.Errorf("%w: the destination %s exists and is not empty (%d entries)", state.ErrConflict, dest, len(entries))
		}
		empty = true
		probe = dest
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	same, err := fsx.SameDevice(home, probe)
	if err != nil {
		return false, err
	}
	if !same {
		return false, fmt.Errorf("%w: %s and %s are on different filesystems; copy the folder there, then run `hand init` in the copy", state.ErrConflict, home, dest)
	}
	return empty, nil
}

func moveBlockers(ctx context.Context, st *state.Store, home, root string) ([]string, error) {
	var blockers []string
	if sup, ok, err := st.LiveSupervisor(ctx); err != nil {
		return nil, err
	} else if ok {
		blockers = append(blockers, fmt.Sprintf("supervisor %s is %s; run `hand supervisor stop`", state.SupervisorRef(sup.ID), sup.Status))
	}
	if attempts, err := st.LiveAttempts(ctx); err != nil {
		return nil, err
	} else if len(attempts) != 0 {
		refs := make([]string, 0, len(attempts))
		for _, a := range attempts {
			refs = append(refs, fmt.Sprintf("%s is %s on t%d", state.AttemptRef(a.ID), a.Status, a.TaskID))
		}
		blockers = append(blockers, strings.Join(refs, ", ")+"; run `hand attempt stop aN`")
	}
	if pid, _, _, live := update.LivePID(filepath.Join(home, "watch.pid")); live {
		blockers = append(blockers, fmt.Sprintf("the watcher is running (pid %d); run `hand watch stop`", pid))
	}
	if pid, _, _, live := update.LivePID(filepath.Join(root, "board.pid")); live {
		blockers = append(blockers, fmt.Sprintf("the board is running (pid %d); run `hand board stop`", pid))
	}
	if inputs, err := st.PendingSupervisorInputs(ctx); err != nil {
		return nil, err
	} else if len(inputs) != 0 {
		blockers = append(blockers, fmt.Sprintf("%d supervisor input(s) are pending delivery; start the supervisor, then stop it", len(inputs)))
	}
	if timers, err := st.Timers(ctx); err != nil {
		return nil, err
	} else if len(timers) != 0 {
		t := timers[0]
		blockers = append(blockers, fmt.Sprintf("timer %s is armed for %s; run `hand wake cancel %s`", state.TimerRef(t.ID), t.Due.UTC().Format(time.RFC3339), state.TimerRef(t.ID)))
	}
	return blockers, nil
}

func moveBackup(home string, now time.Time) string {
	stamp := now.Format("20060102T150405")
	path := filepath.Join(home, "hand.db.pre-move-"+stamp)
	for n := 2; ; n++ {
		_, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return path
		}
		if err != nil {
			return path
		}
		path = filepath.Join(home, fmt.Sprintf("hand.db.pre-move-%s-%d", stamp, n))
	}
}
