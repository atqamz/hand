// Package dashboard reads, renders, and updates data/dashboard.md, the fleet's
// living status file described in SPECS.md.
package dashboard

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/atqamz/secondhand/internal/atomicfile"
)

const TimeFormat = time.RFC3339

const (
	sectionActiveTasks       = "Active Tasks"
	sectionPendingDecisions  = "Pending Decisions"
	sectionRecentEvents      = "Recent Events"
	sectionRecentCompletions = "Recent Completions"
	sectionProjects          = "Projects"
)

const (
	maxRecentEvents      = 20
	maxRecentCompletions = 10
)

type ActiveTask struct {
	ID      string
	Project string
	Kind    string
	State   string
	Age     string
	PR      string
}

type ProjectSummary struct {
	Name            string
	Mode            string
	ActiveTaskCount int
}

type Dashboard struct {
	Updated           string
	ActiveTasks       []ActiveTask
	PendingDecisions  []string
	RecentEvents      []string
	RecentCompletions []string
	Projects          []ProjectSummary
}

type AgentStateUpdate struct {
	ID    string
	State string
	Age   string
}

// Completion describes a task moving into Recent Completions, e.g. rendered as
// "fix-login: nsr | ship | merged | PR #40".
type Completion struct {
	ID      string
	Project string
	Kind    string
	Outcome string
	Detail  string
}

// PendingDecision is upserted by ID: a later SetPendingDecision for the same ID
// replaces the previous line instead of appending a duplicate.
type PendingDecision struct {
	ID   string
	Text string
}

// PRUpdate sets the PR column for an existing active task row. A missing row is
// never created here - active rows come from hand spawn, and a fabricated one
// would invent state rather than reconcile it.
type PRUpdate struct {
	ID string
	PR string
}

// ErrPRRowNotFound reports a SetPR that matched no active row. It is an error
// rather than a flag on PRUpdate because no caller wants to ignore it and three
// call sites in a row did exactly that by accident: a returned flag is silently
// droppable, whereas ignoring an error takes an explicit discard a linter flags
// and a reviewer sees. Every caller has to tell a repair from a no-op - the PR is
// on the task either way, and only the dashboard column is left stale.
var ErrPRRowNotFound = errors.New("no active dashboard row")

type UpdateOpts struct {
	AddActiveTask        *ActiveTask
	UpdateAgentState     *AgentStateUpdate
	AddEvent             string
	Complete             *Completion
	SetPendingDecision   *PendingDecision
	ClearPendingDecision string
	SetProjects          []ProjectSummary
	SetPR                *PRUpdate
}

// Update performs a read-modify-write of the dashboard at path, applying opts and
// stamping the Updated timestamp. Every hand command that mutates fleet state calls
// this so data/dashboard.md stays current, except promote - see cmd/promote.go for why
// its row deliberately stays unchanged.
//
// A SetPR that matched no active row returns ErrPRRowNotFound, after the rest of
// opts has been applied and written: the update that did land is still worth
// keeping, and the caller decides what an unreconciled PR column means for it.
func Update(path string, opts UpdateOpts) error {
	unlock, err := flock(path + ".lock")
	if err != nil {
		return fmt.Errorf("lock dashboard: %w", err)
	}
	defer unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read dashboard: %w", err)
	}
	d, err := Parse(data)
	if err != nil {
		return err
	}

	applyErr := apply(&d, opts)
	d.Updated = time.Now().UTC().Format(TimeFormat)

	if err := atomicfile.Write(path, ".dashboard.md-", Render(d), 0o644); err != nil {
		return fmt.Errorf("write dashboard: %w", err)
	}
	return applyErr
}

func flock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func apply(d *Dashboard, opts UpdateOpts) error {
	if t := opts.AddActiveTask; t != nil {
		d.ActiveTasks = append(d.ActiveTasks, *t)
	}
	if u := opts.UpdateAgentState; u != nil {
		for i := range d.ActiveTasks {
			if d.ActiveTasks[i].ID == u.ID {
				d.ActiveTasks[i].State = u.State
				d.ActiveTasks[i].Age = u.Age
				break
			}
		}
	}
	if opts.AddEvent != "" {
		line := time.Now().UTC().Format("15:04") + " " + opts.AddEvent
		d.RecentEvents = appendBounded(d.RecentEvents, line, maxRecentEvents)
	}
	// Nothing may reference a task with no Active Tasks row: completion removes every
	// trace of the task, not just the row. A question left behind by a torn-down task
	// is one nobody can answer and no later event can retire - the ID is gone from the
	// task list, so the watcher stops tracking it. This is not a third way for the
	// watcher to infer that a question was answered; it is the operator deleting the
	// task, and it is what keeps the section bounded by the live fleet without a cap.
	if c := opts.Complete; c != nil {
		d.ActiveTasks = removeActiveTask(d.ActiveTasks, c.ID)
		d.PendingDecisions = removeByID(d.PendingDecisions, c.ID)
		line := fmt.Sprintf("%s: %s | %s | %s | %s", c.ID, c.Project, c.Kind, c.Outcome, c.Detail)
		d.RecentCompletions = appendBounded(d.RecentCompletions, line, maxRecentCompletions)
	}
	if pd := opts.SetPendingDecision; pd != nil {
		d.PendingDecisions = upsertByID(d.PendingDecisions, pd.ID, fmt.Sprintf("%s: %s", pd.ID, pd.Text))
	}
	if opts.ClearPendingDecision != "" {
		d.PendingDecisions = removeByID(d.PendingDecisions, opts.ClearPendingDecision)
	}
	if opts.SetProjects != nil {
		d.Projects = opts.SetProjects
	}
	if p := opts.SetPR; p != nil {
		matched := false
		for i := range d.ActiveTasks {
			if d.ActiveTasks[i].ID == p.ID {
				d.ActiveTasks[i].PR = p.PR
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%w for %s", ErrPRRowNotFound, p.ID)
		}
	}
	return nil
}

func removeActiveTask(tasks []ActiveTask, id string) []ActiveTask {
	kept := tasks[:0]
	for _, t := range tasks {
		if t.ID != id {
			kept = append(kept, t)
		}
	}
	return kept
}

func appendBounded(lines []string, line string, max int) []string {
	lines = append(lines, line)
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	return lines
}

func upsertByID(lines []string, id, line string) []string {
	prefix := id + ":"
	for i, existing := range lines {
		if strings.HasPrefix(existing, prefix) {
			lines[i] = line
			return lines
		}
	}
	return append(lines, line)
}

func removeByID(lines []string, id string) []string {
	prefix := id + ":"
	kept := lines[:0]
	for _, existing := range lines {
		if !strings.HasPrefix(existing, prefix) {
			kept = append(kept, existing)
		}
	}
	return kept
}

// FormatAge renders the elapsed time since createdAt (RFC3339) as a compact
// dashboard-table value: "just now", "45m", "2h", "3d". Returns "unknown" if
// createdAt doesn't parse.
func FormatAge(createdAt string) string {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return "unknown"
	}
	return FormatDuration(time.Since(t))
}

func FormatDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func Parse(data []byte) (Dashboard, error) {
	var d Dashboard
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	section := ""
	tableRow := 0
	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())

		switch {
		case strings.HasPrefix(trimmed, "## "):
			section = strings.TrimPrefix(trimmed, "## ")
			tableRow = 0
			continue
		case strings.HasPrefix(trimmed, "# "):
			section = ""
			continue
		case strings.HasPrefix(trimmed, "Updated:"):
			d.Updated = strings.TrimSpace(strings.TrimPrefix(trimmed, "Updated:"))
			continue
		case trimmed == "":
			continue
		}

		switch section {
		case sectionActiveTasks:
			tableRow++
			if tableRow <= 2 {
				continue
			}
			if task, ok := parseActiveTaskRow(trimmed); ok {
				d.ActiveTasks = append(d.ActiveTasks, task)
			}
		case sectionPendingDecisions:
			d.PendingDecisions = append(d.PendingDecisions, parseListLine(trimmed))
		case sectionRecentEvents:
			d.RecentEvents = append(d.RecentEvents, parseListLine(trimmed))
		case sectionRecentCompletions:
			d.RecentCompletions = append(d.RecentCompletions, parseListLine(trimmed))
		case sectionProjects:
			if p, ok := parseProjectLine(parseListLine(trimmed)); ok {
				d.Projects = append(d.Projects, p)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Dashboard{}, fmt.Errorf("parse dashboard: %w", err)
	}
	return d, nil
}

func parseListLine(line string) string {
	return strings.TrimPrefix(line, "- ")
}

func parseActiveTaskRow(line string) (ActiveTask, bool) {
	if !strings.HasPrefix(line, "|") {
		return ActiveTask{}, false
	}
	fields := splitTableRow(line)
	if len(fields) != 6 {
		return ActiveTask{}, false
	}
	pr := fields[5]
	if pr == "-" {
		pr = ""
	}
	return ActiveTask{ID: fields[0], Project: fields[1], Kind: fields[2], State: fields[3], Age: fields[4], PR: pr}, true
}

func splitTableRow(line string) []string {
	parts := strings.Split(strings.Trim(line, "|"), "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

func parseProjectLine(line string) (ProjectSummary, bool) {
	nameRest := strings.SplitN(line, ":", 2)
	if len(nameRest) != 2 {
		return ProjectSummary{}, false
	}
	modeCount := strings.SplitN(strings.TrimSpace(nameRest[1]), "|", 2)
	if len(modeCount) != 2 {
		return ProjectSummary{}, false
	}
	countStr := strings.TrimSuffix(strings.TrimSpace(modeCount[1]), "active tasks")
	count, err := strconv.Atoi(strings.TrimSpace(countStr))
	if err != nil {
		return ProjectSummary{}, false
	}
	return ProjectSummary{
		Name:            strings.TrimSpace(nameRest[0]),
		Mode:            strings.TrimSpace(modeCount[0]),
		ActiveTaskCount: count,
	}, true
}

func Render(d Dashboard) []byte {
	var b strings.Builder
	b.WriteString("# Dashboard\n\n")
	b.WriteString("Updated: " + d.Updated + "\n\n")

	b.WriteString("## " + sectionActiveTasks + "\n")
	b.WriteString("| id | project | kind | state | age | pr |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, t := range d.ActiveTasks {
		pr := t.PR
		if pr == "" {
			pr = "-"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", t.ID, t.Project, t.Kind, t.State, t.Age, pr)
	}
	b.WriteString("\n")

	writeList(&b, sectionPendingDecisions, d.PendingDecisions)
	writeList(&b, sectionRecentEvents, d.RecentEvents)
	writeList(&b, sectionRecentCompletions, d.RecentCompletions)

	projectLines := make([]string, len(d.Projects))
	for i, p := range d.Projects {
		projectLines[i] = fmt.Sprintf("%s: %s | %d active tasks", p.Name, p.Mode, p.ActiveTaskCount)
	}
	writeList(&b, sectionProjects, projectLines)

	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

func writeList(b *strings.Builder, title string, lines []string) {
	b.WriteString("## " + title + "\n")
	for _, l := range lines {
		b.WriteString("- " + l + "\n")
	}
	b.WriteString("\n")
}
