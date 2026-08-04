# The status file wins over the database, and there is no `hand dump`

- Date: 2026-08-04
- Status: accepted
- Issues: none
- PRs: none

## Context

A fleet home holds machine state authoritative in sqlite at `state/hand.db`, and a prose corpus authoritative in files under `data/`.
`state/<id>.status` sits across that line: the worker writes it as prose, and `hand` reads it as the outcome signal.

So there are two places that can answer "what did this worker say": the file, and the `last_report_state` / `last_report_note` the watcher persisted from it.
They can disagree, and the design has to say which wins before the disagreement happens rather than after.

The fleet has twice run a stale `hand` binary while every signal that binary produced read healthy.
Both recoveries were `cat` on the status files.

## Decision

When the database and a `.status` file disagree about what a worker said, the file wins.

The database is authoritative for everything the file does not carry: what `hand` recorded, decided or observed, which is most of machine state.
The file is authoritative for what the worker said.
The database never holds a second copy of the file's content as a substitute for it; `last_report_state` is a projection the watcher carries forward, not a rival record.

There is deliberately no `hand dump` and no other command whose purpose is to print machine state for recovery.

## Rejected alternatives

**Make the database authoritative for reports too, since it is authoritative for everything else.**
The choice is not about which store is more reliable, it is about what the two failure modes cost.
A `.status` file is readable by `cat`, `tail -f`, an editor, and a person with no tooling at all.
The database is readable by a working `hand`, which is the thing that was broken both times it mattered.

**Add `hand dump` so recovery has a first-class path.**
A dump command is one more thing that depends on the binary, so it is no help in the case that actually happens.
It would also read as the recommended recovery route, which would move operators off the one route that survives the failure.

**Store reports in the database and render the file from it as a convenience.**
Then the file is a rendering, and nothing durable in `hand` is derived from a rendering (atqamz/secondhand#53).
It also inverts the write direction: the worker would need `hand` to report, which is the coupling this avoids.

## Consequences

`hand status` re-reads the file for its report suffix and its `unacknowledged` flag rather than answering from the row alone, and both views derive that flag from the whole file rather than the 5-line history window, so the two views cannot disagree.

The report channel has to stay a plain append-only text file forever.
Compressing it, rotating it, or moving it into the database each break the recovery this depends on.

sqlite in rollback journal mode with one short-lived process per command is part of the same commitment: a fleet home stays a directory that can be copied, backed up and inspected with ordinary tools.
A daemon or a connection pool would make the home a thing you have to ask a running process about.
