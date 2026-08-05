# One shared stateful fake per external tool, checked against a recorded transcript

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#40
- PRs: atqamz/secondhand#158

## Context

`hand` drives three external CLIs: `treehouse` for worktrees, `herdr` for panes, and `gh` for GitHub.
None can run in the test suite, so every suite fakes them.

The faking was per test: each test scripted the responses it needed for the calls it expected.
That is the cheapest thing to write and it fails in a specific way.
A fake that answers a state-changing command identically before and after that command cannot test anything about the state change.
`hand teardown` returns a worktree and the next `treehouse get` hands out the same slot under a new lease; a scripted fake returns whatever the test author wrote, so the collision guard's whole reason to exist is invisible to it.

The scripts also drift from the real tools independently.
A version banner on stderr, a JSON payload on stdout, an update-available notice sharing that stderr: each is a detail some scripts happened to get right and others did not, and nothing reconciled them.

## Decision

`internal/faketool` installs **one stateful fake per external CLI**, shared by every suite.
A fake holds the state the real tool holds, so a call that changes something is visible to the next call, and a suite exercises the sequence rather than the individual response.

`internal/faketool/FIDELITY.md` records what the real tool does for the calls the suite actually depends on, so a fake is checked against a transcript rather than against somebody's memory of the tool.
Only the calls `hand` makes are recorded; a behavior no test exercises does not belong there.
Every entry was observed by running the real binary, not read off its documentation, and each record notes what the call leaves behind rather than only what it prints.

`tests/contract`, behind the `contract` build tag and `make contract`, re-runs those calls against the real tools and skips where a binary is absent, so a record gone stale against a newer tool is discoverable by running them.
It covers no call that would change anything an operator owns: a scratch treehouse pool, a scratch herdr workspace, and read-only `gh`.
CI never runs it.

Decorative glyphs in the recorded stderr are omitted from the transcripts, and no matcher may depend on one.

## Rejected alternatives

**Keep a script per test.**
It cannot test a state change, which is most of what `hand` does with these tools.
It also multiplies the drift: three tools times every suite, with no single place a corrected observation lands.

**Record and replay transcripts per test, VCR style.**
The recording is per call sequence, so a test that changes its call order needs a re-record, and a re-record needs the real tools.
It also encodes the state machine implicitly in a tape rather than explicitly in a fake, which is harder to reason about than the tool it is imitating.

**Make `tests/contract` part of `make test` or CI.**
It needs three real binaries and network for `gh`, and none of them is present in CI.
A required suite that skips is a suite nobody notices has stopped running.

**Write `FIDELITY.md` from each tool's documentation.**
The details that break `hand` are the undocumented ones: which stream the banner goes to, whether an id is regenerated on reacquisition, what a version older than the floor omits.
Documentation does not carry them and running the binary does.

**Record every behavior each tool has, for completeness.**
An unexercised record cannot go stale in a way any test would catch, so it is a claim nothing verifies.
The recorded set is the dependency surface, deliberately.

## Consequences

A new call to an external tool means three edits: the fake gains the behavior, `FIDELITY.md` gains the observed record, and `tests/contract` gains the check.
Skipping the second leaves a fake nothing verifies.

A shared fake is shared mutable state across a suite, so tests have to construct their own fleet home rather than assuming a clean tool.

`make contract` is in `CONTRIBUTING`'s checklist for a change to how `hand` calls these tools, and it is the operator's own step rather than CI's.

The fidelity rule generalizes: a fake that cannot represent a state change is not a cheaper test, it is a test of nothing.
