package cmd

import (
	"fmt"
	"os"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

type workerInputCommandDeps struct {
	role func() string
}

func newWorkerInputCmd() *cobra.Command {
	return newWorkerInputCmdWithDeps(workerInputCommandDeps{
		role: func() string { return os.Getenv(harness.RoleEnv) },
	})
}

func newWorkerInputCmdWithDeps(deps workerInputCommandDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "worker-input",
		Short:  "Use the canonical WorkerInput runtime protocol",
		Hidden: true,
		// Canonical v19 protocol commands deliberately shadow the root PersistentPreRunE. Ordinary
		// runtime commands still use legacy startup/migration guards until the explicit #339 cutover.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "drain <attempt-id> <executor-binding-id>",
		Short: "Read pending canonical WorkerInputs for this exact worker execution",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkerInputDrain(cmd, deps, args[0], args[1])
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "acknowledge <worker-input-id> <executor-binding-id>",
		Short: "Record that this worker observed one exact canonical WorkerInput",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkerInputAcknowledge(cmd, deps, args[0], args[1])
		},
	})
	return cmd
}

func runWorkerInputDrain(
	_ *cobra.Command,
	deps workerInputCommandDeps,
	_ string,
	_ string,
) error {
	if err := requireWorkerInputProtocolRole(deps); err != nil {
		return err
	}
	return workerInputCallerAttestationUnsupported()
}

func runWorkerInputAcknowledge(
	_ *cobra.Command,
	deps workerInputCommandDeps,
	_ string,
	_ string,
) error {
	if err := requireWorkerInputProtocolRole(deps); err != nil {
		return err
	}
	return workerInputCallerAttestationUnsupported()
}

func requireWorkerInputProtocolRole(deps workerInputCommandDeps) error {
	if deps.role == nil {
		return fmt.Errorf("worker-input protocol role dependency is unavailable")
	}
	if deps.role() != harness.WorkerRole {
		return &ExitError{Err: fmt.Errorf("worker-input protocol requires %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
	}
	return nil
}

func workerInputCallerAttestationUnsupported() error {
	return &ExitError{Err: fmt.Errorf(
		"%w: selected managed Herdr provider cannot supply exact WorkerInput caller attestation",
		store.ErrCanonicalV19HerdrCapabilityUnsupported,
	), Code: 3}
}
