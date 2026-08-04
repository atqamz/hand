# Usage-limit detection is a harness capability, not a condition in the poll loop

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#136, atqamz/secondhand#81, atqamz/secondhand#84, atqamz/secondhand#85, atqamz/secondhand#128
- PRs: atqamz/secondhand#154

## Context

A worker whose harness runs out of quota stops mid-task with the reason on screen and nothing else.
To the rest of the poll loop that stop looks like any other stop, so without something specific the task sits dead until a human notices.

Recognizing it means reading a pane's scrollback and matching wording that belongs to one harness.
Only `claude` has wordings anybody has observed against a real limited run; `codex`, `pi`, `grok` and `opencode` do not, and inventing signatures for them would be guessing about text that stops a worker.

The shape this could take was decided by history.
atqamz/secondhand#81, #84 and #85 each grew the poll loop one conditional at a time, and atqamz/secondhand#128 was the bill for it.

## Decision

`internal/harness` owns a per-harness catalogue of usage-limit signatures: which wordings mean out of quota, and how to read a reset instant out of them.
It exposes `SupportsUsageLimit` and `DetectUsageLimit`.

Only `claude` is in the catalogue.
Every other harness declines: one map lookup, no pane read, no steer, and no way for a bare shell pane to be typed into.

Teaching `hand` about a second harness is an entry in that catalogue, not a branch in the watcher.
The bar for adding one is a refusal catalogued against a real limited run, the same bar `firstRunPrompts` holds.

Recognition is anchored on the quota being *reached*, never on the word "limit" alone, so the harness's own approaching-your-limit warning cannot read as a stop.

A reset instant is only ever a prediction.
It decides when to start trying, never whether the limit is over: that is observed from the pane, and the freshest refusal on screen is the one read, since an older one in scrollback names a reset that has already come and gone.

## Rejected alternatives

**Detect the stop in the watcher with a heuristic over any pane's text.**
This is the shape #81, #84 and #85 took.
Each conditional was individually reasonable and the loop became untestable in aggregate, which is what #128 paid for.
It also means every harness gets probed with wording that belongs to one of them, and a bare shell pane gets typed into.

**Catalogue plausible signatures for every harness now.**
A signature nobody has seen a real limited run produce is a guess about text that decides whether to steer a live pane.
Declining costs a stranded worker a human noticing; a wrong match costs a working pane an unwanted steer.

**Match on the word "limit".**
`claude` prints an approaching-your-limit warning that does not stop the turn, so this reads a working worker as limited.

**Wait out the reset instant and declare the quota back.**
The instant is the harness's own prediction and it is routinely wrong in both directions.
An attempt produces either a pane that starts working or a fresh refusal, and the refusal is the observation the next attempt is scheduled from.

**Read the reset from the first match in scrollback.**
Scrollback holds every refusal the harness has ever printed, and the earlier ones name resets the harness has itself superseded.

## Consequences

`SupportsUsageLimit` gates the pane read, so a fleet of non-`claude` workers pays one map lookup per task per tick and nothing else.

The durable state is two task columns, `usage_limit_retry_at` and `usage_limit_attempts`, with the `limit` hold as the operator-visible projection.
The schedule is the authority and the retry path reads the columns, never the hold; see `holds-are-their-own-table.md`.

The failure mode designed against is a retry storm against an account that is still limited, and five bounds hold it: a floor of ten minutes, doubling backoff capped hourly, every wait capped at 24 hours, exactly one attempt per due window against a durable schedule, and `usage-limit-stuck` announced once after six attempts.

An attempt is the same two-call steer `hand send` performs and takes the same `send:<id>` lock, without waiting, so a poll tick never blocks behind an operator's `--wait`.

An unparseable retry stamp resumes *unlimited*, which is the one place the watcher prefers silence to a duplicate, because the duplicate here is a steer into a live pane rather than a line on stdout.
