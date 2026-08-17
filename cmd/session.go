package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/agentsmd"
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/shellquote"
	"github.com/spf13/cobra"
)

const sessionBacklogLimit = 80

type backlogSummary struct {
	Items  []string
	Queued int
}

var sessionProjectFields = []axi.Column[project.Project]{
	{Name: "name", Value: func(p project.Project) string { return p.Name }},
	{Name: "mode", Value: func(p project.Project) string { return p.Mode }},
	{Name: "url", Value: func(p project.Project) string { return orNone(p.URL) }},
	{Name: "upstream", Value: func(p project.Project) string { return orNone(p.Upstream) }},
}

func newSessionCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage a supervisor session",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Load the bounded supervisor session context",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionStart(cmd, version)
		},
	})
	return cmd
}

func runSessionStart(cmd *cobra.Command, version string) error {
	if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
		return &ExitError{Err: fmt.Errorf("supervisor session bootstrap is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
	}
	fleetHome, err := home.Resolve()
	if err != nil {
		return asPrecondition(err)
	}
	return renderSessionOverview(cmd, version, fleetHome)
}

func renderSessionOverview(cmd *cobra.Command, version, fleetHome string) error {
	operatorPath := filepath.Join(fleetHome, "data", "operator.md")
	operator, err := os.ReadFile(operatorPath)
	if err != nil {
		return sessionContextError(fleetHome, operatorPath, err)
	}
	backlogPath := filepath.Join(fleetHome, "data", "backlog.md")
	backlog, err := readBacklogSummary(backlogPath, sessionBacklogLimit)
	if err != nil {
		return sessionContextError(fleetHome, backlogPath, err)
	}
	projects, err := project.ListReadOnly(fleetHome)
	if err != nil {
		return err
	}
	cfg, err := currentWorkerConfig(fleetHome)
	if err != nil {
		return err
	}
	cols, err := pickFields(taskFields, nil, fleetDefaultFields)
	if err != nil {
		return err
	}
	views, holds, err := fleetViews(cmd, fleetHome, herdr.NewClient(), true)
	if err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		exe = "unknown"
	}
	detected, source := cfg.detection.Name, cfg.detection.Source
	if detected == "" {
		detected = "unknown"
	}
	if source == "" {
		source = "unknown"
	}

	var doc axi.Doc
	doc.Field("session_bootstrap", "complete")
	doc.Field("tool", "hand")
	doc.Field("version", version)
	doc.Field("exec", tildePath(exe))
	doc.Field("home", tildePath(fleetHome))
	doc.Field("supervisor_harness", detected)
	doc.Field("supervisor_harness_source", source)
	appendWorkerConfig(&doc, cfg)
	doc.Field("operator", strings.TrimSuffix(string(operator), "\n"))
	doc.List("instructions", agentsmd.SupervisorInstructions())
	doc.Int("project_count", len(projects))
	axi.Table(&doc, "projects", projects, sessionProjectFields)
	doc.List("backlog", backlog.Items)
	appendFleetState(&doc, views, holds, cols)
	next := classifyNextAction(cfg, len(projects), backlog, views, holds)
	doc.Field("next_action_kind", next.Kind)
	doc.Field("next_action_task", orNone(next.Task))
	doc.Field("next_action_command", orNone(next.Command))
	doc.Field("next_action_reason", next.Reason)
	doc.Help(next.Reason)
	return doc.Render(cmd.OutOrStdout())
}

func sessionContextError(fleetHome, path string, err error) error {
	return &ExitError{
		Err:  fmt.Errorf("read required session context %s: %w; run `hand init %s` to restore it", path, err, shellquote.Quote(fleetHome)),
		Code: 3,
	}
}

func readBacklogSummary(path string, limit int) (backlogSummary, error) {
	f, err := os.Open(path)
	if err != nil {
		return backlogSummary{}, err
	}
	defer func() { _ = f.Close() }()

	var summary backlogSummary
	queued, truncated := false, false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		emit := strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")
		if strings.HasPrefix(line, "## ") {
			heading := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			queued = strings.EqualFold(heading, "queue") || strings.EqualFold(heading, "queued")
		}
		if (strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")) && queued {
			summary.Queued++
		}
		if !emit {
			continue
		}
		if len(summary.Items) < limit {
			summary.Items = append(summary.Items, line)
		} else {
			truncated = true
		}
	}
	if err := scanner.Err(); err != nil {
		return backlogSummary{}, err
	}
	if truncated {
		summary.Items = append(summary.Items, "truncated: additional backlog identity lines omitted; read data/backlog.md for complete context")
	}
	return summary, nil
}
