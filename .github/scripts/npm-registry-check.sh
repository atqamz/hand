#!/usr/bin/env bash
# Classifies one exact package@version against the public npm registry, with no
# publish credentials, per docs/adr/npm-publishes-only-runtime-qualified-targets-behind-one-operator-gate.md.
# Never treats an npm error string as sufficient evidence: every branch below reads
# `npm view --json`'s structured error shape (observed against the real registry, not
# assumed from documentation) rather than grepping human-readable text.
#
# Prints exactly one outcome word to stdout and exits 0 only for the three outcomes
# safe to act on automatically; every other outcome is a name on stderr and a nonzero
# exit, so a caller that does not explicitly branch on the outcome still stops instead
# of silently continuing:
#
#   absent-new-package   the package name has never published any version:
#                        candidate for the bootstrap-token publish path.
#   absent-new-version   the name exists, this exact version does not:
#                        candidate for the token-free OIDC publish path.
#   verified-published   this exact version exists and its registry dist.integrity
#                        and repository.url both match what was packed locally:
#                        already done, safe to skip.
#   integrity-mismatch   this exact version exists but its registry dist.integrity
#                        differs from the local npm-pack integrity: stop.
#   unexpected-ownership a version at this name (existing or the exact one queried)
#                        does not carry the expected repository.url: stop.
#   ambiguous            the registry answered something other than the shapes
#                        above (unexpected HTTP/error code, unparseable JSON,
#                        network failure): stop.
set -euo pipefail

usage() {
  printf '%s\n' "usage: $0 <package> <version> <expected-integrity> <expected-repo-url>" >&2
}

if [[ $# -ne 4 ]]; then
  usage
  exit 2
fi
pkg=$1
version=$2
want_integrity=$3
want_repo=$4

stop() {
  printf '%s\n' "$1"
  printf 'npm-registry-check: %s\n' "$2" >&2
  exit 1
}

query() {
  # Never touches stderr: the human-readable duplicate of the same error npm also
  # writes to stdout as --json would otherwise be logged as if it were a failure of
  # this script rather than the classified outcome the caller asked for.
  npm view "$1" "${@:2}" --json 2>/dev/null
}

version_doc=$(mktemp)
trap 'rm -f "$version_doc"' EXIT
if query "${pkg}@${version}" name version dist.integrity repository.url > "$version_doc"; then
  got_integrity=$(jq -r '."dist.integrity" // empty' "$version_doc")
  got_repo=$(jq -r '."repository.url" // empty' "$version_doc")
  if [[ "$got_integrity" != "$want_integrity" ]]; then
    stop integrity-mismatch "${pkg}@${version} registry dist.integrity ($got_integrity) != local pack integrity ($want_integrity)"
  fi
  if [[ "$got_repo" != "$want_repo" ]]; then
    stop unexpected-ownership "${pkg}@${version} registry repository.url ($got_repo) != expected ($want_repo)"
  fi
  printf 'verified-published\n'
  exit 0
fi

code=$(jq -r '.error.code // empty' "$version_doc" 2>/dev/null || printf '')
summary=$(jq -r '.error.summary // empty' "$version_doc" 2>/dev/null || printf '')
if [[ "$code" != "E404" ]]; then
  stop ambiguous "registry query for ${pkg}@${version} answered code=${code:-<none>} summary=${summary:-<none>}"
fi

case "$summary" in
  "No match found for version"*)
    # The name resolved, so npm looked for the version and failed on that alone -
    # confirm this existing name is still ours before treating it as safe to extend.
    name_doc=$(mktemp)
    trap 'rm -f "$version_doc" "$name_doc"' EXIT
    # A single field argument makes npm view print a bare scalar instead of an object
    # (observed against the real registry), so a second field is requested purely to
    # keep the dotted-key object shape the parsing below expects.
    if ! query "$pkg" name repository.url > "$name_doc"; then
      stop ambiguous "registry query for existing package ${pkg} failed after its version was reported absent"
    fi
    existing_repo=$(jq -r '."repository.url" // empty' "$name_doc")
    if [[ "$existing_repo" != "$want_repo" ]]; then
      stop unexpected-ownership "${pkg} already exists with repository.url ($existing_repo) != expected ($want_repo)"
    fi
    printf 'absent-new-version\n'
    exit 0
    ;;
  *)
    printf 'absent-new-package\n'
    exit 0
    ;;
esac
