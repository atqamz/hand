# Edge is one mutable tag published only by CI

- Date: 2026-08-13
- Status: accepted
- Issues: atqamz/hand#210
- PRs: none

## Context

Maintainers and contributors want to exercise the packaged application at the newest `main` commit, but stable releases follow Release Please and its cadence.

Stable download URLs, Nix, and `go install` semantics are already relied upon and cannot change to accommodate a second channel.

A development channel is only worth installing if every build it offers passed the same gate as a release.

## Decision

Exactly one non-SemVer `edge` tag and one mutable GitHub prerelease name the newest `main` commit that passed the complete CI gate.
Their asset set matches the stable one, so a channel changes which tag is downloaded and nothing else.

Every build embeds version, channel, and commit through `main.version`, `main.channel`, and `main.commit`.
The [`Makefile`](../../Makefile) default is `dev`, while [`.github/workflows/release.yaml`](../../.github/workflows/release.yaml), [`flake.nix`](../../flake.nix), and [`.github/workflows/edge.yaml`](../../.github/workflows/edge.yaml) set `stable` or `edge`.

Freshness is compared per channel: stable by release SemVer, edge by the embedded commit against the commit the `edge` ref resolves to.
[`internal/selfupdate/target.go`](../../internal/selfupdate/target.go) and [its tests](../../internal/selfupdate/target_test.go) own that comparison, and [`cmd/update.go`](../../cmd/update.go) owns channel selection and the flag surface.

Publication happens only from the push-to-`main` CI job that follows the lint, test, E2E, and Nix gates.
That job serializes in a non-canceling concurrency group, rechecks candidate ancestry before building and again before publishing, refuses to publish when the candidate and `edge` histories diverge, and uploads assets and publishes the release before moving the tag.
Candidate assets are uploaded under unique staging names and verified before replacing the exact download names, so an upload failure leaves the previous edge asset set intact.

## Rejected alternatives

- Per-merge prerelease versions multiply tags and releases, impose SemVer ordering on commits that were never released, and give up the one fixed download URL per asset.
- Comparing edge builds by SemVer cannot work, because an edge version is a commit prefix and carries no order.
- A Go `@edge` distribution path needs a resolvable module version and would hand out builds that never passed the gate that gives edge its only guarantee.
- Moving the tag before staging and verifying assets lets a reader resolve `edge` to a commit whose complete release is not downloadable yet.
- Force-resetting the tag on divergence would silently discard published edge history, so the workflow fails and prints the explicit recovery push instead.
- Publishing from a maintainer machine removes the gate and is the reason no local path exists.

## Consequences

The `edge` tag and release are mutable, so anything naming them tracks a moving commit and rollback is a deliberate force push.

A new released asset must be added to the stable and edge build matrices together, or the two channels stop offering the same set.

New embedded build metadata must be set by the Makefile, `flake.nix`, and both workflows, or a build reports itself as `dev` and stops checking for updates.

Cached update state is scoped to the channel that wrote it, so switching channels cannot answer from the other channel's remembered result.
