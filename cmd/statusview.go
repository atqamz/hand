package cmd

import (
	"fmt"
	"strings"

	"github.com/atqamz/secondhand/internal/axi"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/state"
)

// taskView is one task as both status renderers see it: the durable row plus
// everything derived for display, so the fleet table and the single-task detail
// can never disagree about what a task is doing.
type taskView struct {
	task          state.Task
	agentState    string
	reportedState string
	reportedLine  string
	reported      *reportedJSON
	lastReportAt  string
	reportFile    string
	unreadable    bool
	unacked       bool
	gateIssue     string
	held          bool
	hold          state.Hold
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// A worker whose pane is not busy and whose last word was working (or nothing
// at all) has stopped without saying so, which is the one fleet-view marker
// that is about absence rather than about something reported.
func unreportedStop(v taskView) bool {
	if v.unreadable {
		return false
	}
	if v.reportedState != "" && v.reportedState != state.ReportWorking {
		return false
	}
	return herdr.Status(v.agentState).NotBusy()
}

// taskFlags packs the markers the plain-text fleet view used to append to the
// state column as parenthetical suffixes, one token each so a caller can test
// for one without parsing prose.
func taskFlags(v taskView) []string {
	var flags []string
	if v.unreadable {
		flags = append(flags, "report-unreadable")
	}
	if v.unacked {
		flags = append(flags, "unacknowledged")
	}
	if unreportedStop(v) {
		flags = append(flags, "unreported")
	}
	if v.task.DeliveredAt != "" {
		flags = append(flags, "delivered")
	}
	switch {
	case v.task.PR == "":
	case v.task.MergeExecuted:
		flags = append(flags, "merged")
	case v.task.MergeAnnounced:
		flags = append(flags, "merged-external")
	}
	if v.gateIssue != "" {
		flags = append(flags, "gate-"+strings.ReplaceAll(v.gateIssue, " ", "-"))
	}
	return flags
}

// needsAttention is the predicate behind the fleet view's attention aggregate:
// a supervisor reading only the count knows whether any row is asking for
// something without reading the rows.
func needsAttention(v taskView) bool {
	if v.unreadable || v.unacked || v.gateIssue != "" {
		return true
	}
	switch v.reportedState {
	case state.ReportPaused, state.ReportBlocked, state.ReportNeedsDecision, state.ReportFailed:
		return true
	}
	return unreportedStop(v)
}

// taskFields is the vocabulary --fields draws from, for both status views. One
// registry rather than one per view: a field means the same thing wherever it
// is asked for.
var taskFields = []axi.Column[taskView]{
	{Name: "id", Value: func(v taskView) string { return v.task.ID }},
	{Name: "project", Value: func(v taskView) string { return v.task.Project }},
	{Name: "kind", Value: func(v taskView) string { return v.task.Kind }},
	{Name: "harness", Value: func(v taskView) string { return orNone(v.task.Harness) }},
	{Name: "model", Value: func(v taskView) string { return orNone(v.task.Model) }},
	{Name: "effort", Value: func(v taskView) string { return orNone(v.task.Effort) }},
	{Name: "state", Value: func(v taskView) string { return v.agentState }},
	{Name: "reported", Value: func(v taskView) string {
		if v.unreadable {
			return reportUnreadable
		}
		return orNone(v.reportedState)
	}},
	{Name: "report", Value: func(v taskView) string { return orNone(v.reportedLine) }},
	{Name: "age", Value: func(v taskView) string { return formatAge(v.task.CreatedAt) }},
	{Name: "created", Value: func(v taskView) string { return orNone(v.task.CreatedAt) }},
	{Name: "last_report", Value: func(v taskView) string { return formatReportAge(v.lastReportAt) }},
	{Name: "pr", Value: func(v taskView) string { return orNone(v.task.PR) }},
	{Name: "worktree", Value: func(v taskView) string { return orNone(v.task.Worktree) }},
	{Name: "herdr", Value: func(v taskView) string {
		return orNone(strings.Trim(v.task.Herdr.Session+"/"+v.task.Herdr.TabID, "/"))
	}},
	{Name: "brief", Value: func(v taskView) string { return orNone(v.task.Brief) }},
	{Name: "delivered", Value: func(v taskView) string {
		if v.task.DeliveredAt == "" {
			return "none"
		}
		return fmt.Sprintf("%s (%s)", v.task.DeliveredReason, v.task.DeliveredAt)
	}},
	{Name: "held", Value: func(v taskView) string {
		if !v.held {
			return "none"
		}
		return holdDetail(v.hold)
	}},
	{Name: "gate", Value: func(v taskView) string { return orNone(v.gateIssue) }},
	{Name: "report_file", Value: func(v taskView) string { return orNone(v.reportFile) }},
	{Name: "flags", Value: func(v taskView) string { return orNone(strings.Join(taskFlags(v), " ")) }},
}

// The fleet view answers "what is running and what wants me", so it defaults to
// the five fields that answer it; everything else in taskFields is one --fields
// away.
var fleetDefaultFields = []string{"id", "state", "reported", "age", "flags"}

// The single-task view is one item, not a list, so it defaults to everything
// the plain-text detail view printed.
var detailDefaultFields = []string{
	"id", "project", "kind", "harness", "model", "state", "worktree", "herdr",
	"age", "last_report", "pr", "reported", "report", "delivered", "held", "gate", "flags", "report_file",
}

var holdFields = []axi.Column[state.Hold]{
	{Name: "id", Value: func(h state.Hold) string { return h.ID }},
	{Name: "kind", Value: func(h state.Hold) string { return h.Kind }},
	{Name: "detail", Value: func(h state.Hold) string { return holdDetail(h) }},
	{Name: "age", Value: func(h state.Hold) string { return formatAge(h.SetAt) }},
}

// selectFields resolves --fields against taskFields, defaulting to def. An
// unknown name is a usage error, not a silently narrower schema header.
func selectFields(fields, def []string) ([]axi.Column[taskView], error) {
	want := fields
	if len(want) == 0 {
		want = def
	}
	cols, err := axi.Select(taskFields, want)
	if err != nil {
		return nil, &ExitError{Err: err, Code: 2}
	}
	return cols, nil
}

// rejectFieldsWithJSON keeps --fields honest: it narrows the TOON schema
// header, and silently ignoring it next to --json would hand a caller the full
// object it asked to narrow.
func rejectFieldsWithJSON(fields []string, asJSON bool) error {
	if len(fields) > 0 && asJSON {
		return &ExitError{Err: fmt.Errorf("--fields applies to the default TOON output, not --json"), Code: 2}
	}
	return nil
}

func fieldsFlagUsage(def []string) string {
	return fmt.Sprintf("columns to emit, comma-separated (default %s; any of %s)",
		strings.Join(def, ","), strings.Join(axi.Names(taskFields), ","))
}
