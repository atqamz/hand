package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func TestWorkerInputProtocolShadowsLegacyRootStartupHook(t *testing.T) {
	root := &cobra.Command{
		Use: "hand",
		PersistentPreRunE: func(*cobra.Command, []string) error {
			t.Fatal("legacy root startup hook ran for canonical WorkerInput protocol")
			return nil
		},
	}
	runtime := &cobra.Command{Use: "runtime"}
	runtime.AddCommand(newWorkerInputCmdWithDeps(workerInputCommandDeps{
		role: func() string { return harness.WorkerRole },
	}))
	root.AddCommand(runtime)
	root.SetArgs([]string{"runtime", "worker-input", "drain", "attempt-1", "executor-1"})
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(new(strings.Builder))
	_, err := root.ExecuteC()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 || !errors.Is(err, store.ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("worker-input error = %v, want unsupported precondition", err)
	}
	if strings.Contains(out.String(), `"inputs"`) || strings.Contains(out.String(), `"worker_input_id"`) {
		t.Fatalf("worker-input emitted semantic output: %q", out.String())
	}
}
