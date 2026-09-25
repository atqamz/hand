//go:build linux

package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/osfacts"
)

const execGuardExitOnMarker = `while [ ! -e "$0" ]; do sleep 0.05; done; exit "$1"`

func (l *execGuardLaunchTest) establish(t *testing.T) (*exec.Cmd, execguard.Record) {
	t.Helper()
	deps := l.submit(t)
	guard := l.guard(t)
	if err := guard.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.stopGuard(t); _ = guard.Wait() })
	waitExecGuard(t, "the running record", func() bool { _, err := execguard.ReadRecord(l.dir, execguard.KindRunning); return err == nil })
	if state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps); state != "succeeded" || err != nil {
		t.Fatalf("Launch = %q, %v, want an open guarded binding", state, err)
	}
	return guard, mustExecGuardRecord(t, l.dir, execguard.KindRunning)
}

func (l *execGuardLaunchTest) establishKey(t *testing.T, guard osfacts.Incarnation) {
	t.Helper()
	l.submit(t)
	root := osfacts.Incarnation{BootID: guard.BootID, PID: guard.PID + 1, StartTicks: guard.StartTicks + 1}
	key, err := encodeCanonicalV19ExecGuardKey(canonicalV19ExecGuardKey{
		Session: l.key, Assoc: "unobserved", Guard: &guard, Root: &root, ProcessGroup: root.PID,
		Object: &execguard.Object{Device: 1, Inode: 1, SHA256: strings.Repeat("ab", 32)}, ObjectClass: execguard.ClassExact,
	})
	if err == nil {
		err = EstablishCanonicalV19ExecutorBinding(context.Background(), l.home, CanonicalV19ExecutorBindingEvidence{
			OperationID: l.input.OperationID, ProviderExecutorKey: key, EstablishedAt: "2026-09-08T03:07:00Z", EvidenceDigest: "crafted-exec-guard-key",
		})
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (l *execGuardLaunchTest) interrupt(t *testing.T, operationID string, submit bool) {
	t.Helper()
	_, err := PrepareCanonicalV19Interrupt(context.Background(), l.home, CanonicalV19InterruptPrepareInput{
		OperationID: operationID, OperationKey: "operation-key-" + operationID, ExecutorBindingID: l.input.BindingID,
		ReasonCode: "operator-request", CreatedAt: "2026-09-08T03:08:00Z",
	})
	if err == nil && submit {
		_, err = SubmitCanonicalV19Interrupt(context.Background(), l.home, operationID, "2026-09-08T03:09:00Z", "submitted-exec-guard-interrupt")
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (l *execGuardLaunchTest) cease(t *testing.T) (string, osfacts.Observation, error) {
	t.Helper()
	binding, err := readCanonicalV19ExecGuardBinding(context.Background(), l.home, l.input.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	return ceaseCanonicalV19ExecGuardBinding(context.Background(), l.home, binding, time.Now)
}

func (l *execGuardLaunchTest) termination(t *testing.T) (int, string, string) {
	t.Helper()
	db, err := openReadOnly(l.home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	var kind, interrupt string
	if err := db.sql.QueryRow(`SELECT count(*),COALESCE(max(terminal_kind),''),COALESCE(max(interrupt_operation_id),'')
		FROM executor_binding_termination WHERE executor_binding_id=?`, l.input.BindingID).Scan(&count, &kind, &interrupt); err != nil {
		t.Fatal(err)
	}
	return count, kind, interrupt
}

func (l *execGuardLaunchTest) operationState(t *testing.T, operationID string) string {
	t.Helper()
	db, err := openReadOnly(l.home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state string
	if err := db.sql.QueryRow(`SELECT state FROM external_operation WHERE id=?`, operationID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func waitExecGuardCeased(t *testing.T, dir string) {
	t.Helper()
	waitExecGuard(t, "the ceased record", func() bool { _, err := execguard.ReadRecord(dir, execguard.KindCeased); return err == nil })
}

func TestExecGuardCessationClosesAnOpenBindingOnceWithTheKindOfItsCause(t *testing.T) {
	for _, test := range []struct {
		name, exit string
		signal     syscall.Signal
		want       string
	}{
		{name: "harness exit 0", exit: "0", want: "completed"},
		{name: "harness exit 3", exit: "3", want: "failed"},
		{name: "hangup", exit: "0", signal: syscall.SIGHUP, want: "provider-gone"},
		{name: "external termination", exit: "0", signal: syscall.SIGTERM, want: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "exit")
			l := newExecGuardLaunchTest(t, "/bin/sh", "-c", execGuardExitOnMarker, marker, test.exit)
			guard, _ := l.establish(t)
			l.interrupt(t, "operation-interrupt-pending", true)
			if kind, liveness, err := l.cease(t); kind != "" || liveness != osfacts.Alive || err != nil {
				t.Fatalf("cease of a live guard = %q, %q, %v, want B open", kind, liveness, err)
			}
			var err error
			if test.signal != 0 {
				err = guard.Process.Signal(test.signal)
			} else {
				err = os.WriteFile(marker, nil, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			waitExecGuardCeased(t, l.dir)
			for range 2 {
				if kind, _, err := l.cease(t); kind != test.want || err != nil {
					t.Fatalf("cease = %q, %v, want %q from the ceased cause (EG-8)", kind, err, test.want)
				}
			}
			count, kind, interrupt := l.termination(t)
			if count != 1 || kind != test.want || interrupt != "" || l.operationState(t, "operation-interrupt-pending") != "succeeded" {
				t.Fatalf("termination %d %q interrupt %q, Interrupt %q: want one %q row, never interrupted, and the Interrupt succeeded (EG-9)",
					count, kind, interrupt, l.operationState(t, "operation-interrupt-pending"), test.want)
			}
			if _, err := PrepareCanonicalV19SessionRelease(context.Background(), l.home, CanonicalV19SessionReleasePrepareInput{
				OperationID: "operation-release-after-cessation", OperationKey: "operation-key-release-after-cessation",
				SessionBindingID: l.session.BindingID, CreatedAt: "2026-09-08T03:10:00Z",
			}); err != nil {
				t.Fatalf("Session release after cessation: %v, want B closed", err)
			}
		})
	}
}

func TestExecGuardCessationLeavesALiveOrVanishedGuardOpen(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exec sleep 30")
	guard, running := l.establish(t)
	t.Cleanup(func() {
		if osfacts.Observe(*running.Root) == osfacts.Alive {
			_ = syscall.Kill(running.Root.PID, syscall.SIGKILL)
		}
	})
	path := filepath.Join(l.dir, execguard.KindCeased)
	if err := os.WriteFile(path, []byte(`{"protocol":"hand-exec-guard:v9","kind":"ceased"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if kind, _, err := l.cease(t); kind != "" || !errors.Is(err, execguard.ErrUnknownProtocol) {
		t.Fatalf("cease = %q, %v, want the typed refusal of an unknown record version, never absent (EG-17)", kind, err)
	}
	forged := running
	forged.Kind, forged.Guard.StartTicks = execguard.KindCeased, running.Guard.StartTicks+1
	forged.Cause, forged.Exit, forged.Predicate = execguard.CauseHarnessExit, &execguard.Exit{}, "wait4-echild"
	data, err := json.Marshal(forged)
	if err == nil {
		err = os.WriteFile(path, data, 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
	if kind, liveness, err := l.cease(t); kind != "" || liveness != osfacts.Alive || err != nil {
		t.Fatalf("cease = %q, %q, %v, want B open: a ceased record of another incarnation is not G's (EG-3, EG-13)", kind, liveness, err)
	}
	_ = guard.Process.Kill()
	_ = guard.Wait()
	if kind, liveness, err := l.cease(t); kind != "" || liveness != osfacts.Absent || err != nil {
		t.Fatalf("cease = %q, %q, %v, want B open and unknown after a SIGKILLed guard (EG-8, counterexample 3)", kind, liveness, err)
	}
	if count, _, _ := l.termination(t); count != 0 {
		t.Fatalf("termination rows = %d, want none without G's ceased record", count)
	}
}

func TestExecGuardCessationNeverLabelsAForgedInterruptRequestInterrupted(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exec sleep 30")
	_, running := l.establish(t)
	l.interrupt(t, "operation-interrupt-prepared", false)
	if err := execguard.RequestInterrupt(l.dir, running.Guard, "operation-interrupt-prepared"); err != nil {
		t.Fatal(err)
	}
	waitExecGuardCeased(t, l.dir)
	if ceased := mustExecGuardRecord(t, l.dir, execguard.KindCeased); ceased.Cause != execguard.CauseInterruptRequest {
		t.Fatalf("ceased cause = %q, want the forged request's", ceased.Cause)
	}
	kind, _, err := l.cease(t)
	count, _, interrupt := l.termination(t)
	if kind != "failed" || err != nil || count != 1 || interrupt != "" || l.operationState(t, "operation-interrupt-prepared") != "succeeded" {
		t.Fatalf("cease = %q, %v (rows %d, interrupt %q), want failed for a request naming no submitted Interrupt (EG-9, counterexample 15)", kind, err, count, interrupt)
	}
}

func TestExecGuardCessationOfACraftedIncarnation(t *testing.T) {
	self, err := osfacts.ReadIncarnation(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		guard     osfacts.Incarnation
		kind      string
		liveness  osfacts.Observation
		interrupt string
	}{
		{"boot change", osfacts.Incarnation{BootID: "00000000-0000-4000-8000-000000000000", PID: self.PID, StartTicks: self.StartTicks}, "provider-gone", osfacts.BootChanged, "succeeded"},
		{"reused PID", osfacts.Incarnation{BootID: self.BootID, PID: self.PID, StartTicks: self.StartTicks + 1}, "", osfacts.Absent, "submitted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
			l.establishKey(t, test.guard)
			l.interrupt(t, "operation-interrupt-crafted", true)
			kind, liveness, err := l.cease(t)
			if count, _, _ := l.termination(t); kind != test.kind || liveness != test.liveness || err != nil || (count == 1) != (test.kind != "") {
				t.Fatalf("cease = %q, %q, %v with %d rows, want %q (EG-3, EG-8)", kind, liveness, err, count, test.kind)
			}
			if state := l.operationState(t, "operation-interrupt-crafted"); state != test.interrupt {
				t.Fatalf("Interrupt = %q, want %q", state, test.interrupt)
			}
		})
	}
}
