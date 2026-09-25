package cmd

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

type workerInputCommandDeps struct {
	role       func() string
	homeDir    func() (string, error)
	binding    func() string
	credential func() string
}

func newWorkerInputCmd() *cobra.Command {
	return newWorkerInputCmdWithDeps(workerInputCommandDeps{
		role:       func() string { return os.Getenv(harness.RoleEnv) },
		homeDir:    home.Resolve,
		binding:    func() string { return os.Getenv(execguard.ExecutorBindingEnv) },
		credential: func() string { return os.Getenv(execguard.CredentialEnv) },
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
		Use:   "drain [attempt-id] [executor-binding-id]",
		Short: "Read pending canonical WorkerInputs for this exact worker execution",
		Long: "B comes from HAND_WORKER_EXECUTOR_BINDING and its Attempt is derived from B; the doorbell\n" +
			"carries neither. Argv IDs are optional and, if given, must name that exact Attempt/B.",
		Args: usageArgs(cobra.MaximumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			attemptID, executorBindingID := "", ""
			if len(args) > 0 {
				attemptID = args[0]
			}
			if len(args) > 1 {
				executorBindingID = args[1]
			}
			return runWorkerInputDrain(cmd, deps, attemptID, executorBindingID)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "acknowledge <worker-input-id> [executor-binding-id]",
		Short: "Record that this worker observed one exact canonical WorkerInput",
		Long: "B comes from HAND_WORKER_EXECUTOR_BINDING. The argv ExecutorBinding ID is optional and,\n" +
			"if given, must name that exact B.",
		Args: usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			executorBindingID := ""
			if len(args) > 1 {
				executorBindingID = args[1]
			}
			return runWorkerInputAcknowledge(cmd, deps, args[0], executorBindingID)
		},
	})
	return cmd
}

func runWorkerInputDrain(
	cmd *cobra.Command,
	deps workerInputCommandDeps,
	attemptID string,
	executorBindingIDArg string,
) error {
	if err := requireWorkerInputProtocolRole(deps); err != nil {
		return err
	}
	homeDir, binding, credential, err := workerInputCallerCredential(deps, executorBindingIDArg)
	if err != nil {
		return err
	}
	pending, err := store.DrainCanonicalV19WorkerInputs(cmd.Context(), homeDir, store.CanonicalV19WorkerInputDrainInput{
		AttemptID: attemptID, ExecutorBindingID: binding, Credential: credential,
	})
	if err != nil {
		return workerInputExitError(err)
	}
	rows := make([][]string, 0, len(pending))
	for _, item := range pending {
		rows = append(rows, []string{
			item.ID, strconv.FormatInt(item.Ordinal, 10), item.Payload, item.PayloadDigest, item.OriginKind, item.CreatedAt,
		})
	}
	var doc axi.Doc
	doc.Rows("inputs", []string{"id", "ordinal", "payload", "payload_digest", "origin_kind", "created_at"}, rows)
	return doc.Render(cmd.OutOrStdout())
}

func runWorkerInputAcknowledge(
	cmd *cobra.Command,
	deps workerInputCommandDeps,
	workerInputID string,
	executorBindingIDArg string,
) error {
	if err := requireWorkerInputProtocolRole(deps); err != nil {
		return err
	}
	homeDir, binding, credential, err := workerInputCallerCredential(deps, executorBindingIDArg)
	if err != nil {
		return err
	}
	verifier, err := store.VerifyCanonicalV19WorkerInputCallerCredential(cmd.Context(), homeDir, binding, credential)
	if err != nil {
		return workerInputExitError(err)
	}
	existing, found, err := store.ReadCanonicalV19WorkerInputAcknowledgement(cmd.Context(), homeDir, workerInputID)
	if err != nil {
		return workerInputExitError(err)
	}
	observedAt := workerInputAcknowledgeObservedAt(time.Now().UTC().Format(time.RFC3339), existing, found, binding)
	acknowledgement, err := store.CreateCanonicalV19WorkerInputAcknowledgement(cmd.Context(), homeDir,
		store.CanonicalV19WorkerInputAcknowledgementCreateInput{
			WorkerInputID: workerInputID, ExecutorBindingID: binding, ObservedAt: observedAt,
			EvidenceDigest: store.CanonicalV19WorkerInputAcknowledgementEvidenceDigest(workerInputID, binding, observedAt, verifier),
			Credential:     credential,
		})
	if err != nil {
		return workerInputExitError(err)
	}
	var doc axi.Doc
	doc.Field("worker_input_id", acknowledgement.WorkerInputID)
	doc.Field("executor_binding_id", acknowledgement.ExecutorBindingID)
	doc.Field("observed_at", acknowledgement.ObservedAt)
	return doc.Render(cmd.OutOrStdout())
}

// A replay after a lost reply must converge on the first-recorded observed_at, not a fresh
// one, or the writer's exact-evidence-match idempotency sees drift and refuses it as a conflict.
func workerInputAcknowledgeObservedAt(now string, existing store.CanonicalV19WorkerInputAcknowledgement, found bool, binding string) string {
	if found && existing.ExecutorBindingID == binding {
		return existing.ObservedAt
	}
	return now
}

// B and S_B come from the caller's own environment, never from argv. An argv ExecutorBinding
// ID is an optional cross-check and, if given, must name that exact B.
func workerInputCallerCredential(deps workerInputCommandDeps, executorBindingIDArg string) (string, string, string, error) {
	binding := workerInputEnvValue(deps.binding)
	credential := workerInputEnvValue(deps.credential)
	if binding == "" || credential == "" {
		return "", "", "", workerInputMissingEnvironment()
	}
	if executorBindingIDArg != "" && executorBindingIDArg != binding {
		return "", "", "", workerInputCallerAttestationUnsupported()
	}
	if deps.homeDir == nil {
		return "", "", "", fmt.Errorf("worker-input protocol home dependency is unavailable")
	}
	homeDir, err := deps.homeDir()
	if err != nil {
		return "", "", "", err
	}
	return homeDir, binding, credential, nil
}

func workerInputEnvValue(get func() string) string {
	if get == nil {
		return ""
	}
	return get()
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

func workerInputMissingEnvironment() error {
	return &ExitError{Err: fmt.Errorf(
		"%w: %s and %s must both be set in this worker's environment",
		store.ErrCanonicalV19HerdrCapabilityUnsupported, execguard.ExecutorBindingEnv, execguard.CredentialEnv,
	), Code: 3}
}

func workerInputCallerAttestationUnsupported() error {
	return &ExitError{Err: fmt.Errorf(
		"%w: the argv ExecutorBinding ID does not name this environment's exact ExecutorBinding",
		store.ErrCanonicalV19HerdrCapabilityUnsupported,
	), Code: 3}
}

func workerInputExitError(err error) error {
	return &ExitError{Err: err, Code: 3}
}
