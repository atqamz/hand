# Tests state invariants first, examples second

- Date: 2026-08-28
- Status: accepted
- Issues: atqamz/hand#442
- PRs: none

## Context

Every architectural boundary in this repository is already written down as a rule: at most one
provisioning or running attempt per task, reconciliation converges, an observation failure is not
contradiction evidence, unknown lease ownership is not a mismatch, init resets nothing it did not
create. The ADRs state them precisely and each names the code that owns it.

The suite does not test those rules. It tests instances of them. `cmd/init_reconciler_test.go`
asserts that three consecutive `hand init` runs converge; the invariant is that init converges from
any starting state. Both are worth having, and only one of them fails when a change breaks
convergence on a state nobody wrote a case for.

That asymmetry has a second half. A test written by reading the implementation asserts what the code
currently does. It passes before and after a defect is introduced into behavior the example does not
reach, and it keeps passing when the behavior it does reach is wrong, because the implementation was
the oracle. Nothing in the suite currently measures whether a given test is capable of failing at
all, so "the tests pass" carries less information than it appears to.

## Decision

Tests are derived from stated invariants, in this order, and the order is the point.

**1. The invariant map is a reviewed artifact, authored from the ADRs.** It lives at
[`docs/testing-invariants.md`](../testing-invariants.md), each entry carrying a stable id, a
falsifiable statement, the source that owns it, and the layer meant to check it. An entry is derived
from an ADR or from the code that ADR names - never from a test, and never from the implementation's
current behavior, because a map derived from the implementation cannot disagree with it.

A statement earns a line only if an input could break it. If none could, it is a description.

**2. Property tests state the invariant; unit tests sit underneath them.** The property is the
assertion, over generated inputs. Unit tests remain for the specific cases worth naming - a
regression with a name, a boundary a reader should see spelled out - and they are added beside a
property rather than instead of one.

Lifecycle rules are stated as *models*: generated sequences of operations checked against a
reference model after every step. INV-TASK-1 is not expressible as a property over one input,
because the thing that violates it is an ordering.

**3. Mutation testing judges the tests, not the code.** A surviving mutant is a statement that the
suite does not constrain the behavior that mutant changed. It runs per package and on demand,
documented in CONTRIBUTING.md; whether it becomes a gate is decided after its cost here is measured,
not before.

**4. `pgregory.net/rapid` is an accepted test-only dependency.** It brings shrinking that reports a
minimal failing case instead of a random one, and `rapid.StateMachine`, which is what makes layer 2's
models declarative rather than a hand-rolled decoder over `[]byte`. Native `go test -fuzz` stays the
tool wherever a property needs no state machine, since it costs nothing.

**5. The map's `coverage` column is answered by audit, not by assertion.** Every invariant starts
`unaudited`. A phase that adds tests updates only the lines it touched. Writing `covered` on a line
whose test was not read is the same error as deriving the invariant from the implementation.

**6. A test names what it is for, and one that cannot is deleted.** Every test either cites the
invariant id it checks or pins a specific case worth naming - a regression, a boundary a reader
should see spelled out. A test that can name neither is not kept as harmless ballast: it costs
runtime on every run, and it reads as coverage of something.

This cuts in a direction a suite-growing rule does not. A mutant that survives means the tests do not
constrain the behavior it changed; the fix is sometimes a stronger test, and sometimes deleting the
test that appeared to cover it and did not. Removing such a test is progress, and it makes the suite
say something truer than it did before, so it is not treated as a regression in test count.

## Rejected alternatives

- **Raising coverage instead.** Line coverage measures which lines ran, not which behaviors are
  constrained. A test that executes a function and asserts nothing about it counts fully. It is the
  metric that made the current gap invisible.
- **Property tests without a written map.** The properties would then be derived by whoever writes
  them, from the code in front of them, which reintroduces the implementation as the oracle at the
  layer meant to remove it. The map is reviewable precisely because it is separate.
- **Mutation testing first, as the entry point.** It reports on tests that exist. Run against a suite
  of implementation-derived examples it produces a long list of survivors and no guidance about which
  ones matter, because there is no stated invariant to say what should have failed.
- **Native fuzzing only, no new dependency.** Workable, and it stays in use for stateless properties.
  For the lifecycle models it means decoding operation sequences out of the fuzzer's byte slice by
  hand, so the model's logic would sit inside a decoder, and a failure would report a byte slice
  rather than a minimal sequence of operations.
- **A generated map, derived from the ADRs mechanically.** The ADRs state rules in prose whose
  testable form requires judgment - which quantifier, over which inputs, falsifiable how. A
  generated map would be a restatement, and its errors would be invisible for the same reason.
- **Rewriting the existing suite around properties.** The existing tests pass for good reasons and
  encode real cases. Properties are added beside them.
- **Keeping every existing test on the grounds that deleting one loses coverage.** A test that
  constrains nothing is not coverage; it is runtime plus a false signal. The rule above makes its
  removal a normal outcome of the audit rather than something needing separate justification.

## Consequences

An invariant now has one place to be written, one id to be referenced by, and one answer about
whether anything checks it. A change that breaks a documented boundary fails a test that names the
boundary, rather than an example that happened to cover it.

The map also makes absence legible: an invariant with no coverage is a visible line rather than an
unasked question, which is what atqamz/hand#442's phases work through.

Mutation testing introduces a cost that has to stay bounded deliberately. It re-runs the tests once
per mutant, against a suite that already needs the `test` build tag and minutes of `-race` time, so
it is scoped per package from the start and any CI gate waits on measurement.

The map can be wrong, and that is a real risk this record accepts: an incorrect invariant becomes a
test that pins the code to a mistake, which is worse than no test because it looks like a
specification. That is why the map is reviewed as claims about intent, separately from the tests
derived from it, and why its `What is deliberately not an invariant` section is part of the artifact
rather than a footnote.
