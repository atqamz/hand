# Curating Fleet knowledge

Run a deliberate curation pass on operator request, periodically during knowledge
maintenance, or when reasoning identifies substantial low-value duplication.
Do not attach prose rewrites to `hand session start`, `hand orient`, status,
watcher ingestion, WorkerInput drain, or wake handling merely because a file is
large. Work only within the operator's normal file authority.

## Keep prose separate from canonical truth

Treat `data/operator.md` and `data/learnings.md` as curated context. Inspect
canonical facts through the available structured Hand read surfaces; never
reconstruct them from prose or invent a command when a surface is unavailable.
Leave missing or unobservable evidence unknown and report the limitation.

Prose does not own Fleet, Project, Task, Plan, Attempt, Decision, Answer,
Attention, Source, Observation, ActionIntent or typed outcome-postcondition;
WorkspaceBinding, WorktreeBinding, SessionBinding or ExecutorBinding;
WorkerInput, WorkerInputAcknowledgement, WorkerWake, Interrupt, WorkerReport or
WorkerReportAcknowledgement; TerminalReceipt, candidate revision, Qualification,
IntegrationReceipt or integrated revision; Production, Artifact, Publication,
external_operation, scope claim, Hold, Backoff or Repair.

Deleting or compressing a note changes none of those facts. It cannot acknowledge
input/report, answer a Decision, complete/satisfy a Plan or Task, retry, replan,
repair, archive, integrate, publish, wake, interrupt, settle an external operation,
or release resources. Never edit the DB, registry, evidence, receipts, resource
state or Hand-owned projections as part of curation.

Use exact v19 names: semantic post-launch input is WorkerInput; provider re-entry
is a separate WorkerWake mechanism. There is no canonical semantic Send. Do not
collapse these families into generic delivery/result/isolation state. Use
Treehouse or generic IsolationBinding terminology only in an explicitly qualified
v0.7/legacy-v18 compatibility or cutover lesson, never as fresh-v19 authority.

## Curate with recovery and evidence

1. Read the complete target files. Confirm their versioned owner; before a large
   destructive rewrite without Git or another explicit versioned owner, create
   a local plain-file backup of those prose files. Never copy/archive `hand.db`
   in this procedure; DB migration/archive belongs to Hand's cutover machinery.
2. Verify each `Status: fixed-by owner/repo#N` entry with normal forge tooling.
   A closed issue makes the note a curation candidate only. If verification is
   unavailable, retain the uncertainty instead of assuming closure.
3. Choose deliberately: remove a one-off incident with no remaining value,
   compress/generalize a reusable lesson, or keep relevant historical/operator
   rationale. Issue closure proves no canonical work or resource is settled.
4. Cluster duplicate lessons by their actual conditions. Merge toward the
   strongest concrete evidence; preserve conflicting observations and materially
   different versions, platforms, exceptions or failure causes separately.
5. Rewrite narrative as what is true, why/evidence, and how to apply it. Keep
   exact commands, errors, file:line citations, evidentiary URLs/issues, source
   revisions, platform qualifiers and counterexamples needed to falsify the rule.
6. Preserve standing preferences, constraints and their rationale conservatively
   in `operator.md`. In `learnings.md`, rewrite/prune on contact and prefer reusable
   lessons over append-only incident diaries. Apply different retention postures.
7. Review the proposed deletions/merges for lost evidence or exceptions, then
   apply the authorized prose changes. Re-read the resulting files and verify
   important retained commands/errors/citations before removing any temporary
   backup. Keep the backup if validation is incomplete.
8. Confirm the diff is limited to authorized prose and its recovery copy. Do not
   infer a canonical transition from the edit or run mutation commands to make
   workflow state agree with the new prose.

If a file exceeds one Supervisor context, delegate a complete read and proposed
edit/diff to a bounded Worker under the normal delegation rules. Require the
proposal to name deleted/merged clusters and retained evidence. Review it in
Supervisor context and apply only with normal file authority; analysis grants
the Worker no authority to edit user prose or canonical state.

## Review examples

| Input | Curation decision |
| --- | --- |
| A verified closed fixed-by issue records only a transient outage. | Remove the obsolete incident if no useful exception or rationale remains; do not settle any Task or Repair. |
| A closed issue explains why a lost response must be reconciled before retry. | Retain a concise reusable rule with its command, error and issue evidence. |
| An operator preference records why a particular repository must remain local. | Preserve the preference and rationale even if the motivating incident is closed. |
| Two retry notes share conditions, but only one has an exact revision and error. | Merge around the stronger evidence; do not upgrade the unverified account into a fact. |
| Similar startup failures describe a trust decision and a slow devshell. | Keep distinct causes/treatments and version qualifiers; never grant trust from prose. |
| A long story contains a reproducible command, error and file:line witness. | Shorten the story while retaining those exact witnesses and applicability limits. |
| A large non-versioned file is rewritten, or a Worker proposes deletions. | Establish a prose backup first; require Supervisor review, re-read the result, and retain recovery until validation succeeds. |

In every example, canonical identity, lifecycle, resource, input, acknowledgement,
report, Source/Action, receipt, Qualification and Integration state remain untouched.
