# npm Trusted Publisher enrollment

Required before the second stable release publishes to npm. This is a one-time,
operator-performed procedure on npmjs.com; nothing in this repository executes it, and it
is out of scope for `atqamz/hand#283` to automate (npm has no API to configure Trusted
Publishing for a package that does not exist yet - see
[npm/cli#8544](https://github.com/npm/cli/issues/8544)).

## Why this cannot wait

`.github/scripts/npm-publish-target.sh` only ever uses `NPM_BOOTSTRAP_TOKEN` to create a
package name's first version (the `absent-new-package` outcome). Every later version of an
already-existing package (`absent-new-version`) publishes through npm Trusted Publishing
(OIDC, via the workflow's `id-token: write` permission) with no token at all. If a
package's first version was published but Trusted Publishing was never configured for it,
the *next* stable release's npm-publish job fails outright at that package - there is no
implicit fallback to the bootstrap token for a second version, by design (a second version
is not "absent," so the bootstrap path is never eligible for it).

Concretely: once `0.7.0` (or whichever release goes first) successfully publishes every
current package name for the first time, this enrollment must happen before `0.7.1` or the
next stable release, or that release's npm-publish job fails.

## Prerequisites

- Every package name this repository currently owns has published at least one version.
  Confirm with `npm view <name> version` for each - a bare version string (not an `E404`)
  means it exists.
- As of this record that is the meta package and one package per target
  `packaging/npm/generate.sh --print-targets` derives from
  `internal/toolchain/runtime.lock.json` at enrollment time - check that command's output
  rather than assuming today's set, since it can grow.
- Signed in to npmjs.com as an account with admin access to the `@atqamz` scope, with 2FA
  available (npm requires 2FA to change publishing security settings).

## Procedure

Repeat for every existing `@atqamz/hand*` package name (the meta package, and each
platform package `--print-targets` currently lists):

1. Open `https://www.npmjs.com/package/<name>/access` (or the package page's "Settings"
   tab).
2. Find the Trusted Publisher / OIDC configuration section and add a new trusted
   publisher with:
   - Provider: GitHub Actions
   - Organization or user: `atqamz`
   - Repository: `hand`
   - Workflow filename: `release.yaml`
   - Environment name: `npm-publish`
   The environment name must match `.github/workflows/release.yaml`'s `npm-publish` job
   exactly - npm scopes the trust relationship to that GitHub Actions OIDC claim.
3. Save. npm's UI wording for this flow has changed before and may again; the fields
   above are the durable requirement regardless of exact button labels.
4. Confirm the package's settings show the trusted publisher as active before moving to
   the next name.

No change to `.github/workflows/release.yaml` or `.github/scripts/npm-publish-target.sh`
is needed to take advantage of this: the `absent-new-version` branch already always
attempts the OIDC path first and only fails if no trusted publisher is configured for that
name yet.

## After enrollment

- The pinned npm CLI (`npm@12.0.2`, see `.github/workflows/release.yaml`) already supports
  OIDC Trusted Publishing; no toolchain change is needed alongside enrollment.
- `NPM_BOOTSTRAP_TOKEN` stays necessary only for a package name that has never been
  published before - for example the first platform package created when a new target
  newly qualifies in `internal/toolchain/runtime.lock.json`. It should not be revoked
  while any future first-time publish might still need it, but it is no longer used for
  any name already enrolled here.
- Repeat this procedure once for each brand-new package name after its first version
  publishes, since Trusted Publishing is configured per package name, not per scope.

See
[`docs/adr/npm-publishes-only-runtime-qualified-targets-behind-one-operator-gate.md`](adr/npm-publishes-only-runtime-qualified-targets-behind-one-operator-gate.md)
for the design this procedure completes.
