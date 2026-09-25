//go:build linux

package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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

func mustExecGuardRecord(t *testing.T, dir, kind string) execguard.Record {
	t.Helper()
	record, err := execguard.ReadRecord(dir, kind)
	if err != nil {
		t.Fatalf("read %s record: %v", kind, err)
	}
	return record
}

func assertExecGuardHandoffGone(t *testing.T, dir string) {
	t.Helper()
	for _, pattern := range []string{"handoff", "fenced", "claim.*"} {
		if matches, _ := filepath.Glob(filepath.Join(dir, pattern)); len(matches) != 0 {
			t.Errorf("%s left after L settled: %v", pattern, matches)
		}
	}
}
