package store

import (
	"errors"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// Checks INV-TASK-1,2,3,4,6,7,8,12 (model layer) and INV-TASK-5 (property, the teardown
// transition's natural companion), from "Task and Attempt lifecycle" in
// docs/testing-invariants.md, over generated operation sequences via rapid.StateMachine.
func TestTaskAttemptLifecycleStateMachine(t *testing.T) {
	db, _ := openTemp(t)

	rapid.Check(t, func(rt *rapid.T) {
		// task first: attempt and send_attempt cascade off task_id, but task's own
		// active_attempt_id -> attempt(id) FK carries no ON DELETE CASCADE, so deleting attempt
		// first would fail with a still-referencing task row.
		for _, stmt := range []string{`DELETE FROM task`, `DELETE FROM hold`} {
			if _, err := db.sql.Exec(stmt); err != nil {
				rt.Fatal(err)
			}
		}
		sm := &lifecycleModel{db: db, ids: []string{"a", "b", "c"}, tasks: map[string]*modelTask{}}
		rt.Repeat(rapid.StateMachineActions(sm))
	})
}

// One attempt row's model state: real id, ordinal, currently expected lifecycle, and the
// execution identity it was created with - never updated after creation, so INV-TASK-8 is
// whether the store still agrees.
type modelAttempt struct {
	id             int64
	ordinal        int
	lifecycle      AttemptLifecycle
	harness        string
	model          string
	effort         string
	executionClass string
	plannedAgainst string
	profile        string
	source         string
	digest         string
}

// One task's model state; attempts is append-only and never shrinks or reorders, matching
// INV-TASK-2/5's promise that a row is terminalized, never removed.
type modelTask struct {
	lifecycle      TaskLifecycle
	kind           string
	attempts       []*modelAttempt
	activeOrdinal  int // 0 means no attempt is provisioning or running
	reportOffset   int64
	reportDigest   string
	mergeExecuted  bool
	mergeAnnounced bool
	delivered      bool
}

func (m *modelTask) active() *modelAttempt {
	if m.activeOrdinal == 0 {
		return nil
	}
	return m.attempts[m.activeOrdinal-1]
}

// The rapid.StateMachine. ids is a closed, deliberately small pool so id reuse and terminal
// reactivation are frequent rather than rare, and a shrunk failure names only a couple of
// tasks a reader can follow.
type lifecycleModel struct {
	db      *DB
	ids     []string
	tasks   map[string]*modelTask
	counter int
}

// Mints a small monotonic tag for execution-identity strings and timestamps, not itself
// rapid-drawn: values only need to differ per attempt so a cross-attempt copy can't hide
// behind a coincidental match, and staying undrawn keeps shrunk cases about actions, not text.
func (m *lifecycleModel) next() int {
	m.counter++
	return m.counter
}

func (m *lifecycleModel) pickID(t *rapid.T, label string) string {
	return rapid.SampledFrom(m.ids).Draw(t, label)
}

// Draws which attempt an action targets: the active one, the most recent non-active one
// (often terminal - the INV-TASK-3 reactivation probe), or a deliberately nonexistent id (0,
// which AUTOINCREMENT never issues and never repeats).
func (m *lifecycleModel) target(t *rapid.T, task *modelTask) (id int64, known *modelAttempt) {
	choice := rapid.SampledFrom([]string{"active", "stale", "none"}).Draw(t, "target")
	if task != nil {
		switch choice {
		case "active":
			if a := task.active(); a != nil {
				return a.id, a
			}
		case "stale":
			active := task.active()
			for i := len(task.attempts) - 1; i >= 0; i-- {
				if a := task.attempts[i]; a != active {
					return a.id, a
				}
			}
		}
	}
	return 0, nil
}

func newAttemptDraw(n int) Attempt {
	return Attempt{
		Lifecycle: AttemptProvisioning,
		Harness:   fmt.Sprintf("harness-%d", n), Model: fmt.Sprintf("model-%d", n), Effort: fmt.Sprintf("effort-%d", n),
		ExecutionClass: fmt.Sprintf("class-%d", n), PlannedAgainst: fmt.Sprintf("planned-%d", n),
		RequestedProfile: fmt.Sprintf("profile-%d", n), RoutingSource: fmt.Sprintf("source-%d", n),
		BriefDigest: fmt.Sprintf("digest-%d", n),
	}
}

func newModelAttempt(created Attempt, ordinal int) *modelAttempt {
	return &modelAttempt{
		id: created.ID, ordinal: ordinal, lifecycle: AttemptProvisioning,
		harness: created.Harness, model: created.Model, effort: created.Effort, executionClass: created.ExecutionClass,
		plannedAgainst: created.PlannedAgainst, profile: created.RequestedProfile, source: created.RoutingSource, digest: created.BriefDigest,
	}
}

// Legal iff id names no existing row, open or terminal.
func (m *lifecycleModel) Spawn(t *rapid.T) {
	id := m.pickID(t, "spawn-id")
	kind := rapid.SampledFrom([]string{KindShip, KindScout}).Draw(t, "spawn-kind")
	draw := newAttemptDraw(m.next())
	draw.TaskID = id

	created, err := m.db.CreateTaskWithAttempt(Task{ID: id, Kind: kind, Lifecycle: TaskOpen}, draw)

	if existing := m.tasks[id]; existing != nil {
		if err == nil {
			t.Fatalf("Spawn(%s) succeeded, want a refusal: task already exists (model=%+v)", id, existing)
		}
		return
	}
	if err != nil {
		t.Fatalf("Spawn(%s) = %v, want success on a fresh id", id, err)
	}
	if created.Ordinal != 1 {
		t.Fatalf("Spawn(%s) ordinal = %d, want 1", id, created.Ordinal)
	}
	m.tasks[id] = &modelTask{
		lifecycle: TaskOpen, kind: kind,
		attempts: []*modelAttempt{newModelAttempt(created, 1)}, activeOrdinal: 1,
	}
}

// Bundles MarkLaunchSubmitted, MarkLaunchConfirmed and MarkAttemptRunning - also reconcile's
// confirm-launch arm. Legal iff the target is the task's active, still-provisioning attempt;
// every intermediate guard is implied by that once all three run from empty timestamps.
func (m *lifecycleModel) ConfirmRunning(t *rapid.T) {
	id := m.pickID(t, "confirm-id")
	task := m.tasks[id]
	attemptID, known := m.target(t, task)
	stamp := fmt.Sprintf("t%d", m.next())

	err := m.db.MarkLaunchSubmitted(id, attemptID, stamp)
	if err == nil {
		err = m.db.MarkLaunchConfirmed(id, attemptID, stamp)
	}
	if err == nil {
		err = m.db.MarkAttemptRunning(id, attemptID)
	}

	legal := task != nil && known != nil && task.lifecycle == TaskOpen &&
		task.active() == known && known.lifecycle == AttemptProvisioning
	if !legal {
		if err == nil {
			t.Fatalf("ConfirmRunning(%s, attempt %d) succeeded, want a refusal (task=%+v, target=%+v)", id, attemptID, task, known)
		}
		return
	}
	if err != nil {
		t.Fatalf("ConfirmRunning(%s, attempt %d) = %v, want success", id, attemptID, err)
	}
	known.lifecycle = AttemptRunning
}

// Advances the report cursor; legal whenever the task exists. announce sometimes flips
// merge_announced too, exercising the flag Merge's announce-only path also sets.
func (m *lifecycleModel) Report(t *rapid.T) {
	id := m.pickID(t, "report-id")
	task := m.tasks[id]
	n := m.next()
	offset := int64(n)
	digest := fmt.Sprintf("digest-%d", n)
	announce := rapid.Bool().Draw(t, "report-announces-merge")

	err := m.db.SetTaskReportState(id, offset, digest, announce)
	if task == nil {
		if err == nil {
			t.Fatalf("Report(%s) succeeded, want a refusal: no such task", id)
		}
		return
	}
	if err != nil {
		t.Fatalf("Report(%s) = %v, want success", id, err)
	}
	task.reportOffset = offset
	task.reportDigest = digest
	task.mergeAnnounced = task.mergeAnnounced || announce
}

// Not reflected in modelTask: hold has no foreign key to task or attempt (INV-HOLD-1), so
// Check's ordinary comparison is what would catch an accidental coupling.
func (m *lifecycleModel) SetHold(t *rapid.T) {
	id := m.pickID(t, "hold-id")
	kind := rapid.SampledFrom([]string{HoldKindOperator, HoldKindBlocked, HoldKindLimit}).Draw(t, "hold-kind")
	blockedOn := ""
	if kind == HoldKindBlocked {
		blockedOn = m.pickID(t, "hold-blocked-on")
	}
	if err := m.db.SetHold(Hold{ID: id, Kind: kind, Reason: "reason", BlockedOn: blockedOn, SetAt: "t"}); err != nil {
		t.Fatalf("SetHold(%s) = %v, want success", id, err)
	}
}

func (m *lifecycleModel) ClearHold(t *rapid.T) {
	id := m.pickID(t, "clear-hold-id")
	if err := m.db.ClearHold(id); err != nil && !errors.Is(err, ErrHoldNotFound) {
		t.Fatalf("ClearHold(%s) = %v, want nil or ErrHoldNotFound", id, err)
	}
}

// Legal iff the task is open.
func (m *lifecycleModel) Deliver(t *rapid.T) {
	id := m.pickID(t, "deliver-id")
	task := m.tasks[id]
	n := m.next()

	err := m.db.SetTaskDelivery(id, fmt.Sprintf("t%d", n), fmt.Sprintf("reason-%d", n))

	legal := task != nil && task.lifecycle == TaskOpen
	if !legal {
		if err == nil {
			t.Fatalf("Deliver(%s) succeeded, want a refusal (task=%+v)", id, task)
		}
		return
	}
	if err != nil {
		t.Fatalf("Deliver(%s) = %v, want success", id, err)
	}
	task.delivered = true
}

// Draws between SetTaskMerge (task must be open) and SetTaskMergeAnnounced (existence only) -
// the MergeExecuted/MergeAnnounced distinction INV-REC-11 names, checked here only as a
// correctness property of a primitive this model already drives.
func (m *lifecycleModel) Merge(t *rapid.T) {
	id := m.pickID(t, "merge-id")
	task := m.tasks[id]
	announceOnly := rapid.Bool().Draw(t, "merge-announce-only")
	n := m.next()

	var err error
	if announceOnly {
		err = m.db.SetTaskMergeAnnounced(id)
	} else {
		err = m.db.SetTaskMerge(id, fmt.Sprintf("t%d", n))
	}

	legal := task != nil && (announceOnly || task.lifecycle == TaskOpen)
	if !legal {
		if err == nil {
			t.Fatalf("Merge(%s, announceOnly=%v) succeeded, want a refusal (task=%+v)", id, announceOnly, task)
		}
		return
	}
	if err != nil {
		t.Fatalf("Merge(%s, announceOnly=%v) = %v, want success", id, announceOnly, err)
	}
	if announceOnly {
		task.mergeAnnounced = true
	} else {
		task.mergeExecuted = true
	}
}

// Legal iff the task is open, Kind scout, the given attempt is active, and scoutFrom matches
// its real lifecycle (sometimes drawn as a lie, to probe the ownership-conflict guard).
// Requiring running rather than provisioning is INV-TASK-12.
func (m *lifecycleModel) Promote(t *rapid.T) {
	id := m.pickID(t, "promote-id")
	task := m.tasks[id]
	attemptID, known := m.target(t, task)

	scoutFrom := AttemptLifecycle("")
	if known != nil {
		scoutFrom = known.lifecycle
	}
	if rapid.Bool().Draw(t, "promote-lies-about-scout-lifecycle") {
		scoutFrom = rapid.SampledFrom([]AttemptLifecycle{
			AttemptProvisioning, AttemptRunning, AttemptCompleted, AttemptFailed, AttemptInterrupted,
		}).Draw(t, "promote-lied-lifecycle")
	}
	n := m.next()
	ship := newAttemptDraw(n)
	ship.TaskID = id

	created, err := m.db.PromoteTask(id, attemptID, scoutFrom, ship)

	legal := task != nil && known != nil && task.lifecycle == TaskOpen && task.kind == KindScout &&
		task.active() == known && scoutFrom == known.lifecycle && known.lifecycle == AttemptRunning
	if !legal {
		if err == nil {
			t.Fatalf("Promote(%s) succeeded, want a refusal (task=%+v, target=%+v, scoutFrom=%s)", id, task, known, scoutFrom)
		}
		return
	}
	if err != nil {
		t.Fatalf("Promote(%s) = %v, want success", id, err)
	}
	known.lifecycle = AttemptCompleted
	newAttempt := newModelAttempt(created, known.ordinal+1)
	task.attempts = append(task.attempts, newAttempt)
	task.activeOrdinal = newAttempt.ordinal
	task.kind = KindShip
	// PromoteTask clears delivered_at/delivered_reason: the scout's delivery mark no longer
	// speaks for the ship attempt's own work (promote.go's own Result.Help says so).
	task.delivered = false
}

// attemptTo is drawn from all five lifecycle values, including the two active ones, to probe
// illegal reactivation (the isActiveAttempt guard) alongside confirmRunning's probe of the
// same INV-TASK-3 territory. Also INV-TASK-5: terminalizes, never deletes.
func (m *lifecycleModel) Teardown(t *rapid.T) {
	id := m.pickID(t, "teardown-id")
	task := m.tasks[id]
	attemptID, known := m.target(t, task)
	to := rapid.SampledFrom([]AttemptLifecycle{
		AttemptProvisioning, AttemptRunning, AttemptCompleted, AttemptFailed, AttemptInterrupted,
	}).Draw(t, "teardown-to")
	from := AttemptLifecycle("")
	if known != nil {
		from = known.lifecycle
	}

	err := m.db.TerminalizeTaskAndAttempt(id, attemptID, from, to)

	validTransition := (from == AttemptProvisioning && (to == AttemptRunning || to == AttemptFailed || to == AttemptInterrupted)) ||
		(from == AttemptRunning && (to == AttemptCompleted || to == AttemptFailed || to == AttemptInterrupted))
	activeTarget := to == AttemptProvisioning || to == AttemptRunning
	legal := task != nil && known != nil && task.lifecycle == TaskOpen && task.active() == known && validTransition && !activeTarget
	if !legal {
		if err == nil {
			t.Fatalf("Teardown(%s, %s -> %s) succeeded, want a refusal (task=%+v, target=%+v)", id, from, to, task, known)
		}
		return
	}
	if err != nil {
		t.Fatalf("Teardown(%s, %s -> %s) = %v, want success", id, from, to, err)
	}
	known.lifecycle = to
	task.lifecycle = TaskTerminal
	task.activeOrdinal = 0
}

// Legal iff the task exists and is terminal; every prior attempt row must stay byte-identical
// after, checked in Check against the model's own snapshot (INV-TASK-6).
func (m *lifecycleModel) Reopen(t *rapid.T) {
	id := m.pickID(t, "reopen-id")
	task := m.tasks[id]
	draw := newAttemptDraw(m.next())
	draw.TaskID = id

	created, err := m.db.ReopenTask(id, draw)

	legal := task != nil && task.lifecycle == TaskTerminal
	if !legal {
		if err == nil {
			t.Fatalf("Reopen(%s) succeeded, want a refusal (task=%+v)", id, task)
		}
		return
	}
	if err != nil {
		t.Fatalf("Reopen(%s) = %v, want success", id, err)
	}
	newAttempt := newModelAttempt(created, len(task.attempts)+1)
	task.attempts = append(task.attempts, newAttempt)
	task.activeOrdinal = newAttempt.ordinal
	task.lifecycle = TaskOpen
}

func (m *lifecycleModel) Check(t *rapid.T) {
	for _, id := range m.ids {
		task := m.tasks[id]
		history, found, err := m.db.ReadTaskHistory(id)
		if err != nil {
			t.Fatalf("ReadTaskHistory(%s) = %v", id, err)
		}
		if task == nil {
			if found {
				t.Fatalf("ReadTaskHistory(%s) found a row though the model never spawned it", id)
			}
			continue
		}
		// INV-TASK-5: teardown terminalizes, it never deletes - the row must still be there.
		if !found {
			t.Fatalf("ReadTaskHistory(%s) not found, want the row this model spawned", id)
		}
		if history.Task.Lifecycle != task.lifecycle {
			t.Fatalf("task %s lifecycle = %s, want %s", id, history.Task.Lifecycle, task.lifecycle)
		}
		if history.Task.Kind != task.kind {
			t.Fatalf("task %s kind = %s, want %s", id, history.Task.Kind, task.kind)
		}
		// INV-TASK-4: neither promote nor reopen ever touches this in the model, so any drift
		// here after either one is a real violation.
		if history.Task.ReportOffset != task.reportOffset || history.Task.ReportDigest != task.reportDigest {
			t.Fatalf("task %s report cursor = (%d, %s), want (%d, %s)", id, history.Task.ReportOffset, history.Task.ReportDigest, task.reportOffset, task.reportDigest)
		}
		if history.Task.MergeExecuted != task.mergeExecuted {
			t.Fatalf("task %s merge_executed = %v, want %v", id, history.Task.MergeExecuted, task.mergeExecuted)
		}
		if history.Task.MergeAnnounced != task.mergeAnnounced {
			t.Fatalf("task %s merge_announced = %v, want %v", id, history.Task.MergeAnnounced, task.mergeAnnounced)
		}
		if (history.Task.DeliveredAt != "") != task.delivered {
			t.Fatalf("task %s delivered_at = %q, want delivered=%v", id, history.Task.DeliveredAt, task.delivered)
		}

		// INV-TASK-1, counted independently of active_attempt_id: at most one attempt in an
		// active lifecycle value, and it must be exactly the one the model tracks as active.
		activeCount := 0
		var activeID int64
		for _, a := range history.Attempts {
			if a.Lifecycle == AttemptProvisioning || a.Lifecycle == AttemptRunning {
				activeCount++
				activeID = a.ID
			}
		}
		if activeCount > 1 {
			t.Fatalf("task %s has %d provisioning-or-running attempts, want at most 1", id, activeCount)
		}
		wantActive := task.active()
		if (wantActive != nil) != (activeCount == 1) {
			t.Fatalf("task %s active-attempt count = %d, want present=%v", id, activeCount, wantActive != nil)
		}
		if wantActive != nil && activeID != wantActive.id {
			t.Fatalf("task %s's provisioning-or-running attempt is %d, want %d", id, activeID, wantActive.id)
		}
		wantActiveID := int64(0)
		if wantActive != nil {
			wantActiveID = wantActive.id
		}
		if history.Task.ActiveAttemptID != wantActiveID {
			t.Fatalf("task %s active_attempt_id = %d, want %d", id, history.Task.ActiveAttemptID, wantActiveID)
		}

		// INV-TASK-2 (ordinals contiguous, never reused) and INV-TASK-8 (execution identity
		// write-once) together; INV-TASK-3 falls out of never un-terminalizing what the model
		// has already marked terminal.
		if len(history.Attempts) != len(task.attempts) {
			t.Fatalf("task %s has %d attempts, want %d", id, len(history.Attempts), len(task.attempts))
		}
		for i, want := range task.attempts {
			got := history.Attempts[i]
			if got.Ordinal != i+1 || got.Ordinal != want.ordinal {
				t.Fatalf("task %s attempt index %d has ordinal %d, want %d", id, i, got.Ordinal, i+1)
			}
			if got.ID != want.id {
				t.Fatalf("task %s attempt index %d has id %d, want %d", id, i, got.ID, want.id)
			}
			if got.Lifecycle != want.lifecycle {
				t.Fatalf("attempt %d (task %s) lifecycle = %s, want %s", got.ID, id, got.Lifecycle, want.lifecycle)
			}
			if got.Harness != want.harness || got.Model != want.model || got.Effort != want.effort ||
				got.ExecutionClass != want.executionClass || got.PlannedAgainst != want.plannedAgainst ||
				got.RequestedProfile != want.profile || got.RoutingSource != want.source || got.BriefDigest != want.digest {
				t.Fatalf("attempt %d (task %s) execution identity drifted from creation: got %+v, want harness=%s model=%s effort=%s class=%s planned=%s profile=%s source=%s digest=%s",
					got.ID, id, got, want.harness, want.model, want.effort, want.executionClass, want.plannedAgainst, want.profile, want.source, want.digest)
			}
		}
	}
}
