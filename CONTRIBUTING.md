# Contributing

## Getting started

git clone https://github.com/atqamz/hand
cd hand
nix develop
make build
make test

`nix develop` is optional and provides the full toolchain: Go, golangci-lint, gopls, gotools, gcc, and jq.
gcc is required because `make test` runs with `-race`, which needs CGO, and jq because `tests/edgepublish` runs the edge publish script through the `gh` fake, which shells out to `jq`.
Without Nix, install those yourself.

To dogfood the tool from its own checkout, enter `nix develop`, run `make build` whenever `./hand` is absent or stale, and launch any supported harness from the checkout root.
In a main session, when `HAND_ROLE` is not `worker`, bootstrap before responding or acting: if `state/hand.db` is absent, run `./hand init` first, then run `./hand session start` after initialization and at the start of every later supervising session.

Every directory `hand init` creates at the checkout root is gitignored, so the fleet home lives alongside the source without ever being committed.
The tracked `AGENTS.md` is already the exact canonical content `hand init` writes, so initializing a clean checkout leaves it unchanged; `internal/agentsmd`'s `generatedBody` is the authoritative source, and its tests own refresh and `hand doctor` behavior.

## Repository conventions

- Behavioral contracts belong beside their implementation, command help, and focused tests. `docs/adr/README.md` owns the narrow bar for durable architectural rationale.
- `docs/testing-invariants.md` is the map of the rules the suite holds hand to, each with a stable id. A new test cites the invariant id it checks, or names the specific case it pins; a test that can name neither is deleted rather than kept. `docs/adr/tests-state-invariants-first-examples-second.md` owns why.
- Command output goes through `internal/axi` as TOON and every failure through `cmd/root.go`'s error document; `hand watch`'s event stream is the exception. Package and command tests own these shapes.
- Harness/herdr syntax, exit enforcement, watch's stdout/errOut split, and first-run prompt handling are owned by their implementations and closest tests under `internal/harness`, `internal/herdr`, `internal/watcher`, and `cmd`.
- `herdr`, `treehouse` and `gh` are faked once in `internal/faketool` for every suite. `internal/faketool/FIDELITY.md` records observed external behavior, `tests/contract` (`make contract`) rechecks it hermetically, and `make contract-live` separately probes reversible calls against installed tools. Extend the shared fake, never hand-write another.
- Test, release, and write conventions live as doc comments: `tests/e2e` (`fakes_test.go`, `e2e_test.go`), GitHub access via `gh` (`internal/ghutil`), AGENTS.md refresh (`internal/agentsmd`), atomic writes (`internal/atomicfile`).

## Dogfooding edge builds

An installed edge release tracks the newest `main` commit that passed the complete CI gate.
It is useful for maintainers and contributors who want to exercise the packaged application during normal work.

Opt into the channel with:

```sh
hand update --channel edge
```

After the edge binary is installed, `hand update` continues following edge automatically.
Use `hand update --channel stable` to switch back explicitly.
Edge can contain unreleased behavior and state/schema changes, so switching back to an older stable binary may not be compatible with every migration performed while using edge.

An executable built as `./hand` from the checkout remains the preferred path while actively changing or debugging `hand` itself.
An installed edge release is the preferred path for dogfooding the newest CI-verified `main` during normal work.

## Making changes

1. Open an issue describing the intent, design, or proposal, and get agreement there before writing code. This applies to any contribution, no matter the size. See "Reporting issues" below for what to include.
2. Fork and branch from main.
3. Make changes.
4. make lint && make test. A bare `go test ./...` is not the suite: the `test` build tag is what installs the external-tool fakes, and without it the cmd suite refuses with one line naming the tag rather than failing dozens of tests on absent tools.
5. make e2e if you changed CLI behavior (end-to-end suite, excluded from make test).
6. make contract if you changed how hand calls herdr, treehouse or gh. It checks hermetic shared-tool fixtures against the records in internal/faketool/FIDELITY.md, so CI runs it on every change. Run make contract-live separately to smoke-test reversible calls against installed tools and the live GitHub API.
7. nix build .#default if you changed Go dependencies (CI builds the flake, and a stale vendorHash in flake.nix fails it).
8. Open a PR whose body carries a closing keyword (Closes, Fixes, or Resolves) directly preceding a fully qualified atqamz/hand#N, on its own line. A bare #N links but reads ambiguously outside the repo, and a reference without the keyword links the issue without ever closing it.

Commits use conventional commits: feat:, fix:, chore:, etc.
release-please handles versioning and changelogs from these.

## Comments

The default is no comment.
Add one only for a why the code cannot show: a hidden constraint, a subtle invariant, a workaround for a specific bug.
Restating code, narrating what, banners, and doc comments on the obvious are noise no linter can catch, so they stay a reviewer's call.

Two rules bound the comments that clear that bar, enforced by `make lint` rather than by a reviewer reading a diff:

1. A comment may not open with the identifier it documents.
2. A comment block may not exceed three lines.

Consecutive `//` lines are one block, and neither a bare `//` line inside a run nor a blank line above a doc comment breaks it.
Both are ways of writing six lines of prose in front of one declaration while satisfying a three-line rule, and the blank-line form also drops the first half out of godoc.
Rule 1 applies wherever Go's doc convention does not: unexported declarations, everything in `_test.go`, and comments inside function bodies.
An exported declaration's doc comment is required by convention to open with its name, so it is exempt from rule 1, but not from rule 2.
Exempt from both rules: the package doc comment, directives (`//go:build`, `//go:generate`, `//nolint`, `// #nosec`), and files carrying the generated-code header.

Rule 2 will occasionally be wrong, because a genuinely subtle invariant sometimes needs a fourth line.
That is accepted: a rule that is right most of the time and mechanically enforced binds harder than one that is right always and enforced never.
Behavior a caller depends on belongs with its implementation, command help, and focused tests.
User or contributor guidance belongs in README.md or this file.
`docs/adr/README.md` owns the narrower bar for durable architectural rationale.

`go run ./tools/commentlint .` runs the check alone and prints one `file:line:column` per violation.

## Mutation testing

Mutation testing judges the tests, not the code: it changes one line of production behavior at a
time and checks whether any test fails. A mutant the suite kills means some test constrains that
line; a mutant that lives means nothing does, whatever the coverage report says. See
`docs/adr/tests-state-invariants-first-examples-second.md` for why this exists and
`docs/testing-invariants.md` for the invariants the suite is meant to hold.

It runs per package, on demand, never as part of `make test` or CI. Against 38+ packages whose
`-race` suite already takes minutes, a full-tree run would take hours and re-running it on every
push isn't the point - the point is finding out, package by package, where the suite is weaker than
its coverage number suggests.

### Tool

[`gremlins`](https://github.com/go-gremlins/gremlins) (pinned `v0.6.0`), chosen over the older
`go-mutesting`: `go-mutesting` has no tagged release since 2021 (its latest resolvable version is a
2021-06-10 pseudo-version), while gremlins ships regular tagged releases, understands Go build tags
natively, and exposes `--workers`/`--test-cpu` to bound how much of the machine a run is allowed to
use - necessary on a dev box already running other work.

gremlins is a standalone CLI, never imported by hand's code, so it is not a `go.mod` dependency.
Install it once per machine:

```sh
go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0
```

### Running one package

```sh
gremlins unleash --tags=test ./internal/shellquote
```

or, equivalently:

```sh
make mutation PKG=./internal/shellquote
```

`--tags=test` is required: hand's suite installs its external-tool fakes behind that build tag (see
"Making changes" above), and gremlins otherwise mutates and tests an incomplete build.

Useful flags:

- `--dry-run` finds and counts mutants without running the suite against any of them - it still
  gathers coverage once, but skips the per-mutant test loop, so it is cheap and safe to run on any
  package to size it before committing to a real run.
- `--workers=N` / `--test-cpu=N` bound concurrency. On a machine already doing other work, check
  `uptime` and `nproc` first and pick something conservative; gremlins otherwise defaults to using
  the whole machine.
- `-o results.json` writes a machine-readable report alongside the terminal output.

### How long it takes

It scales with (number of covered mutants) x (one test-run wall-clock for that package). Measured
anchor: `internal/shellquote` (9 lines of source, 2 mutants) completes in under half a second.
`internal/store` and `internal/runtime` are multiple orders of magnitude larger and their suites are
already minutes long under `-race`; gremlins runs each mutant's test pass without `-race`, but a run
across either package is still expected to take minutes, not seconds. See `docs/adr/` for the phase 7
gate-decision record, which carries real measured per-package numbers.

### Reading the output

Each mutant gets one line: the mutant kind, and the file:line:col it was applied to. Terminal states:

- `KILLED` - a test failed with the mutation in place. Good: something constrains this line.
- `LIVED` - every test still passed. A finding: nothing the suite runs would notice this line
  changing. Read the surrounding test(s) and decide whether a test is missing, or whether the line
  genuinely carries no behavior worth constraining.
- `NOT COVERED` - no test executes this line at all. Different from `LIVED`: this is a coverage gap,
  not a suite-strength gap.
- `TIMED OUT` - the mutation likely produced an infinite loop or similar; treated as killed for
  scoring purposes but worth a glance.
- `NOT VIABLE` - the mutated code doesn't compile; not a finding, just discarded.

The summary line reports two percentages: **test efficacy** (killed / (killed + lived) - the number
that matters) and **mutator coverage** ((killed + lived) / total mutants found - how much of the
mutated surface any test reaches at all).

### Package scope

Mutation testing here targets exactly the packages atqamz/hand#442 phases 1 through 6 added
invariant-driven property or model tests to, not the whole tree:

- `internal/shellquote`, `internal/age`, `internal/axi` (phase 1, phase 2)
- `internal/registry` (phase 3)
- `internal/store`, `internal/completion` (phase 4, phase 5)
- `internal/runtime` (phase 6)

These are the packages with a first-class claim that "the tests constrain the behavior" worth
checking. Extending the scope to another package means adding it to this list deliberately, not
running gremlins against the whole tree.

## Reporting issues

Open a GitHub issue with repro steps, OS, arch, and hand --version.
