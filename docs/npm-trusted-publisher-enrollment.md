# npm Trusted Publisher enrollment

Two prerequisites gate this npm surface: the operator-approval gate below, required before
the *first* release can publish anything, and Trusted Publisher enrollment, required before
the *second* stable release publishes to npm. The latter is a one-time, operator-performed
procedure on npmjs.com; nothing in this repository executes it, and it is out of scope for
`atqamz/hand#283` to automate (npm has no API to configure Trusted Publishing for a package
that does not exist yet - see [npm/cli#8544](https://github.com/npm/cli/issues/8544)).

## The operator-approval gate (required before the first release)

`.github/workflows/release.yaml`'s `npm-publish` job runs under `environment: npm-publish`.
Referencing an environment name that does not yet exist as a repository environment does
not fail and does not wait: GitHub auto-creates it with no protection rules at all, and the
job runs immediately, unattended. The entire operator-approval design described in
[the ADR](adr/npm-publishes-only-runtime-qualified-targets-behind-one-operator-gate.md)
depends on this environment actually existing with a reviewer configured before that ever
happens - an auto-created environment is not a gate, it just has the same name as one.

As of 2026-08-29, both of the following are true and verified on `atqamz/hand`:

- The `npm-publish` environment exists, with `atqamz` as a required reviewer and a
  protected-branches deployment policy (`main` is a protected branch, so this is
  satisfiable). Verify with:

  ```sh
  gh api repos/atqamz/hand/environments/npm-publish
  ```

  A response with a `required_reviewers` protection rule naming the operator confirms the
  gate is real. An empty `protection_rules` array means the environment was auto-created
  and is not gating anything.
- `NPM_BOOTSTRAP_TOKEN` is a secret scoped to the `npm-publish` environment, not a bare
  repository secret - a repository secret is readable by every workflow job in the repo,
  which would let an ungated job read it regardless of the environment's own protection.
  The repository-level secret of the same name has been deleted. Verify with:

  ```sh
  gh secret list --repo atqamz/hand                    # must not list NPM_BOOTSTRAP_TOKEN
  gh secret list --repo atqamz/hand --env npm-publish  # must list it here instead
  ```

If a fresh clone, fork, or repository recreation ever needs this redone: create the
`npm-publish` environment under repository Settings -> Environments, add the intended
approver as a required reviewer, restrict deployment branches to protected branches, then
add `NPM_BOOTSTRAP_TOKEN` as a secret scoped to that environment - never as a repository
secret.

## Why Trusted Publisher enrollment cannot wait

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
- `NPM_BOOTSTRAP_TOKEN` is needed only for a package name that has never been published
  before - for example the first platform package created when a new target newly
  qualifies in `internal/toolchain/runtime.lock.json`. It is never used for a name
  already enrolled here.
- Repeat this procedure once for each brand-new package name after its first version
  publishes, since Trusted Publishing is configured per package name, not per scope.

## The bootstrap token no longer exists

As of 2026-08-30 the operator revoked the token on npmjs.com and it was deleted from the
`npm-publish` environment; `gh secret list --repo atqamz/hand --env npm-publish` returns
nothing. Every package name this repository owns - `@atqamz/hand`, `@atqamz/hand-linux-x64`,
`@atqamz/hand-win32-x64` - published through v0.7.2 and is enrolled above, so releases of
existing names need no token at all.

A release that has to create a *new* package name therefore fails, by design and visibly:
`.github/scripts/npm-publish-target.sh` refuses before touching the registry with
`<name> has never been published and NPM_BOOTSTRAP_TOKEN is not set; refusing`. Nothing is
left half-published.

That happens the first time a currently unqualified target qualifies. As of this record
`internal/toolchain/runtime.lock.json` marks `darwin/amd64`, `darwin/arm64`, and
`linux/arm64` unsupported, and `packaging/npm/generate.sh` derives the published set from
that file, so any of them qualifying introduces a name that has never been published.

When that happens: create a fresh Granular Access Token on npmjs.com scoped to the
`@atqamz` scope with publish permission, add it as `NPM_BOOTSTRAP_TOKEN` scoped to the
`npm-publish` environment (never as a repository secret), let the release publish the new
name, enroll that name with the procedure above, then revoke the token again.

See
[`docs/adr/npm-publishes-only-runtime-qualified-targets-behind-one-operator-gate.md`](adr/npm-publishes-only-runtime-qualified-targets-behind-one-operator-gate.md)
for the design this procedure completes.
