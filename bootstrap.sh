#!/bin/sh
set -eu

HAND_RELEASE_TAG='@HAND_RELEASE_TAG@'
HAND_RELEASE_VERSION='@HAND_RELEASE_VERSION@'
HAND_RELEASE_COMMIT='@HAND_RELEASE_COMMIT@'
HAND_RELEASE_RUNTIME_ID='@HAND_RELEASE_RUNTIME_ID@'
HAND_RELEASE_CHECKSUMS_ASSET='checksums.txt'
HAND_RELEASE_MANIFEST_ASSET='release-manifest.json'
HAND_RELEASE_ASSET_LINUX_AMD64='hand-linux-amd64.tar.gz'
HAND_RELEASE_ASSET_LINUX_ARM64='hand-linux-arm64.tar.gz'
HAND_RELEASE_ASSET_DARWIN_AMD64='hand-darwin-amd64.tar.gz'
HAND_RELEASE_ASSET_DARWIN_ARM64='hand-darwin-arm64.tar.gz'

fleet="${HOME}/secondhand-fleet"
check_only=0

log() { printf '%s\n' "$*" >&2; }
die() { log "bootstrap.sh: $*"; exit 1; }

release_placeholder_prefix=$(printf '@HAND%s' '_RELEASE_')
case "$HAND_RELEASE_TAG" in
  "$release_placeholder_prefix"*) die "this source template is not a release-bound bootstrap asset" ;;
esac
case "$HAND_RELEASE_VERSION" in
  "$release_placeholder_prefix"*) die "this source template is not a release-bound bootstrap asset" ;;
esac
case "$HAND_RELEASE_COMMIT" in
  "$release_placeholder_prefix"*) die "this source template is not a release-bound bootstrap asset" ;;
esac
case "$HAND_RELEASE_RUNTIME_ID" in
  "$release_placeholder_prefix"*) die "this source template is not a release-bound bootstrap asset" ;;
esac

usage() {
  cat <<'EOF'
Usage: bootstrap.sh [--fleet PATH] [--yes] [--check] [--help]

  --fleet PATH  fleet home to create or reconcile (default: $HOME/secondhand-fleet)
  --yes         accepted for compatibility; the canonical release command is already explicit
  --check       read-only: report readiness, install or mutate nothing
  --help        show this message
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --fleet)
      [ $# -ge 2 ] || die "--fleet requires a path"
      fleet=$2
      shift 2
      ;;
    --fleet=*)
      fleet=${1#--fleet=}
      shift
      ;;
    --yes) shift ;;
    --check) check_only=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown argument: $1 (see --help)" ;;
  esac
done

case "$(uname -s)" in
  Linux) hand_goos=linux ;;
  Darwin) hand_goos=darwin ;;
  *) die "unsupported OS $(uname -s); use bootstrap.ps1 on Windows" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) hand_goarch=amd64 ;;
  arm64|aarch64) hand_goarch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

case "$hand_goos/$hand_goarch" in
  linux/amd64) hand_asset=$HAND_RELEASE_ASSET_LINUX_AMD64 ;;
  linux/arm64) hand_asset=$HAND_RELEASE_ASSET_LINUX_ARM64 ;;
  darwin/amd64) hand_asset=$HAND_RELEASE_ASSET_DARWIN_AMD64 ;;
  darwin/arm64) hand_asset=$HAND_RELEASE_ASSET_DARWIN_ARM64 ;;
  *) die "unsupported platform $hand_goos/$hand_goarch" ;;
esac

hand_command=hand
hand_available=0
if command -v hand >/dev/null 2>&1; then
  hand_available=1
fi

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "sha256sum or shasum is required to verify the hand release"
  fi
}

download() {
  destination=$1
  url=$2
  if ! curl -fsSL --retry 2 --connect-timeout 15 --max-time 600 -o "$destination" "$url"; then
    die "download failed: $url"
  fi
  [ -s "$destination" ] || die "download was empty: $url"
}

ensure_hand() {
  [ "$check_only" -eq 0 ] || {
    log "hand: not installed (check mode: no changes made)"
    return 0
  }

  hand_tmp=$(mktemp -d) || die "could not create a temporary directory for hand"
  cleanup_hand() { rm -rf "$hand_tmp"; }
  trap cleanup_hand EXIT HUP INT TERM

  hand_base="https://github.com/atqamz/hand/releases/download/${HAND_RELEASE_TAG}"
  download "$hand_tmp/$hand_asset" "$hand_base/$hand_asset"
  download "$hand_tmp/$HAND_RELEASE_CHECKSUMS_ASSET" "$hand_base/$HAND_RELEASE_CHECKSUMS_ASSET"
  download "$hand_tmp/$HAND_RELEASE_MANIFEST_ASSET" "$hand_base/$HAND_RELEASE_MANIFEST_ASSET"

  hand_want=$(awk -v asset="$hand_asset" '$2 == asset || $2 == "*" asset {print $1; exit}' "$hand_tmp/$HAND_RELEASE_CHECKSUMS_ASSET")
  [ -n "$hand_want" ] || die "$HAND_RELEASE_CHECKSUMS_ASSET has no entry for $hand_asset"
  [ "${#hand_want}" -eq 64 ] || die "invalid checksum for $hand_asset"
  case "$hand_want" in
    *[!0-9a-fA-F]*) die "invalid checksum for $hand_asset" ;;
  esac
  hand_got=$(sha256_file "$hand_tmp/$hand_asset")
  [ "$hand_got" = "$hand_want" ] || die "checksum mismatch for $hand_asset: want $hand_want, got $hand_got"
  manifest_want=$(awk -v asset="$HAND_RELEASE_MANIFEST_ASSET" '$2 == asset || $2 == "*" asset {print $1; exit}' "$hand_tmp/$HAND_RELEASE_CHECKSUMS_ASSET")
  [ "${#manifest_want}" -eq 64 ] || die "invalid checksum for $HAND_RELEASE_MANIFEST_ASSET"
  manifest_got=$(sha256_file "$hand_tmp/$HAND_RELEASE_MANIFEST_ASSET")
  [ "$manifest_got" = "$manifest_want" ] || die "checksum mismatch for $HAND_RELEASE_MANIFEST_ASSET: want $manifest_want, got $manifest_got"
  manifest_commit=$(sed -n 's/.*"commit"[[:space:]]*:[[:space:]]*"\([0-9a-fA-F]*\)".*/\1/p' "$hand_tmp/$HAND_RELEASE_MANIFEST_ASSET" | head -n1)
  [ "$manifest_commit" = "$HAND_RELEASE_COMMIT" ] || die "release manifest commit does not match $HAND_RELEASE_COMMIT"

  case "$hand_asset" in
    *.tar.gz)
      tar -xzf "$hand_tmp/$hand_asset" -C "$hand_tmp" hand || die "could not extract the verified hand release"
      hand_source=$hand_tmp/hand
      ;;
    *) die "unsupported release archive $hand_asset" ;;
  esac
  [ -f "$hand_source" ] || die "verified release archive does not contain hand"

  hand_bin_dir=${HAND_INSTALL_DIR:-$HOME/.local/bin}
  if ! mkdir -p "$hand_bin_dir" || ! install -m 755 "$hand_source" "$hand_bin_dir/hand"; then
    die "could not install hand to $hand_bin_dir; resolve permissions without sudo and rerun bootstrap.sh"
  fi
  case ":$PATH:" in
    *":$hand_bin_dir:"*) ;;
    *) PATH="$hand_bin_dir:$PATH"; export PATH ;;
  esac
  command -v hand >/dev/null 2>&1 || die "hand was installed but is not on PATH; add $hand_bin_dir to PATH and rerun bootstrap.sh"
  hand_available=1
  trap - EXIT HUP INT TERM
  rm -rf "$hand_tmp"
}

if [ "$hand_available" -eq 0 ]; then
  ensure_hand
fi

ensure_private_runtime() {
  if [ "$check_only" -eq 1 ]; then
    log "private runtime status (check mode: no changes made):"
    "$hand_command" runtime status || true
    return 0
  fi

  runtime_status=$("$hand_command" runtime status 2>/dev/null || true)
  case "$runtime_status" in
    *"ready: true"*) return 0 ;;
  esac
  log "ensuring private pinned Git, Treehouse, and Herdr runtime for $HAND_RELEASE_VERSION ($HAND_RELEASE_RUNTIME_ID)"
  "$hand_command" runtime ensure || die "private runtime is not ready; repair with: hand runtime ensure"
}

ensure_private_runtime

fleet_state() {
  if [ ! -e "$fleet" ]; then
    printf 'absent\n'
    return 0
  fi
  [ -d "$fleet" ] || die "$fleet exists and is not a directory"
  if [ -f "$fleet/state/hand.db" ] && [ -d "$fleet/state" ]; then
    printf 'fleet\n'
  elif [ -f "$fleet/data/projects.md" ] && [ -d "$fleet/data" ] && [ -d "$fleet/state" ]; then
    printf 'fleet\n'
  elif [ -z "$(ls -A "$fleet" 2>/dev/null)" ]; then
    printf 'empty\n'
  else
    printf 'foreign\n'
  fi
}

state=$(fleet_state)
if [ "$state" = "foreign" ]; then
  die "$fleet exists, is not empty, and is not a recognized Secondhand fleet; refusing to adopt it - pass --fleet with an empty or already-initialized path"
fi

if [ "$check_only" -eq 1 ]; then
  log ""
  log "fleet target: $fleet ($state)"
  if [ "$state" != "fleet" ]; then
    log "hand init has not run here; check mode makes no changes, so readiness cannot be evaluated further"
    exit 0
  fi
  if [ "$hand_available" -eq 0 ]; then
    log "hand is not installed; readiness cannot be evaluated further"
    exit 0
  fi
  doctor_out=$(HAND_HOME="$fleet" "$hand_command" doctor 2>&1) || true
  log ""
  log "$doctor_out"
  exit 0
fi

[ "$state" = "absent" ] && mkdir -p "$fleet"

if ! init_out=$("$hand_command" init "$fleet" 2>&1); then
  log "$init_out"
  die "hand init failed against $fleet; recover by resolving the reported error, then rerun: bootstrap.sh --fleet $fleet"
fi
log "$init_out"

doctor_out=$(HAND_HOME="$fleet" "$hand_command" doctor 2>&1) || true
log ""
log "$doctor_out"

doctor_field() {
  printf '%s\n' "$2" | sed -n "s/^$1: //p" | head -n1
}

doctor_list() {
  printf '%s\n' "$2" | sed -n "/^$1\\[/,/^[^ ]/ { s/^  - //p; }"
}

installed_harnesses() {
  printf '%s\n' "$1" | awk '
    /^harnesses\[/ { in_block=1; next }
    in_block && /^  [a-z]/ {
      if ($2 == "true") print $1
      next
    }
    in_block { in_block=0 }
  '
}

ready=$(doctor_field ready "$doctor_out")
if [ "$ready" != "true" ]; then
  blocking=$(doctor_list blocking "$doctor_out")
  log ""
  log "Secondhand is not ready yet. Blocking:"
  for item in $blocking; do
    log "  - $item"
  done
  die "recover the items above, then rerun: HAND_HOME=$fleet hand doctor"
fi

harnesses=$(installed_harnesses "$doctor_out")
count=$(printf '%s\n' "$harnesses" | grep -c . || true)

log ""
log "Secondhand is ready."
log ""
log "Next:"
log ""
log "  cd $fleet"
if [ "$count" -eq 1 ]; then
  log "  $harnesses"
elif [ "$count" -gt 1 ]; then
  log "  <choose one of the installed harnesses below>"
  for h in $harnesses; do
    log "    $h"
  done
else
  log "  <install and authenticate at least one supported coding-agent harness, then run hand doctor>"
fi
