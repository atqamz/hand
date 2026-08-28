# A test build cannot resolve the real Secondhand infrastructure root

- Date: 2026-08-28
- Status: accepted
- Issue: atqamz/hand#413
- Pull requests: none

## Context

`internal/secondhand.Home()` is the one place every component that reads or writes under
`~/.secondhand` - the registry, the private runtime, the worktree pools - resolves that root from.
Before this change it always consulted `SECONDHAND_HOME` first and fell back to the operator's real
`$HOME/.secondhand` when the variable was unset. `make test` set `SECONDHAND_HOME` to a fresh
`mktemp -d` for the whole suite, and several packages' own `TestMain` set it again more narrowly, so
isolation held as long as every test entry point remembered to do so.

It did not always. `hand`'s own test suite left 3366 dead rows in the operator's real
`~/.secondhand/registry.db` - `/tmp/.../TestXxx.../002` homes from runs that, by the time they were
found, could no longer be attributed to a specific invocation. The mechanism that made this possible
was not a missing override in any one test; it was that `Home()` had a code path that succeeded by
falling back to the real root, and success there is silent. A convention that every test must
remember to set one environment variable, with no enforcement if it doesn't, is the shape of bug this
issue exists to close, not a fix for it.

## Decision

`Home()` still reads `SECONDHAND_HOME` first. When it is unset, `Home()` checks
`internal/testtag.Present` - the existing compile-time constant that is `true` only in a binary built
with the `test` tag - and if it is true, `Home()` returns `ErrHomeNotOverridden` instead of falling
back to `os.UserHomeDir()`. The fallback branch that resolves the real root is unreachable code in any
test binary's control flow once `SECONDHAND_HOME` is unset; it is not merely unexercised by
convention. Every package that reaches `Home()` in a `-tags=test` build - directly or through
`registry.Register`, `worktree.PoolsRoot`, or anything else layered on it - now fails loudly instead
of succeeding quietly against the operator's real infrastructure.

`cmd/init.go`'s `registerFleet` already surfaces a `registry.Open`/`registry.Register` error as a
failed `hand init`, so this refusal reaches the command layer as a real, non-zero-exit failure rather
than a value some caller discards.

`internal/secondhand`'s own test package additionally runs under `testtag.Main`, so `go test` on it
without `-tags=test` refuses immediately rather than silently exercising a different contract - the
same convention `internal/git`, `internal/worktree`, and other packages that guard "tests must not do
X" already use.

## Rejected alternatives

- Trusting `make test`'s `SECONDHAND_HOME=$(mktemp -d)` alone: it isolates a `go test ./...` run
  launched exactly that way, but does nothing for a single package run directly, an IDE test runner,
  or any future test entry point that does not go through the Makefile - exactly the gap the 3366 rows
  came from.
- A per-package `TestMain` convention (what `cmd`, `internal/registry`, and `tests/e2e` already do):
  real and necessary for setting an isolated value, but it cannot make a *missing* one fail, because a
  package with no `TestMain` at all falls straight through to the same silent default.
  This decision does not replace those `TestMain`s - they still supply the actual isolated path - it
  closes the gap they leave when a package has none.
- A new dedicated build-tag pair mirroring `internal/testtag`'s own `present_prod.go` /
  `present_testmode.go` split, applied to `secondhand.Home()` itself: it would work, but it would
  duplicate the exact enforcement `internal/testtag` already provides and had already been reused
  successfully as `testtag.Main` in several packages' `TestMain`s.

## Consequences

A test build can still choose *where* isolation points - the override is not fixed - but it can no
longer choose *not to* isolate by omission. The 3-line production fallback (`SECONDHAND_HOME` unset,
`testtag.Present` false, resolve `$HOME/.secondhand`) is exercised only by a real `hand` binary and
`go build`/`go vet`, never by `go test -tags=test`, mirroring how `internal/testtag`'s own
`present_prod.go` is not separately unit-tested either; both are trivial enough that compiling
successfully under the untagged build is the check.

Any future code that resolves `~/.secondhand` by a path other than `secondhand.Home()` sits outside
this guarantee. There is one such path today: `hand init` refuses to register a home inside Hand's own
Treehouse worktree pool (`worktree.PoolsRoot()`, itself derived from `secondhand.Home()`) or inside
another Fleet's `projects/` tree, checked structurally against the filesystem rather than against the
registry - consistent with
[The landed-work guard reads the work, not the record](the-landed-work-guard-reads-the-work-not-the-record.md)
- so that a stale or absent registry can never make an unsafe target look safe.
