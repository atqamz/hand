package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

const legacyV18QuiescenceTestProjectID = "p_00000000000000000000000000000001"

func TestClassifyLegacyV18CutoverDurableStateAcceptsTerminalArchiveOnlyHistory(t *testing.T) {
	home := createLegacyV18DurableQuiescenceFixture(t)

	plan, err := classifyLegacyV18DurableQuiescenceTestHome(t, home)
	if err != nil {
		t.Fatalf("classifyLegacyV18CutoverDurableState: %v", err)
	}
	if err := validateFleetID(plan.FleetID); err != nil {
		t.Fatalf("FleetID = %q: %v", plan.FleetID, err)
	}
	if len(plan.Projects) != 1 || plan.Projects[0].ProjectID != legacyV18QuiescenceTestProjectID || plan.Projects[0].Name != "demo" {
		t.Fatalf("Projects = %+v, want exact demo Project observation request", plan.Projects)
	}
	if plan.Projects[0].ClonePath != filepath.Join(home, "projects", "demo") {
		t.Fatalf("ClonePath = %q, want managed demo clone", plan.Projects[0].ClonePath)
	}
	if len(plan.Worktrees) != 0 || len(plan.Herdr) != 0 {
		t.Fatalf("resource observations = worktrees %+v Herdr %+v, want none", plan.Worktrees, plan.Herdr)
	}
}

func TestClassifyLegacyV18CutoverDurableStateRejectsSafetyRelevantOpenFacts(t *testing.T) {
	tests := []struct {
		name string
		code string
		mutate func(t *testing.T, db *DB)
	}{
		{
			name: "open task",
			code: "task-nonterminal",
			mutate: func(t *testing.T, db *DB) {
				execLegacyV18DurableQuiescenceTest(t, db, `UPDATE task SET lifecycle = 'open' WHERE id = 'task-1'`)
			},
		},
		{
			name: "active attempt relation",
			code: "task-active-attempt",
			mutate: func(t *testing.T, db *DB) {
				execLegacyV18DurableQuiescenceTest(t, db, `UPDATE task SET active_attempt_id = (SELECT id FROM attempt WHERE task_id = 'task-1') WHERE id = 'task-1'`)
			},
		},
		{
			name: "repair marker",
			code: "task-repair-open",
			mutate: func(t *testing.T, db *DB) {
				execLegacyV18DurableQuiescenceTest(t, db, `UPDATE task SET repair_code = 'running-pane-missing' WHERE id = 'task-1'`)
			},
		},
		{
			name: "hold",
			code: "hold-open",
			mutate: func(t *testing.T, db *DB) {
				execLegacyV18DurableQuiescenceTest(t, db, `INSERT INTO hold (id, kind, reason, set_at) VALUES ('operator-hold', 'operator', 'stop', '2026-01-01T00:00:00Z')`)
			},
		},
		{
			name: "running attempt",
			code: "attempt-nonterminal",
			mutate: func(t *testing.T, db *DB) {
				execLegacyV18DurableQuiescenceTest(t, db, `UPDATE attempt SET lifecycle = 'running' WHERE task_id = 'task-1'`)
			},
		},
		{
			name: "usage limit backoff",
			code: "attempt-backoff-open",
			mutate: func(t *testing.T, db *DB) {
				execLegacyV18DurableQuiescenceTest(t, db, `UPDATE attempt SET usage_limit_retry_at = '2026-01-02T00:00:00Z' WHERE task_id = 'task-1'`)
			},
		},
		{
			name: "legacy staged input residual",
			code: "attempt-staged-input-residual",
			mutate: func(t *testing.T, db *DB) {
				execLegacyV18DurableQuiescenceTest(t, db, `UPDATE attempt SET send_undelivered_message = 'staged but unresolved' WHERE task_id = 'task-1'`)
			},
		},
		{
			name: "completion pending",
			code: "attempt-completion-pending",
			mutate: func(t *testing.T, db *DB) {
				execLegacyV18DurableQuiescenceTest(t, db, `UPDATE attempt SET teardown_completion_state = 'pending' WHERE task_id = 'task-1'`)
			},
		},
		{
			name: "pending send",
			code: "send-unresolved",
			mutate: func(t *testing.T, db *DB) {
				insertLegacyV18DurableQuiescenceSend(t, db, "pending", "pending-before-text")
			},
		},
		{
			name: "uncertain send",
			code: "send-unresolved",
			mutate: func(t *testing.T, db *DB) {
				insertLegacyV18DurableQuiescenceSend(t, db, "uncertain", "enter-outcome-ambiguous")
			},
		},
		{
			name: "not submitted but composer touched",
			code: "send-staged-or-unresolved",
			mutate: func(t *testing.T, db *DB) {
				insertLegacyV18DurableQuiescenceSend(t, db, "not-submitted", SendReasonEnterRejectedAfterTextStaged)
			},
		},
		{
			name: "worktree release unresolved",
			code: "worktree-release-unresolved",
			mutate: func(t *testing.T, db *DB) {
				execLegacyV18DurableQuiescenceTest(t, db, `UPDATE attempt SET worktree = '/tmp/demo-worktree', lease_id = 'lease-1' WHERE task_id = 'task-1'`)
			},
		},
		{
			name: "incomplete Herdr ownership",
			code: "herdr-identity-incomplete",
			mutate: func(t *testing.T, db *DB) {
				execLegacyV18DurableQuiescenceTest(t, db, `UPDATE attempt SET herdr_workspace_id = 'workspace-1', teardown_herdr_state = 'abandoned' WHERE task_id = 'task-1'`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := createLegacyV18DurableQuiescenceFixture(t)
			db, err := Open(home)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, db)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			_, err = classifyLegacyV18DurableQuiescenceTestHome(t, home)
			requireLegacyV18CutoverBlockerCode(t, err, test.code)
		})
	}
}

func TestClassifyLegacyV18CutoverDurableStateAcceptsOnlyRetrySafeNotSubmittedSend(t *testing.T) {
	home := createLegacyV18DurableQuiescenceFixture(t)
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	insertLegacyV18DurableQuiescenceSend(t, db, "not-submitted", SendReasonTextRejectedBeforeAcceptance+":PRE_SIDE_EFFECT")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := classifyLegacyV18DurableQuiescenceTestHome(t, home); err != nil {
		t.Fatalf("retry-safe not-submitted send blocked cutover: %v", err)
	}
}

func TestClassifyLegacyV18CutoverDurableStatePlansReleasedAndAbandonedResourceObservations(t *testing.T) {
	home := createLegacyV18DurableQuiescenceFixture(t)
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	execLegacyV18DurableQuiescenceTest(t, db, `UPDATE attempt SET
		worktree = '/tmp/demo-worktree', lease_id = 'lease-1', teardown_worktree_state = 'released',
		herdr_session = 'hand-fleet', herdr_workspace_id = 'workspace-1', herdr_tab_id = 'tab-1', herdr_pane_id = 'pane-1', teardown_herdr_state = 'abandoned',
		teardown_disposition = 'completed'
		WHERE task_id = 'task-1'`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	plan, err := classifyLegacyV18DurableQuiescenceTestHome(t, home)
	if err != nil {
		t.Fatalf("classifyLegacyV18CutoverDurableState: %v", err)
	}
	if len(plan.Worktrees) != 1 {
		t.Fatalf("Worktrees = %+v, want one observation", plan.Worktrees)
	}
	if got := plan.Worktrees[0]; got.ProjectID != legacyV18QuiescenceTestProjectID || got.ProjectName != "demo" || got.LeaseID != "lease-1" || got.TeardownState != TeardownResourceReleased {
		t.Fatalf("Worktree observation = %+v", got)
	}
	if len(plan.Herdr) != 1 {
		t.Fatalf("Herdr = %+v, want one observation", plan.Herdr)
	}
	if got := plan.Herdr[0]; got.ProjectID != legacyV18QuiescenceTestProjectID || got.ProjectName != "demo" || got.WorkspaceID != "workspace-1" || got.TabID != "tab-1" || got.PaneID != "pane-1" || got.TeardownState != TeardownResourceAbandoned {
		t.Fatalf("Herdr observation = %+v", got)
	}
}

func TestClassifyLegacyV18CutoverDurableStateResolvesCurrentProjectThroughProjectID(t *testing.T) {
	home := createLegacyV18DurableQuiescenceFixture(t)
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	execLegacyV18DurableQuiescenceTest(t, db, `UPDATE project SET name = 'renamed' WHERE id = '`+legacyV18QuiescenceTestProjectID+`'`)
	execLegacyV18DurableQuiescenceTest(t, db, `UPDATE attempt SET worktree = '/tmp/demo-worktree', lease_id = 'lease-1', teardown_worktree_state = 'released' WHERE task_id = 'task-1'`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	plan, err := classifyLegacyV18DurableQuiescenceTestHome(t, home)
	if err != nil {
		t.Fatalf("classifyLegacyV18CutoverDurableState: %v", err)
	}
	if len(plan.Worktrees) != 1 || plan.Worktrees[0].ProjectName != "renamed" || plan.Worktrees[0].ClonePath != filepath.Join(home, "projects", "renamed") {
		t.Fatalf("Worktree observation = %+v, want current project identity/name", plan.Worktrees)
	}
}

func TestPlanLegacyV18CutoverObservationsRequiresLiveGateAndLockClosure(t *testing.T) {
	home := createLegacyV18DurableQuiescenceFixture(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Close() }()
	locks, err := acquireLegacyV18CutoverLocks(context.Background(), home, gate)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := planLegacyV18CutoverObservations(context.Background(), home, gate, locks); err != nil {
		t.Fatalf("plan with live gate and locks: %v", err)
	}
	if err := locks.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := planLegacyV18CutoverObservations(context.Background(), home, gate, locks); !errors.Is(err, errLegacyV18CutoverDurableStateUnsafe) {
		t.Fatalf("plan after lock release = %v, want errLegacyV18CutoverDurableStateUnsafe", err)
	}
}

func createLegacyV18DurableQuiescenceFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	execLegacyV18DurableQuiescenceTest(t, db, `INSERT INTO project (id, name, url, mode, position, upstream)
		VALUES ('`+legacyV18QuiescenceTestProjectID+`', 'demo', 'https://github.com/example/demo.git', 'direct-pr', 0, '')`)
	execLegacyV18DurableQuiescenceTest(t, db, `INSERT INTO task (id, project, project_id, kind, brief, lifecycle, created_at)
		VALUES ('task-1', 'demo', '`+legacyV18QuiescenceTestProjectID+`', 'ship', 'brief.md', 'terminal', '2026-01-01T00:00:00Z')`)
	execLegacyV18DurableQuiescenceTest(t, db, `INSERT INTO attempt (task_id, ordinal, lifecycle, created_at)
		VALUES ('task-1', 1, 'completed', '2026-01-01T00:00:00Z')`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return home
}

func classifyLegacyV18DurableQuiescenceTestHome(t *testing.T, home string) (legacyV18CutoverObservationPlan, error) {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	return classifyLegacyV18CutoverDurableState(home, db.sql)
}

func execLegacyV18DurableQuiescenceTest(t *testing.T, db *DB, statement string) {
	t.Helper()
	if _, err := db.sql.Exec(statement); err != nil {
		t.Fatalf("exec %q: %v", statement, err)
	}
}

func insertLegacyV18DurableQuiescenceSend(t *testing.T, db *DB, state, reason string) {
	t.Helper()
	execLegacyV18DurableQuiescenceTest(t, db, `INSERT INTO send_attempt
		(task_id, attempt_id, origin, message, state, reason_code, created_at, finalized_at)
		VALUES ('task-1', (SELECT id FROM attempt WHERE task_id = 'task-1'), 'operator', 'message', '`+state+`', '`+reason+`', '2026-01-01T00:00:00Z', '2026-01-01T00:00:01Z')`)
}

func requireLegacyV18CutoverBlockerCode(t *testing.T, err error, code string) {
	t.Helper()
	if !errors.Is(err, errLegacyV18CutoverDurableStateUnsafe) {
		t.Fatalf("error = %v, want errLegacyV18CutoverDurableStateUnsafe", err)
	}
	var blocked *legacyV18CutoverBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %T %v, want *legacyV18CutoverBlockedError", err, err)
	}
	for _, blocker := range blocked.Blockers {
		if blocker.Code == code {
			return
		}
	}
	t.Fatalf("blockers = %+v, want code %q", blocked.Blockers, code)
}
