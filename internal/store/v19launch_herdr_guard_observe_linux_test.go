//go:build linux

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/osfacts"
)

// The test binary doubles as `hand exec-guard`, run against the Launch's own handoff.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "exec-guard" {
		code, err := execguard.Run(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(125)
		}
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func (l *execGuardLaunchTest) installHerdr(t *testing.T, fake faketool.Herdr) (canonicalV19HerdrLaunchDeps, string) {
	t.Helper()
	fake.Log = filepath.Join(t.TempDir(), "herdr.log")
	fake.Workspaces = []faketool.HerdrWorkspace{{
		ID: l.key.WorkspaceID, Label: canonicalV19HerdrSessionWorkspaceLabel(l.session.BindingID),
		Tabs: []faketool.HerdrTab{{ID: l.key.TabID, Label: "1", Pane: l.key.PaneID}},
	}}
	fake.Install(t, faketool.Bin(t))
	deps := canonicalV19HerdrLaunchDefaultDeps()
	deps.settle = 200 * time.Millisecond
	t.Cleanup(func() { l.stopGuard(t) })
	return deps, fake.Log
}

func (l *execGuardLaunchTest) submit(t *testing.T) canonicalV19HerdrLaunchDeps {
	t.Helper()
	deps, _ := l.installHerdr(t, faketool.Herdr{})
	if _, err := prepareCanonicalV19ExecGuardLaunch(context.Background(), l.home, l.input); err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Launch(context.Background(), l.home, l.input.OperationID, "2026-09-08T03:06:00Z", "submitted-exec-guard"); err != nil {
		t.Fatal(err)
	}
	return deps
}

func (l *execGuardLaunchTest) guard(t *testing.T) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exec.Command(self, "exec-guard", execguard.Locator(l.dir))
}

func (l *execGuardLaunchTest) stopGuard(t *testing.T) {
	running, err := execguard.ReadRecord(l.dir, execguard.KindRunning)
	if err != nil || osfacts.Observe(running.Guard) != osfacts.Alive {
		return
	}
	if err := execguard.RequestInterrupt(l.dir, running.Guard, "operation-test-cleanup"); err != nil {
		t.Error(err)
	}
	for deadline := time.Now().Add(10 * time.Second); osfacts.Observe(running.Guard) == osfacts.Alive; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Error("the exec guard outlived its cleanup interrupt")
			return
		}
	}
}

func (l *execGuardLaunchTest) binding(t *testing.T) (string, string, string) {
	t.Helper()
	db, err := openReadOnly(l.home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state, key, terminal string
	if err := db.sql.QueryRow(`SELECT o.state,COALESCE(e.provider_executor_key,''),COALESCE(x.terminal_kind,'')
		FROM external_operation o
		LEFT JOIN executor_binding e ON e.launch_operation_id=o.id AND e.adapter_ref='herdr'
		LEFT JOIN executor_binding_termination x ON x.executor_binding_id=e.id
		WHERE o.id=?`, l.input.OperationID).Scan(&state, &key, &terminal); err != nil {
		t.Fatal(err)
	}
	return state, key, terminal
}

func TestExecGuardReconcileSucceedsFromALiveGuardWithoutPaneAssociation(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exec sleep 30")
	deps := l.submit(t)
	guard := l.guard(t)
	if err := guard.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.stopGuard(t); _ = guard.Wait() })
	waitExecGuard(t, "the running record", func() bool { _, err := execguard.ReadRecord(l.dir, execguard.KindRunning); return err == nil })
	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps)
	persisted, key, terminal := l.binding(t)
	parsed, keyErr := parseCanonicalV19ExecGuardKey(key)
	running := mustExecGuardRecord(t, l.dir, execguard.KindRunning)
	if state != "succeeded" || err != nil || persisted != "succeeded" || terminal != "" || keyErr != nil ||
		*parsed.Guard != running.Guard || *parsed.Root != *running.Root || parsed.ObjectClass != execguard.ClassExact || parsed.Assoc != "unobserved" {
		t.Fatalf("reconcile = %q, %v: binding %q key %q (%v) termination %q, want an open key naming G and R with assoc unobserved (EG-3, EG-5, counterexample 17)",
			state, err, persisted, key, keyErr, terminal)
	}
	assertExecGuardHandoffGone(t, l.dir)
}

func TestExecGuardReconcileRejectsOnlyFromARefusedRecord(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/nonexistent/harness")
	deps := l.submit(t)
	_ = l.guard(t).Run()
	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps)
	if persisted, key, _ := l.binding(t); state != "rejected" || err == nil || persisted != "rejected" || key != "" {
		t.Fatalf("reconcile = %q (%q, key %q), %v, want rejected from the guard's refusal", state, persisted, key, err)
	}
	if refused := mustExecGuardRecord(t, l.dir, execguard.KindRefused); refused.Reason == "" {
		t.Fatal("rejected without a refused record")
	}
	assertExecGuardHandoffGone(t, l.dir)
}

func TestExecGuardReconcileFencesAnUnclaimedHandoffToUncertainNeverNoEffect(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	deps := l.submit(t)
	for range 2 {
		if state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps); state != "uncertain" || err == nil {
			t.Fatalf("reconcile = %q, %v, want uncertain: a won fence without O1 is never no-effect (EG-6, counterexample 10)", state, err)
		}
	}
	if _, err := os.Stat(filepath.Join(l.dir, "fenced")); err != nil {
		t.Fatalf("fence tombstone: %v, want it kept until L settles", err)
	}
	_ = l.guard(t).Run()
	if _, err := os.Stat(filepath.Join(l.dir, execguard.KindClaimed)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("late guard claim: %v, want the fence to exclude it (EG-2)", err)
	}
}

func TestExecGuardReconcileFencesAPreparedLaunchToNoEffect(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	deps, _ := l.installHerdr(t, faketool.Herdr{})
	if _, err := prepareCanonicalV19ExecGuardLaunch(context.Background(), l.home, l.input); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps); state != "no-effect" || err != nil {
			t.Fatalf("reconcile = %q, %v, want a stable no-effect: fence, settle, then delete", state, err)
		}
	}
	assertExecGuardHandoffGone(t, l.dir)
}

func TestExecGuardReconcileLeavesALiveStartingGuardUntouched(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	deps := l.submit(t)
	self, err := osfacts.ReadIncarnation(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	l.claimAs(t, self)
	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps)
	_, fenceErr := os.Stat(filepath.Join(l.dir, "fenced"))
	if persisted, _, _ := l.binding(t); state != "submitted" || err == nil || persisted != "submitted" || !errors.Is(fenceErr, fs.ErrNotExist) {
		t.Fatalf("reconcile = %q (%q), %v, tombstone %v: want no transition and no fence while a live guard starts", state, persisted, err, fenceErr)
	}
}

func TestExecGuardReconcileOfAGuardGoneAfterItsClaimIsUncertain(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	deps := l.submit(t)
	self, err := osfacts.ReadIncarnation(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	self.StartTicks++
	l.claimAs(t, self)
	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps)
	if persisted, key, _ := l.binding(t); state != "uncertain" || err == nil || persisted != "uncertain" || key != "" {
		t.Fatalf("reconcile = %q (%q, key %q), %v, want uncertain, never rejected or no-effect, after a crash past the claim (counterexample 4)", state, persisted, key, err)
	}
}

func TestExecGuardReconcileOfACrashBeforeTheHandoffIsNoEffect(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	deps, _ := l.installHerdr(t, faketool.Herdr{})
	if _, err := prepareCanonicalV19ExecGuardLaunch(context.Background(), l.home, l.input); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(execguard.Locator(l.dir)); err != nil {
		t.Fatal(err)
	}
	if state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps); state != "no-effect" || err != nil {
		t.Fatalf("reconcile = %q, %v, want no-effect for a Launch that crashed after Tx A and before its handoff", state, err)
	}
}

func (l *execGuardLaunchTest) claimAs(t *testing.T, guard osfacts.Incarnation) {
	t.Helper()
	data, err := json.Marshal(execguard.Record{Protocol: execguard.Protocol, Kind: execguard.KindClaimed, LaunchOperationID: l.input.OperationID, Guard: guard})
	if err == nil {
		err = os.Rename(execguard.Locator(l.dir), filepath.Join(l.dir, "claim.test"))
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(l.dir, execguard.KindClaimed), data, 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecGuardReconcileOfAFastExitSucceedsWithItsTerminationInOneTransaction(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 3")
	deps := l.submit(t)
	_ = l.guard(t).Run()
	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps)
	if persisted, key, terminal := l.binding(t); state != "succeeded" || err != nil || persisted != "succeeded" || key == "" || terminal != "failed" {
		t.Fatalf("reconcile = %q (%q, key %q, termination %q), %v, want succeeded with a failed termination, never no-effect (counterexample 13)", state, persisted, key, terminal, err)
	}
	assertExecGuardHandoffGone(t, l.dir)
}

func TestExecGuardReconcileOfAKilledGuardStaysUncertainWithoutTermination(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exec sleep 30")
	deps := l.submit(t)
	guard := l.guard(t)
	if err := guard.Start(); err != nil {
		t.Fatal(err)
	}
	waitExecGuard(t, "the running record", func() bool { _, err := execguard.ReadRecord(l.dir, execguard.KindRunning); return err == nil })
	root := *mustExecGuardRecord(t, l.dir, execguard.KindRunning).Root
	t.Cleanup(func() {
		if osfacts.Observe(root) == osfacts.Alive {
			_ = syscall.Kill(root.PID, syscall.SIGKILL)
		}
	})
	_ = guard.Process.Kill()
	_ = guard.Wait()
	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps)
	if persisted, key, terminal := l.binding(t); state != "uncertain" || err == nil || key != "" || terminal != "" || persisted != "uncertain" {
		t.Fatalf("reconcile = %q (%q, key %q, termination %q), %v, want uncertain with no binding while R may live on (EG-8, counterexample 3)", state, persisted, key, terminal, err)
	}
}

func TestExecGuardRecordsThatConflictWithTheCommitmentOrTheOSNeverSucceed(t *testing.T) {
	for _, test := range []struct{ name, kind, field string }{
		{"another request digest", execguard.KindRunning, "request_digest"},
		{"another launch-spec digest", execguard.KindPinned, "launch_spec_digest"},
		{"another V_B", execguard.KindRunning, "credential_verifier"},
		{"same guard PID with another start time", execguard.KindCeased, "guard"},
	} {
		t.Run(test.name, func(t *testing.T) {
			l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
			deps := l.submit(t)
			_ = l.guard(t).Run()
			path := filepath.Join(l.dir, test.kind)
			var record map[string]any
			data, err := os.ReadFile(path)
			if err == nil {
				err = json.Unmarshal(data, &record)
			}
			if err != nil {
				t.Fatal(err)
			}
			if guard, ok := record[test.field].(map[string]any); ok {
				guard["start_ticks"] = guard["start_ticks"].(float64) + 1
			} else {
				record[test.field] = strings.Repeat("0", 64)
			}
			if data, err = json.Marshal(record); err == nil {
				err = os.WriteFile(path, data, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps)
			if persisted, key, _ := l.binding(t); state != "uncertain" || err == nil || key != "" || persisted != "uncertain" {
				t.Fatalf("reconcile = %q (%q, key %q), %v, want uncertain with no binding (EG-3, EG-5)", state, persisted, key, err)
			}
		})
	}
}

func TestExecGuardUnknownRecordVersionIsRefusedNotAbsent(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	deps := l.submit(t)
	if err := os.WriteFile(filepath.Join(l.dir, execguard.KindClaimed), []byte(`{"protocol":"hand-exec-guard:v9","kind":"claimed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps)
	if _, statErr := os.Stat(execguard.Locator(l.dir)); state != "submitted" || !errors.Is(err, execguard.ErrUnknownProtocol) || statErr != nil {
		t.Fatalf("reconcile = %q, %v (handoff %v), want the typed refusal before any fence or transition (EG-17)", state, err, statErr)
	}
}

func TestExecGuardPaneAssociationNeedsTerminalAndForegroundAgreement(t *testing.T) {
	for _, test := range []struct {
		name                       string
		paneTTY, tty               uint64
		paneForeground, foreground int
		want                       string
	}{
		{"all agree", 34816, 34816, 4101, 4101, "observed"},
		{"another terminal", 34817, 34816, 4101, 4101, "mismatch"},
		{"Herdr reports another foreground", 34816, 34816, 4100, 4101, "mismatch"},
		{"the OS reports another foreground", 34816, 34816, 4101, 4100, "mismatch"},
	} {
		if got := canonicalV19ExecGuardAssociationOf(test.paneTTY, test.paneForeground, test.tty, test.foreground, 4101); got != test.want {
			t.Errorf("%s: assoc = %s, want %s", test.name, got, test.want)
		}
	}
}

func mustExecGuardRecord(t *testing.T, dir, kind string) execguard.Record {
	t.Helper()
	record, err := execguard.ReadRecord(dir, kind)
	if err != nil {
		t.Fatalf("read %s record: %v", kind, err)
	}
	return record
}

func waitExecGuard(t *testing.T, what string, ready func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ready(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func assertExecGuardHandoffGone(t *testing.T, dir string) {
	t.Helper()
	for _, pattern := range []string{"handoff", "fenced", "claim.*"} {
		if matches, _ := filepath.Glob(filepath.Join(dir, pattern)); len(matches) != 0 {
			t.Errorf("%s left after L settled: %v", pattern, matches)
		}
	}
}
