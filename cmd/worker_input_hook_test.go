package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

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
		role:        func() string { return harness.WorkerRole },
		resolveHome: func() (string, error) { return "/fleet", nil },
		now:         func() time.Time { return time.Date(2026, 9, 9, 9, 32, 0, 0, time.UTC) },
		drain: func(context.Context, string, store.CanonicalV19WorkerInputDrainInput) ([]store.CanonicalV19WorkerInput, error) {
			return []store.CanonicalV19WorkerInput{}, nil
		},
	}))
	root.AddCommand(runtime)
	root.SetArgs([]string{"runtime", "worker-input", "drain", "attempt-1", "executor-1"})
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(new(strings.Builder))
	if _, err := root.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"inputs":[]`) {
		t.Fatalf("drain output = %q", out.String())
	}
}
