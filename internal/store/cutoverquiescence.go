package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var errLegacyV18CutoverDurableStateUnsafe = errors.New("legacy v18 cutover durable state is not quiescent")

type legacyV18CutoverBlocker struct {
	Code    string
	Subject string
	Detail  string
}

type legacyV18CutoverBlockedError struct {
	Blockers []legacyV18CutoverBlocker
}

func (e *legacyV18CutoverBlockedError) Error() string {
	if e == nil || len(e.Blockers) == 0 {
		return errLegacyV18CutoverDurableStateUnsafe.Error()
	}
	parts := make([]string, 0, len(e.Blockers))
	for _, blocker := range e.Blockers {
		part := blocker.Code + " " + blocker.Subject
		if blocker.Detail != "" {
			part += ": " + blocker.Detail
		}
		parts = append(parts, part)
	}
	return errLegacyV18CutoverDurableStateUnsafe.Error() + ": " + strings.Join(parts, "; ")
}

func (e *legacyV18CutoverBlockedError) Unwrap() error {
	return errLegacyV18CutoverDurableStateUnsafe
}

type legacyV18CutoverProjectObservationRequest struct {
	ProjectID string
	Name      string
	URL       string
	Mode      string
	Upstream  string
	ClonePath string
}

type legacyV18CutoverWorktreeObservationRequest struct {
	TaskID        string
	AttemptID     int64
	ProjectID     string
	ProjectName   string
	ClonePath     string
	WorktreePath  string
	LeaseID       string
	TeardownState string
}

type legacyV18CutoverHerdrObservationRequest struct {
	TaskID        string
	AttemptID     int64
	ProjectID     string
	ProjectName   string
	Session       string
	WorkspaceID   string
	TabID         string
	PaneID        string
	TeardownState string
}

type legacyV18CutoverObservationPlan struct {
	FleetID   string
	Projects  []legacyV18CutoverProjectObservationRequest
	Worktrees []legacyV18CutoverWorktreeObservationRequest
	Herdr     []legacyV18CutoverHerdrObservationRequest
}

type legacyV18CutoverTaskProject struct {
	ProjectID string
}

func planLegacyV18CutoverObservations(ctx context.Context, homeDir string, gate *legacyV18CutoverGate, locks *legacyV18CutoverLocks) (legacyV18CutoverObservationPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if gate == nil || gate.conn == nil || gate.releaseMigration == nil {
		return legacyV18CutoverObservationPlan{}, fmt.Errorf("%w: 5A2 EXCLUSIVE source gate is not held", errLegacyV18CutoverDurableStateUnsafe)
	}
	if locks == nil || len(locks.held) == 0 {
		return legacyV18CutoverObservationPlan{}, fmt.Errorf("%w: 5A3 Fleet-local lock closure is not held", errLegacyV18CutoverDurableStateUnsafe)
	}
	for _, held := range locks.held {
		if err := held.verifyPathIdentity(); err != nil {
			return legacyV18CutoverObservationPlan{}, fmt.Errorf("%w: revalidate Fleet-local lock closure: %w", errLegacyV18CutoverDurableStateUnsafe, err)
		}
	}
	var queryOnly int
	if err := gate.conn.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil {
		return legacyV18CutoverObservationPlan{}, fmt.Errorf("inspect legacy v18 quiescence query_only: %w", err)
	}
	if queryOnly != 1 {
		return legacyV18CutoverObservationPlan{}, fmt.Errorf("%w: EXCLUSIVE source gate query_only = %d, want 1", errLegacyV18CutoverDurableStateUnsafe, queryOnly)
	}
	return classifyLegacyV18CutoverDurableState(homeDir, sqliteConnQueryer{ctx: ctx, conn: gate.conn})
}

func classifyLegacyV18CutoverDurableState(homeDir string, q sqliteQueryer) (legacyV18CutoverObservationPlan, error) {
	plan := legacyV18CutoverObservationPlan{}
	var blockers []legacyV18CutoverBlocker
	addBlocker := func(code, subject, detail string) {
		blockers = append(blockers, legacyV18CutoverBlocker{Code: code, Subject: subject, Detail: detail})
	}

	fleetRows, err := q.Query(`SELECT singleton, fleet_id FROM fleet_identity ORDER BY singleton`)
	if err != nil {
		return plan, fmt.Errorf("read legacy v18 Fleet identity for cutover quiescence: %w", err)
	}
	fleetCount := 0
	for fleetRows.Next() {
		fleetCount++
		var singleton int
		var fleetID string
		if err := fleetRows.Scan(&singleton, &fleetID); err != nil {
			_ = fleetRows.Close()
			return plan, fmt.Errorf("read legacy v18 Fleet identity for cutover quiescence: %w", err)
		}
		if singleton != 1 {
			addBlocker("fleet-identity-invalid", "fleet", fmt.Sprintf("singleton=%d", singleton))
			continue
		}
		if err := validateFleetID(fleetID); err != nil {
			addBlocker("fleet-identity-invalid", "fleet", err.Error())
			continue
		}
		plan.FleetID = fleetID
	}
	if err := fleetRows.Err(); err != nil {
		_ = fleetRows.Close()
		return plan, fmt.Errorf("read legacy v18 Fleet identity for cutover quiescence: %w", err)
	}
	if err := fleetRows.Close(); err != nil {
		return plan, fmt.Errorf("close legacy v18 Fleet identity for cutover quiescence: %w", err)
	}
	if fleetCount != 1 {
		addBlocker("fleet-identity-invalid", "fleet", fmt.Sprintf("rows=%d, want 1", fleetCount))
	}

	projectsByID := make(map[string]legacyV18CutoverProjectObservationRequest)
	projectRows, err := q.Query(`SELECT id, name, url, mode, upstream FROM project ORDER BY position, name`)
	if err != nil {
		return plan, fmt.Errorf("read legacy v18 Projects for cutover quiescence: %w", err)
	}
	for projectRows.Next() {
		var project legacyV18CutoverProjectObservationRequest
		if err := projectRows.Scan(&project.ProjectID, &project.Name, &project.URL, &project.Mode, &project.Upstream); err != nil {
			_ = projectRows.Close()
			return plan, fmt.Errorf("read legacy v18 Projects for cutover quiescence: %w", err)
		}
		project.ClonePath = filepath.Join(homeDir, "projects", project.Name)
		if !validLegacyV18ProjectID(project.ProjectID) {
			addBlocker("project-identity-invalid", "project:"+project.Name, "surrogate identity is not a p_<32 lowercase hex> value")
		}
		if !validLegacyV18CutoverProjectName(project.Name) {
			addBlocker("project-name-invalid", "project:"+project.Name, "name is not a registry-safe path segment")
		}
		projectsByID[project.ProjectID] = project
		plan.Projects = append(plan.Projects, project)
	}
	if err := projectRows.Err(); err != nil {
		_ = projectRows.Close()
		return plan, fmt.Errorf("read legacy v18 Projects for cutover quiescence: %w", err)
	}
	if err := projectRows.Close(); err != nil {
		return plan, fmt.Errorf("close legacy v18 Projects for cutover quiescence: %w", err)
	}

	taskProjects := make(map[string]legacyV18CutoverTaskProject)
	taskRows, err := q.Query(`SELECT id, project_id, lifecycle, active_attempt_id, repair_code FROM task ORDER BY id`)
	if err != nil {
		return plan, fmt.Errorf("read legacy v18 Tasks for cutover quiescence: %w", err)
	}
	for taskRows.Next() {
		var id, projectID, lifecycle, repairCode string
		var activeAttempt sql.NullInt64
		if err := taskRows.Scan(&id, &projectID, &lifecycle, &activeAttempt, &repairCode); err != nil {
			_ = taskRows.Close()
			return plan, fmt.Errorf("read legacy v18 Tasks for cutover quiescence: %w", err)
		}
		taskProjects[id] = legacyV18CutoverTaskProject{ProjectID: projectID}
		if lifecycle != string(TaskTerminal) {
			addBlocker("task-nonterminal", "task:"+id, "lifecycle="+strconv.Quote(lifecycle))
		}
		if activeAttempt.Valid {
			addBlocker("task-active-attempt", "task:"+id, fmt.Sprintf("active_attempt_id=%d", activeAttempt.Int64))
		}
		if repairCode != "" {
			addBlocker("task-repair-open", "task:"+id, "repair_code="+strconv.Quote(repairCode))
		}
	}
	if err := taskRows.Err(); err != nil {
		_ = taskRows.Close()
		return plan, fmt.Errorf("read legacy v18 Tasks for cutover quiescence: %w", err)
	}
	if err := taskRows.Close(); err != nil {
		return plan, fmt.Errorf("close legacy v18 Tasks for cutover quiescence: %w", err)
	}

	holdRows, err := q.Query(`SELECT id, kind FROM hold ORDER BY id`)
	if err != nil {
		return plan, fmt.Errorf("read legacy v18 Holds for cutover quiescence: %w", err)
	}
	for holdRows.Next() {
		var id, kind string
		if err := holdRows.Scan(&id, &kind); err != nil {
			_ = holdRows.Close()
			return plan, fmt.Errorf("read legacy v18 Holds for cutover quiescence: %w", err)
		}
		addBlocker("hold-open", "hold:"+id, "kind="+strconv.Quote(kind))
	}
	if err := holdRows.Err(); err != nil {
		_ = holdRows.Close()
		return plan, fmt.Errorf("read legacy v18 Holds for cutover quiescence: %w", err)
	}
	if err := holdRows.Close(); err != nil {
		return plan, fmt.Errorf("close legacy v18 Holds for cutover quiescence: %w", err)
	}

	attemptRows, err := q.Query(`SELECT id, task_id, lifecycle,
		worktree, lease_id,
		herdr_session, herdr_workspace_id, herdr_tab_id, herdr_pane_id,
		launch_submitted_at, launch_confirmed_at,
		send_undelivered_message, usage_limit_retry_at,
		teardown_terminal_attempt, teardown_disposition,
		teardown_herdr_state, teardown_worktree_state, teardown_completion_state
		FROM attempt ORDER BY task_id, ordinal, id`)
	if err != nil {
		return plan, fmt.Errorf("read legacy v18 Attempts for cutover quiescence: %w", err)
	}
	for attemptRows.Next() {
		var id int64
		var taskID, lifecycle string
		var worktreePath, leaseID string
		var herdrSession, herdrWorkspaceID, herdrTabID, herdrPaneID string
		var launchSubmittedAt, launchConfirmedAt string
		var sendUndeliveredMessage, usageLimitRetryAt string
		var teardownTerminalAttempt, teardownDisposition string
		var teardownHerdrState, teardownWorktreeState, teardownCompletionState string
		if err := attemptRows.Scan(
			&id, &taskID, &lifecycle,
			&worktreePath, &leaseID,
			&herdrSession, &herdrWorkspaceID, &herdrTabID, &herdrPaneID,
			&launchSubmittedAt, &launchConfirmedAt,
			&sendUndeliveredMessage, &usageLimitRetryAt,
			&teardownTerminalAttempt, &teardownDisposition,
			&teardownHerdrState, &teardownWorktreeState, &teardownCompletionState,
		); err != nil {
			_ = attemptRows.Close()
			return plan, fmt.Errorf("read legacy v18 Attempts for cutover quiescence: %w", err)
		}
		subject := "attempt:" + strconv.FormatInt(id, 10)
		if !legacyV18AttemptLifecycleTerminal(lifecycle) {
			addBlocker("attempt-nonterminal", subject, "lifecycle="+strconv.Quote(lifecycle))
		}
		if teardownTerminalAttempt != "" && teardownTerminalAttempt != lifecycle {
			addBlocker("attempt-teardown-lifecycle-mismatch", subject, "lifecycle="+strconv.Quote(lifecycle)+" teardown_terminal_attempt="+strconv.Quote(teardownTerminalAttempt))
		}
		if launchConfirmedAt != "" && launchSubmittedAt == "" {
			addBlocker("attempt-launch-evidence-invalid", subject, "launch confirmation exists without durable submission evidence")
		}
		if (launchSubmittedAt != "" || launchConfirmedAt != "") && teardownDisposition == "" && legacyV18AttemptLifecycleTerminal(lifecycle) {
			addBlocker("attempt-launch-unresolved", subject, "terminal launched attempt has no durable teardown disposition")
		}
		if usageLimitRetryAt != "" {
			addBlocker("attempt-backoff-open", subject, "usage-limit retry/backoff state remains open")
		}
		if sendUndeliveredMessage != "" {
			addBlocker("attempt-staged-input-residual", subject, "legacy undelivered input residual is non-empty")
		}
		switch teardownCompletionState {
		case "", TeardownCompletionAppended:
		case TeardownCompletionPending:
			addBlocker("attempt-completion-pending", subject, "teardown completion append remains pending")
		default:
			addBlocker("attempt-completion-state-unknown", subject, "teardown_completion_state="+strconv.Quote(teardownCompletionState))
		}

		taskProject := taskProjects[taskID]
		// An empty ProjectID is deliberate historical provenance when the registered Project was
		// removed. Exact v0.7.2 migration refuses to reattach that task if the name is later reused,
		// so cutover must never recover authority by falling back to task.project.
		project, projectKnown := projectsByID[taskProject.ProjectID]

		if worktreePath == "" {
			if leaseID != "" || teardownWorktreeState != "" {
				addBlocker("worktree-identity-inconsistent", subject, "worktree path is empty while lease/teardown state is present")
			}
		} else {
			if !projectKnown {
				addBlocker("worktree-project-unresolved", subject, "current Project identity for the recorded worktree cannot be resolved")
			}
			if !legacyV18ResourceStateNeedsObservation(teardownWorktreeState) {
				addBlocker("worktree-release-unresolved", subject, "teardown_worktree_state="+strconv.Quote(teardownWorktreeState))
			} else if projectKnown {
				plan.Worktrees = append(plan.Worktrees, legacyV18CutoverWorktreeObservationRequest{
					TaskID: taskID, AttemptID: id,
					ProjectID: project.ProjectID, ProjectName: project.Name, ClonePath: project.ClonePath,
					WorktreePath: worktreePath, LeaseID: leaseID, TeardownState: teardownWorktreeState,
				})
			}
		}

		hasHerdrIdentity := herdrWorkspaceID != "" || herdrTabID != "" || herdrPaneID != ""
		if !hasHerdrIdentity {
			if teardownHerdrState != "" {
				addBlocker("herdr-identity-inconsistent", subject, "Herdr identity is empty while teardown state is present")
			}
		} else if herdrWorkspaceID == "" || herdrTabID == "" || herdrPaneID == "" {
			addBlocker("herdr-identity-incomplete", subject, "workspace, tab, and pane identity are not all present")
		} else if !projectKnown {
			addBlocker("herdr-project-unresolved", subject, "current Project identity for the recorded Herdr ownership cannot be resolved")
		} else if !legacyV18ResourceStateNeedsObservation(teardownHerdrState) {
			addBlocker("herdr-release-unresolved", subject, "teardown_herdr_state="+strconv.Quote(teardownHerdrState))
		} else {
			plan.Herdr = append(plan.Herdr, legacyV18CutoverHerdrObservationRequest{
				TaskID: taskID, AttemptID: id,
				ProjectID: project.ProjectID, ProjectName: project.Name,
				Session: herdrSession, WorkspaceID: herdrWorkspaceID, TabID: herdrTabID, PaneID: herdrPaneID,
				TeardownState: teardownHerdrState,
			})
		}
	}
	if err := attemptRows.Err(); err != nil {
		_ = attemptRows.Close()
		return plan, fmt.Errorf("read legacy v18 Attempts for cutover quiescence: %w", err)
	}
	if err := attemptRows.Close(); err != nil {
		return plan, fmt.Errorf("close legacy v18 Attempts for cutover quiescence: %w", err)
	}

	sendRows, err := q.Query(`SELECT id, task_id, attempt_id, state, reason_code FROM send_attempt ORDER BY id`)
	if err != nil {
		return plan, fmt.Errorf("read legacy v18 Sends for cutover quiescence: %w", err)
	}
	for sendRows.Next() {
		var id, attemptID int64
		var taskID, sendState, reasonCode string
		if err := sendRows.Scan(&id, &taskID, &attemptID, &sendState, &reasonCode); err != nil {
			_ = sendRows.Close()
			return plan, fmt.Errorf("read legacy v18 Sends for cutover quiescence: %w", err)
		}
		subject := "send:" + strconv.FormatInt(id, 10)
		switch SendState(sendState) {
		case SendSubmitted:
		case SendNotSubmitted:
			if !SendRetrySafe(SendAttempt{State: SendNotSubmitted, ReasonCode: reasonCode}) {
				addBlocker("send-staged-or-unresolved", subject, "state=\"not-submitted\" reason_code="+strconv.Quote(reasonCode))
			}
		case SendPending, SendUncertain:
			addBlocker("send-unresolved", subject, "state="+strconv.Quote(sendState)+" reason_code="+strconv.Quote(reasonCode))
		default:
			addBlocker("send-state-unknown", subject, "state="+strconv.Quote(sendState))
		}
	}
	if err := sendRows.Err(); err != nil {
		_ = sendRows.Close()
		return plan, fmt.Errorf("read legacy v18 Sends for cutover quiescence: %w", err)
	}
	if err := sendRows.Close(); err != nil {
		return plan, fmt.Errorf("close legacy v18 Sends for cutover quiescence: %w", err)
	}

	if len(blockers) != 0 {
		sort.Slice(blockers, func(i, j int) bool {
			if blockers[i].Code != blockers[j].Code {
				return blockers[i].Code < blockers[j].Code
			}
			if blockers[i].Subject != blockers[j].Subject {
				return blockers[i].Subject < blockers[j].Subject
			}
			return blockers[i].Detail < blockers[j].Detail
		})
		return legacyV18CutoverObservationPlan{}, &legacyV18CutoverBlockedError{Blockers: blockers}
	}

	sort.Slice(plan.Projects, func(i, j int) bool {
		if plan.Projects[i].ProjectID != plan.Projects[j].ProjectID {
			return plan.Projects[i].ProjectID < plan.Projects[j].ProjectID
		}
		return plan.Projects[i].Name < plan.Projects[j].Name
	})
	sort.Slice(plan.Worktrees, func(i, j int) bool {
		if plan.Worktrees[i].TaskID != plan.Worktrees[j].TaskID {
			return plan.Worktrees[i].TaskID < plan.Worktrees[j].TaskID
		}
		return plan.Worktrees[i].AttemptID < plan.Worktrees[j].AttemptID
	})
	sort.Slice(plan.Herdr, func(i, j int) bool {
		if plan.Herdr[i].TaskID != plan.Herdr[j].TaskID {
			return plan.Herdr[i].TaskID < plan.Herdr[j].TaskID
		}
		return plan.Herdr[i].AttemptID < plan.Herdr[j].AttemptID
	})
	return plan, nil
}

func legacyV18AttemptLifecycleTerminal(lifecycle string) bool {
	switch AttemptLifecycle(lifecycle) {
	case AttemptCompleted, AttemptFailed, AttemptInterrupted:
		return true
	default:
		return false
	}
}

func legacyV18ResourceStateNeedsObservation(state string) bool {
	return state == TeardownResourceReleased || state == TeardownResourceAbandoned
}

func validLegacyV18ProjectID(id string) bool {
	if len(id) != len(projectIDPrefix)+32 || !strings.HasPrefix(id, projectIDPrefix) {
		return false
	}
	for _, r := range id[len(projectIDPrefix):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validLegacyV18CutoverProjectName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		alphaNumeric := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !alphaNumeric && r != '.' && r != '_' && r != '-' {
			return false
		}
		if i == 0 && !alphaNumeric {
			return false
		}
	}
	return true
}
