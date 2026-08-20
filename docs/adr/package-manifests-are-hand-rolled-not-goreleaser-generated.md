# Package manifests are hand-rolled, not GoReleaser-generated

- Date: 2026-08-20
- Status: accepted
- Issues: atqamz/hand#177
- PRs: none

## Context

atqamz/hand#177 asks whether GoReleaser should replace or absorb parts of the release pipeline while
Hand adds Homebrew, WinGet, npm, AUR, `.deb`, and `.rpm` distribution surfaces.

The existing pipeline is not a generic "build and tag" job. [`release-please-config.json`](../../release-please-config.json)
and [`.github/workflows/release.yaml`](../../.github/workflows/release.yaml) own version, release PR, and tag
lifecycle. [`.github/workflows/edge.yaml`](../../.github/workflows/edge.yaml) and
[`.github/scripts/edge-publish.sh`](../../.github/scripts/edge-publish.sh) publish a rolling `edge` prerelease
under invariants [`edge-is-one-mutable-tag-published-only-by-ci.md`](edge-is-one-mutable-tag-published-only-by-ci.md)
records: candidate ancestry checks before and after the build, refusal on diverged history, staged uploads under
unique names before the exact download names are replaced, and recovery from exactly one interrupted candidate's
backup group. [`tests/edgepublish`](../../tests/edgepublish) pins that sequence against the shared `gh` fake.

GoReleaser's release model is one version, one tag, one release per invocation. It has no first-class concept of
a mutable rolling prerelease with ancestry-checked promotion, staged-then-renamed asset replacement, or
interrupted-run recovery from a specific backup group. Reproducing those invariants through GoReleaser hooks
would mean writing the same safety-critical logic a second time, behind an abstraction this repository does not
otherwise need, only to reach parity with what already works and is tested.

Homebrew formulae, WinGet manifests, npm packages, an AUR `PKGBUILD`, and `.deb`/`.rpm` packages are each a small,
well-documented text or archive format. Every one of them can be produced from the tagged release, without a
templating engine standing between the source archive and the package manifest. See
`packaging/README.md` for the per-surface table of which ones read the prebuilt archive directly and which
build from tagged source instead, and why.

## Decision

The release pipeline keeps its current shape: release-please owns version/PR/tag lifecycle, and
`.github/workflows/release.yaml` plus `edge.yaml`/`edge-publish.sh` keep owning stable and edge publication
exactly as documented in `edge-is-one-mutable-tag-published-only-by-ci.md`. GoReleaser is not adopted.

Each new package surface is a small, independent generator that produces that surface's manifest or archive: a
Homebrew formula, a WinGet manifest, npm package files, an AUR `PKGBUILD`, and `.deb`/`.rpm` archives. Each reads
an already-built, checksummed release, but four of them (Homebrew, npm, `.deb`, `.rpm`) build from the tagged
source with their own `-X main.distribution=<surface>` rather than repackaging the prebuilt
`hand-*.tar.gz`/`.zip` asset, because that shared asset always embeds `distribution:github` and reusing it
directly would make `hand update` wrongly try to self-replace a package-manager-owned binary. WinGet and AUR are
the two that do consume the prebuilt asset directly, since WinGet has no build step and an AUR `-bin` package is
prebuilt by convention. See `packaging/README.md` for the full per-surface table. None of them touches version,
tag, or edge-publication logic.

## Rejected alternatives

- Migrating wholesale to GoReleaser for uniformity would force reimplementing edge's ancestry, no-backwards-
  movement, and partial-publication recovery behind a tool that has no first-class model for a rolling mutable
  prerelease, trading a tested implementation for an untested one for the sake of using one tool everywhere.
- Adopting GoReleaser only for the stable channel and hand-rolling edge would leave two different build
  definitions for the one asset set both channels must keep identical, exactly the drift
  `edge-is-one-mutable-tag-published-only-by-ci.md` warns a shared matrix prevents.
- Adopting `nfpm` (the library GoReleaser itself uses for `.deb`/`.rpm`) as a standalone dependency was considered
  and rejected for the same reason hand-rolling `.deb`/`.rpm` was chosen instead: a single static binary's
  package is simple enough that the tool adds a dependency without adding capability.

## Consequences

A future package surface is added as its own small generator consuming the existing release artifacts, not as a
GoReleaser configuration block.

A new release invariant (a new platform archive, a new checksum entry) is still added to `release.yaml` and
`edge.yaml` together, as `edge-is-one-mutable-tag-published-only-by-ci.md` already requires; no separate
GoReleaser config exists to fall out of sync with them.
