# A file-only harness gets the launch statement appended to its brief

- Date: 2026-08-28
- Status: accepted
- Issues: atqamz/hand#418, atqamz/hand#579 (case D)
- PRs: none

## Context

`briefPrompt` carries the report path and the operator-decision rule to a worker as CLI prompt
text. grok and pi take no prompt argument at all: `buildGrok`/`buildPi` handed the brief over as a
bare file path, so neither string ever reached them. `promptCapable` listed only claude, codex,
opencode and antigravity, and `CarriesPrompt`'s doc comment cited atqamz/hand#36 - closed, and about
herdr's agent-detection manifests, not prompt carriage - so the gap had no tracking issue and hand
kept launching a worker that structurally could not report.

Neither CLI exposes a real prompt flag to verify: they were not installed in the environment this
fix was written in, and the standing rule is that no unverified flag reaches `promptCapable`.

## Decision

For a harness with no prompt argument, hand appends the same report-path sentence and
operator-decision rule text every prompt-capable harness gets - via `launchStatement`, the one
function both paths call - to the brief file itself, at provision time, before the launch command
runs. The block is wrapped in a `---`-delimited appendix with an explicit marker sentence naming it
as hand's text, not the supervisor's brief, echoing the tone the front-matter disclaimer already
uses for the brief's own leading block. Exact appendix equality makes the append idempotent, so a
reopen or resume with the same launch statement does not grow a second copy or rewrite the file.

`CarriesPrompt` changes meaning from "takes a CLI prompt argument" to "receives the report path and
operator-decision rule by some channel". grok and pi now return `true`: `internal/harness/harness.go`
owns both delivery mechanisms, and no caller outside it needs to know which one a given harness uses.

`harness.Build` stays a pure function from `Options` to a `LaunchSpec`: the append lives in the
exported `harness.AppendPromptToBrief`, called once by `internal/runtime/provision.go` - the
provisioning path, which already owns `briefPath` - immediately before `Build`. `Build` alone is also
called from reconcile's `reconciliationActionConfirmLaunch` arm to reconstruct already-persisted
launch evidence for pane-text comparison; that arm observes and must not write, so it must never
reach the append, and it does not, because the append is not inside `Build`.

## Launch currentness correction (2026-09-21)

Marker presence alone did not prove the report channel or worker authority still matched a reused
brief. Preparation for every supported harness recognizes the existing appendix boundary and compares its entire
suffix with the statement generated from the current launch options. A stale, edited, or duplicated
appendix refuses before the worker is built or launched, leaving every brief byte intact. Rewriting
the supervisor-authored brief without the old appendix lets provisioning deliver the current statement
through the selected harness's normal channel. A marker mentioned only in ordinary prose does not
suppress the generated appendix.

Boundary recognition includes LF, CRLF, and mixed line endings at their original byte offsets.
Converting a brief's line endings must not hide an earlier appendix behind a later valid LF block.
Equality still compares the original suffix byte-for-byte: edited line endings refuse even when the
launch options match. Preparation never normalizes or rewrites operator text to make it pass.

Switching from grok or pi to an argument-based harness does not bypass this validation. Only grok
and pi append a missing block; every other supported harness leaves the brief untouched. An absent
brief retains the argument-based path's existing no-append behavior, not a readiness claim. Other
inspection failures refuse instead of being treated as absence. Exact appendix equality remains a
read-only no-op even when the selected harness changes.

The provisioning regression exercises inherited grok/pi instructions followed by a Claude launch,
using the existing runtime fixture and real harness builder. Stale instructions refuse before Build
or provider launch. Successful cleanup clears only returned worktree evidence; failed cleanup keeps
that exact ownership evidence visible. An explicit brief rewrite can resume the same provisioning
Attempt with the current report channel and worker authority. These fixture-backed assertions do not
prove a real harness obeys the instructions.

This intentionally refuses rather than replacing an arbitrary suffix: text added after the generated
block may belong to the operator. Existing provisioning failure cleanup remains responsible for any
already-acquired worktree. This is a narrow case-D safety guard, not proof of provider delivery,
WorkerReport ingestion, or canonical v19 Fleet/Attempt identity; those obligations remain separate.

## Rejected alternatives

- Guessing a `--prompt`-shaped flag for grok or pi without running `--help` against the real CLI
  repeats the exact failure this issue is about, one layer down.
- Refusing to dispatch to grok or pi at `internal/runtime/dispatch.go` was the documented fallback
  and remains available if the brief turns out to be read before hand finishes writing it, or a CLI
  mishandles trailing text; neither was found to be true.
- Leaving `CarriesPrompt` false for grok and pi while appending anyway would keep emitting a
  "cannot carry the operator-decision rule; launching anyway" warning and blocking mechanical-class
  routing to them, both wrong once the content actually reaches the worker.
- Calling the append from inside `buildGrok`/`buildPi` was the first shape this took. It reads
  clean, but `harness.Build` is not only called at provision time: reconcile's confirm-launch arm
  calls it to reconstruct persisted launch evidence, which turns an observation into a write and a
  build that can fail on a missing brief file into a reconcile failure. The append belongs to
  provisioning, which is a fact about the caller, not about the harness, so it moved to the caller.

## Consequences

Every harness hand dispatches to - all six - now carries the report path and the operator-decision
rule in identical wording, so a grok or pi worker is no longer structurally mute. Adding a seventh
harness with neither a prompt flag nor a wired append path leaves it out of `promptCapable`, which
still produces the existing "cannot carry" warning and mechanical-class refusal rather than a silent
launch.
