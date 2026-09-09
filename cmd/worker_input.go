package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

type workerInputCommandDeps struct {
	resolveHome func() (string, error)
	drain       func(context.Context, string, store.CanonicalV19WorkerInputDrainInput) ([]store.CanonicalV19WorkerInput, error)
	readAck     func(context.Context, string, string) (store.CanonicalV19WorkerInputAcknowledgement, bool, error)
	createAck   func(context.Context, string, store.CanonicalV19WorkerInputAcknowledgementCreateInput) (store.CanonicalV19WorkerInputAcknowledgement, error)
	now         func() time.Time
	role        func() string
}

type workerInputDrainOutput struct {
	AttemptID         string                       `json:"attempt_id"`
	ExecutorBindingID string                       `json:"executor_binding_id"`
	DrainedAt         string                       `json:"drained_at"`
	Inputs            []workerInputDrainOutputItem `json:"inputs"`
}

type workerInputDrainOutputItem struct {
	ID            string `json:"id"`
	Ordinal       int64  `json:"ordinal"`
	Payload       string `json:"payload"`
	PayloadDigest string `json:"payload_digest"`
	OriginKind    string `json:"origin_kind"`
	CreatedAt     string `json:"created_at"`
}

type workerInputAcknowledgementOutput struct {
	WorkerInputID     string `json:"worker_input_id"`
	ExecutorBindingID string `json:"executor_binding_id"`
	ActorKind         string `json:"actor_kind"`
	ObservedAt        string `json:"observed_at"`
	EvidenceDigest    string `json:"evidence_digest"`
	Replayed          bool   `json:"replayed"`
}

func newWorkerInputCmd() *cobra.Command {
	return newWorkerInputCmdWithDeps(workerInputCommandDeps{
		resolveHome: home.Resolve,
		drain:       store.DrainCanonicalV19WorkerInputs,
		readAck:     store.ReadCanonicalV19WorkerInputAcknowledgement,
		createAck:   store.CreateCanonicalV19WorkerInputAcknowledgement,
		now:         time.Now,
		role:        func() string { return os.Getenv(harness.RoleEnv) },
	})
}

func newWorkerInputCmdWithDeps(deps workerInputCommandDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "worker-input",
		Short:  "Use the canonical WorkerInput runtime protocol",
		Hidden: true,
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
	cmd *cobra.Command,
	deps workerInputCommandDeps,
	attemptID string,
	executorBindingID string,
) error {
	if err := requireWorkerInputProtocolRole(deps); err != nil {
		return err
	}
	if deps.resolveHome == nil || deps.drain == nil || deps.now == nil {
		return fmt.Errorf("worker-input drain dependencies are incomplete")
	}
	fleetHome, err := deps.resolveHome()
	if err != nil {
		return asPrecondition(err)
	}
	pending, err := deps.drain(cmd.Context(), fleetHome, store.CanonicalV19WorkerInputDrainInput{
		AttemptID: attemptID, ExecutorBindingID: executorBindingID,
	})
	if err != nil {
		if errors.Is(err, store.ErrCanonicalV19WorkerInputDrainNotCurrent) {
			return &ExitError{Err: err, Code: 3}
		}
		return err
	}
	output := workerInputDrainOutput{
		AttemptID: attemptID, ExecutorBindingID: executorBindingID,
		DrainedAt: deps.now().UTC().Format(time.RFC3339Nano),
		Inputs:    make([]workerInputDrainOutputItem, 0, len(pending)),
	}
	for _, input := range pending {
		output.Inputs = append(output.Inputs, workerInputDrainOutputItem{
			ID: input.ID, Ordinal: input.Ordinal, Payload: input.Payload,
			PayloadDigest: input.PayloadDigest, OriginKind: input.OriginKind, CreatedAt: input.CreatedAt,
		})
	}
	return encodeWorkerInputProtocolOutput(cmd, output)
}

func runWorkerInputAcknowledge(
	cmd *cobra.Command,
	deps workerInputCommandDeps,
	workerInputID string,
	executorBindingID string,
) error {
	if err := requireWorkerInputProtocolRole(deps); err != nil {
		return err
	}
	if deps.resolveHome == nil || deps.readAck == nil || deps.createAck == nil || deps.now == nil {
		return fmt.Errorf("worker-input acknowledge dependencies are incomplete")
	}
	fleetHome, err := deps.resolveHome()
	if err != nil {
		return asPrecondition(err)
	}
	if acknowledgement, found, err := deps.readAck(cmd.Context(), fleetHome, workerInputID); err != nil {
		return err
	} else if found {
		return renderWorkerInputAcknowledgement(cmd, acknowledgement, executorBindingID, true)
	}

	observedAt := deps.now().UTC().Format(time.RFC3339Nano)
	acknowledgement, err := deps.createAck(cmd.Context(), fleetHome, store.CanonicalV19WorkerInputAcknowledgementCreateInput{
		WorkerInputID: workerInputID, ExecutorBindingID: executorBindingID,
		ObservedAt: observedAt, EvidenceDigest: workerInputAcknowledgementEvidenceDigest(workerInputID, executorBindingID, observedAt),
	})
	if err != nil {
		if !errors.Is(err, store.ErrCanonicalV19WorkerInputAcknowledgementConflict) {
			return err
		}
		// A concurrent acknowledgement may have committed after the read above. Re-read exact immutable
		// evidence rather than manufacturing a second observation timestamp.
		existing, found, readErr := deps.readAck(cmd.Context(), fleetHome, workerInputID)
		if readErr != nil {
			return readErr
		}
		if !found {
			return &ExitError{Err: err, Code: 3}
		}
		return renderWorkerInputAcknowledgement(cmd, existing, executorBindingID, true)
	}
	return renderWorkerInputAcknowledgement(cmd, acknowledgement, executorBindingID, false)
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

func renderWorkerInputAcknowledgement(
	cmd *cobra.Command,
	acknowledgement store.CanonicalV19WorkerInputAcknowledgement,
	executorBindingID string,
	replayed bool,
) error {
	if acknowledgement.WorkerInputID == "" || acknowledgement.ExecutorBindingID != executorBindingID || acknowledgement.ActorKind != "worker" {
		return &ExitError{Err: fmt.Errorf(
			"canonical v19 WorkerInputAcknowledgement does not belong to exact ExecutorBinding %q", executorBindingID,
		), Code: 3}
	}
	return encodeWorkerInputProtocolOutput(cmd, workerInputAcknowledgementOutput{
		WorkerInputID: acknowledgement.WorkerInputID, ExecutorBindingID: acknowledgement.ExecutorBindingID,
		ActorKind: acknowledgement.ActorKind, ObservedAt: acknowledgement.ObservedAt,
		EvidenceDigest: acknowledgement.EvidenceDigest, Replayed: replayed,
	})
}

func workerInputAcknowledgementEvidenceDigest(workerInputID, executorBindingID, observedAt string) string {
	payload, _ := json.Marshal(struct {
		Domain            string `json:"domain"`
		WorkerInputID     string `json:"worker_input_id"`
		ExecutorBindingID string `json:"executor_binding_id"`
		ObservedAt        string `json:"observed_at"`
	}{
		Domain: "hand:v19:worker-input-ack-protocol:v1", WorkerInputID: workerInputID,
		ExecutorBindingID: executorBindingID, ObservedAt: observedAt,
	})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func encodeWorkerInputProtocolOutput(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
