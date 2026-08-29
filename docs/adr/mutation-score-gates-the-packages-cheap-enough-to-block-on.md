# Mutation score gates the packages cheap enough to block on

- Date: 2026-08-29
- Status: accepted
- Issues: atqamz/hand#442
- PRs: none

## Context

Phase 7 of atqamz/hand#442 ran gremlins v0.6.0 against the seven packages phases 1 through 6 added
invariant-driven property or model tests to: `internal/shellquote`, `internal/age`, `internal/axi`,
`internal/registry`, `internal/completion`, `internal/store`, `internal/runtime`. Every package
scored 100% test efficacy except `internal/completion` (86.96%, three LIVED mutants: two real gaps
filed as atqamz/hand#498, one - `migrated++` versus `migrated--` in `MigrateProjectIdentity` - an
equivalent mutant no test can ever kill, since the value it changes is only ever compared against
zero). Real, measured wall-clock, sequentially, on an otherwise-quiet 22-core host:

| package | mutants | wall-clock |
|---|---|---|
| `internal/shellquote` | 2 | 0.46s |
| `internal/age` | 9 | 0.59s |
| `internal/axi` | 6 | 1.3s |
| `internal/completion` | 31 | 13.4s |
| `internal/registry` | 116 | 32.9s |
| `internal/store` | 829 | 5m30s |
| `internal/runtime` | 832 | 10m14s |

Full detail, including the settings that produced these numbers, is in CONTRIBUTING.md's "Mutation
testing" section.

Two things surfaced while measuring this that bear directly on whether a gate is trustworthy, not
only on whether it is affordable:

**gremlins' own timeout can be wrong by an order of magnitude, silently.** It times its per-mutant
timeout from a coverage-gathering pass that never passes `-count=1`. On a host that has already
tested a package's unmodified source - true of any CI runner restoring a `setup-go` cache, which this
repository's own CI already does - that pass is a Go test-cache hit in well under a second, regardless
of how long the suite actually takes. `internal/store`'s first real run this way reported 595 of 622
covered mutants `TIMED OUT`; its true baseline was 2.9s, its cached one 177ms. The fix
(`go clean -testcache` before every invocation, now baked into `make mutation`) is necessary
wherever this runs, including in CI - not only during local, repeated iteration.

**A percentage threshold cannot distinguish a real gap from an equivalent mutant.** `completion`'s
score is not 100% and, as currently written, never can be: one of its three survivors is
behaviorally identical to the code it replaced, for every input, so no test - however strong - could
ever kill it. A gate stated as "efficacy >= N%" either sets N below 100 for every package (weakening
the five packages that currently earn a perfect score for no reason) or blocks `completion` forever
on a mutant that is not a defect. The only correct threshold is per-mutant identity: does a specific
`file:line:col` + operator that was `KILLED` before now come back `LIVED`. That requires diffing
`-o results.json` output against a stored baseline, not comparing one aggregate number.

CI's runners (`ubuntu-latest`, standard GitHub-hosted, 4 vCPU) are far smaller than the 22-core host
these numbers were measured on. Reruning the same packages with `--workers` sized to 4 cores instead
of 6-8 would cost proportionally more for the two large packages - a rough scale-up puts
`internal/store` near 8-9 minutes and `internal/runtime` near 20 minutes on CI hardware, though this
is an extrapolation, not a second measurement on that hardware.

## Decision

The gate is split by measured cost, not applied uniformly across the scoped packages:

**`internal/shellquote`, `internal/age`, `internal/axi`, `internal/completion`, and
`internal/registry` gate every push and PR.** Combined, they measured under 50 seconds - negligible
next to this repository's existing multi-minute `-race` suite, cheap enough that scoping the trigger
to changed paths would save nothing worth the added workflow complexity. The gate compares each
mutant's `KILLED`/`LIVED` outcome against a checked-in baseline keyed by `file:line:col` and operator;
a mutant that flips from `KILLED` to `LIVED` fails the build, a `NOT COVERED` count that grows is
reported but does not fail it (that is a coverage question, which this issue explicitly is not
about), and `completion`'s one known equivalent mutant is recorded in the baseline as an accepted
`LIVED` rather than chased.

**`internal/store` and `internal/runtime` do not gate on every push.** At an estimated 8-20 minutes
apiece on CI-sized hardware, blocking every PR - including ones that touch neither package - on this
cost is not justified by what phase 7 actually found: both packages are already at 100% test
efficacy, so the gate's entire job for them today is detecting a future regression, not fixing a
present gap. That is better served by running them only when a PR's diff touches
`internal/store/**` or `internal/runtime/**`, or on a schedule (e.g. nightly against `main`), non-
blocking, filing or updating a tracking issue on a new survivor rather than failing the workflow that
found it. This keeps the expensive packages' protection real without taxing unrelated work.

Implementing the workflow change itself is left for a follow-up: it needs validating against real
GitHub Actions infrastructure (cache interaction, matrix job cost) that cannot be confirmed from a
local checkout, and this phase's own instructions hold off on pushing or opening a PR pending review
of this decision first.

## Rejected alternatives

- **Gate all seven packages uniformly.** Rejected on measured cost: `store` and `runtime` together
  are two to three orders of magnitude more expensive than the other five, and paying that on every
  push regardless of what changed is not what the numbers justify.
- **An aggregate efficacy percentage threshold (e.g. "fail under 95%").** Rejected because it cannot
  tell a real survivor from an equivalent mutant - see `completion` above - and because it would let
  a package regress from 100% down to the threshold without the gate ever noticing, which a per-
  mutant baseline diff does not allow.
- **No gate at all, fully manual forever.** Rejected now that cost is measured rather than assumed:
  five of the seven packages are cheap enough that not gating them leaves free protection on the
  table for negligible cost.
- **Extend mutation testing to the full 38+-package tree.** Out of scope per atqamz/hand#442 itself;
  the packages tested here are the ones with a first-class claim that "the tests constrain the
  behavior," which is what makes a survivor there a meaningful finding rather than noise.

## Consequences

Five packages get a real, cheap, always-on regression check the moment the workflow lands. The two
expensive packages keep the protection phase 7 already gave them (both at 100% today) without an
ongoing CI tax, at the cost of a regression there surfacing on the next scheduled run or touching PR
rather than immediately - an accepted latency given neither package is currently in a bad state to
begin with.

The baseline file this gate depends on has to be regenerated deliberately whenever a mutant's
identity legitimately changes (a line moves, an operator's mutation changes, `completion`'s equivalent
mutant is refactored away) - an unreviewed regeneration would silently accept a real regression as
the new normal, the same failure mode `docs/adr/tests-state-invariants-first-examples-second.md`
already warns about for the invariant map itself.
