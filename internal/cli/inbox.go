package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/atqamz/hand/internal/inbox"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const pruneUsage = "usage: hand inbox prune --older-than DURATION [--dry-run]; DURATION is a number of days from 1d to 9999d, like 30d"

var inboxCommands = map[string]handler{
	"size":  cmdInboxSize,
	"prune": cmdInboxPrune,
}

func init() {
	commands["inbox"] = func(r *runner, args []string) error { return sub(r, args, "inbox", inboxCommands) }
}

func cmdInboxSize(r *runner, args []string) error {
	if _, err := parse(flags("inbox size"), args, 0); err != nil {
		return err
	}
	if err := r.needHome(); err != nil {
		return err
	}
	u, err := inbox.Size(r.home)
	if err != nil {
		return err
	}
	now := r.env.Now()
	var d toon.Doc
	d.Field("inbox", filepath.Join(r.home, inbox.Folder))
	d.Field("files", strconv.Itoa(u.Files))
	d.Field("size", inbox.MiB(u.Bytes))
	if u.Files > 0 {
		d.Field("oldest", state.Ahead(max(0, now.Sub(u.Oldest))))
		d.Field("newest", state.Ahead(max(0, now.Sub(u.Newest))))
	}
	d.Field("trash_files", strconv.Itoa(u.TrashFiles))
	d.Field("trash_size", inbox.MiB(u.TrashBytes))
	return r.print(&d)
}

func cmdInboxPrune(r *runner, args []string) error {
	fs := flags("inbox prune")
	older := fs.String("older-than", "", "move the files older than this many days, like 30d, to inbox/.trash")
	dry := fs.Bool("dry-run", false, "list what would be moved and deleted, and change nothing")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	if *older == "" {
		return usageError{pruneUsage}
	}
	age, ok := inbox.ParseAge(*older)
	if !ok {
		return usageError{fmt.Sprintf("inbox prune: --older-than %q is not a number of days from 1d to 9999d, like 30d", *older)}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	p, err := r.pruneInbox(r.ctx(), st, age, *dry)
	if err != nil && p.Moved == nil {
		return err
	}
	now := r.env.Now()
	var d toon.Doc
	d.Field("inbox", filepath.Join(r.home, inbox.Folder))
	if *dry {
		d.Bool("dry_run", true)
	}
	d.Field("moved", strconv.Itoa(len(p.Moved)))
	d.Field("moved_size", inbox.MiB(inbox.Bytes(p.Moved)))
	d.Field("deleted", strconv.Itoa(len(p.Deleted)))
	d.Field("freed", inbox.MiB(inbox.Bytes(p.Deleted)))
	if *dry {
		d.Rows("move", []string{"name", "age", "size"}, fileRows(p.Moved, now))
		d.Rows("delete", []string{"name", "age", "size"}, fileRows(p.Deleted, now))
	}
	if len(p.Failed) > 0 {
		rows := make([][]string, len(p.Failed))
		for i, f := range p.Failed {
			rows[i] = []string{f.Name, f.Err.Error()}
		}
		d.Rows("failed", []string{"name", "error"}, rows)
	}
	if perr := r.print(&d); perr != nil {
		return errors.Join(err, perr)
	}
	if len(p.Failed) > 0 {
		err = errors.Join(err, fmt.Errorf("inbox prune: %d file(s) could not be pruned; see failed", len(p.Failed)))
	}
	return err
}

func fileRows(files []inbox.File, now time.Time) [][]string {
	rows := make([][]string, len(files))
	for i, f := range files {
		rows[i] = []string{f.Name, state.Ahead(max(0, now.Sub(f.Mod))), inbox.MiB(f.Size)}
	}
	return rows
}

func (r *runner) pruneInbox(ctx context.Context, st *state.Store, olderThan time.Duration, dry bool) (inbox.Pruned, error) {
	if !dry {
		unlock, ok, err := r.lock("inbox", false)
		if err != nil {
			return inbox.Pruned{}, err
		}
		if !ok {
			return inbox.Pruned{}, fmt.Errorf("%w: another inbox prune is running for %s", state.ErrConflict, r.home)
		}
		defer unlock()
	}
	p, err := inbox.Prune(r.home, r.env.Now(), olderThan, dry)
	if err == nil && !dry && len(p.Moved) > 0 {
		err = st.InboxPruned(ctx, inbox.Count(len(p.Moved), inbox.Bytes(p.Moved)))
	}
	return p, err
}
