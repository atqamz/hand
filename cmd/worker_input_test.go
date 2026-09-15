package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/store"
)

func TestWorkerInputProtocolRequiresWorkerRole(t *testing.T) {
	cmd := newWorkerInputCmdWithDeps(workerInputCommandDeps{
		role: func() string { return "" },
	})
	cmd.SetArgs([]string{"drain", "attempt-1", "executor-1"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	_, err := cmd.ExecuteC()
	if err == nil {
		t.Fatal("worker-input drain unexpectedly accepted non-worker role")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("worker-input role error = %v, want precondition exit 3", err)
	}
	if !strings.Contains(err.Error(), "HAND_ROLE=worker") {
		t.Fatalf("worker-input role error = %q", err)
	}
}

func TestWorkerInputProtocolRefusesUnattestedHerdrCallerBeforeSemanticAccess(t *testing.T) {
	tests := [][]string{
		{"drain", "attempt-2", "executor-2"},
		{"acknowledge", "input-2", "executor-2"},
	}
	for _, args := range tests {
		t.Run(args[0], func(t *testing.T) {
			cmd := newWorkerInputCmdWithDeps(workerInputCommandDeps{
				role: func() string { return harness.WorkerRole },
			})
			cmd.SetArgs(args)
			var out strings.Builder
			cmd.SetOut(&out)
			cmd.SetErr(new(strings.Builder))
			_, err := cmd.ExecuteC()
			var exitErr *ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != 3 || !errors.Is(err, store.ErrCanonicalV19HerdrCapabilityUnsupported) {
				t.Fatalf("unattested %s error = %v, want attestation precondition exit 3", args[0], err)
			}
			if strings.Contains(out.String(), `"inputs"`) || strings.Contains(out.String(), `"worker_input_id"`) {
				t.Fatalf("unattested %s emitted semantic output: %q", args[0], out.String())
			}
		})
	}
}
