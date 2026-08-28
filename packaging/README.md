# Packaging

Package-manager surfaces for atqamz/hand#177 Phase 3. Every generator reads an already
CI-verified, checksummed release (`gh release ...`); none of them build the release
pipeline itself, which stays owned by release-please and `.github/workflows/`
(see `docs/adr/package-manifests-are-hand-rolled-not-goreleaser-generated.md`).

A distribution's binary must embed the right `-X main.distribution=...` value, or `hand
update` cannot tell that distribution apart from a direct GitHub download (see
atqamz/hand#177 Phase 2). Homebrew, npm, `.deb`, and `.rpm` build from the tagged source
with their own `-X main.distribution` for exactly that reason, rather than repackaging
the prebuilt `hand-*.tar.gz`/`.zip` release assets, which always carry `distribution:
github`.

| Surface  | Generator                     | Builds from       | Status |
|----------|--------------------------------|--------------------|--------|
| Homebrew | `homebrew/generate.sh <tag>`  | source (`go build`) | live: `brew install atqamz/tap/hand` (atqamz/homebrew-tap) |
| npm      | `npm/generate.sh --version X.Y.Z --commit <sha>` | source (cross-compiled per runtime-qualified target) | ready; not yet published - the next stable release publishes it automatically, behind operator approval |
| `.deb`   | `deb/build.sh <tag> <arch>`   | source (cross-compiled) | ready; not attached to releases yet |
| `.rpm`   | `rpm/build.sh <tag> <arch>`   | source (cross-compiled) | ready; not attached to releases yet |
| WinGet   | `winget/generate.sh <tag>`    | prebuilt release asset | ready; not submitted to winget-pkgs yet |
| AUR      | `aur/generate.sh <tag>`       | prebuilt release asset (that's what `-bin` means) | ready; not pushed to AUR yet |

npm only publishes the subset of targets `internal/toolchain/runtime.lock.json` marks
runtime-qualified (today linux/amd64 and windows/amd64), derived at generation time
rather than tracked as a second list, and its `npm-publish` release job pauses for
operator approval every release rather than publishing unattended (see
`docs/adr/npm-publishes-only-runtime-qualified-targets-behind-one-operator-gate.md`).
The operator must configure the `npm-publish` GitHub Environment (required reviewer,
`NPM_BOOTSTRAP_TOKEN` scoped to it) before the first release can publish, and must
complete `docs/npm-trusted-publisher-enrollment.md` after that first release or the
second one fails at the npm step.

WinGet and AUR intentionally stay on the prebuilt asset: WinGet has no build step, and an
AUR `-bin` package is defined as installing a prebuilt binary rather than compiling one
(a source-building AUR package would be named plain `hand`, not `hand-bin`). Their
binaries will read `distribution: github` until the release pipeline gains a dedicated
build variant for each - do that before either surface is actually published, not after.
