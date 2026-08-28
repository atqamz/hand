#!/usr/bin/env bash
# Verifies-or-publishes one exact package@version, per the Phase 2/3 state machine in
# atqamz/hand#283: npm-registry-check.sh classifies the target first, and only an
# absent name or absent version ever reaches a `npm publish` call. Any other outcome
# (integrity/ownership mismatch, an ambiguous registry answer) already made
# npm-registry-check.sh exit nonzero with its own reason on stderr, which aborts this
# script right at the `outcome=` assignment below under `set -e` - nothing here needs to
# special-case those outcomes again.
set -euo pipefail

usage() {
  printf '%s\n' "usage: $0 <package> <version> <tarball> <expected-integrity> <expected-repo-url>" >&2
}

if [[ $# -ne 5 ]]; then
  usage
  exit 2
fi
pkg=$1
version=$2
tarball=$3
integrity=$4
repo_url=$5

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
outcome="$("$script_dir/npm-registry-check.sh" "$pkg" "$version" "$integrity" "$repo_url")"

case "$outcome" in
  verified-published)
    echo "npm-publish-target: ${pkg}@${version} is already verified published; skipping"
    exit 0
    ;;
  absent-new-package)
    if [[ -z "${NPM_BOOTSTRAP_TOKEN:-}" ]]; then
      echo "npm-publish-target: ${pkg} has never been published and NPM_BOOTSTRAP_TOKEN is not set; refusing" >&2
      exit 1
    fi
    npmrc_dir="$(mktemp -d)"
    printf '//registry.npmjs.org/:_authToken=${NODE_AUTH_TOKEN}\n' > "$npmrc_dir/.npmrc"
    (
      export NODE_AUTH_TOKEN="$NPM_BOOTSTRAP_TOKEN"
      export NPM_CONFIG_USERCONFIG="$npmrc_dir/.npmrc"
      identity="$(npm whoami 2>&1)" || {
        echo "npm-publish-target: bootstrap token identity check (npm whoami) failed: $identity" >&2
        exit 1
      }
      echo "npm-publish-target: publishing ${pkg}@${version} as npm user $identity (bootstrap token, first creation)"
      npm publish "$tarball" --access public --provenance
    )
    rm -rf "$npmrc_dir"
    ;;
  absent-new-version)
    # actions/setup-node's registry-url option is what plants an _authToken entry
    # (atqamz/hand#283); this workflow never passes it, so any such entry here would
    # mean something else configured one, and it must not be left to shadow OIDC.
    if npm config list 2>/dev/null | grep -q '_authToken'; then
      echo "npm-publish-target: a classic npm _authToken config is present; refusing to let it shadow OIDC" >&2
      exit 1
    fi
    echo "npm-publish-target: publishing ${pkg}@${version} via npm Trusted Publishing OIDC"
    npm publish "$tarball" --access public
    ;;
  *)
    # Reachable only if npm-registry-check.sh's contract changes underneath this
    # script: every currently defined stop outcome already exits before this case.
    echo "npm-publish-target: unrecognized registry-check outcome: $outcome" >&2
    exit 1
    ;;
esac

verify="$("$script_dir/npm-registry-check.sh" "$pkg" "$version" "$integrity" "$repo_url")"
if [[ "$verify" != "verified-published" ]]; then
  echo "npm-publish-target: ${pkg}@${version} did not verify as published immediately after npm publish succeeded: $verify" >&2
  exit 1
fi
echo "npm-publish-target: ${pkg}@${version} verified published"
