package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/update"
)

func adoptOlds(root string, f state.Fleet, home string, extra []string) (olds []string, due bool, err error) {
	rec, err := fleet.ReadHomes(home)
	if err != nil {
		return nil, false, err
	}
	due = len(extra) > 0 ||
		rec.Current != "" && !samePlace(rec.Current, home) ||
		f.Home != "" && !samePlace(f.Home, home)
	registered, _ := fleet.Home(root, f.ID)
	known := slices.Concat(extra, []string{rec.Current, f.Home, registered}, rec.Homes)
	for _, c := range known {
		if c != "" && !samePlace(c, home) && !slices.ContainsFunc(olds, func(o string) bool { return samePath(o, c) }) {
			olds = append(olds, c)
		}
	}
	return olds, due && len(olds) > 0, nil
}

func (r *runner) adoptBlockers(ctx context.Context, st *state.Store, f state.Fleet, home string, olds []string) ([]string, error) {
	var blockers []string
	if sup, ok, err := st.LiveSupervisor(ctx); err != nil {
		return nil, err
	} else if ok && sup.PID > 0 && rootAlive(sup.PID, sup.StartMarker) {
		blockers = append(blockers, fmt.Sprintf("supervisor %s is running (pid %d); run `hand supervisor stop`", state.SupervisorRef(sup.ID), sup.PID))
	}
	held, err := watchLocked(home)
	if err != nil {
		return nil, err
	}
	if held {
		pid, _, _, _ := update.LivePID(filepath.Join(home, "watch.pid"))
		blockers = append(blockers, fmt.Sprintf("the watcher is running (pid %d); run `hand watch stop`", pid))
	}
	if registered, _ := fleet.Home(r.root, f.ID); registered != "" {
		if pid, _, _, live := update.LivePID(filepath.Join(r.root, "board.pid")); live {
			blockers = append(blockers, fmt.Sprintf("the board is running (pid %d) and serves this fleet; run `hand board stop`", pid))
		}
	}
	for _, old := range olds {
		if rel, err := filepath.Rel(old, home); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			blockers = append(blockers, fmt.Sprintf("the new home %s is inside the old home %s, so the rewritten paths would still name the old one; move the fleet out of %s first", home, old, old))
		}
	}
	return blockers, nil
}

func (r *runner) adoptRefusal(ctx context.Context, st *state.Store, f state.Fleet, home string, olds []string) ([]string, error) {
	if err := fleet.RefuseCopies(f.ID, olds); err != nil {
		return []string{err.Error()}, nil
	}
	return r.adoptBlockers(ctx, st, f, home, olds)
}

func (r *runner) adopt(ctx context.Context, st *state.Store, f state.Fleet, home string, olds []string) (state.MoveReport, string, error) {
	blocked, err := r.adoptRefusal(ctx, st, f, home, olds)
	if err != nil {
		return state.MoveReport{}, "", err
	}
	if len(blocked) != 0 {
		return state.MoveReport{}, "", fmt.Errorf("%w: %s", state.ErrConflict, strings.Join(blocked, "; "))
	}
	dir := fleet.Backups(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return state.MoveReport{}, "", err
	}
	backup := moveBackup(dir, "pre-adopt-", r.env.Now().UTC())
	if err := st.Backup(ctx, backup); err != nil {
		return state.MoveReport{}, "", err
	}
	rep, err := st.Adopt(ctx, olds, home)
	if err != nil || rep.EventSeq == 0 {
		_ = os.Remove(backup)
		backup = ""
	}
	if err != nil {
		return rep, "", err
	}
	if _, err := fleet.Register(r.root, f.ID, home); err != nil {
		return rep, backup, fmt.Errorf("%w: the database at %s now names %s, but the fleet record could not be updated: %v; run `hand fleet adopt` here again", state.ErrConflict, home, home, err)
	}
	return rep, backup, nil
}

func foreignPath(p string) bool {
	return runtime.GOOS != "windows" && (strings.HasPrefix(p, `\\`) || len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') && driveLetter(p[0]))
}

func (r *runner) claimUnadopted(f state.Fleet, olds []string) error {
	if err := fleet.RefuseCopies(f.ID, olds); err != nil {
		return fmt.Errorf("%w: %v", state.ErrConflict, err)
	}
	r.fleet = f
	return nil
}

func (r *runner) adoptOnOpen(st *state.Store, f state.Fleet) (state.Fleet, error) {
	ctx := r.ctx()
	if _, _, pending, err := st.PendingMove(ctx); err != nil || pending {
		return f, nil
	}
	olds, due, err := adoptOlds(r.root, f, r.home, nil)
	if err != nil || !due {
		return f, err
	}
	rep, _, err := r.adopt(ctx, st, f, r.home, olds)
	if err != nil {
		return f, err
	}
	if rep.EventSeq != 0 {
		fmt.Fprintf(r.env.Stderr, "hand: adopted %s: %d cells, %d occurrences of %s rewritten\n", r.home, len(rep.Cells), rep.Absolute+rep.Tilde, strings.Join(olds, ", "))
	}
	return st.Fleet(ctx)
}

func cmdFleetAdopt(r *runner, args []string) error {
	set := flags("fleet adopt")
	check := set.Bool("check", false, "count what would be rewritten and change nothing")
	var extra []string
	set.Func("from", "an old home to rewrite to this folder, in addition to the recorded ones; repeatable", func(v string) error {
		if foreignPath(v) {
			extra = append(extra, v)
			return nil
		}
		p, err := canonical(v)
		extra = append(extra, p)
		return err
	})
	if err := set.Parse(args); err != nil {
		return helpOr(set, err, usageError{fmt.Sprintf("fleet adopt: %v", err)})
	}
	if set.NArg() != 0 {
		return usageError{fmt.Sprintf("fleet adopt: want no argument after flags, got %d", set.NArg())}
	}
	home, err := resolveHome(r.env, r.home, r.getwd)
	if err != nil {
		return err
	}
	if home == "" {
		return fmt.Errorf("%w: not inside a fleet home; cd into one or pass --home DIR", state.ErrNotFound)
	}
	db := filepath.Join(home, "hand.db")
	if _, err := os.Stat(db); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: no hand home at %s; run `hand init`", state.ErrNotFound, home)
	} else if err != nil {
		return err
	}
	root, err := fleet.Root(r.env.Getenv)
	if err != nil {
		return err
	}
	r.root = root
	var st *state.Store
	if *check {
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
	if from, to, pending, err := st.PendingMove(ctx); err != nil {
		return err
	} else if pending {
		return fmt.Errorf("%w: a move from %s to %s is pending; run `hand fleet move --yes %s` to finish it, then adopt", state.ErrConflict, from, to, to)
	}
	olds, due, err := adoptOlds(root, f, home, extra)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("fleet", f.Name)
	d.Field("id", f.ID)
	d.Field("home", home)
	if !due {
		d.Field("adopt", "current")
		d.Help("The database and the fleet record already name " + home + "; pass `--from OLD` to rewrite another home")
		return r.print(&d)
	}
	d.List("from", olds)
	if *check {
		rep, err := st.PlanAdopt(ctx, olds, home)
		if err != nil {
			return err
		}
		blocked, err := r.adoptRefusal(ctx, st, f, home, olds)
		if err != nil {
			return err
		}
		d.Field("adopt", "check")
		adoptCounts(&d, rep)
		d.Field("verification", strconv.Itoa(len(rep.Hits)))
		help := []string{"Run `hand fleet adopt` here to rewrite the database, or run any `hand` command here to adopt it first"}
		if len(blocked) != 0 {
			d.List("blocked", blocked)
			help = []string{"The adoption refuses until the blocked items are resolved"}
		}
		if len(rep.Hits) != 0 {
			d.Rows("remaining", []string{"table", "column", "ref"}, moveOnlyRows(rep.Hits))
			help = append(help, fmt.Sprintf("The verification would find %d old home path(s) in a spelling the rewrite does not change; edit them to name %s by hand", len(rep.Hits), home))
		}
		d.Help(help...)
		return r.print(&d)
	}
	rep, backup, err := r.adopt(ctx, st, f, home, olds)
	if err != nil {
		return err
	}
	d.Field("adopt", map[bool]string{true: "adopted", false: "current"}[rep.EventSeq != 0])
	adoptCounts(&d, rep)
	if rep.EventSeq != 0 {
		d.Field("kept", fmt.Sprintf("event e%d (fleet.adopted) names the old homes as history", rep.EventSeq))
		d.Field("foreign_key_check", strconv.Itoa(rep.ForeignKeys))
		d.Field("integrity_check", rep.Integrity)
		d.Field("backup", backup)
		d.Help("The recorded paths now name " + home + "; run `hand init` here to repair worktrees of projects inside it and to refresh the skills")
	}
	return r.print(&d)
}

func adoptCounts(d *toon.Doc, rep state.MoveReport) {
	d.Field("cells", strconv.Itoa(len(rep.Cells)))
	d.Field("occurrences", strconv.Itoa(rep.Absolute+rep.Tilde))
	type key struct{ table, column string }
	var order []key
	cells, occurrences := map[key]int{}, map[key]int{}
	for _, c := range rep.Cells {
		k := key{c.Table, c.Column}
		if _, ok := cells[k]; !ok {
			order = append(order, k)
		}
		cells[k]++
		occurrences[k] += c.Absolute + c.Tilde
	}
	rows := make([][]string, 0, len(order))
	for _, k := range order {
		rows = append(rows, []string{k.table, k.column, strconv.Itoa(cells[k]), strconv.Itoa(occurrences[k])})
	}
	if len(rows) > 0 {
		d.Rows("columns", []string{"table", "column", "cells", "occurrences"}, rows)
	}
}
