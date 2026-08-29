package cmd

import (
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/store"
)

var (
	unconfiguredWorker = workerConfig{}
	configuredWorker   = workerConfig{harness: harness.Codex}
)

func TestClassifyNextActionExactPrecedence(t *testing.T) {
	queuedBacklog := backlogSummary{Queued: 1}
	uncertainSend := &state.SendAttempt{State: state.SendUncertain}
	partialSend := &state.SendAttempt{State: state.SendNotSubmitted, ReasonCode: state.SendReasonEnterRejectedAfterTextStaged}
	notSubmittedSend := &state.SendAttempt{State: state.SendNotSubmitted, ReasonCode: state.SendReasonTextRejectedBeforeAcceptance}
	pendingSend := &state.SendAttempt{State: state.SendPending}

	tests := []struct {
		name     string
		cfg      workerConfig
		projects int
		backlog  backlogSummary
		views    []taskView
		holds    []state.Hold
		want     nextAction
	}{
		{
			"missing harness leads everything",
			unconfiguredWorker, 0, backlogSummary{}, nil, nil,
			nextAction{Kind: nextActionNeedsConfig, Command: "hand config set harness <name>", Reason: workerConfigHelp(unconfiguredWorker)[0]},
		},
		{
			"needs-repair outranks queued work",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "repair-task", RepairCode: "E_ORPHAN"}}}, nil,
			nextAction{Kind: nextActionNeedsRepair, Task: "repair-task", Command: "hand status repair-task", Reason: statusReason("repair-task", "resolve its repair ambiguity")},
		},
		{
			"send-uncertain outranks queued work",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "send-task"}, latestSend: uncertainSend}}, nil,
			nextAction{Kind: nextActionSendUncertain, Task: "send-task", Command: "hand status send-task", Reason: statusReason("send-task", "confirm whether its uncertain send reached the worker")},
		},
		{
			"send-uncertain outranks send-partial",
			configuredWorker, 1, backlogSummary{},
			[]taskView{
				{task: state.Task{ID: "b-partial"}, latestSend: partialSend},
				{task: state.Task{ID: "a-uncertain"}, latestSend: uncertainSend},
			}, nil,
			nextAction{Kind: nextActionSendUncertain, Task: "a-uncertain", Command: "hand status a-uncertain", Reason: statusReason("a-uncertain", "confirm whether its uncertain send reached the worker")},
		},
		{
			"send-partial outranks queued work",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "send-task"}, latestSend: partialSend}}, nil,
			nextAction{Kind: nextActionSendPartial, Task: "send-task", Command: "hand status send-task", Reason: statusReason("send-task", "confirm whether its send needs to be retried")},
		},
		{
			"send-not-submitted outranks queued work",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "send-task"}, latestSend: notSubmittedSend}}, nil,
			nextAction{Kind: nextActionSendNotSubmitted, Task: "send-task", Command: "hand status send-task", Reason: statusReason("send-task", "resend the message that was not submitted")},
		},
		{
			"send-partial outranks send-not-submitted",
			configuredWorker, 1, backlogSummary{},
			[]taskView{
				{task: state.Task{ID: "b-not-submitted"}, latestSend: notSubmittedSend},
				{task: state.Task{ID: "a-partial"}, latestSend: partialSend},
			}, nil,
			nextAction{Kind: nextActionSendPartial, Task: "a-partial", Command: "hand status a-partial", Reason: statusReason("a-partial", "confirm whether its send needs to be retried")},
		},
		{
			"send-pending is not an attention condition",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "send-task"}, latestSend: pendingSend}}, nil,
			nextAction{Kind: nextActionQueued, Command: "hand spawn <id> <project>", Reason: "Read `data/backlog.md` and prepare the queued task; dispatch it with `hand spawn <id> <project>` when its brief is ready"},
		},
		{
			"needs-decision outranks queued work",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "decide-task"}, reportedState: state.ReportNeedsDecision}}, nil,
			nextAction{Kind: state.ReportNeedsDecision, Task: "decide-task", Command: "hand status decide-task", Reason: statusReason("decide-task", "answer the operator decision it is waiting on")},
		},
		{
			"failed outranks queued work",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "failed-task"}, reportedState: state.ReportFailed}}, nil,
			nextAction{Kind: state.ReportFailed, Task: "failed-task", Command: "hand status failed-task", Reason: statusReason("failed-task", "investigate why it failed")},
		},
		{
			"report-unreadable outranks queued work",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "unreadable-task"}, unreadable: true}}, nil,
			nextAction{Kind: nextActionReportUnreadable, Task: "unreadable-task", Command: "hand status unreadable-task", Reason: statusReason("unreadable-task", "investigate its unreadable report")},
		},
		{
			"unreported outranks queued work",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "silent-task"}, agentState: "idle"}}, nil,
			nextAction{Kind: nextActionUnreported, Task: "silent-task", Command: "hand status silent-task", Reason: "Run `hand status silent-task`; it stopped without reporting a terminal state"},
		},
		{
			"blocked outranks queued work",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "blocked-task"}, reportedState: state.ReportBlocked}}, nil,
			nextAction{Kind: state.ReportBlocked, Task: "blocked-task", Command: "hand status blocked-task", Reason: statusReason("blocked-task", "resolve what is blocking it")},
		},
		{
			"paused outranks queued work",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "paused-task"}, reportedState: state.ReportPaused}}, nil,
			nextAction{Kind: state.ReportPaused, Task: "paused-task", Command: "hand status paused-task", Reason: statusReason("paused-task", "resume or resolve why it is paused")},
		},
		{
			"unacknowledged outranks a hold",
			configuredWorker, 1, backlogSummary{},
			[]taskView{{task: state.Task{ID: "unacked-task"}, unacked: true}}, []state.Hold{{ID: "held-task"}},
			nextAction{Kind: nextActionUnacknowledged, Task: "unacked-task", Command: "hand status unacked-task", Reason: statusReason("unacked-task", "act on its unacknowledged worker event")},
		},
		{
			"hold outranks needs-project",
			configuredWorker, 0, backlogSummary{}, nil, []state.Hold{{ID: "orphan-hold"}},
			nextAction{Kind: nextActionHold, Task: "orphan-hold", Command: "none", Reason: "Operator or supervisor judgment is needed to resolve its active hold"},
		},
		{
			"hold without a task has no safe command",
			configuredWorker, 0, backlogSummary{}, nil, []state.Hold{{ID: "orphan-hold"}},
			nextAction{Kind: nextActionHold, Task: "orphan-hold", Command: "none", Reason: "Operator or supervisor judgment is needed to resolve its active hold"},
		},
		{
			"operator hold suppresses underlying attention",
			configuredWorker, 1, backlogSummary{}, []taskView{{task: state.Task{ID: "operator-held"}, held: true, agentState: "idle"}},
			[]state.Hold{{ID: "operator-held", Kind: state.HoldKindOperator, Reason: "needs a call"}},
			nextAction{Kind: nextActionMonitor, Command: "hand watch --until-event", Reason: "Run `hand watch --until-event` as a background task; re-arm it after an event or when intentionally resuming after interruption or takeover"},
		},
		{
			"blocked hold suppresses underlying attention",
			configuredWorker, 1, backlogSummary{}, []taskView{{task: state.Task{ID: "blocked-held"}, held: true, agentState: "idle"}},
			[]state.Hold{{ID: "blocked-held", Kind: state.HoldKindBlocked, BlockedOn: "other-task", Reason: "waiting for other-task"}},
			nextAction{Kind: nextActionMonitor, Command: "hand watch --until-event", Reason: "Run `hand watch --until-event` as a background task; re-arm it after an event or when intentionally resuming after interruption or takeover"},
		},
		{
			"gate outranks queued work",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "gate-task"}, gateObserved: ghutil.ObservationAbsent}}, nil,
			nextAction{Kind: nextActionGate, Task: "gate-task", Command: "hand status gate-task", Reason: statusReason("gate-task", "confirm its delivery gate status")},
		},
		{
			"terminal work with no obligation does not steal attention",
			configuredWorker, 1, queuedBacklog,
			[]taskView{{task: state.Task{ID: "clean-task"}}}, nil,
			nextAction{Kind: nextActionQueued, Command: "hand spawn <id> <project>", Reason: "Read `data/backlog.md` and prepare the queued task; dispatch it with `hand spawn <id> <project>` when its brief is ready"},
		},
		{
			"needs-project when nothing is registered",
			configuredWorker, 0, backlogSummary{}, nil, nil,
			nextAction{Kind: nextActionNeedsProject, Command: "hand project add <source>", Reason: "Run `hand project add <source>` or `hand project create <name>` to register the first project"},
		},
		{
			"queued work is selected when nothing stronger exists",
			configuredWorker, 1, queuedBacklog, nil, nil,
			nextAction{Kind: nextActionQueued, Command: "hand spawn <id> <project>", Reason: "Read `data/backlog.md` and prepare the queued task; dispatch it with `hand spawn <id> <project>` when its brief is ready"},
		},
		{
			"monitor when the fleet is active and nothing is queued",
			configuredWorker, 1, backlogSummary{}, []taskView{{task: state.Task{ID: "running-task"}}}, nil,
			nextAction{Kind: nextActionMonitor, Command: "hand watch --until-event", Reason: "Run `hand watch --until-event` as a background task; re-arm it after an event or when intentionally resuming after interruption or takeover"},
		},
		{
			"idle only when nothing else applies",
			configuredWorker, 1, backlogSummary{}, nil, nil,
			nextAction{Kind: nextActionIdle, Reason: "The fleet is ready and idle"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyNextAction(test.cfg, test.projects, test.backlog, test.views, test.holds)
			if got != test.want {
				t.Fatalf("classifyNextAction() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestClassifyNextActionIsOrderIndependent(t *testing.T) {
	views := []taskView{
		{task: state.Task{ID: "c-clean"}},
		{task: state.Task{ID: "a-repair", RepairCode: "E_ORPHAN"}},
		{task: state.Task{ID: "b-unacked"}, unacked: true},
	}
	reversed := make([]taskView, len(views))
	for i, v := range views {
		reversed[len(views)-1-i] = v
	}

	forward := classifyNextAction(configuredWorker, 1, backlogSummary{Queued: 1}, views, nil)
	backward := classifyNextAction(configuredWorker, 1, backlogSummary{Queued: 1}, reversed, nil)
	if forward != backward {
		t.Fatalf("forward = %#v, backward = %#v, want order-independent classification", forward, backward)
	}
	if forward.Task != "a-repair" {
		t.Fatalf("task = %q, want the needs-repair candidate regardless of input order", forward.Task)
	}
}

func TestClassifyNextActionClearingHoldRestoresUnderlyingAttention(t *testing.T) {
	view := taskView{task: state.Task{ID: "task-1"}, held: true, agentState: "idle"}
	deferred := classifyNextAction(configuredWorker, 1, backlogSummary{}, []taskView{view}, []state.Hold{{ID: "task-1", Kind: state.HoldKindOperator}})
	if deferred.Kind != nextActionMonitor || deferred.Task != "" {
		t.Fatalf("held action = %#v, want monitoring while deferred", deferred)
	}

	view.held = false
	released := classifyNextAction(configuredWorker, 1, backlogSummary{}, []taskView{view}, nil)
	if released.Kind != nextActionUnreported || released.Task != "task-1" {
		t.Fatalf("cleared action = %#v, want the underlying task attention", released)
	}
}

func TestClassifyNextActionRanksRuntimeAttentionFromSharedDerivation(t *testing.T) {
	for _, test := range []struct {
		name   string
		view   taskView
		kind   string
		reason string
	}{
		{name: "unreachable", view: taskView{task: state.Task{ID: "unreachable-task"}, unreachable: true}, kind: nextActionUnreachable, reason: "investigate why its worker runtime is unreachable"},
		{name: "runtime unknown", view: taskView{task: state.Task{ID: "unknown-task"}, attempt: &state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "pane-1"}}, agentState: string(herdr.StatusUnknown)}, kind: nextActionRuntimeUnknown, reason: "investigate why its worker runtime is unknown"},
		{name: "parked", view: taskView{task: state.Task{ID: "parked-task"}, parked: true, parkedActionable: true}, kind: nextActionParked, reason: "investigate why its worker has been silent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := classifyNextAction(configuredWorker, 1, backlogSummary{}, []taskView{test.view}, nil)
			want := nextAction{Kind: test.kind, Task: test.view.task.ID, Command: statusCommand(test.view.task.ID), Reason: statusReason(test.view.task.ID, test.reason)}
			if got != want {
				t.Fatalf("classifyNextAction() = %#v, want %#v", got, want)
			}
		})
	}
}

// atqamz/hand#492: the ranking's own guard against regressing back to acting on parked without
// parkedActionable; the decision itself is covered in cmd/status_test.go.
func TestClassifyNextActionIgnoresAnUncorroboratedPark(t *testing.T) {
	view := taskView{task: state.Task{ID: "parked-task"}, parked: true, parkedActionable: false}
	got := classifyNextAction(configuredWorker, 1, backlogSummary{}, []taskView{view}, nil)
	if got.Kind == nextActionParked {
		t.Fatalf("classifyNextAction() = %#v, want an uncorroborated park to never rank as the next action", got)
	}
}

func TestClassifyNextActionTieBreakIsLowestTaskID(t *testing.T) {
	orderings := [][]string{
		{"c-third", "a-first", "b-second"},
		{"a-first", "b-second", "c-third"},
		{"b-second", "c-third", "a-first"},
	}
	for _, order := range orderings {
		views := make([]taskView, len(order))
		for i, id := range order {
			views[i] = taskView{task: state.Task{ID: id, RepairCode: "E_ORPHAN"}}
		}
		got := classifyNextAction(configuredWorker, 1, backlogSummary{}, views, nil)
		if got.Task != "a-first" {
			t.Fatalf("order %v: task = %q, want lowest task.ID a-first", order, got.Task)
		}
	}
}

func TestClassifyNextActionFieldNamesArePinned(t *testing.T) {
	fields := reflect.VisibleFields(reflect.TypeOf(nextAction{}))
	var names []string
	for _, f := range fields {
		names = append(names, f.Name)
	}
	want := []string{"Kind", "Task", "Command", "Reason"}
	if len(names) != len(want) {
		t.Fatalf("nextAction fields = %v, want exactly %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("nextAction fields = %v, want exactly %v", names, want)
		}
	}
}

func observeNextAction(t *testing.T, home string) nextAction {
	t.Helper()
	views, holds, _, err := fleetViews(context.Background(), io.Discard, home, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	return classifyNextAction(configuredWorker, 1, backlogSummary{Queued: 1}, views, holds)
}

func TestClassifyNextActionSurvivesRestart(t *testing.T) {
	home := t.TempDir()
	if err := initLayout(home); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddProject(store.Project{Name: "demo", URL: "local", Mode: "local-only"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(store.Task{ID: "durable-repair", Project: "demo", Kind: store.KindShip, RepairCode: "E_ORPHAN"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	before := observeNextAction(t, home)
	after := observeNextAction(t, home)
	if before != after {
		t.Fatalf("before restart = %#v, after reopening the same home = %#v, want identical classification", before, after)
	}
	if before.Kind != nextActionNeedsRepair || before.Task != "durable-repair" {
		t.Fatalf("classification = %#v, want the durable repair task to still lead", before)
	}
}
