package runtime

import "strings"

const (
	repairCodeProvisioningLaunchAmbiguous = "provisioning-launch-ambiguous"
	repairCodeProvisioningPaneMissing     = "provisioning-pane-missing"
	repairCodeLaunchSubmittedPaneMissing  = "launch-submitted-pane-missing"
	repairCodeLaunchAgentMismatch         = "launch-agent-mismatch"
	repairCodeRunningPaneMissing          = "running-pane-missing"
	repairCodeRunningPaneIdentityMismatch = "running-pane-identity-mismatch"
	repairCodeHerdrOwnershipIncomplete    = "herdr-ownership-incomplete"
	repairCodeHerdrOwnershipMismatch      = "herdr-ownership-mismatch"
	repairCodeWorktreeDirty               = "worktree-dirty"
	repairCodeWorktreeOwnershipMismatch   = "worktree-ownership-mismatch"
	repairCodeLegacyWorktreeUnprovable    = "legacy-worktree-ownership-unprovable"
	repairCodeWorktreeUnobservable        = "worktree-ownership-unobservable"
	repairCodeWorktreeLocalCommits        = "worktree-local-commits"
	repairCodeWorktreeCommitSafetyUnknown = "worktree-commit-safety-unknown"
	repairCodeTeardownResourceAmbiguous   = "teardown-resource-ambiguous"
	repairCodeCompletionEvidenceMismatch  = "completion-evidence-mismatch"
	repairCodeMergeFactMismatch           = "merge-fact-mismatch"
)

// How a diagnosis ends, from the operator's side of it.
type repairClass string

const (
	// A supported hand command leaves the state, without attesting to anything and without changing
	// the world outside Hand first.
	repairClassSupportedCommand repairClass = "supported-command"
	// Ownership can be neither proven nor disproven, so the only way out is an operator attestation
	// scoped to that one resource, which relinquishes Hand's claim and destroys nothing.
	repairClassAttestation repairClass = "explicit-supported-attestation"
	// The diagnosis is about the world outside Hand, so reconciling again is what ends it once that
	// world is fixed.
	repairClassExternalFix repairClass = "retryable-after-external-fix"
)

type repairTreatment struct {
	Class     repairClass
	Treatment string
}

const (
	treatmentTeardownReopen       = "end this attempt with `hand teardown <id>` (add --force when no worktree is left to inspect), then `hand reconcile <id>` clears this diagnosis and `hand reopen <id>` starts a new attempt"
	treatmentTeardownSettleReopen = "end this attempt with `hand teardown <id>`, which records the teardown decision even when the recorded lease cannot be released, after which `hand reconcile <id>` settles the recorded claim and clears this diagnosis, and `hand reopen <id>` starts a new attempt"
	treatmentAbandonWorktree      = "end this attempt with `hand teardown <id>` if it is still active, then `hand reconcile <id> --abandon-worktree` attests that Hand relinquishes the recorded lease, and returns, prunes or deletes nothing"
	treatmentAbandonPane          = "end this attempt with `hand teardown <id>` if it is still active, then `hand reconcile <id> --abandon-pane` attests that Hand relinquishes the recorded pane identity, and closes no pane, tab or workspace"
)

// The one enumeration of every repair code Hand can answer with, and of the supported treatment that
// ends each one. A code missing from here is a diagnosis an operator cannot act on, so the package
// tests assert this map covers every declared code (atqamz/hand#254).
var repairTreatments = map[string]repairTreatment{
	repairCodeProvisioningLaunchAmbiguous: {repairClassSupportedCommand, treatmentTeardownReopen},
	repairCodeProvisioningPaneMissing: {repairClassSupportedCommand,
		"reconcile unwinds a launch that persisted no owned resource on its own; while a worktree lease is still recorded, " + treatmentTeardownReopen},
	repairCodeLaunchSubmittedPaneMissing: {repairClassSupportedCommand, treatmentTeardownReopen},
	repairCodeLaunchAgentMismatch: {repairClassExternalFix,
		"answer the harness first-run dialog in that pane yourself once so the recorded harness becomes observable and `hand reconcile <id>` clears this diagnosis, or " + treatmentTeardownReopen},
	repairCodeRunningPaneMissing: {repairClassSupportedCommand,
		"restore the landing evidence so reconcile can converge this attempt to the lifecycle that evidence proves, or " + treatmentTeardownReopen},
	repairCodeRunningPaneIdentityMismatch: {repairClassSupportedCommand, treatmentTeardownReopen},
	repairCodeHerdrOwnershipIncomplete:    {repairClassAttestation, treatmentAbandonPane},
	repairCodeHerdrOwnershipMismatch:      {repairClassSupportedCommand, treatmentTeardownReopen},
	repairCodeWorktreeDirty: {repairClassExternalFix,
		"commit, push or discard what that worktree holds, then `hand reconcile <id>` clears this diagnosis"},
	repairCodeWorktreeOwnershipMismatch: {repairClassSupportedCommand,
		"reconcile relinquishes a historical claim on a path another lease now holds; for the current attempt, " + treatmentTeardownSettleReopen},
	repairCodeLegacyWorktreeUnprovable: {repairClassAttestation, treatmentAbandonWorktree},
	repairCodeWorktreeUnobservable:     {repairClassAttestation, treatmentAbandonWorktree},
	repairCodeWorktreeLocalCommits: {repairClassExternalFix,
		"push those commits, or record the pull request whose head commit holds them, then `hand reconcile <id>` clears this diagnosis"},
	repairCodeWorktreeCommitSafetyUnknown: {repairClassExternalFix,
		"restore the named observation in that clone so the comparison can be made, then `hand reconcile <id>` clears this diagnosis"},
	repairCodeTeardownResourceAmbiguous:  {repairClassAttestation, treatmentAbandonPane},
	repairCodeCompletionEvidenceMismatch: {repairClassExternalFix, "restore that attempt's record in the fleet home's state/completions.jsonl, then `hand reconcile <id>` clears this diagnosis"},
	repairCodeMergeFactMismatch: {repairClassExternalFix,
		"merge the recorded pull request or branch, or restore the access that makes the merge observable, then `hand reconcile <id>` clears this diagnosis"},
}

// A persisted reason names its own way out, because the reason is what `hand status` and `hand
// reconcile` show an operator who has nothing else to go on.
func repairReasonWithTreatment(taskID, code, reason string) string {
	treatment, found := repairTreatments[code]
	if !found {
		return reason + "; no supported treatment is recorded for this diagnosis, which is a defect in Hand rather than a state you can repair"
	}
	return reason + "; " + strings.ReplaceAll(treatment.Treatment, "<id>", taskID)
}
