package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/agentsmd"
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/orientation"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/shellquote"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/supervision"
	"github.com/atqamz/hand/internal/watcher"
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

// One-time runtime bootstrap: resolve the Fleet, validate the role, identify
// the harness and its runtime, qualify the installed bridge. Read-only against
// static integration (init owns those bytes) and not a current-work read.
func runSessionStart(cmd *cobra.Command, version string) error {
	if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
		return &ExitError{Err: fmt.Errorf("supervisor session bootstrap is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
	}
	fleetHome, err := home.Resolve()
	if err != nil {
		return asPrecondition(err)
	}
	if _, err := state.FleetIDReadOnly(fleetHome); err != nil {
		// A home without fleet identity is a bootstrap precondition: the
		// remedy is init. Any other store fault stays the owning reader's
		// general error rather than being reclassified.
		if errors.Is(err, state.ErrFleetIdentityMissing) {
			return &ExitError{Err: fmt.Errorf("read Fleet identity for supervisor bootstrap: %w; run `hand init %s` to restore it", err, shellquote.Quote(fleetHome)), Code: 3}
		}
		return fmt.Errorf("read Fleet identity for supervisor bootstrap: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "unknown"
	}
	detection, err := harness.DetectCurrent()
	if err != nil {
		return err
	}

	status, err := supervision.IntegrationStatus(cmd.Context(), supervision.StatusInput{
		Home:      fleetHome,
		Detection: detection,
		Exe:       exe,
	})
	if err != nil {
		return err
	}

	var doc axi.Doc
	doc.Field("session_bootstrap", "complete")
	doc.Field("tool", "hand")
	doc.Field("version", version)
	doc.Field("exec", tildePath(exe))
	doc.Field("home", tildePath(fleetHome))
	doc.Field("supervisor_harness", orNone(status.Harness))
	doc.Field("supervisor_harness_source", orNone(status.HarnessSource))
	doc.Field("host_runtime_version", orNone(status.RuntimeVersion))
	doc.Field("host_api_generation", orNone(status.RuntimeAPIGeneration))
	doc.Field("host_platform", orNone(status.RuntimePlatform))
	doc.Field("host_capability", orNone(status.RuntimeCapability))
	doc.Field("host_capability_qualification", orNone(status.Qualification))
	doc.Field("host_capability_qualification_reason", orNone(status.QualificationReason))
	doc.Field("runtime_identity_status", orNone(status.RuntimeIdentity))
	doc.Field("runtime_identity_detail", orNone(status.RuntimeDetail))
	doc.Field("bootstrap_integration_status", orNone(status.Integration))
	doc.Field("bootstrap_integration_detail", orNone(status.IntegrationDetail))
	doc.Field("integration_mechanism_version", orNone(status.IntegrationVersion))
	doc.Field("addressability_status", orNone(status.Addressability))
	doc.Field("addressability_reason", orNone(status.AddressabilityReason))
	doc.Field("wake_delivery_capability", status.WakeDelivery)
	doc.Field("wake_delivery_reason", orNone(status.WakeDeliveryReason))
	doc.Field("wake_delivery_attachment_status", orNone(status.Attachment))
	doc.Field("watcher_observer_liveness", orNone(status.WatcherLiveness))
	lastAccepted, lastOriented := supervision.OpenLedger(fleetHome).Progress()
	doc.Field("wake_delivery_last_accepted", timestampOrNone(lastAccepted))
	doc.Field("orientation_progress", timestampOrNone(lastOriented))
	doc.Field("next_command", "hand orient")
	help := []string{
		"Before reasoning or acting in every Supervisor turn, run `hand orient`",
		"After an automatic wake/re-entry, run `hand orient` before any action",
	}
	switch status.WakeDelivery {
	case supervision.CapabilityDegraded:
		help = append(help, "Unattended Supervisor turn delivery is degraded here: "+status.WakeDeliveryReason)
	case supervision.CapabilityUnsupported:
		help = append(help, "Unattended Supervisor turn delivery is unsupported here: "+status.WakeDeliveryReason)
	case supervision.CapabilityUnqualified:
		help = append(help, "Wake delivery is available but not yet live-qualified on this build: "+status.WakeDeliveryReason)
	}
	if status.Integration == "stale" || status.Integration == "absent" || status.Integration == "conflict" {
		help = append(help, "Repair Hand-owned integration with `hand init "+shellquote.Quote(fleetHome)+"`; then restart or reload this harness if it caches configuration at startup")
	}
	doc.Help(help...)
	return doc.Render(cmd.OutOrStdout())
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
	registryWarnings, err := registry.Preflight(fleetHome, true)
	if err != nil {
		return asPrecondition(err)
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
	client, err := currentHerdrClient(fleetHome)
	if err != nil {
		return err
	}
	views, holds, blockers, err := fleetViews(cmd.Context(), cmd.ErrOrStderr(), fleetHome, client, true)
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

	next := classifyNextAction(cfg, len(projects), backlog, views, holds)
	monitorState, monitorReason, err := readOnlyMonitorState(fleetHome, monitorTargetCount(views))
	if err != nil {
		return err
	}
	oriented, err := buildSessionOrientation(cmd.Context(), fleetHome, views, next, monitorState, monitorReason, registryWarnings)
	if err != nil {
		return err
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
	appendHerdrSession(&doc, client.ObserveSession(cmd.Context()))
	appendFleetState(&doc, views, holds, blockers, cols)
	appendSessionOrientation(&doc, oriented)
	doc.Field("next_action_kind", next.Kind)
	doc.Field("next_action_task", orNone(next.Task))
	doc.Field("next_action_command", orNone(next.Command))
	doc.Field("next_action_reason", next.Reason)
	doc.Help(next.Reason)
	return doc.Render(cmd.OutOrStdout())
}

func buildSessionOrientation(ctx context.Context, fleetHome string, views []taskView, next nextAction, monitorState orientation.MonitorState, monitorReason string, registryWarnings []string) (orientation.SupervisorOrientation, error) {
	fleetID, err := state.FleetIDReadOnly(fleetHome)
	if err != nil {
		return orientation.SupervisorOrientation{}, fmt.Errorf("read Fleet identity for orientation: %w", err)
	}
	provider := orientation.NewProvider(func(context.Context) (orientation.Evidence, error) {
		evidence := orientation.Evidence{FleetID: fleetID, MonitorState: monitorState}
		for _, warning := range registryWarnings {
			evidence.Errors = append(evidence.Errors, orientation.BoundedError{Kind: "registry", Reason: warning})
		}
		for _, view := range views {
			targetEvidence := orientation.TaskTargetEvidence(taskTargetFacts(view))
			if monitorableView(view) {
				evidence.Targets = append(evidence.Targets, targetEvidence)
			}
			evidence.Work = append(evidence.Work, orientation.WorkEvidence{ID: view.task.ID, Kind: "task", State: view.agentState, Reported: view.reportedState})
			appendTaskAttention(&evidence, view, targetEvidence)
		}
		if next.Kind != "" {
			evidence.NextActions = []orientation.NextAction{{Kind: next.Kind, Target: next.Task, Command: next.Command, Reason: next.Reason}}
		}
		if monitorState != orientation.MonitorStateAlreadyArmed {
			evidence.NextActions = append(evidence.NextActions, orientation.NextAction{
				Kind: "monitor-rearm", Command: "hand watch --until-event",
				Reason: monitorReason,
			})
		}
		if monitorReason != "" && monitorState != orientation.MonitorStateRearmed {
			evidence.Errors = append(evidence.Errors, orientation.BoundedError{Kind: "monitor", Reason: monitorReason})
		}
		return evidence, nil
	})
	return provider.Orientation(ctx)
}

func sessionTargets(fleetID string, views []taskView) []watcher.TargetBinding {
	targets := make([]watcher.TargetBinding, 0, len(views))
	for _, view := range views {
		if !monitorableView(view) {
			continue
		}
		target := orientation.TaskTarget(fleetID, taskTargetFacts(view))
		targets = append(targets, watcher.TargetBinding{TaskID: view.task.ID, Target: target})
	}
	return targets
}

func monitorTargetCount(views []taskView) int {
	count := 0
	for _, view := range views {
		if monitorableView(view) {
			count++
		}
	}
	return count
}

func readOnlyMonitorState(fleetHome string, targetCount int) (orientation.MonitorState, string, error) {
	attached, err := watcher.IsAttached(fleetHome)
	if err != nil {
		return orientation.MonitorStateUnknown, "watcher ownership is unknown: " + err.Error(), nil
	}
	if attached {
		return orientation.MonitorStateAlreadyArmed, "a watcher already owns this Fleet home", nil
	}
	if targetCount == 0 {
		return orientation.MonitorStateRearmed, "no monitor targets require a bounded arm", nil
	}
	return orientation.MonitorStateUnknown, "run `hand watch --until-event` to establish monitoring", nil
}

func monitorableView(view taskView) bool {
	return view.task.Lifecycle == state.TaskOpen && view.attempt != nil && view.attempt.Lifecycle == state.AttemptRunning
}

func taskTargetFacts(view taskView) orientation.TaskTargetFacts {
	attempt := view.execution()
	return orientation.TaskTargetFacts{
		ID: view.task.ID, Kind: "task", CreatedAt: view.task.CreatedAt, Lifecycle: string(view.task.Lifecycle),
		ActiveAttemptID: view.task.ActiveAttemptID, AttemptID: attempt.ID, AttemptLifecycle: string(attempt.Lifecycle),
		RuntimeIdentity: []string{attempt.Herdr.Session, attempt.Herdr.WorkspaceID, attempt.Herdr.TabID, attempt.Herdr.PaneID},
		StatusChangedAt: attempt.StatusChangedAt, StatusChangedFor: attempt.StatusChangedFor, ReportState: view.reportedState,
		ReportOffset: view.task.ReportOffset, ReportDigest: view.task.ReportDigest, DoneVerified: attempt.DoneVerified,
		PR: view.task.PR, MergeExecuted: view.task.MergeExecuted, MergeAnnounced: view.task.MergeAnnounced,
	}
}

func appendSessionOrientation(doc *axi.Doc, result orientation.SupervisorOrientation) {
	doc.Field("orientation_schema", result.Schema)
	doc.Field("fleet_id", result.FleetID)
	doc.Field("monitor_state", string(result.MonitorState))
	doc.Bool("orientation_truncated", result.Truncated)
	doc.Int("orientation_omitted", result.Omitted)
	monitorRows := make([][]string, 0, len(result.Monitors))
	for _, target := range result.Monitors {
		monitorRows = append(monitorRows, []string{target.ID, target.Kind, target.Currentness.String()})
	}
	doc.Rows("monitor_targets", []string{"id", "kind", "currentness"}, monitorRows)
	workRows := make([][]string, 0, len(result.Work))
	for _, work := range result.Work {
		workRows = append(workRows, []string{work.ID, work.Kind, work.State, work.Reported, work.Monitor.ID, work.Monitor.Currentness.String()})
	}
	doc.Rows("orientation_work", []string{"id", "kind", "state", "reported", "target_id", "currentness"}, workRows)
	actionRows := make([][]string, 0, len(result.Actionable))
	for _, subject := range result.Actionable {
		actionRows = append(actionRows, []string{subject.Target.ID, subject.Kind, subject.Reason, subject.Provenance})
	}
	doc.Rows("orientation_actionable", []string{"target_id", "kind", "reason", "provenance"}, actionRows)
	deferredRows := make([][]string, 0, len(result.Deferred))
	for _, subject := range result.Deferred {
		deferredRows = append(deferredRows, []string{subject.TargetID, subject.Kind, subject.HoldKind, subject.BlockedOn, subject.Provenance})
	}
	doc.Rows("orientation_deferred", []string{"target_id", "kind", "hold_kind", "blocked_on", "provenance"}, deferredRows)
	nextRows := make([][]string, 0, len(result.NextActions))
	for _, action := range result.NextActions {
		nextRows = append(nextRows, []string{action.Kind, action.Target, action.Command, action.Reason})
	}
	doc.Rows("orientation_next_actions", []string{"kind", "target", "command", "reason"}, nextRows)
	errorRows := make([][]string, 0, len(result.Errors))
	for _, failure := range result.Errors {
		errorRows = append(errorRows, []string{failure.Kind, failure.Reason})
	}
	doc.Rows("orientation_errors", []string{"kind", "reason"}, errorRows)
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
