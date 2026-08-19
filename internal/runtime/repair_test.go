package runtime

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

// One Herdr fake whose observed agent and workspace presence are settable, so a case can drive exact,
// absent, mismatched and agent-mismatched ownership from the same client.
type repairHerdr struct {
	healthyReconcileHerdr
	agent  string
	absent bool
}

func (f *repairHerdr) FindWorkspaceByLabel(label string) (herdr.Workspace, bool, error) {
	if f.absent {
		return herdr.Workspace{}, false, nil
	}
	return f.healthyReconcileHerdr.FindWorkspaceByLabel(label)
}

func (f *repairHerdr) PaneGet(string) (herdr.Pane, error) {
	agent := f.agent
	if agent == "" {
		agent = "claude"
	}
	return herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1", Agent: agent}, nil
}

func repairFixture(t *testing.T, task state.Task, attempt state.Attempt) (string, state.Attempt) {
	t.Helper()
	home := reconcileFixture(t)
	task.ID, task.Project, task.Brief = "task-1", "demo", "data/task-1/brief.md"
	if task.Kind == "" {
		task.Kind = state.KindShip
	}
	if task.Lifecycle == "" {
		task.Lifecycle = state.TaskOpen
	}
	attempt.TaskID = "task-1"
	if attempt.Lifecycle == "" {
		attempt.Lifecycle = state.AttemptProvisioning
	}
	if attempt.Harness == "" {
		attempt.Harness = "claude"
	}
	created, err := state.CreateTaskWithAttempt(home, task, attempt)
	if err != nil {
		t.Fatal(err)
	}
	return home, created
}

// The launch evidence a running Attempt has to carry before state accepts the lifecycle.
func repairRunningAttempt(worktreePath, leaseID string, ownership state.Herdr) state.Attempt {
	return state.Attempt{
		Lifecycle: state.AttemptProvisioning, Worktree: worktreePath, LeaseID: leaseID, Herdr: ownership,
		LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
	}
}

func repairMarkRunning(t *testing.T, home string, attempt state.Attempt) {
	t.Helper()
	if err := state.MarkAttemptRunning(home, attempt.TaskID, attempt.ID); err != nil {
		t.Fatal(err)
	}
}

func repairTeardownDecision(t *testing.T, home string, attempt state.Attempt, terminal state.AttemptLifecycle, disposition string) {
	t.Helper()
	if err := state.SetAttemptTeardownDecision(home, attempt.TaskID, attempt.ID, terminal, disposition); err != nil {
		t.Fatal(err)
	}
}

func repairReconcile(t *testing.T, home string, r *Runtime, req ReconcileRequest) {
	t.Helper()
	req.Home, req.ID = home, "task-1"
	if _, err := r.Reconcile(req); err != nil {
		t.Fatalf("Reconcile(%+v) = %v", req, err)
	}
}

// Teardown is the treatment for most diagnoses, and a teardown blocked at the worktree step is still
// the supported way to record the decision reconcile needs, so a case says which it expects.
func repairTeardown(t *testing.T, home string, r *Runtime, force bool, wantErr string) {
	t.Helper()
	_, err := r.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1", Force: force})
	if wantErr == "" {
		if err != nil {
			t.Fatalf("Teardown() = %v, want the attempt ended", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("Teardown() = %v, want a refusal containing %q", err, wantErr)
	}
}

func repairRuntime(client herdrClient) *Runtime {
	r := reconcileRuntime(client, nil)
	r.deps.prMerged = func(context.Context, string) (bool, error) {
		return false, errors.New("no GitHub access in this test")
	}
	r.deps.prHead = func(context.Context, string) (string, error) {
		return "", errors.New("no GitHub access in this test")
	}
	return r
}

func repairLeaseObservation(r *Runtime, lease worktree.LeaseObservation) {
	r.deps.worktree.observeLease = func(string, string) worktree.LeaseObservation { return lease }
}

func repairCommitSafety(r *Runtime, state worktree.CommitSafetyState, probe worktree.CommitSafetyProbe) {
	r.deps.worktree.observeCommits = func(path string) worktree.CommitSafetyObservation {
		probe.Command, probe.WorkingDir = "git rev-list --count HEAD --not --remotes", path
		return worktree.CommitSafetyObservation{State: state, Probe: probe}
	}
}

// One repair code, driven into durable state and then treated with the supported commands its
// treatment names. The treatment is what the diagnosis promises an operator, so the way out has to be
// exercised, not just described.
type repairPathCase struct {
	code  string
	drive func(t *testing.T) (string, *Runtime)
	treat func(t *testing.T, home string, r *Runtime)
}

func repairPathCases() []repairPathCase {
	return []repairPathCase{
		{
			code: repairCodeProvisioningLaunchAmbiguous,
			drive: func(t *testing.T) (string, *Runtime) {
				home, _ := repairFixture(t, state.Task{}, state.Attempt{Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}})
				return home, repairRuntime(&repairHerdr{})
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				repairTeardown(t, home, r, false, "")
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
		{
			code: repairCodeProvisioningPaneMissing,
			drive: func(t *testing.T) (string, *Runtime) {
				home, _ := repairFixture(t, state.Task{}, state.Attempt{
					Worktree: "/pool/1", LeaseID: "lease-1", LaunchSubmittedAt: "2026-08-15T00:00:00Z",
				})
				return home, repairRuntime(&repairHerdr{})
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				repairTeardown(t, home, r, true, "")
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
		{
			code: repairCodeLaunchSubmittedPaneMissing,
			drive: func(t *testing.T) (string, *Runtime) {
				home, _ := repairFixture(t, state.Task{}, state.Attempt{
					Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, LaunchSubmittedAt: "2026-08-15T00:00:00Z",
				})
				return home, repairRuntime(&repairHerdr{absent: true})
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				repairTeardown(t, home, r, true, "")
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
		{
			code: repairCodeLaunchAgentMismatch,
			drive: func(t *testing.T) (string, *Runtime) {
				home, _ := repairFixture(t, state.Task{}, state.Attempt{
					Harness: "codex", Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
					LaunchSubmittedAt: "2026-08-15T00:00:00Z",
				})
				return home, repairRuntime(&repairHerdr{agent: "claude"})
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				r.deps.herdr = func() herdrClient { return &repairHerdr{agent: "codex"} }
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
		{
			code: repairCodeRunningPaneMissing,
			drive: func(t *testing.T) (string, *Runtime) {
				home, attempt := repairFixture(t, state.Task{PR: "https://github.com/demo/demo/pull/1"},
					repairRunningAttempt("", "", state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}))
				repairMarkRunning(t, home, attempt)
				return home, repairRuntime(&repairHerdr{absent: true})
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				r.deps.prMerged = func(context.Context, string) (bool, error) { return true, nil }
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
		{
			code: repairCodeRunningPaneIdentityMismatch,
			drive: func(t *testing.T) (string, *Runtime) {
				home, attempt := repairFixture(t, state.Task{},
					repairRunningAttempt("", "", state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-9"}))
				repairMarkRunning(t, home, attempt)
				return home, repairRuntime(&repairHerdr{})
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				repairTeardown(t, home, r, true, "")
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
		{
			code: repairCodeHerdrOwnershipIncomplete,
			drive: func(t *testing.T) (string, *Runtime) {
				home, attempt := repairFixture(t, state.Task{}, repairRunningAttempt("", "", state.Herdr{WorkspaceID: "ws-1"}))
				repairMarkRunning(t, home, attempt)
				return home, repairRuntime(&repairHerdr{})
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				repairTeardown(t, home, r, true, "")
				repairReconcile(t, home, r, ReconcileRequest{AbandonPane: true})
			},
		},
		{
			code: repairCodeHerdrOwnershipMismatch,
			drive: func(t *testing.T) (string, *Runtime) {
				home, _ := repairFixture(t, state.Task{}, state.Attempt{
					Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-9"}, LaunchSubmittedAt: "2026-08-15T00:00:00Z",
				})
				return home, repairRuntime(&repairHerdr{})
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				repairTeardown(t, home, r, true, "")
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
		{
			code: repairCodeWorktreeDirty,
			drive: func(t *testing.T) (string, *Runtime) {
				home, attempt := repairFixture(t, state.Task{}, repairRunningAttempt("/pool/1", "lease-1", state.Herdr{}))
				repairMarkRunning(t, home, attempt)
				repairTeardownDecision(t, home, attempt, state.AttemptCompleted, state.TeardownDispositionCompleted)
				r := repairRuntime(&repairHerdr{})
				r.deps.worktree.observeClean = func(string) (worktree.Cleanliness, error) { return worktree.Dirty, nil }
				return home, r
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				r.deps.worktree.observeClean = func(string) (worktree.Cleanliness, error) { return worktree.Clean, nil }
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
		{
			code: repairCodeWorktreeOwnershipMismatch,
			drive: func(t *testing.T) (string, *Runtime) {
				home, attempt := repairFixture(t, state.Task{}, repairRunningAttempt("/pool/1", "lease-1", state.Herdr{}))
				repairMarkRunning(t, home, attempt)
				r := repairRuntime(&repairHerdr{})
				repairLeaseObservation(r, worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "lease-9"})
				return home, r
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				repairTeardown(t, home, r, true, "prove worktree ownership")
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
		{
			code: repairCodeLegacyWorktreeUnprovable,
			drive: func(t *testing.T) (string, *Runtime) {
				home, attempt := repairFixture(t, state.Task{}, repairRunningAttempt("/pool/1", "lease-1", state.Herdr{}))
				repairMarkRunning(t, home, attempt)
				repairTeardownDecision(t, home, attempt, state.AttemptCompleted, state.TeardownDispositionCompleted)
				r := repairRuntime(&repairHerdr{})
				repairLeaseObservation(r, worktree.LeaseObservation{State: worktree.LeaseUnprovable})
				return home, r
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				repairReconcile(t, home, r, ReconcileRequest{AbandonWorktree: true})
			},
		},
		{
			code: repairCodeWorktreeUnobservable,
			drive: func(t *testing.T) (string, *Runtime) {
				home, attempt := repairFixture(t, state.Task{}, repairRunningAttempt("/pool/1", "lease-1", state.Herdr{}))
				repairMarkRunning(t, home, attempt)
				repairTeardownDecision(t, home, attempt, state.AttemptCompleted, state.TeardownDispositionCompleted)
				r := repairRuntime(&repairHerdr{})
				repairLeaseObservation(r, worktree.LeaseObservation{State: worktree.LeaseUnknown, Probe: worktree.LeaseProbe{
					Command: "treehouse status --json", WorkingDir: "/pool/1", Reason: "treehouse reported no pool entries",
				}})
				return home, r
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				repairReconcile(t, home, r, ReconcileRequest{AbandonWorktree: true})
			},
		},
		{
			code: repairCodeWorktreeLocalCommits,
			drive: func(t *testing.T) (string, *Runtime) {
				home, attempt := repairFixture(t, state.Task{}, repairRunningAttempt("/pool/1", "lease-1", state.Herdr{}))
				repairMarkRunning(t, home, attempt)
				repairTeardownDecision(t, home, attempt, state.AttemptCompleted, state.TeardownDispositionCompleted)
				r := repairRuntime(&repairHerdr{})
				repairCommitSafety(r, worktree.CommitSafetyLocalOnly, worktree.CommitSafetyProbe{LocalOnly: 2, RemoteRefs: 1})
				return home, r
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				repairCommitSafety(r, worktree.CommitSafetyRemoteObserved, worktree.CommitSafetyProbe{RemoteRefs: 1})
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
		{
			code: repairCodeWorktreeCommitSafetyUnknown,
			drive: func(t *testing.T) (string, *Runtime) {
				home, attempt := repairFixture(t, state.Task{}, repairRunningAttempt("/pool/1", "lease-1", state.Herdr{}))
				repairMarkRunning(t, home, attempt)
				repairTeardownDecision(t, home, attempt, state.AttemptCompleted, state.TeardownDispositionCompleted)
				r := repairRuntime(&repairHerdr{})
				repairCommitSafety(r, worktree.CommitSafetyUnknown, worktree.CommitSafetyProbe{Reason: "resolve worktree HEAD failed"})
				return home, r
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				repairCommitSafety(r, worktree.CommitSafetyRemoteObserved, worktree.CommitSafetyProbe{RemoteRefs: 1})
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
		{
			code: repairCodeTeardownResourceAmbiguous,
			drive: func(t *testing.T) (string, *Runtime) {
				home, attempt := repairFixture(t, state.Task{},
					repairRunningAttempt("", "", state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-9"}))
				repairMarkRunning(t, home, attempt)
				repairTeardownDecision(t, home, attempt, state.AttemptCompleted, state.TeardownDispositionCompleted)
				return home, repairRuntime(&repairHerdr{})
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				repairReconcile(t, home, r, ReconcileRequest{AbandonPane: true})
			},
		},
		{
			code: repairCodeCompletionEvidenceMismatch,
			drive: func(t *testing.T) (string, *Runtime) {
				home, attempt := repairFixture(t, state.Task{}, state.Attempt{LaunchSubmittedAt: "2026-08-15T00:00:00Z"})
				repairTeardownDecision(t, home, attempt, state.AttemptInterrupted, state.TeardownDispositionForced)
				for _, next := range []string{state.TeardownCompletionPending, state.TeardownCompletionAppended} {
					if err := state.SetAttemptTeardownCompletionState(home, "task-1", attempt.ID, state.AttemptProvisioning, next); err != nil {
						t.Fatal(err)
					}
				}
				if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
					t.Fatal(err)
				}
				return home, repairRuntime(&repairHerdr{})
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				history, err := state.ReadHistoryReadOnly(home, "task-1")
				if err != nil {
					t.Fatal(err)
				}
				record := completion.Record{
					ID: "task-1", Project: "demo", Kind: state.KindShip, Outcome: "interrupted",
					TornDownAt: "2026-08-15T00:00:02Z", AttemptID: history.Attempts[0].ID, AttemptLifecycle: string(state.AttemptInterrupted),
				}
				if err := completion.Append(home, record); err != nil {
					t.Fatal(err)
				}
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
		{
			code: repairCodeMergeFactMismatch,
			drive: func(t *testing.T) (string, *Runtime) {
				home, attempt := repairFixture(t, state.Task{PR: "https://github.com/demo/demo/pull/1"},
					repairRunningAttempt("", "", state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}))
				repairMarkRunning(t, home, attempt)
				if err := state.SetTaskMergeAnnounced(home, "task-1"); err != nil {
					t.Fatal(err)
				}
				r := repairRuntime(&repairHerdr{})
				r.deps.prMerged = func(context.Context, string) (bool, error) { return false, nil }
				return home, r
			},
			treat: func(t *testing.T, home string, r *Runtime) {
				r.deps.prMerged = func(context.Context, string) (bool, error) { return true, nil }
				repairReconcile(t, home, r, ReconcileRequest{})
			},
		},
	}
}

// Every diagnosis Hand can answer with is driven into durable state and then out of it again through
// the commands its treatment names, so no repair code can be reachable without a way out
// (atqamz/hand#254).
func TestEveryRepairCodeIsDrivenInAndTreatedOut(t *testing.T) {
	for _, test := range repairPathCases() {
		t.Run(test.code, func(t *testing.T) {
			home, r := test.drive(t)
			repairReconcile(t, home, r, ReconcileRequest{})
			history, err := state.ReadHistory(home, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if history.Task.RepairCode != test.code {
				t.Fatalf("repair code = %q (%q), want %q", history.Task.RepairCode, history.Task.RepairReason, test.code)
			}
			treatment := repairTreatments[test.code].Treatment
			if want := strings.ReplaceAll(treatment, "<id>", "task-1"); !strings.Contains(history.Task.RepairReason, want) {
				t.Fatalf("repair reason = %q, want it to name its treatment %q", history.Task.RepairReason, want)
			}
			test.treat(t, home, r)
			history, err = state.ReadHistory(home, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if history.Task.RepairCode != "" {
				t.Fatalf("repair code after treatment = %q (%q), want the diagnosis cleared", history.Task.RepairCode, history.Task.RepairReason)
			}
		})
	}
}

// The enumeration itself: a repair code declared in the package but absent from the treatment registry
// or from the matrix above is a diagnosis with no reachable way out, and fails here rather than in a
// fleet.
func TestRepairCodeEnumerationIsComplete(t *testing.T) {
	declared := declaredRepairCodes(t)
	if len(declared) == 0 {
		t.Fatal("no repair codes found in the package source")
	}
	covered := map[string]bool{}
	for _, test := range repairPathCases() {
		if covered[test.code] {
			t.Fatalf("repair code %q has more than one matrix case", test.code)
		}
		covered[test.code] = true
	}
	for name, code := range declared {
		treatment, found := repairTreatments[code]
		if !found {
			t.Fatalf("%s (%q) has no entry in repairTreatments, so an operator who reaches it has nothing to run", name, code)
		}
		switch treatment.Class {
		case repairClassSupportedCommand, repairClassAttestation, repairClassExternalFix:
		default:
			t.Fatalf("%s (%q) has class %q, which is none of the three supported treatment classes", name, code, treatment.Class)
		}
		if !strings.Contains(treatment.Treatment, "hand ") {
			t.Fatalf("%s (%q) has treatment %q, which names no hand command", name, code, treatment.Treatment)
		}
		if !covered[code] {
			t.Fatalf("%s (%q) has no case in repairPathCases, so its treatment is described but never exercised", name, code)
		}
		delete(covered, code)
	}
	for code := range covered {
		t.Fatalf("repairPathCases covers %q, which no repair code constant declares", code)
	}
	for code := range repairTreatments {
		if !containsDeclaredCode(declared, code) {
			t.Fatalf("repairTreatments covers %q, which no repair code constant declares", code)
		}
	}
}

func declaredRepairCodes(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			group, ok := decl.(*ast.GenDecl)
			if !ok || group.Tok != token.CONST {
				continue
			}
			for _, spec := range group.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, ident := range value.Names {
					if !strings.HasPrefix(ident.Name, "repairCode") || i >= len(value.Values) {
						continue
					}
					literal, ok := value.Values[i].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						t.Fatalf("%s in %s is not a string literal, so the enumeration cannot be read", ident.Name, name)
					}
					codes[ident.Name] = strings.Trim(literal.Value, `"`)
				}
			}
		}
	}
	return codes
}

func containsDeclaredCode(declared map[string]string, code string) bool {
	for _, candidate := range declared {
		if candidate == code {
			return true
		}
	}
	return false
}

func TestRepairReasonNamesItsTreatment(t *testing.T) {
	reason := repairReasonWithTreatment("task-7", repairCodeWorktreeUnobservable, "recorded Treehouse lease was not observed")
	for _, want := range []string{"recorded Treehouse lease was not observed", "hand reconcile task-7 --abandon-worktree", "returns, prunes or deletes nothing"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason = %q, want it to contain %q", reason, want)
		}
	}
	if strings.Contains(reason, "<id>") {
		t.Fatalf("reason = %q, want the task ID substituted", reason)
	}
	undiagnosed := repairReasonWithTreatment("task-7", "invented-code", "something Hand cannot treat")
	if !strings.Contains(undiagnosed, "defect in Hand rather than a state you can repair") {
		t.Fatalf("reason for an unregistered code = %q, want it to name the defect", undiagnosed)
	}
}

// Durable state is written through the store, never around it: a test that hand-edited the fleet
// database or a Treehouse pool file would prove nothing about the commands operators actually run.
func TestPackageTestsWriteNoDurableStateDirectly(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		// Split so this guard does not match itself.
		for _, forbidden := range []string{"hand" + ".db", "treehouse-state" + ".json"} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("%s mentions %q; runtime tests must drive durable state through internal/state and internal/worktree", filepath.Join(mustWorkingDir(t), entry.Name()), forbidden)
			}
		}
	}
}

func mustWorkingDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
