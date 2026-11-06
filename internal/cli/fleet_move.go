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
	fromFlag := set.String("from", "", "recorded home to rewrite; default: the folder this fleet is in")
	rewriteOnly := set.Bool("rewrite-only", false, "rewrite the recorded home to this folder without moving it")
	if err := set.Parse(args); err != nil {
		return helpOr(set, err, usageError{fmt.Sprintf("fleet move: %v", err)})
	}
	rest := set.Args()
	if *dry == *yes {
		return usageError{"fleet move: pass --dry-run to list the changes, or --yes to rewrite the database"}
	}
	if *rewriteOnly {
		if len(rest) > 1 {
			return usageError{fmt.Sprintf("fleet move: want at most one folder with --rewrite-only, got %d", len(rest))}
		}
	} else if len(rest) != 1 {
		return usageError{fmt.Sprintf("fleet move: want one destination folder after flags, got %d", len(rest))}
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
	from := home
	if *fromFlag != "" {
		if from, err = canonical(*fromFlag); err != nil {
			return err
		}
	}
	dest := home
	if *rewriteOnly {
		if len(rest) == 1 {
			to, err := canonical(rest[0])
			if err != nil {
				return err
			}
			if !samePlace(to, home) {
				return fmt.Errorf("%w: --rewrite-only rewrites the recorded paths to the folder it runs in (%s), not %s", state.ErrConflict, home, to)
			}
		}
	} else if dest, err = canonical(rest[0]); err != nil {
		return err
	}
	if pf, pt, pending, err := st.PendingMove(ctx); err != nil {
		return err
	} else if pending {
		return r.finishPendingMove(st, f, root, home, dest, pf, pt, *dry, *rewriteOnly, len(rest) == 1)
	}
	switch {
	case f.Home == "" && *fromFlag == "":
		return fmt.Errorf("%w: this database records no home, so the command cannot tell a fleet in place from a copy of one; pass `--from %s` if this fleet is in place, or `--from OLD` with the folder its recorded paths name", state.ErrConflict, home)
	case f.Home != "" && !samePlace(f.Home, home) && *fromFlag == "":
		return fmt.Errorf("%w: the database records this fleet's home as %s, but this folder is %s; run `hand fleet move --rewrite-only --from %s` here to rewrite the recorded paths", state.ErrConflict, f.Home, home, f.Home)
	case f.Home != "" && !samePlace(f.Home, from):
		return fmt.Errorf("%w: the database records this fleet's home as %s, not %s; pass `--from %s`", state.ErrConflict, f.Home, from, f.Home)
	}
	if !*rewriteOnly && !samePlace(from, home) {
		return fmt.Errorf("%w: %s is not the folder this fleet is in; run `hand fleet move --rewrite-only --from %s` here first, then run the move again", state.ErrConflict, from, from)
	}
	if samePlace(from, dest) {
		return fmt.Errorf("%w: %s is already this fleet's home", state.ErrConflict, dest)
	}
	registered, err := fleet.Home(root, f.ID)
	if err != nil {
		return err
	}
	if registered != home && !samePlace(registered, home) {
		return fmt.Errorf("%w: fleet %s is registered at %s, not %s; move the registered home or delete the copy", state.ErrConflict, f.ID, registered, home)
	}
	var empty bool
	if !*rewriteOnly {
		if empty, err = moveDestination(home, dest); err != nil {
			return err
		}
	}
	blockers, err := moveBlockers(ctx, st, home, root)
	if err != nil {
		return err
	}
	if len(blockers) != 0 {
		return fmt.Errorf("%w: %s", state.ErrConflict, strings.Join(blockers, "; "))
	}
	if *dry {
		rep, err := st.PlanHome(ctx, from, dest)
		if err != nil {
			return err
		}
		return r.printMoveDryRun(f, rep)
	}
	backup := moveBackup(home, r.env.Now().UTC())
	if err := st.Backup(ctx, backup); err != nil {
		return err
	}
	if *rewriteOnly {
		rep, err := st.RewriteHome(ctx, from, dest)
		if err != nil {
			return fmt.Errorf("%w; the database was not changed, the pre-move copy is %s", err, backup)
		}
		return r.printMoveRewritten(f, root, rep, backup)
	}
	if empty {
		if err := os.Remove(dest); err != nil {
			return err
		}
	}
	rep, err := st.MoveHome(ctx, from, dest)
	if err != nil {
		return fmt.Errorf("%w; the database was not changed, the pre-move copy is %s", err, backup)
	}
	if err := st.Close(); err != nil {
		return fmt.Errorf("%w: the database at %s now names %s, but closing it failed: %v; the pre-move database is %s; run `hand fleet move --yes %s` to finish the rename", state.ErrConflict, home, dest, err, backup, dest)
	}
	if err := fsx.Rename(home, dest); err != nil {
		return fmt.Errorf("%w: the database at %s now names %s, but the folder did not move: %v; the pre-move database is %s; run `hand fleet move --yes %s` to finish the rename, or undo it by copying %s over %s and removing %s and %s", state.ErrConflict, home, dest, err, backup, dest, backup, db, db+"-wal", db+"-shm")
	}
	if _, err := fleet.Register(root, f.ID, dest); err != nil {
		return fmt.Errorf("%w: the folder moved to %s, but %s still names %s: %v; run `hand fleet move --yes %s` in %s to finish, or `hand init` there", state.ErrConflict, dest, filepath.Join(root, "fleets", f.ID), home, err, dest, dest)
	}
	seq, hits, err := confirmMove(r, ctx, dest, from, dest)
	if err != nil {
		return fmt.Errorf("%w: the folder moved to %s, but recording fleet.moved failed: %v; run `hand fleet move --yes %s` in %s to finish", state.ErrConflict, dest, err, dest, dest)
	}
	return r.printMoveMoved(f, root, rep, seq, len(hits), backup)
}

func (r *runner) finishPendingMove(st *state.Store, f state.Fleet, root, home, dest, from, to string, dry, rewriteOnly, haveDest bool) error {
	if dry {
		var d toon.Doc
		d.Field("move", "pending")
		d.Field("fleet", f.Name)
		d.Field("id", f.ID)
		d.Field("from", from)
		d.Field("to", to)
		d.Field("home", home)
		d.Help("The move from " + shellQuote(from) + " to " + shellQuote(to) + " did not finish; run `hand fleet move --yes " + shellQuote(to) + "` in " + shellQuote(from) + " (or in " + shellQuote(to) + ") to finish it")
		return r.print(&d)
	}
	ctx := r.ctx()
	if samePlace(home, to) {
		if haveDest && !samePlace(dest, to) {
			return fmt.Errorf("%w: the move from %s to %s is pending and this folder is already %s; run `hand fleet move --yes %s` here to finish it first, then start the move to %s", state.ErrConflict, from, to, to, to, dest)
		}
		if _, err := fleet.Register(root, f.ID, to); err != nil {
			return fmt.Errorf("%w: the folder is at %s, but %s still names %s: %v; run `hand init` in %s", state.ErrConflict, to, filepath.Join(root, "fleets", f.ID), home, err, to)
		}
		seq, err := st.ConfirmMove(ctx, from, to)
		if err != nil {
			return fmt.Errorf("%w: the folder is at %s, but recording fleet.moved failed: %v", state.ErrConflict, to, err)
		}
		hits, err := st.VerifyHome(ctx, from, to)
		if err != nil {
			return err
		}
		return r.printMoveFinished(f, root, home, from, to, seq, len(hits))
	}
	if !samePlace(home, from) {
		return fmt.Errorf("%w: a move from %s to %s is pending, but this folder is %s; run the command in one of those folders", state.ErrConflict, from, to, home)
	}
	if rewriteOnly || !haveDest || !samePlace(dest, to) {
		return fmt.Errorf("%w: the move from %s to %s did not finish; run `hand fleet move --yes %s` in %s to finish it", state.ErrConflict, from, to, to, from)
	}
	backup, backupText := preMoveDatabase(home)
	empty, err := moveDestination(home, to)
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
	if empty {
		if err := os.Remove(to); err != nil {
			return err
		}
	}
	db := filepath.Join(home, "hand.db")
	if err := st.Close(); err != nil {
		return fmt.Errorf("%w: the database at %s already names %s, but closing it failed: %v; %s; run `hand fleet move --yes %s` again", state.ErrConflict, home, to, err, backupText, to)
	}
	if err := fsx.Rename(home, to); err != nil {
		undo := "; run `hand fleet move --yes " + shellQuote(to) + "` again"
		if backup != "" {
			undo = fmt.Sprintf("; run `hand fleet move --yes %s` again, or undo it by copying %s over %s and removing %s and %s", shellQuote(to), backup, db, db+"-wal", db+"-shm")
		}
		return fmt.Errorf("%w: the database at %s names %s, but the folder did not move: %v; %s%s", state.ErrConflict, home, to, err, backupText, undo)
	}
	if _, err := fleet.Register(root, f.ID, to); err != nil {
		return fmt.Errorf("%w: the folder moved to %s, but %s still names %s: %v; run `hand fleet move --yes %s` in %s to finish", state.ErrConflict, to, filepath.Join(root, "fleets", f.ID), home, err, to, to)
	}
	seq, hits, err := confirmMove(r, ctx, to, from, to)
	if err != nil {
		return fmt.Errorf("%w: the folder moved to %s, but recording fleet.moved failed: %v; run `hand fleet move --yes %s` in %s to finish", state.ErrConflict, to, err, to, to)
	}
	return r.printMoveResumed(f, root, home, from, to, seq, len(hits))
}

func confirmMove(r *runner, ctx context.Context, dir, from, to string) (int64, []state.MoveCell, error) {
	st, err := state.Open(filepath.Join(dir, "hand.db"), r.env.Now)
	if err != nil {
		return 0, nil, err
	}
	defer st.Close()
	seq, err := st.ConfirmMove(ctx, from, to)
	if err != nil {
		return 0, nil, err
	}
	hits, err := st.VerifyHome(ctx, from, to)
	return seq, hits, err
}

func (r *runner) printMoveDryRun(f state.Fleet, rep state.MoveReport) error {
	var d toon.Doc
	d.Field("move", "dry-run")
	d.Field("fleet", f.Name)
	d.Field("id", f.ID)
	d.Field("from", rep.From)
	d.Field("to", rep.To)
	d.Field("absolute", strconv.Itoa(rep.Absolute))
	d.Field("tilde", strconv.Itoa(rep.Tilde))
	rows := make([][]string, 0, len(rep.Cells))
	for _, c := range rep.Cells {
		rows = append(rows, []string{c.Table, c.Column, c.Ref, strconv.Itoa(c.Absolute), strconv.Itoa(c.Tilde)})
	}
	d.Rows("cells", []string{"table", "column", "ref", "absolute", "tilde"}, rows)
	help := []string{"Run `hand fleet move --yes " + shellQuote(rep.To) + "` to rewrite the database and move the folder"}
	d.Field("verification", strconv.Itoa(len(rep.Hits)))
	if len(rep.Hits) > 0 {
		d.Rows("remaining", []string{"table", "column", "ref"}, moveOnlyRows(rep.Hits))
		help = append(help, fmt.Sprintf("The verification would find %d old home path(s) in a spelling the rewrite does not change; edit them to name %s by hand, then run --yes again", len(rep.Hits), rep.To))
	}
	d.Help(help...)
	return r.print(&d)
}

func (r *runner) printMoveMoved(f state.Fleet, root string, rep state.MoveReport, seq int64, remaining int, backup string) error {
	var d toon.Doc
	d.Field("move", "moved")
	d.Field("fleet", f.Name)
	d.Field("id", f.ID)
	d.Field("from", rep.From)
	d.Field("to", rep.To)
	rows := [][]string{
		{"before", strconv.Itoa(rep.Absolute), strconv.Itoa(rep.Tilde), strconv.Itoa(len(rep.Cells))},
		{"after", strconv.Itoa(remaining), "0", "0"},
	}
	d.Rows("counts", []string{"when", "absolute", "tilde", "cells"}, rows)
	d.Field("verification", strconv.Itoa(remaining))
	d.Field("kept", fmt.Sprintf("event e%d (fleet.moved) names the old home as history", seq))
	d.Field("foreign_key_check", strconv.Itoa(rep.ForeignKeys))
	d.Field("integrity_check", rep.Integrity)
	d.Field("rows", "ok")
	d.Field("backup", filepath.Join(rep.To, filepath.Base(backup)))
	d.Field("home", rep.To)
	d.Field("registry", filepath.Join(root, "fleets", f.ID))
	help := []string{"Run `hand init` in " + rep.To + " to repair worktrees of projects that were inside the old home and now live there, and to refresh the skills"}
	d.Help(help...)
	return r.print(&d)
}

func (r *runner) printMoveRewritten(f state.Fleet, root string, rep state.MoveReport, backup string) error {
	var d toon.Doc
	d.Field("move", "rewritten")
	d.Field("fleet", f.Name)
	d.Field("id", f.ID)
	d.Field("from", rep.From)
	d.Field("to", rep.To)
	rows := [][]string{
		{"before", strconv.Itoa(rep.Absolute), strconv.Itoa(rep.Tilde), strconv.Itoa(len(rep.Cells))},
		{"after", strconv.Itoa(rep.AfterAbsolute), "0", "0"},
	}
	d.Rows("counts", []string{"when", "absolute", "tilde", "cells"}, rows)
	d.Field("verification", strconv.Itoa(rep.AfterAbsolute))
	d.Field("kept", fmt.Sprintf("event e%d (fleet.moved) names the old home as history", rep.EventSeq))
	d.Field("backup", backup)
	d.Field("home", rep.To)
	d.Field("registry", filepath.Join(root, "fleets", f.ID))
	help := []string{"The recorded paths now name " + rep.To + "; run `hand init` there to repair worktrees of projects inside it and to refresh the skills"}
	d.Help(help...)
	return r.print(&d)
}

func (r *runner) printMoveFinished(f state.Fleet, root, home, from, to string, seq int64, remaining int) error {
	var d toon.Doc
	d.Field("move", "finished")
	d.Field("fleet", f.Name)
	d.Field("id", f.ID)
	d.Field("from", from)
	d.Field("to", to)
	d.Field("verification", strconv.Itoa(remaining))
	d.Field("kept", fmt.Sprintf("event e%d (fleet.moved) names the old home as history", seq))
	d.Field("home", home)
	d.Field("registry", filepath.Join(root, "fleets", f.ID))
	d.Help("The move is complete; run `hand init` in " + to + " to repair worktrees and refresh the skills")
	return r.print(&d)
}

func (r *runner) printMoveResumed(f state.Fleet, root, home, from, to string, seq int64, remaining int) error {
	var d toon.Doc
	d.Field("move", "resumed")
	d.Field("fleet", f.Name)
	d.Field("id", f.ID)
	d.Field("from", from)
	d.Field("to", to)
	d.Field("verification", strconv.Itoa(remaining))
	d.Field("kept", fmt.Sprintf("event e%d (fleet.moved) names the old home as history", seq))
	d.Field("home", to)
	d.Field("registry", filepath.Join(root, "fleets", f.ID))
	d.Help("The move is complete; run `hand init` in " + to + " to repair worktrees and refresh the skills")
	return r.print(&d)
}

func moveOnlyRows(cells []state.MoveCell) [][]string {
	rows := make([][]string, 0, len(cells))
	for _, c := range cells {
		rows = append(rows, []string{c.Table, c.Column, c.Ref})
	}
	return rows
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
		return false, fmt.Errorf("%w: %s and %s are on different filesystems; copy the folder there, delete or move the original, run `hand init` in the copy, then run `hand fleet move --rewrite-only --from %s` in it", state.ErrConflict, home, dest, home)
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

func preMoveDatabase(home string) (string, string) {
	matches, err := filepath.Glob(filepath.Join(home, "hand.db.pre-move-*"))
	if err == nil && len(matches) != 0 {
		newest := matches[0]
		for _, m := range matches[1:] {
			if m > newest {
				newest = m
			}
		}
		return newest, "the pre-move database is " + newest
	}
	return "", "the pre-move database is the hand.db.pre-move-* copy beside the database"
}
