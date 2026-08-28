# A file-only harness gets the launch statement appended to its brief

- Date: 2026-08-28
- Status: accepted
- Issues: atqamz/hand#418
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
uses for the brief's own leading block. The append is idempotent (checked by marker presence) so a
reopen or resume that re-provisions the same brief file does not grow a second copy.

`CarriesPrompt` changes meaning from "takes a CLI prompt argument" to "receives the report path and
operator-decision rule by some channel". grok and pi now return `true`: `internal/harness/harness.go`
owns both delivery mechanisms, and no caller outside it needs to know which one a given harness uses.

## Rejected alternatives

- Guessing a `--prompt`-shaped flag for grok or pi without running `--help` against the real CLI
  repeats the exact failure this issue is about, one layer down.
- Refusing to dispatch to grok or pi at `internal/runtime/dispatch.go` was the documented fallback
  and remains available if the brief turns out to be read before hand finishes writing it, or a CLI
  mishandles trailing text; neither was found to be true.
- Leaving `CarriesPrompt` false for grok and pi while appending anyway would keep emitting a
  "cannot carry the operator-decision rule; launching anyway" warning and blocking mechanical-class
  routing to them, both wrong once the content actually reaches the worker.

## Consequences

Every harness hand dispatches to - all six - now carries the report path and the operator-decision
rule in identical wording, so a grok or pi worker is no longer structurally mute. Adding a seventh
harness with neither a prompt flag nor a wired append path leaves it out of `promptCapable`, which
still produces the existing "cannot carry" warning and mechanical-class refusal rather than a silent
launch.
