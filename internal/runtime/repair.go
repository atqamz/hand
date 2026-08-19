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

// A state a task can be stuck in while Hand answers no diagnosis at all, which is the worst member of
// the set the enumeration below covers: nothing tells the operator it exists, so nothing tells them
// how to leave it. Named here so its treatment is enumerated and tested like any code's.
const stuckStateRunningAttemptNeverStarted = "running-attempt-never-started"

// How a stuck state ends, from the operator's side of it.
type repairClass string

const (
	// A supported hand command leaves the state, without attesting to anything and without changing
	// the world outside Hand first.
	repairClassSupportedCommand repairClass = "supported-command"
	// A fact no observation can settle, so the only way out is an operator attestation scoped to that
	// one fact, which relinquishes a claim or records a decision and destroys nothing.
	repairClassAttestation repairClass = "explicit-supported-attestation"
	// The diagnosis is about the world outside Hand, so reconciling again is what ends it once that
	// world is fixed.
	repairClassExternalFix repairClass = "retryable-after-external-fix"
)

type stuckStateTreatment struct {
	Class     repairClass
	Treatment string
	// Set for a stuck state Hand cannot diagnose, whose treatment is therefore reachable only through
	// command help and this enumeration rather than through a persisted repair reason.
	Undiagnosed bool
}

const (
	treatmentTeardownReopen       = "end this attempt with `hand teardown <id>` (add --force when no worktree is left to inspect), then `hand reconcile <id>` clears this diagnosis and `hand reopen <id>` starts a new attempt"
	treatmentTeardownSettleReopen = "end this attempt with `hand teardown <id>`, which records the teardown decision even when the recorded lease cannot be released, after which `hand reconcile <id>` settles the recorded claim and clears this diagnosis, and `hand reopen <id>` starts a new attempt"
	treatmentAbandonWorktree      = "end this attempt with `hand teardown <id>` if it is still active, then `hand reconcile <id> --abandon-worktree` attests that Hand relinquishes the recorded lease, and returns, prunes or deletes nothing"
	treatmentAbandonPane          = "end this attempt with `hand teardown <id>` if it is still active, then `hand reconcile <id> --abandon-pane` attests that Hand relinquishes the recorded pane identity, and closes no pane, tab or workspace"
	treatmentAttemptNeverStarted  = "`hand reconcile <id> --attempt-never-started` attests that this attempt's worker took no turn, which records an honest teardown decision and releases nothing itself, after which the ordinary release path converges the attempt under its own guards and `hand reopen <id>` starts a new one"
)

// The one enumeration of every state a task can be stuck in, and of the supported treatment that ends
// each one. An entry missing from here is a state an operator cannot act on, so the package tests
// assert this map covers every declared repair code and stuck state (atqamz/hand#254).
var stuckStateTreatments = map[string]stuckStateTreatment{
	repairCodeProvisioningLaunchAmbiguous: {Class: repairClassSupportedCommand, Treatment: treatmentTeardownReopen},
	repairCodeProvisioningPaneMissing: {
		Class:     repairClassSupportedCommand,
		Treatment: "reconcile unwinds a launch that persisted no owned resource on its own; while a worktree lease is still recorded, " + treatmentTeardownReopen,
	},
	repairCodeLaunchSubmittedPaneMissing: {Class: repairClassSupportedCommand, Treatment: treatmentTeardownReopen},
	repairCodeLaunchAgentMismatch: {
		Class:     repairClassExternalFix,
		Treatment: "answer the harness first-run dialog in that pane yourself once so the recorded harness becomes observable and `hand reconcile <id>` clears this diagnosis, or " + treatmentTeardownReopen,
	},
	repairCodeRunningPaneMissing: {
		Class:     repairClassSupportedCommand,
		Treatment: "restore the landing evidence so reconcile can converge this attempt to the lifecycle that evidence proves, or " + treatmentTeardownReopen,
	},
	repairCodeRunningPaneIdentityMismatch: {Class: repairClassSupportedCommand, Treatment: treatmentTeardownReopen},
	repairCodeHerdrOwnershipIncomplete:    {Class: repairClassAttestation, Treatment: treatmentAbandonPane},
	repairCodeHerdrOwnershipMismatch:      {Class: repairClassSupportedCommand, Treatment: treatmentTeardownReopen},
	repairCodeWorktreeDirty: {
		Class:     repairClassExternalFix,
		Treatment: "commit, push or discard what that worktree holds, then `hand reconcile <id>` clears this diagnosis",
	},
	repairCodeWorktreeOwnershipMismatch: {
		Class:     repairClassSupportedCommand,
		Treatment: "reconcile relinquishes a historical claim on a path another lease now holds; for the current attempt, " + treatmentTeardownSettleReopen,
	},
	repairCodeLegacyWorktreeUnprovable: {Class: repairClassAttestation, Treatment: treatmentAbandonWorktree},
	repairCodeWorktreeUnobservable:     {Class: repairClassAttestation, Treatment: treatmentAbandonWorktree},
	repairCodeWorktreeLocalCommits: {
		Class:     repairClassExternalFix,
		Treatment: "push those commits, or record the pull request whose head commit holds them, then `hand reconcile <id>` clears this diagnosis",
	},
	repairCodeWorktreeCommitSafetyUnknown: {
		Class:     repairClassExternalFix,
		Treatment: "restore the named observation in that clone so the comparison can be made, then `hand reconcile <id>` clears this diagnosis",
	},
	repairCodeTeardownResourceAmbiguous:  {Class: repairClassAttestation, Treatment: treatmentAbandonPane},
	repairCodeCompletionEvidenceMismatch: {Class: repairClassExternalFix, Treatment: "restore that attempt's record in the fleet home's state/completions.jsonl, then `hand reconcile <id>` clears this diagnosis"},
	repairCodeMergeFactMismatch: {
		Class:     repairClassExternalFix,
		Treatment: "merge the recorded pull request or branch, or restore the access that makes the merge observable, then `hand reconcile <id>` clears this diagnosis",
	},
	stuckStateRunningAttemptNeverStarted: {
		Class:       repairClassAttestation,
		Treatment:   treatmentAttemptNeverStarted,
		Undiagnosed: true,
	},
}

// A persisted reason names its own way out, because the reason is what `hand status` and `hand
// reconcile` show an operator who has nothing else to go on.
func repairReasonWithTreatment(taskID, code, reason string) string {
	treatment, found := stuckStateTreatments[code]
	if !found {
		return reason + "; no supported treatment is recorded for this diagnosis, which is a defect in Hand rather than a state you can repair"
	}
	return reason + "; " + strings.ReplaceAll(treatment.Treatment, "<id>", taskID)
}
