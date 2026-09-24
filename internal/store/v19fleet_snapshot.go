package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const CanonicalV19FleetSnapshotSchema = "hand.fleet.core.v1"

type CanonicalV19FleetSnapshot struct {
	Schema                      string
	Completeness                string
	FleetID                     string
	DBReadAt                    string
	Projects                    []CanonicalV19SnapshotProject
	CurrentOpenWorktreeBindings []CanonicalV19SnapshotCurrentWorktreeBinding
	CurrentOpenSessionBindings  []CanonicalV19SnapshotCurrentSessionBinding
	CurrentOpenExecutorBindings []CanonicalV19SnapshotCurrentExecutorBinding
	LatestReportMetadata        []CanonicalV19SnapshotLatestWorkerReportMetadata
	UnacknowledgedInputs        []CanonicalV19SnapshotWorkerInput
	UnresolvedOperations        []CanonicalV19SnapshotOperation
	CurrentOpenTaskHolds        []CanonicalV19SnapshotTaskHold
}

type CanonicalV19SnapshotProject struct {
	ID          string
	Ordinal     int64
	DisplayName string
	RetiredAt   string
	Workspace   *CanonicalV19SnapshotWorkspace
	Policy      *CanonicalV19SnapshotPolicy
	Tasks       []CanonicalV19SnapshotTask
}

type CanonicalV19SnapshotWorkspace struct {
	ID                     string
	RepositoryLocator      string
	PhysicalIdentityDigest string
	Revision               string
}

type CanonicalV19SnapshotPolicy struct {
	ID           string
	PolicyDigest string
}

type CanonicalV19SnapshotTask struct {
	ID         string
	Ordinal    int64
	GoalDigest string
	Plan       *CanonicalV19SnapshotPlan
}

type CanonicalV19SnapshotPlan struct {
	ID                                      string
	Ordinal                                 int64
	LineageKind                             string
	Intent                                  string
	Judgment                                string
	BriefDigest                             string
	WorkspaceBindingID                      string
	PolicyRevisionID                        string
	CapturedWorkspaceRevision               string
	CapturedWorkspacePhysicalIdentityDigest string
	CapturedPolicyDigest                    string
	Attempt                                 *CanonicalV19SnapshotAttempt
}

type CanonicalV19SnapshotAttempt struct {
	ID                string
	Ordinal           int64
	WorkerHarnessRef  string
	WorkerProfileRef  string
	ModelRef          string
	EffortRef         string
	SessionAdapterRef string
}

func ReadCanonicalV19FleetSnapshot(ctx context.Context, homeDir string) (CanonicalV19FleetSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot := CanonicalV19FleetSnapshot{
		Schema: CanonicalV19FleetSnapshotSchema, Completeness: "partial",
		Projects: make([]CanonicalV19SnapshotProject, 0),
	}
	db, err := openReadOnly(homeDir)
	if err != nil {
		return CanonicalV19FleetSnapshot{}, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CanonicalV19FleetSnapshot{}, fmt.Errorf("begin canonical FleetSnapshot read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19FleetSnapshot{}, fmt.Errorf("validate canonical FleetSnapshot source: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT fleet_id FROM fleet WHERE singleton=1`).Scan(&snapshot.FleetID); err != nil {
		return CanonicalV19FleetSnapshot{}, fmt.Errorf("read canonical FleetSnapshot identity: %w", err)
	}
	snapshot.DBReadAt = time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := tx.QueryContext(ctx, `SELECT p.id,p.ordinal,p.display_name,p.retired_at,
		COALESCE(w.id,''),COALESCE(w.repository_locator,''),COALESCE(w.physical_identity_digest,''),COALESCE(w.revision,''),
		COALESCE(pr.id,''),COALESCE(pr.policy_digest,''),
		COALESCE(t.id,''),COALESCE(t.ordinal,0),COALESCE(t.goal_digest,''),
		COALESCE(pl.id,''),COALESCE(pl.ordinal,0),COALESCE(pl.lineage_kind,''),COALESCE(pl.intent,''),
		COALESCE(pl.judgment,''),COALESCE(pl.brief_digest,''),COALESCE(pl.workspace_binding_id,''),
		COALESCE(pl.policy_revision_id,''),COALESCE(pw.revision,''),
		COALESCE(pw.physical_identity_digest,''),COALESCE(pp.policy_digest,''),
		COALESCE(a.id,''),COALESCE(a.ordinal,0),COALESCE(a.worker_harness_ref,''),
		COALESCE(a.worker_profile_ref,''),COALESCE(a.model_ref,''),COALESCE(a.effort_ref,''),
		COALESCE(a.session_adapter_ref,'')
		FROM project p
		LEFT JOIN workspace_binding w ON w.project_id=p.id AND w.superseded_at=''
		LEFT JOIN policy_revision pr ON pr.project_id=p.id AND pr.superseded_at=''
		LEFT JOIN task t ON t.project_id=p.id AND t.lifecycle='active'
		LEFT JOIN plan pl ON pl.task_id=t.id AND pl.lifecycle='active'
		LEFT JOIN workspace_binding pw ON pw.id=pl.workspace_binding_id
		LEFT JOIN policy_revision pp ON pp.id=pl.policy_revision_id
		LEFT JOIN attempt a ON a.plan_id=pl.id AND a.lifecycle='active'
		ORDER BY p.ordinal,p.id,t.ordinal,t.id`)
	if err != nil {
		return CanonicalV19FleetSnapshot{}, fmt.Errorf("read canonical FleetSnapshot lineage: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var project CanonicalV19SnapshotProject
		var workspace CanonicalV19SnapshotWorkspace
		var policy CanonicalV19SnapshotPolicy
		var task CanonicalV19SnapshotTask
		var plan CanonicalV19SnapshotPlan
		var attempt CanonicalV19SnapshotAttempt
		if err := rows.Scan(
			&project.ID, &project.Ordinal, &project.DisplayName, &project.RetiredAt,
			&workspace.ID, &workspace.RepositoryLocator, &workspace.PhysicalIdentityDigest, &workspace.Revision,
			&policy.ID, &policy.PolicyDigest,
			&task.ID, &task.Ordinal, &task.GoalDigest,
			&plan.ID, &plan.Ordinal, &plan.LineageKind, &plan.Intent, &plan.Judgment,
			&plan.BriefDigest, &plan.WorkspaceBindingID, &plan.PolicyRevisionID,
			&plan.CapturedWorkspaceRevision, &plan.CapturedWorkspacePhysicalIdentityDigest,
			&plan.CapturedPolicyDigest,
			&attempt.ID, &attempt.Ordinal, &attempt.WorkerHarnessRef, &attempt.WorkerProfileRef,
			&attempt.ModelRef, &attempt.EffortRef, &attempt.SessionAdapterRef,
		); err != nil {
			return CanonicalV19FleetSnapshot{}, fmt.Errorf("scan canonical FleetSnapshot lineage: %w", err)
		}
		if len(snapshot.Projects) == 0 || snapshot.Projects[len(snapshot.Projects)-1].ID != project.ID {
			project.Tasks = make([]CanonicalV19SnapshotTask, 0)
			if workspace.ID != "" {
				project.Workspace = &workspace
			}
			if policy.ID != "" {
				project.Policy = &policy
			}
			snapshot.Projects = append(snapshot.Projects, project)
		}
		if task.ID != "" {
			if plan.ID != "" {
				if attempt.ID != "" {
					plan.Attempt = &attempt
				}
				task.Plan = &plan
			}
			project := &snapshot.Projects[len(snapshot.Projects)-1]
			project.Tasks = append(project.Tasks, task)
		}
	}
	if err := rows.Err(); err != nil {
		return CanonicalV19FleetSnapshot{}, fmt.Errorf("iterate canonical FleetSnapshot lineage: %w", err)
	}
	if err := rows.Close(); err != nil {
		return CanonicalV19FleetSnapshot{}, fmt.Errorf("close canonical FleetSnapshot lineage: %w", err)
	}
	snapshot.CurrentOpenWorktreeBindings, err = readCanonicalV19SnapshotCurrentWorktreeBindings(ctx, tx)
	if err != nil {
		return CanonicalV19FleetSnapshot{}, err
	}
	snapshot.CurrentOpenSessionBindings, err = readCanonicalV19SnapshotCurrentSessionBindings(ctx, tx)
	if err != nil {
		return CanonicalV19FleetSnapshot{}, err
	}
	snapshot.CurrentOpenExecutorBindings, err = readCanonicalV19SnapshotCurrentExecutorBindings(ctx, tx)
	if err != nil {
		return CanonicalV19FleetSnapshot{}, err
	}
	snapshot.LatestReportMetadata, err = readCanonicalV19SnapshotLatestWorkerReportMetadata(ctx, tx)
	if err != nil {
		return CanonicalV19FleetSnapshot{}, err
	}
	snapshot.UnacknowledgedInputs, err = readCanonicalV19SnapshotInputs(ctx, tx)
	if err != nil {
		return CanonicalV19FleetSnapshot{}, err
	}
	snapshot.UnresolvedOperations, err = readCanonicalV19SnapshotOperations(ctx, tx)
	if err != nil {
		return CanonicalV19FleetSnapshot{}, err
	}
	snapshot.CurrentOpenTaskHolds, err = readCanonicalV19SnapshotCurrentTaskHolds(ctx, tx)
	if err != nil {
		return CanonicalV19FleetSnapshot{}, err
	}
	return snapshot, nil
}
