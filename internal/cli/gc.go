package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/inbox"
	"github.com/atqamz/hand/internal/retention"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const gcUsage = "usage: hand gc --older-than DURATION [--yes] [SCOPE...]; DURATION is a number of days from 1d to 9999d, like 30d; a SCOPE is reports, shots or a path inside one, like reports/t12; flags come before scopes"

var gcFields = []string{"path", "files", "bytes", "oldest"}

func init() {
	commands["gc"] = cmdGC
}

func cmdGC(r *runner, args []string) error {
	set := flags("gc")
	older := set.String("older-than", "", "list the files in reports/ and shots/ not modified for this many days, like 30d")
	yes := set.Bool("yes", false, "delete the listed files; without it nothing is deleted")
	if err := set.Parse(args); err != nil {
		return usageError{fmt.Sprintf("gc: %v", err)}
	}
	if *older == "" {
		return usageError{gcUsage}
	}
	age, ok := inbox.ParseAge(*older)
	if !ok {
		return usageError{fmt.Sprintf("gc: --older-than %q is not a number of days from 1d to 9999d, like 30d", *older)}
	}
	var scopes []string
	for _, arg := range set.Args() {
		if strings.HasPrefix(arg, "-") {
			return usageError{gcUsage}
		}
		s, err := retention.Scope(arg)
		if err != nil {
			return usageError{"gc: scope " + err.Error()}
		}
		scopes = append(scopes, s)
	}
	if err := r.needHome(); err != nil {
		return err
	}
	if *yes {
		unlock, ok, err := r.lock("gc", false)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: another hand gc is running for %s", state.ErrConflict, r.home)
		}
		defer unlock()
	}
	all, err := retention.Scan(r.home)
	if err != nil {
		return err
	}
	now := r.env.Now()
	found := retention.Within(retention.Older(all, now, age), scopes)
	var failed []retention.Failure
	if *yes {
		st, err := r.store()
		if err != nil {
			return err
		}
		defer st.Close()
		var removed []retention.File
		removed, failed = retention.Remove(r.home, found)
		if len(removed) > 0 {
			if err := st.GcRemoved(r.ctx(), inbox.Count(len(removed), retention.Bytes(removed))+" older than "+*older); err != nil {
				return err
			}
		}
		found = removed
	}
	var d toon.Doc
	d.Field("older_than", *older)
	d.Field("applied", strconv.FormatBool(*yes))
	rows := make([][]string, 0, len(found))
	for _, g := range retention.Groups(found) {
		rows = append(rows, []string{g.Path, strconv.Itoa(g.Files), strconv.FormatInt(g.Bytes, 10), g.Oldest.UTC().Format(time.DateOnly)})
	}
	d.Rows("candidates", gcFields, rows)
	d.Field("total_files", strconv.Itoa(len(found)))
	d.Field("total_bytes", strconv.FormatInt(retention.Bytes(found), 10))
	if len(failed) > 0 {
		frows := make([][]string, len(failed))
		for i, f := range failed {
			frows[i] = []string{f.Path, f.Err.Error()}
		}
		d.Rows("failed", []string{"path", "error"}, frows)
	}
	if !*yes && len(found) > 0 {
		d.Help("Delete exactly these with `hand gc --older-than " + *older + " --yes`, or name a scope after the flags to delete part of them; memory/ and briefs/ are never touched")
	}
	if perr := r.print(&d); perr != nil || len(failed) == 0 {
		return perr
	}
	return fmt.Errorf("gc: %d file(s) could not be deleted; see failed", len(failed))
}
