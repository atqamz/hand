# Supervisor turn delivery is host-specific

- Date: 2026-08-23
- Status: accepted
- Issues: atqamz/hand#355
- PRs: none

## Context

`hand watch --until-event` detects, prints, and exits correctly, but a process exit is only a
delivered wake where an owning host converts that exit into another Supervisor reasoning
opportunity. After the outer Supervisor turn has ended, nothing starts another turn and the
operator must nudge the harness by hand. Detection is not delivery; an accepted request is not
reasoning.

There is no universal "wake an LLM" primitive. Each supported host exposes its own mechanism,
and Hand's cross-platform semantic logic stays in Go rather than porting any shell/PID
machinery.

## Decision

Hand owns actionability, currentness, wake episode identity (fleet + target + currentness +
actionable kind, never rendered reason, PIDs, or wall-clock), level catch-up, dedupe/retry, the
bounded wait primitive (`hand supervision wait --host <harness>`), disposable mechanism
progress bookkeeping under `state/runtime/`, and integration installation/qualification. The
Supervisor host owns converting one coalesced Hand wake into exactly one reasoning opportunity,
whose first Hand read is `hand orient`.

Per host:

```text
claude    -> Hand-owned Stop hook in project .claude/settings.json; eligible wake = exit 2 feedback = follow-up turn
codex     -> Fleet-local .codex/hooks.json async Stop hook owns the post-turn wait; it reads the thread from the Stop payload and enqueues codex queue on that exact thread
opencode  -> persistent TUI plugin; synchronous prompt API only; promptAsync/204 is not delivery
pi        -> extension followUp message with turn trigger; session generations retire stale callbacks
grok      -> host-owned background task completion notification re-enters the model
```

`hand orient` marks currently actionable episodes oriented in a disposable ledger so an
unchanged condition cannot storm turns; changed currentness is a new episode and wakes normally.
Wake eligibility reads unbounded actionable evidence - never the 64-item bounded rendered
orientation - so a full display slice cannot starve newer subjects.

Rejected: a detached universal Hand supervisor daemon (still needs per-host integration plus
orphan/lifetime complexity across three platforms); treating notification delivery as reasoning
progress; one generic supervision healthy boolean; canonicalizing runtime/conversation IDs.

## Consequences

Every harness carries an explicit qualified outcome - supported (live-proven only), available-unqualified
(static preconditions hold, live proof pending), degraded, or unsupported, always with the exact reason -
surfaced separately through `hand doctor`, `hand session start`, and `hand orient` diagnostics. A provider
whose host lacks the required primitive is reported as not unattended-supervision capable instead of silently
healthy. Live no-operator-input dogfood per claimed host remains the release gate for the 0.7 support matrix;
until then every host reports available-unqualified at best.
