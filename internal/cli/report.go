package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var reportCommands = map[string]handler{
	"add":  cmdReportAdd,
	"show": cmdReportShow,
	"list": cmdReportList,
	"ack":  cmdReportAck,
}

func init() {
	commands["report"] = func(r *runner, args []string) error { return sub(r, args, "report", reportCommands) }
}

var worktreeName = regexp.MustCompile(`^t[0-9]+-a([0-9]+)$`)

func reportFooter(exe string) string {
	return "\n\n---\nWhen you finish, report to Hand from inside this worktree:\n" +
		"  " + shellQuote(exe) + " report add --status done --file SUMMARY.md\n" +
		"Use --status stuck instead if you cannot continue, or --status progress for a milestone. " +
		"Say what changed, list the commits, and give any PR link. Hand reads your report, not your terminal."
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (r *runner) attemptHere(ctx context.Context) (int64, string, error) {
	wd, err := r.getwd()
	if err != nil {
		return 0, "", err
	}
	out, err := git(ctx, wd, "rev-parse", "--show-toplevel")
	if err != nil {
		return 0, "", fmt.Errorf("%w: %s is not inside a Hand worktree; pass --attempt", state.ErrInvalid, wd)
	}
	top := strings.TrimSpace(out)
	m := worktreeName.FindStringSubmatch(filepath.Base(top))
	id := filepath.Base(filepath.Dir(top))
	worktrees := filepath.Dir(filepath.Dir(top))
	if m == nil || !state.FleetID.MatchString(id) || filepath.Base(worktrees) != "worktrees" {
		return 0, "", fmt.Errorf("%w: %s is not a Hand worktree; pass --attempt", state.ErrInvalid, top)
	}
	root := filepath.Dir(worktrees)
	home, err := fleet.Home(root, id)
	if err != nil {
		return 0, "", fmt.Errorf("%w; pass --attempt", err)
	}
	if _, err := os.Stat(filepath.Join(home, "hand.db")); err != nil {
		return 0, "", fmt.Errorf("%w: no hand home at %s; pass --attempt", state.ErrInvalid, home)
	}
	if r.home != "" && r.home != home {
		return 0, "", fmt.Errorf("%w: this worktree belongs to the fleet at %s, not %s", state.ErrConflict, home, r.home)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, "", err
	}
	r.home, r.root = home, root
	return n, top, nil
}

func cmdReportAdd(r *runner, args []string) error {
	fs := flags("report add")
	attempt := fs.String("attempt", "", "attempt id; default: the Hand worktree you are in")
	status := fs.String("status", "", "progress, done or stuck")
	text := fs.String("text", "", "report text")
	file := fs.String("file", "", "file holding the report")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	if (*text == "") == (*file == "") {
		return usageError{"report add: give exactly one of --text or --file"}
	}
	body := *text
	if *file != "" {
		b, err := os.ReadFile(*file)
		if err != nil {
			return fmt.Errorf("%w: %v", state.ErrInvalid, err)
		}
		body = string(b)
	}
	ctx := context.Background()
	id, top, hereErr := r.attemptHere(ctx)
	if errors.Is(hereErr, state.ErrConflict) {
		return hereErr
	}
	switch {
	case *attempt != "":
		n, err := parseID("a", *attempt)
		if err != nil {
			return err
		}
		if hereErr == nil && n != id {
			return fmt.Errorf("%w: this worktree belongs to attempt %s, not %s", state.ErrConflict, state.AttemptRef(id), state.AttemptRef(n))
		}
		id = n
	case hereErr != nil:
		return hereErr
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	if top != "" {
		a, err := st.Attempt(ctx, id)
		if err != nil {
			return err
		}
		if real, err := filepath.EvalSymlinks(a.Worktree); err != nil || real != top {
			return fmt.Errorf("%w: %s is not the worktree of attempt %s", state.ErrConflict, top, state.AttemptRef(id))
		}
	}
	rep, err := st.AddReport(ctx, id, *status, body)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("report", state.ReportRef(rep.ID))
	d.Field("attempt", state.AttemptRef(rep.AttemptID))
	d.Field("status", rep.Status)
	return r.print(&d)
}

func cmdReportShow(r *runner, args []string) error {
	pos, err := parse(flags("report show"), args, 1)
	if err != nil {
		return err
	}
	id, err := parseID("r", pos[0])
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	rep, err := st.Report(context.Background(), id)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("report", state.ReportRef(rep.ID))
	d.Field("attempt", state.AttemptRef(rep.AttemptID))
	d.Field("task", state.TaskRef(rep.TaskID))
	d.Field("status", rep.Status)
	d.Field("created_at", rep.CreatedAt)
	if rep.AckedAt == "" {
		d.Field("acked", "no")
	} else {
		d.Field("acked", rep.AckedAt+" by "+rep.AckedBy)
	}
	d.Field("body", rep.Body)
	if rep.AckedAt == "" {
		d.Help("Acknowledge it once read: `hand report ack " + state.ReportRef(rep.ID) + "`")
	}
	return r.print(&d)
}

func cmdReportList(r *runner, args []string) error {
	fs := flags("report list")
	task := fs.String("task", "", "only this task's reports")
	attempt := fs.String("attempt", "", "only this attempt's reports")
	unacked := fs.Bool("unacked", false, "only reports not yet acknowledged")
	limit := fs.Int("limit", 20, "maximum rows")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	f := state.ReportFilter{Unacked: *unacked}
	if *task != "" {
		id, err := parseID("t", *task)
		if err != nil {
			return err
		}
		f.TaskID = id
	}
	if *attempt != "" {
		id, err := parseID("a", *attempt)
		if err != nil {
			return err
		}
		f.AttemptID = id
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	reps, err := st.Reports(context.Background(), f, *limit)
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(reps))
	for _, rep := range reps {
		acked := "no"
		if rep.AckedAt != "" {
			acked = "yes"
		}
		rows = append(rows, []string{state.ReportRef(rep.ID), state.AttemptRef(rep.AttemptID), state.TaskRef(rep.TaskID), rep.Status, acked, rep.Summary()})
	}
	var d toon.Doc
	d.Rows("reports", []string{"id", "attempt", "task", "status", "acked", "summary"}, rows)
	return r.print(&d)
}

func cmdReportAck(r *runner, args []string) error {
	fs := flags("report ack")
	by := fs.String("by", "supervisor", "who read the report")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	id, err := parseID("r", pos[0])
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	rep, err := st.AckReport(context.Background(), id, *by)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("report", state.ReportRef(rep.ID))
	d.Field("acked_by", rep.AckedBy)
	return r.print(&d)
}
