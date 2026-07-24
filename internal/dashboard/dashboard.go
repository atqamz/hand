// Package dashboard reads, renders, and updates data/dashboard.md, the fleet's
// living status file described in SPECS.md.
package dashboard

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

type UpdateOpts struct {
	AddActiveTask        *ActiveTask
	UpdateAgentState     *AgentStateUpdate
	AddEvent             string
	Complete             *Completion
	SetPendingDecision   *PendingDecision
	ClearPendingDecision string
	SetProjects          []ProjectSummary
}

// Update performs a read-modify-write of the dashboard at path, applying opts and
// stamping the Updated timestamp. Every hand command that mutates fleet state calls
// this so data/dashboard.md stays current.
func Update(path string, opts UpdateOpts) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read dashboard: %w", err)
	}
	d, err := Parse(data)
	if err != nil {
		return err
	}

	apply(&d, opts)
	d.Updated = time.Now().UTC().Format(TimeFormat)

	return writeAtomic(path, Render(d))
}

func apply(d *Dashboard, opts UpdateOpts) {
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
	if c := opts.Complete; c != nil {
		d.ActiveTasks = removeActiveTask(d.ActiveTasks, c.ID)
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
	d := time.Since(t)
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

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dashboard.md-")
	if err != nil {
		return fmt.Errorf("create temp dashboard file: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := func() { _ = os.Remove(tmpName) }

	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("chmod temp dashboard file: %w", err)
	}
	n, err := tmp.Write(data)
	if err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("write temp dashboard file: %w", err)
	}
	if n != len(data) {
		_ = tmp.Close()
		removeTemp()
		return io.ErrShortWrite
	}
	if err := tmp.Close(); err != nil {
		removeTemp()
		return fmt.Errorf("close temp dashboard file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		removeTemp()
		return fmt.Errorf("rename temp dashboard file: %w", err)
	}
	return nil
}
