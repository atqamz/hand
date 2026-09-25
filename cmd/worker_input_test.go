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

func TestWorkerInputProtocolRefusesMissingOrMismatchedEnvironment(t *testing.T) {
	tests := []struct {
		name       string
		binding    string
		credential string
		argBinding string
	}{
		{"missing credential", "executor-1", "", "executor-1"},
		{"missing binding", "", "credential-value", "executor-1"},
		{"binding does not name the argv ExecutorBinding", "executor-other", "credential-value", "executor-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := newWorkerInputCmdWithDeps(workerInputCommandDeps{
				role:       func() string { return harness.WorkerRole },
				binding:    func() string { return test.binding },
				credential: func() string { return test.credential },
				homeDir: func() (string, error) {
					t.Fatal("home dependency must not be reached before caller attestation")
					return "", nil
				},
			})
			cmd.SetArgs([]string{"drain", "attempt-1", test.argBinding})
			var out strings.Builder
			cmd.SetOut(&out)
			cmd.SetErr(new(strings.Builder))
			_, err := cmd.ExecuteC()
			var exitErr *ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != 3 || !errors.Is(err, store.ErrCanonicalV19HerdrCapabilityUnsupported) {
				t.Fatalf("%s: error = %v, want attestation precondition exit 3", test.name, err)
			}
		})
	}
}

func TestWorkerInputProtocolNeverEchoesTheCredential(t *testing.T) {
	const marker = "s-b-secret-marker-should-never-appear-anywhere"
	homeDir := t.TempDir()
	tests := [][]string{
		{"drain", "attempt-1", "executor-1"},
		{"acknowledge", "input-1", "executor-1"},
	}
	for _, args := range tests {
		t.Run(args[0], func(t *testing.T) {
			cmd := newWorkerInputCmdWithDeps(workerInputCommandDeps{
				role:       func() string { return harness.WorkerRole },
				binding:    func() string { return "executor-1" },
				credential: func() string { return marker },
				homeDir:    func() (string, error) { return homeDir, nil },
			})
			cmd.SetArgs(args)
			var out, errOut strings.Builder
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			_, err := cmd.ExecuteC()
			if err == nil {
				t.Fatalf("%s against an empty home unexpectedly succeeded", args[0])
			}
			if strings.Contains(out.String(), marker) || strings.Contains(errOut.String(), marker) || strings.Contains(err.Error(), marker) {
				t.Fatalf("%s leaked the credential: out=%q err_out=%q err=%v", args[0], out.String(), errOut.String(), err)
			}
		})
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
