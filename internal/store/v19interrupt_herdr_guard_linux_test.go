//go:build linux

package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/osfacts"
)

func (l *execGuardLaunchTest) reconcileInterrupt(operationID string) (string, error) {
	deps := canonicalV19HerdrInterruptDefaultDeps()
	deps.execGuard = true
	return reconcileCanonicalV19HerdrInterrupt(context.Background(), l.home, operationID, deps)
}

func (l *execGuardLaunchTest) interruptUntilSettled(operationID string) (string, error) {
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if state, err := l.reconcileInterrupt(operationID); state != "submitted" || time.Now().After(deadline) {
			return state, err
		}
	}
}

func (l *execGuardLaunchTest) requested(t *testing.T) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(l.dir, execguard.KindInterruptRequest))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestExecGuardInterruptSucceedsOnlyOnceTheGuardRecordsCessation(t *testing.T) {
	const id = "operation-interrupt-guard"
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exec sleep 30")
	guard, running := l.establish(t)
	l.interrupt(t, id, true)
	if err := guard.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guard.Process.Signal(syscall.SIGCONT) })
	for range 2 {
		if state, err := l.reconcileInterrupt(id); state != "submitted" || err == nil {
			t.Fatalf("reconcile = %q, %v, want submitted while a stalled guard records no cessation (EG-9, counterexample 11)", state, err)
		}
	}
	request := mustExecGuardRecord(t, l.dir, execguard.KindInterruptRequest)
	if count, _, _ := l.termination(t); request.Guard != running.Guard || request.InterruptOperationID != id || count != 0 {
		t.Fatalf("request %+v with %d terminations, want one idempotent request naming exactly G and no success from its acceptance", request, count)
	}
	if err := guard.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	state, err := l.interruptUntilSettled(id)
	count, kind, interrupt := l.termination(t)
	if state != "succeeded" || err != nil || count != 1 || kind != "interrupted" || interrupt != id {
		t.Fatalf("reconcile = %q, %v with termination %d %q %q, want succeeded and interrupted by this Interrupt (EG-9)", state, err, count, kind, interrupt)
	}
	if state, err := l.reconcileInterrupt(id); state != "succeeded" || err != nil {
		t.Fatalf("reconcile after the termination = %q, %v, want a stable succeeded", state, err)
	}
}

func TestExecGuardInterruptPreparedBeforeACrashSettlesNoEffectWithoutARequest(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exec sleep 30")
	l.establish(t)
	l.interrupt(t, "operation-interrupt-prepared", false)
	if state, err := l.reconcileInterrupt("operation-interrupt-prepared"); state != "no-effect" || err != nil || l.requested(t) {
		t.Fatalf("reconcile = %q, %v (request written %v), want no-effect: no request precedes Tx B", state, err, l.requested(t))
	}
	if kind, liveness, err := l.cease(t); kind != "" || liveness != osfacts.Alive || err != nil {
		t.Fatalf("cease = %q, %q, %v, want B open", kind, liveness, err)
	}
	l.interrupt(t, "operation-interrupt-replacement", true)
}

func TestExecGuardInterruptOfAKilledGuardStaysUncertainWithoutTermination(t *testing.T) {
	const id = "operation-interrupt-killed-guard"
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exec sleep 30")
	guard, running := l.establish(t)
	t.Cleanup(func() {
		if osfacts.Observe(*running.Root) == osfacts.Alive {
			_ = syscall.Kill(running.Root.PID, syscall.SIGKILL)
		}
	})
	l.interrupt(t, id, true)
	_ = guard.Process.Kill()
	_ = guard.Wait()
	for range 2 {
		if state, err := l.reconcileInterrupt(id); state != "uncertain" || err == nil {
			t.Fatalf("reconcile = %q, %v, want uncertain after a guard crash (EG-8, counterexample 3)", state, err)
		}
	}
	if count, _, _ := l.termination(t); count != 0 || l.requested(t) {
		t.Fatalf("%d terminations, request written %v, want neither for an absent G", count, l.requested(t))
	}
}

func TestExecGuardInterruptAfterANaturalExitSucceedsWithTheExitKind(t *testing.T) {
	const id = "operation-interrupt-after-exit"
	marker := filepath.Join(t.TempDir(), "exit")
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", execGuardExitOnMarker, marker, "0")
	l.establish(t)
	l.interrupt(t, id, true)
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitExecGuardCeased(t, l.dir)
	state, err := l.reconcileInterrupt(id)
	count, kind, interrupt := l.termination(t)
	if state != "succeeded" || err != nil || count != 1 || kind != "completed" || interrupt != "" || l.requested(t) {
		t.Fatalf("reconcile = %q, %v with termination %d %q %q, want succeeded and completed, never interrupted (EG-9)", state, err, count, kind, interrupt)
	}
}

func TestExecGuardInterruptOfACraftedIncarnation(t *testing.T) {
	self, err := osfacts.ReadIncarnation(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		guard osfacts.Incarnation
		state string
		kind  string
	}{
		{"boot change", osfacts.Incarnation{BootID: "00000000-0000-4000-8000-000000000000", PID: self.PID, StartTicks: self.StartTicks}, "succeeded", "provider-gone"},
		{"reused PID", osfacts.Incarnation{BootID: self.BootID, PID: self.PID, StartTicks: self.StartTicks + 1}, "uncertain", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			const id = "operation-interrupt-crafted"
			l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
			l.establishKey(t, test.guard)
			l.interrupt(t, id, true)
			state, err := l.reconcileInterrupt(id)
			if _, kind, _ := l.termination(t); state != test.state || (err == nil) != (test.kind != "") || kind != test.kind || l.requested(t) {
				t.Fatalf("reconcile = %q, %v with termination %q (request written %v), want %q and no request to a reused PID (EG-3, EG-8, counterexample 2)",
					state, err, kind, l.requested(t), test.state)
			}
		})
	}
}

func TestExecGuardInterruptWaitsBehindAnUncertainWakeWhileCessationStillClosesB(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "exit")
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", execGuardExitOnMarker, marker, "0")
	l.establish(t)
	launch := CanonicalV19LaunchRequest{AttemptID: l.input.AttemptID, BindingID: l.input.BindingID}
	wake := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-guard", 1)
	_, err := CreateCanonicalV19WorkerInput(context.Background(), l.home, canonicalV19WorkerInputCreateInput(launch, "worker-input-guard", "instruction", "digest-worker-input-guard"))
	if err == nil {
		_, err = PrepareCanonicalV19WorkerWake(context.Background(), l.home, wake)
	}
	if err == nil {
		_, err = SubmitCanonicalV19WorkerWake(context.Background(), l.home, wake.OperationID, "2026-09-09T05:16:00Z", "worker-wake-submitted")
	}
	if err == nil {
		err = ClassifyCanonicalV19WorkerWake(context.Background(), l.home, CanonicalV19WorkerWakeTransitionInput{
			OperationID: wake.OperationID, State: "uncertain", ObservedAt: "2026-09-09T05:17:00Z", EvidenceDigest: "worker-wake-uncertain",
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareCanonicalV19Interrupt(context.Background(), l.home, CanonicalV19InterruptPrepareInput{
		OperationID: "operation-interrupt-behind-wake", OperationKey: "operation-key-interrupt-behind-wake",
		ExecutorBindingID: l.input.BindingID, ReasonCode: "operator-request", CreatedAt: "2026-09-09T05:18:00Z",
	}); !errors.Is(err, ErrCanonicalV19InterruptConflict) {
		t.Fatalf("Interrupt behind an uncertain wake = %v, want the executor-control claim conflict", err)
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitExecGuardCeased(t, l.dir)
	if kind, _, err := l.cease(t); kind != "completed" || err != nil || l.operationState(t, wake.OperationID) != "uncertain" {
		t.Fatalf("cease = %q, %v, wake %q, want natural cessation to close B while the wake stays uncertain", kind, err, l.operationState(t, wake.OperationID))
	}
}

func TestProductionInterruptReconcileKeepsTheRevisionOneRefusalForAGuardedBinding(t *testing.T) {
	self, err := osfacts.ReadIncarnation(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	l.establishKey(t, self)
	l.interrupt(t, "operation-interrupt-production", true)
	state, err := ReconcileCanonicalV19HerdrInterrupt(context.Background(), l.home, "operation-interrupt-production")
	if count, _, _ := l.termination(t); state != "uncertain" || !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) || l.requested(t) || count != 0 {
		t.Fatalf("production reconcile = %q, %v (request written %v, %d terminations), want the revision-1 refusal untouched", state, err, l.requested(t), count)
	}
}
