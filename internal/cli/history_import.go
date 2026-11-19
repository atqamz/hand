package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/transcript"
)

const importChunk = 500

type importRow struct {
	ref, harness, session string
	entries, inserted     int
	status                string
}

func cmdHistoryImport(r *runner, args []string) error {
	fs := flags("history import")
	dry := fs.Bool("dry-run", false, "read and report; write nothing")
	var only []string
	fs.Func("session", "import only this harness session; repeat for each", func(s string) error {
		if s == "" {
			return errors.New("the session is empty")
		}
		only = append(only, s)
		return nil
	})
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	ctx := r.ctx()
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	sups, err := st.Supervisors(ctx)
	if err != nil {
		return err
	}
	groups, err := importGroups(sups, only)
	if err != nil {
		return err
	}
	var rows []importRow
	var entries, inserted int
	for _, g := range groups {
		row, err := importSession(ctx, r, st, g, *dry)
		if err != nil {
			return err
		}
		rows = append(rows, row)
		entries += row.entries
		inserted += row.inserted
	}
	total := fmt.Sprintf("%d sessions; %d entries; %d inserted", len(rows), entries, inserted)
	if *dry {
		total = fmt.Sprintf("%d sessions; %d entries; %d would be inserted", len(rows), entries, inserted)
	}
	return r.print(fitRows(len(rows), func(n int) *toon.Doc { return importPage(rows, n, total, *dry, r.env.command()) }))
}

func importGroups(sups []state.Supervisor, only []string) ([][]state.Supervisor, error) {
	type key struct{ harness, session string }
	var groups [][]state.Supervisor
	at := map[key]int{}
	for _, s := range sups {
		if s.Session == "" {
			if len(only) == 0 {
				groups = append(groups, []state.Supervisor{s})
			}
			continue
		}
		k := key{s.Harness, s.Session}
		if i, ok := at[k]; ok {
			groups[i] = append(groups[i], s)
			continue
		}
		if len(only) > 0 && !slices.Contains(only, s.Session) {
			continue
		}
		at[k] = len(groups)
		groups = append(groups, []state.Supervisor{s})
	}
	for _, want := range only {
		found := false
		for _, g := range groups {
			found = found || g[0].Session == want
		}
		if !found {
			return nil, fmt.Errorf("%w: no supervisor recorded session %s", state.ErrNotFound, want)
		}
	}
	return groups, nil
}

func importSession(ctx context.Context, r *runner, st *state.Store, group []state.Supervisor, dry bool) (importRow, error) {
	first := group[0]
	row := importRow{ref: state.SupervisorRef(first.ID), harness: first.Harness, session: first.Session}
	paths := r.transcriptPaths()
	switch {
	case first.Session == "":
		row.status = "no session recorded"
		return row, nil
	case first.Harness == "opencode" && group[len(group)-1].Live():
		row.status = "still live"
		return row, nil
	case first.Harness == "opencode" && paths.Opencode == "":
		row.status = "harness missing"
		return row, nil
	}
	read := &transcript.Reader{Paths: paths}
	found, err := read.ReadEnded(ctx, first.Harness, first.Session, r.transcriptDir(first.Harness))
	switch {
	case errors.Is(err, transcript.ErrNoSession):
		row.status = "no transcript"
		return row, nil
	case err != nil:
		row.status = "unreadable"
		return row, nil
	}
	var order []int64
	owned := map[int64][]state.Reply{}
	var all []state.Reply
	counts := map[string]int{}
	for _, e := range found {
		if e.Role != "supervisor" {
			continue
		}
		owner := group[len(group)-1]
		if at, err := time.Parse(time.RFC3339Nano, e.At); err == nil {
			if s, ok := deliveredTo(group, at); ok {
				owner = s
			} else if s, ok := bornAfter(group, at); ok {
				owner = s
			}
		}
		n := counts[e.At]
		counts[e.At]++
		if _, ok := owned[owner.ID]; !ok {
			order = append(order, owner.ID)
		}
		reply := state.Reply{At: e.At, N: n, Body: e.Text}
		owned[owner.ID] = append(owned[owner.ID], reply)
		all = append(all, reply)
	}
	row.status, row.entries = "ok", len(all)
	if dry {
		row.inserted, err = st.NewReplies(ctx, first.Session, all)
		return row, err
	}
	for _, id := range order {
		for list := owned[id]; len(list) > 0; {
			chunk := list[:min(importChunk, len(list))]
			list = list[len(chunk):]
			n, err := st.AddSupervisorReplies(ctx, id, first.Session, chunk)
			if err != nil {
				return row, err
			}
			row.inserted += n
		}
	}
	return row, nil
}

func bornAfter(group []state.Supervisor, at time.Time) (state.Supervisor, bool) {
	for _, s := range group {
		if born, err := time.Parse(time.RFC3339Nano, s.CreatedAt); err == nil && at.Before(born) {
			return s, true
		}
	}
	return state.Supervisor{}, false
}

func importPage(rows []importRow, n int, total string, dry bool, command string) *toon.Doc {
	var d toon.Doc
	if dry {
		d.Field("dry_run", "true")
	}
	d.Field("total", total)
	out := make([][]string, 0, n)
	for _, row := range rows[:n] {
		out = append(out, []string{row.ref, row.harness, row.session, strconv.Itoa(row.entries), strconv.Itoa(row.inserted), row.status})
	}
	d.Rows("sessions", []string{"ref", "harness", "session", "entries", "inserted", "status"}, out)
	switch {
	case n < len(rows):
		d.Help(fmt.Sprintf("%d more sessions not shown; the total counts them all. `%s history import --session ID` takes one", len(rows)-n, command))
	case dry:
		d.Help("Nothing written. Run without --dry-run to store the replies")
	default:
		d.Help("`" + command + " history search --who supervisor QUERY` finds the replies")
	}
	return &d
}
