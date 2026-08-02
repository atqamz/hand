// Package dashboard renders data/dashboard.md, described in SPECS.md. Every
// derivable section is re-derived from the store on every write rather than
// patched into the previous rendering, the root of atqamz/secondhand#53.
package dashboard

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/atqamz/secondhand/internal/atomicfile"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
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

// Completion describes a task moving into Recent Completions, e.g. rendered as
// "fix-login: nsr | ship | merged | PR #40".
type Completion struct {
	ID      string
	Project string
	Kind    string
	Outcome string
	Detail  string
}

// Only what cannot be derived: the two append-only logs. Every other section
// is rebuilt from the store on every call, so no caller can leave one behind.
type UpdateOpts struct {
	AddEvent string
	Complete *Completion
}

func Path(homeDir string) string {
	return filepath.Join(homeDir, "data", "dashboard.md")
}

// Every hand command that mutates fleet state calls this, and a command that
// only reads may call it too: the result depends on the store, not on which
// caller got there first.
func Update(homeDir string, opts UpdateOpts) error {
	path := Path(homeDir)
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

	apply(&d, opts)
	if err := derive(&d, homeDir); err != nil {
		return err
	}
	d.Updated = time.Now().UTC().Format(TimeFormat)

	if err := atomicfile.Write(path, ".dashboard.md-", Render(d), 0o644); err != nil {
		return fmt.Errorf("write dashboard: %w", err)
	}
	return nil
}

// A worker's needs-decision line has run to ~200 words, and one entry that
// long consumes the whole section it is meant to be scanned in
// (atqamz/secondhand#53).
const pendingDecisionBudget = 160

// The same bound for Recent Events, whose entries are raw report text.
const eventBudget = 160

// The whole membership rule for Pending Decisions: a worker said, in its own
// words, that it is waiting on someone. Nothing infers an entry from an idle
// pane any more (atqamz/secondhand#53).
var awaitsOperator = map[string]bool{
	state.ReportBlocked:       true,
	state.ReportNeedsDecision: true,
}

// The words that retire a pending question. `paused` is deliberately absent: a
// worker that asks something and then parks is still waiting for the answer.
var answersTheQuestion = map[string]bool{
	state.ReportWorking: true,
	state.ReportDone:    true,
	state.ReportFailed:  true,
}

// The open question is not the last line: `needs-decision:` then `paused:`
// leaves it open. Replaying beats remembering, because a latched flag is how a
// cleared question used to survive the line that cleared it.
func openQuestion(lines []state.ReportLine) (state.ReportLine, bool) {
	var open state.ReportLine
	found := false
	for _, line := range lines {
		switch {
		case line.Malformed:
		case awaitsOperator[line.State]:
			open, found = line, true
		case answersTheQuestion[line.State]:
			found = false
		}
	}
	return open, found
}

// The whole answer to the dashboard disagreeing with machine state: there is
// nothing to reconcile if the previous rendering is never a source.
func derive(d *Dashboard, homeDir string) error {
	tasks, err := state.List(homeDir)
	if err != nil {
		return err
	}
	projects, err := project.List(homeDir)
	if err != nil {
		return err
	}

	d.ActiveTasks = nil
	d.PendingDecisions = nil
	activeCounts := make(map[string]int, len(projects))
	for _, t := range tasks {
		activeCounts[t.Project]++
		lines, err := state.ReadReportLines(homeDir, t.ID)
		if err != nil {
			return err
		}
		reported, ok := state.LastReportedState(lines)
		d.ActiveTasks = append(d.ActiveTasks, ActiveTask{
			ID: t.ID, Project: t.Project, Kind: t.Kind,
			State: taskState(reported, ok), Age: FormatAge(t.CreatedAt), PR: t.PR,
		})
		if open, waiting := openQuestion(lines); waiting {
			d.PendingDecisions = append(d.PendingDecisions,
				fmt.Sprintf("%s: %s: %s (see %s)", t.ID, open.State, truncate(open.Note, pendingDecisionBudget), state.ReportPath(homeDir, t.ID)))
		}
	}

	d.Projects = make([]ProjectSummary, len(projects))
	for i, p := range projects {
		d.Projects[i] = ProjectSummary{Name: p.Name, Mode: p.Mode, ActiveTaskCount: activeCounts[p.Name]}
	}
	return nil
}

// The state column for a worker that has not classified a line yet. It means
// "not recorded", never "guessed wrong".
const unreported = "unreported"

func taskState(reported state.ReportLine, ok bool) string {
	if !ok {
		return unreported
	}
	return reported.State
}

func truncate(text string, budget int) string {
	runes := []rune(text)
	if len(runes) <= budget {
		return text
	}
	return fmt.Sprintf("%s... [+%d chars]", string(runes[:budget]), len(runes)-budget)
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

func apply(d *Dashboard, opts UpdateOpts) {
	if opts.AddEvent != "" {
		line := time.Now().UTC().Format("15:04") + " " + truncate(opts.AddEvent, eventBudget)
		d.RecentEvents = appendBounded(d.RecentEvents, line, maxRecentEvents)
	}
	if c := opts.Complete; c != nil {
		line := fmt.Sprintf("%s: %s | %s | %s | %s", c.ID, c.Project, c.Kind, c.Outcome, c.Detail)
		d.RecentCompletions = appendBounded(d.RecentCompletions, line, maxRecentCompletions)
	}
}

func appendBounded(lines []string, line string, max int) []string {
	lines = append(lines, line)
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	return lines
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

// Only what Update cannot rebuild: the Updated stamp and the two append-only
// logs. The previous rendering is not evidence about the fleet, and reading it
// back as such is what let a stale row outlive the state behind it.
func Parse(data []byte) (Dashboard, error) {
	var d Dashboard
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	section := ""
	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())

		switch {
		case strings.HasPrefix(trimmed, "## "):
			section = strings.TrimPrefix(trimmed, "## ")
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
		case sectionRecentEvents:
			d.RecentEvents = append(d.RecentEvents, parseListLine(trimmed))
		case sectionRecentCompletions:
			d.RecentCompletions = append(d.RecentCompletions, parseListLine(trimmed))
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
