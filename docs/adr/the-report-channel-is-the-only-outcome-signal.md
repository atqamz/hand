# The report file owns worker outcome

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#53, atqamz/secondhand#87
- PRs: none single

## Context

Herdr owns whether a pane is busy, but its idle and done labels do not explain why a headless worker stopped. A worker must also be able to report when the `hand` binary or database is the thing being repaired.

## Decision

`state/<id>.status` is the sole source of what a worker says happened. It is a plain worker-written, hand-read file with a fixed small vocabulary. Herdr state is independent liveness evidence, and completion claims are checked against artifacts before the tool treats them as verified.

When a stored projection and the file disagree about the worker's report, the file wins. There is no database-backed replacement or recovery dump. [`internal/state/report.go`](../../internal/state/report.go), status rendering, and watcher tests own parsing and acknowledgement behavior; [`internal/agentsmd`](../../internal/agentsmd) owns the worker guidance.

## Rejected alternatives

- Inferring outcome from herdr confuses attention state with task completion.
- Requiring a reporting subcommand makes the recovery channel depend on the binary and database it may be reporting about.
- Free-form model classification turns terminal state into a probability.
- Trusting a worker's done claim alone lets mistaken completion become fleet truth.

## Consequences

Every reader must tolerate partial writes and incorrect append behavior without inventing a report. The channel is removed with the task so a reused id starts clean, while plain text remains available to ordinary recovery tools.
