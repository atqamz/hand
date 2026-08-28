# npm publishes only runtime-qualified targets behind one operator gate

- Date: 2026-08-29
- Status: accepted
- Issues: atqamz/hand#283
- PRs: none

## Context

atqamz/hand#283 asks for stable releases to publish to npm automatically, without a human ever running `npm publish`.

atqamz/hand#354 (closed) established runtime qualification: [`internal/toolchain/runtime.lock.json`](../../internal/toolchain/runtime.lock.json) records, per `GOOS/GOARCH`, whether Hand's private Git+Treehouse+Herdr runtime is available, marking an unqualified target `"unsupported": "<reason>"`. [`tests/release/prepare_test.go`](../../tests/release/prepare_test.go)'s `TestReleaseTargetsMatchRuntimeLockSupport` already guards the GitHub release build matrix against this same file. That matrix (five targets) and the runtime-qualified subset are not the same set: `linux/arm64`, `darwin/amd64`, and `darwin/arm64` build and ship on GitHub today despite being runtime-unqualified. `#283`'s invariant that published equals qualified means npm cannot simply mirror the GitHub matrix.

npm has no mechanism to pre-configure Trusted Publishing (OIDC) for a package name before that name has published at least one version ([npm/cli#8544](https://github.com/npm/cli/issues/8544), open). A first-ever publish of any name needs some other credential.

`#283` names a staged human-approval publish flow as a non-goal, reasoning that the issue calls for automatic rather than manual per-release publication. Separately, the operator asked for exactly that kind of checkpoint: every mechanical step (packing, auth, `npm publish`, verification) runs unattended, but a human approves before anything reaches the registry. These are different claims - "no human ever types `npm publish`" versus "no human decision point at all" - and only the first is what `#283` rules out.

## Decision

[`packaging/npm/generate.sh`](../../packaging/npm/generate.sh) derives the published target set at generation time by reading `runtime.lock.json` directly and keeping every target with no `unsupported` entry. It never writes that set down anywhere else: [`tests/release/npm_generate_test.go`](../../tests/release/npm_generate_test.go)'s `TestNpmGeneratePrintTargetsMatchesTheRuntimeLock` independently re-derives the same set from the lock file and compares, and [`tests/release/npm_workflow_test.go`](../../tests/release/npm_workflow_test.go)'s `TestNpmPublishStepNeverHardcodesATargetList` fails if a future edit writes a platform key into the workflow itself. As of this record that yields two platforms, `linux/amd64` and `windows/amd64`; a target qualifying or regressing changes npm's output with no edit to `generate.sh` or `release.yaml`.

[`.github/workflows/release.yaml`](../../.github/workflows/release.yaml)'s `npm-publish` job runs under `environment: npm-publish`, a GitHub Environment with the operator configured as a required reviewer. The job queues for approval before it starts, every release, not only the first - that pause is the human decision point `#283`'s automation goal does not eliminate. Everything after approval is unattended.

Two authentication paths cover the two states a package name can be in: a bootstrap Granular Access Token (`NPM_BOOTSTRAP_TOKEN`, scoped to the `npm-publish` environment, never logged or written to a file) creates a name's first version; npm Trusted Publishing (OIDC, via the job's `id-token: write` permission) covers every version after that, with no long-lived token involved. [`.github/scripts/npm-registry-check.sh`](../../.github/scripts/npm-registry-check.sh) classifies each `package@version` against the live registry first; [`.github/scripts/npm-publish-target.sh`](../../.github/scripts/npm-publish-target.sh) only calls `npm publish` for an absent name or an absent version, re-verifying immediately after, and refuses closed on any other outcome (integrity mismatch, ownership mismatch, an ambiguous registry answer).

Platform packages publish before the meta package, in the order `generate.sh` derives, because the meta package's `optionalDependencies` pin platform-package versions that must already exist for `npm install` to resolve. The GitHub release's `publish` job now depends on `npm-publish`, so a release cannot go non-draft while npm publication for that release is still pending approval or has failed.

The npm launcher's unsupported-platform message names the specific platform - with a dedicated message for macOS, since GitHub still builds a `darwin` binary an npm user cannot otherwise find - and the exact reinstall command, rather than a generic "no matching package" error. That branch is not theoretical: a macOS user running `npm install -g @atqamz/hand` today will hit it.

## Rejected alternatives

- Publishing npm's package set from the same five-target GitHub build matrix would ship packages users cannot run, since `darwin/amd64`, `darwin/arm64`, and `linux/arm64` have no qualified Hand runtime yet - the same invariant `TestReleaseTargetsMatchRuntimeLockSupport` already enforces on the GitHub side.
- A second, npm-owned target list - even one disciplined to stay "in sync" with `runtime.lock.json` by hand - was rejected because a second list is a second place to update and a silent way for the two to disagree. Deriving directly from the one lock file at generation time removes the second list rather than disciplining its maintenance.
- Fully ungated automatic publication (no environment, no reviewer) matches a literal reading of `#283`'s "automatic" framing, but the operator asked for an approval checkpoint explicitly. The two are reconciled by automating every mechanical step while reserving the publish decision itself for a human, every release.
- Waiting for npm Trusted Publishing to support pre-registration ([npm/cli#8544](https://github.com/npm/cli/issues/8544)) before publishing at all was rejected in favor of a bootstrap token for first creation, since that upstream issue is unresolved and the first stable npm release should not block on it. See [`docs/npm-trusted-publisher-enrollment.md`](../npm-trusted-publisher-enrollment.md) for the one-time enrollment that retires the bootstrap token to first-creation-only use.
- Extending [`internal/faketool`](../../internal/faketool) with a shared npm fake was rejected: that package exists for tools reused broadly across Hand's own runtime (`gh`, `herdr`, `treehouse`). npm's usage is scoped to this one release pipeline, so [`tests/npmpublish`](../../tests/npmpublish) owns a small dedicated fake instead, mirroring `tests/edgepublish`'s precedent for `gh`.

## Consequences

A future runtime-qualification change - a target regressing or a new platform qualifying - changes npm's published set automatically on the next release; no npm-specific file needs an edit.

Every release, including the first, pauses for operator approval before any `npm publish` call happens. A fully unattended release pipeline is not possible for the npm surface, by design; this is the intended behavior, not a gap to close later.

`NPM_BOOTSTRAP_TOKEN` must live as a secret scoped to the `npm-publish` environment, not a bare repository secret, so that only a job already past the approval gate can ever see it.

The second stable release fails at its npm-publish job for any package name that published a first version but was never enrolled in Trusted Publishing - see `docs/npm-trusted-publisher-enrollment.md`. That enrollment is required operator follow-up after the first release, not optional cleanup.
