package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/orientation"
	"github.com/atqamz/hand/internal/supervision"
	"github.com/spf13/cobra"
)

// hand orient is the one observation every Supervisor reasoning turn begins
// with, including the turn an automatic wake re-enters. Read-mostly: the only
// thing it records is that this turn actually oriented the current episodes.
func newOrientCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "orient",
		Short: "Observe current bounded Supervisor orientation for this reasoning turn",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
				return &ExitError{Err: fmt.Errorf("hand orient is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			snapshot, err := loadFleetSnapshot(cmd.Context(), cmd.ErrOrStderr(), fleetHome)
			if err != nil {
				return err
			}
			evidence := snapshot.evidence()

			ledger := supervision.OpenLedger(fleetHome)
			if err := ledger.MarkOriented(supervision.FromEvidence(evidence)); err != nil {
				return err
			}

			result := orientation.Build(evidence)
			var doc axi.Doc
			appendSessionOrientation(&doc, result)
			doc.Field("herdr_session_name", orNone(snapshot.herdrSession.Name))
			doc.Field("herdr_session_state", orNone(string(snapshot.herdrSession.State)))
			doc.Field("herdr_session_reason", orNone(snapshot.herdrSession.Reason))
			doc.Field("next_action_kind", snapshot.next.Kind)
			doc.Field("next_action_task", orNone(snapshot.next.Task))
			doc.Field("next_action_command", orNone(snapshot.next.Command))
			doc.Field("next_action_reason", snapshot.next.Reason)
			appendSupervisionProgress(&doc, ledger)
			doc.Help("Reason and act on this observation; run `hand orient` again after every automatic wake")
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

// Renders the mechanism-progress diagnostics that must stay distinguishable
// from canonical truth: acceptance without a later orientation stamp means a
// host took the wake but no reasoning turn returned.
func appendSupervisionProgress(doc *axi.Doc, ledger *supervision.Ledger) {
	lastAccepted, lastOriented := ledger.Progress()
	doc.Field("wake_delivery_last_accepted", timestampOrNone(lastAccepted))
	doc.Field("orientation_progress", timestampOrNone(lastOriented))
}

func timestampOrNone(t *time.Time) string {
	if t == nil {
		return "none"
	}
	return t.UTC().Format(time.RFC3339)
}

// Answers, separately: harness identity, runtime addressability, integration
// currency, unattended turn capability, bridge attachment, wake acceptance,
// orient progress, and watcher liveness. No one field proves autonomy.
func appendSupervisionDiagnostics(doc *axi.Doc, ctx context.Context, fleetHome string) {
	detection, err := harness.DetectCurrent()
	if err != nil {
		doc.Field("supervisor_harness", "unknown")
		doc.Field("supervisor_diagnosis_error", err.Error())
		return
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "unknown"
	}
	status, err := supervision.IntegrationStatus(ctx, supervision.StatusInput{
		Home:      fleetHome,
		Detection: detection,
		Exe:       exe,
	})
	if err != nil {
		doc.Field("supervisor_harness", orNone(detection.Name))
		doc.Field("supervisor_diagnosis_error", err.Error())
		return
	}
	doc.Field("supervisor_harness", orNone(status.Harness))
	doc.Field("supervisor_harness_source", orNone(status.HarnessSource))
	doc.Field("host_runtime_version", orNone(status.RuntimeVersion))
	doc.Field("host_api_generation", orNone(status.RuntimeAPIGeneration))
	doc.Field("host_platform", orNone(status.RuntimePlatform))
	doc.Field("host_capability", orNone(status.RuntimeCapability))
	doc.Field("host_capability_qualification", orNone(status.Qualification))
	doc.Field("host_capability_qualification_reason", orNone(status.QualificationReason))
	doc.Field("runtime_identity_status", orNone(status.RuntimeIdentity))
	doc.Field("runtime_identity_detail", orNone(status.RuntimeDetail))
	doc.Field("bootstrap_integration_status", orNone(status.Integration))
	doc.Field("bootstrap_integration_detail", orNone(status.IntegrationDetail))
	doc.Field("integration_mechanism_version", orNone(status.IntegrationVersion))
	doc.Field("addressability_status", orNone(status.Addressability))
	doc.Field("addressability_reason", orNone(status.AddressabilityReason))
	doc.Field("wake_delivery_capability", status.WakeDelivery)
	doc.Field("wake_delivery_reason", orNone(status.WakeDeliveryReason))
	doc.Field("wake_delivery_attachment_status", orNone(status.Attachment))
	lastAccepted, lastOriented := supervision.OpenLedger(fleetHome).Progress()
	doc.Field("wake_delivery_last_accepted", timestampOrNone(lastAccepted))
	doc.Field("orientation_progress", timestampOrNone(lastOriented))
	doc.Field("watcher_observer_liveness", orNone(status.WatcherLiveness))
}
