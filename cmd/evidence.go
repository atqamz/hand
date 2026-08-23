package cmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/atqamz/hand/internal/attention"
	"github.com/atqamz/hand/internal/orientation"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/state"
)

// Everything the orientation evidence builder derives from, loaded once per
// invocation. hand orient and the supervision wait read through it so every
// consumer reasons over exactly the same authoritative level.
type fleetSnapshot struct {
	fleetID          string
	registryWarnings []string
	projects         []project.Project
	backlog          backlogSummary
	cfg              workerConfig
	views            []taskView
	holds            []state.Hold
	next             nextAction
	monitorState     orientation.MonitorState
	monitorReason    string
}

// Performs the one read-only sweep of durable Fleet state the orientation
// path is allowed. Every fault propagates: a partially read Fleet must never
// render as a quiet one.
func loadFleetSnapshot(ctx context.Context, warnOut io.Writer, fleetHome string) (*fleetSnapshot, error) {
	snapshot := &fleetSnapshot{}
	fleetID, err := state.FleetIDReadOnly(fleetHome)
	if err != nil {
		return nil, fmt.Errorf("read Fleet identity for orientation: %w", err)
	}
	snapshot.fleetID = fleetID
	warnings, err := registry.Preflight(fleetHome, true)
	if err != nil {
		return nil, asPrecondition(err)
	}
	snapshot.registryWarnings = warnings
	projects, err := project.ListReadOnly(fleetHome)
	if err != nil {
		return nil, err
	}
	snapshot.projects = projects
	cfg, err := currentWorkerConfig(fleetHome)
	if err != nil {
		return nil, err
	}
	snapshot.cfg = cfg
	backlogPath := filepath.Join(fleetHome, "data", "backlog.md")
	backlog, err := readBacklogSummary(backlogPath, sessionBacklogLimit)
	if err != nil {
		return nil, sessionContextError(fleetHome, backlogPath, err)
	}
	snapshot.backlog = backlog
	client, err := currentHerdrClient(fleetHome)
	if err != nil {
		return nil, err
	}
	views, holds, err := fleetViews(ctx, warnOut, fleetHome, client, true)
	if err != nil {
		return nil, err
	}
	snapshot.views = views
	snapshot.holds = holds
	snapshot.next = classifyNextAction(cfg, len(projects), backlog, views, holds)
	snapshot.monitorState, snapshot.monitorReason, err = readOnlyMonitorState(fleetHome, monitorTargetCount(views))
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

// Assembles the unbounded underlying orientation evidence. Wake eligibility
// consumes this directly; only rendered orientation applies bounds afterwards.
func (s *fleetSnapshot) evidence() orientation.Evidence {
	evidence := orientation.Evidence{FleetID: s.fleetID, MonitorState: s.monitorState}
	for _, warning := range s.registryWarnings {
		evidence.Errors = append(evidence.Errors, orientation.BoundedError{Kind: "registry", Reason: warning})
	}
	for _, view := range s.views {
		targetEvidence := orientation.TaskTargetEvidence(taskTargetFacts(view))
		if monitorableView(view) {
			evidence.Targets = append(evidence.Targets, targetEvidence)
		}
		evidence.Work = append(evidence.Work, orientation.WorkEvidence{ID: view.task.ID, Kind: "task", State: view.agentState, Reported: view.reportedState})
		for _, subject := range attention.Derive(taskAttentionEvidence(view)) {
			if !subject.Actionable {
				continue
			}
			evidence.Actionable = append(evidence.Actionable, orientation.ActionableEvidence{
				TargetID: view.task.ID, TargetKind: "task", Generation: targetEvidence.Generation,
				Kind: subject.Kind, Reason: subject.Reason, Provenance: subject.Provenance,
			})
		}
	}
	if s.next.Kind != "" {
		evidence.NextActions = append(evidence.NextActions, orientation.NextAction{Kind: s.next.Kind, Target: s.next.Task, Command: s.next.Command, Reason: s.next.Reason})
	}
	if s.monitorState != orientation.MonitorStateAlreadyArmed {
		evidence.NextActions = append(evidence.NextActions, orientation.NextAction{
			Kind: "monitor-rearm", Command: "hand watch --until-event",
			Reason: s.monitorReason,
		})
	}
	if s.monitorReason != "" && s.monitorState != orientation.MonitorStateRearmed {
		evidence.Errors = append(evidence.Errors, orientation.BoundedError{Kind: "monitor", Reason: s.monitorReason})
	}
	return evidence
}
