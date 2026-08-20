#!/bin/sh
set -eu

# Optional, explicitly opt-in Secondhand adoption for Linux and macOS: acquires hand if missing,
# offers to install missing foundational dependencies (git, treehouse, herdr) with consent,
# reconciles a fleet home with `hand init`, reads readiness from `hand doctor`, and prints the
# exact next command. Never installs a coding-agent harness or no-mistakes; never reimplements
# `hand init` or `hand doctor` validation logic.
#
# Usage: bootstrap.sh [--fleet PATH] [--yes] [--check] [--help]
#   --fleet PATH  fleet home to create or reconcile (default: $HOME/secondhand-fleet)
#   --yes         explicit non-interactive consent to install missing foundational dependencies
#   --check       read-only: report readiness, install or mutate nothing

fleet="${HOME}/secondhand-fleet"
consent_yes=0
check_only=0

log() { printf '%s\n' "$*" >&2; }
die() { log "bootstrap.sh: $*"; exit 1; }

usage() {
  cat <<'EOF'
Usage: bootstrap.sh [--fleet PATH] [--yes] [--check] [--help]

  --fleet PATH  fleet home to create or reconcile (default: $HOME/secondhand-fleet)
  --yes         explicit non-interactive consent to install missing foundational dependencies
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
    --yes) consent_yes=1; shift ;;
    --check) check_only=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown argument: $1 (see --help)" ;;
  esac
done

case "$(uname -s)" in
  Linux|Darwin) ;;
  *) die "unsupported OS $(uname -s); use bootstrap.ps1 on Windows" ;;
esac

interactive=0
if [ "$check_only" -eq 0 ] && [ -t 0 ] && [ -t 1 ]; then
  interactive=1
fi

# shellcheck disable=SC1007
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

# ---- step 1: acquire or verify hand -----------------------------------------------------------

hand_available=0
if command -v hand >/dev/null 2>&1; then
  hand_available=1
fi

# ensure_hand only ever runs when hand is missing. In check mode it reports and returns without
# mutating; otherwise it dies with an actionable message on every path that cannot end with hand
# on PATH, so callers never have to re-check its result.
ensure_hand() {
  if [ "$check_only" -eq 1 ]; then
    log "hand: not installed (check mode: no changes made)"
    return 0
  fi
  if [ "$consent_yes" -eq 0 ] && [ "$interactive" -eq 0 ]; then
    die "hand is not installed, and bootstrap is not running interactively without --yes: refusing to install it"
  fi
  if [ "$consent_yes" -eq 0 ]; then
    printf 'hand is not installed. Install it now via install.sh? [y/N] '
    read -r reply || reply=""
    case "$reply" in
      y|Y|yes|YES) ;;
      *) die "hand install declined; cannot continue" ;;
    esac
  fi
  if [ -f "$script_dir/install.sh" ]; then
    log "installing hand via $script_dir/install.sh"
    if ! sh "$script_dir/install.sh"; then
      die "install.sh failed; recover by resolving the reported error and rerunning bootstrap.sh"
    fi
  else
    log "installing hand from checksum-verified GitHub release"
    hand_tmp=$(mktemp -d) || die "could not create a temporary directory for hand"
    case "$(uname -s)" in
      Linux) hand_goos=linux ;;
      Darwin) hand_goos=darwin ;;
      *) rm -rf "$hand_tmp"; die "unsupported OS for hand fallback" ;;
    esac
    case "$(uname -m)" in
      x86_64|amd64) hand_goarch=amd64 ;;
      arm64|aarch64) hand_goarch=arm64 ;;
      *) rm -rf "$hand_tmp"; die "unsupported architecture for hand fallback" ;;
    esac
    if ! hand_tag=$(curl -fsSL "https://api.github.com/repos/atqamz/hand/releases/latest" |
      sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1) || [ -z "$hand_tag" ]; then
      rm -rf "$hand_tmp"
      die "could not resolve the latest hand release tag"
    fi
    hand_asset="hand-${hand_goos}-${hand_goarch}.tar.gz"
    hand_base="https://github.com/atqamz/hand/releases/download/$hand_tag"
    if ! curl -fsSL -o "$hand_tmp/$hand_asset" "$hand_base/$hand_asset" ||
      ! curl -fsSL -o "$hand_tmp/checksums.txt" "$hand_base/checksums.txt"; then
      rm -rf "$hand_tmp"
      die "could not download the hand release or checksums"
    fi
    hand_want=$(sed -n "s/^\([0-9a-f]*\)[[:space:]]*\*\{0,1\}${hand_asset}\$/\1/p" "$hand_tmp/checksums.txt" | head -n1)
    if [ -z "$hand_want" ]; then
      rm -rf "$hand_tmp"
      die "checksums.txt has no entry for $hand_asset"
    fi
    if command -v sha256sum >/dev/null 2>&1; then
      hand_got=$(sha256sum "$hand_tmp/$hand_asset" | cut -d' ' -f1)
    elif command -v shasum >/dev/null 2>&1; then
      hand_got=$(shasum -a 256 "$hand_tmp/$hand_asset" | cut -d' ' -f1)
    else
      rm -rf "$hand_tmp"
      die "sha256sum or shasum is required to verify hand"
    fi
    if [ "$hand_got" != "$hand_want" ]; then
      rm -rf "$hand_tmp"
      die "checksum mismatch for $hand_asset: want $hand_want, got $hand_got"
    fi
    if ! tar xzf "$hand_tmp/$hand_asset" -C "$hand_tmp" hand; then
      rm -rf "$hand_tmp"
      die "could not extract the verified hand release"
    fi
    hand_bin_dir=${HAND_INSTALL_DIR:-$HOME/.local/bin}
    if ! mkdir -p "$hand_bin_dir" || ! install -m 755 "$hand_tmp/hand" "$hand_bin_dir/hand"; then
      rm -rf "$hand_tmp"
      die "could not install the verified hand release"
    fi
    rm -rf "$hand_tmp"
  fi
  case ":$PATH:" in
    *":${HAND_INSTALL_DIR:-$HOME/.local/bin}:"*) ;;
    *) PATH="${HAND_INSTALL_DIR:-$HOME/.local/bin}:$PATH" ;;
  esac
  command -v hand >/dev/null 2>&1 || die "hand was installed but is still not on PATH; add ${HAND_INSTALL_DIR:-$HOME/.local/bin} to PATH and rerun bootstrap.sh"
}

if [ "$hand_available" -eq 0 ]; then
  ensure_hand
fi

# ---- step 2: detect/install missing foundational dependencies --------------------------------

pkg_manager() {
  for mgr in apt-get dnf yum pacman apk zypper brew; do
    if command -v "$mgr" >/dev/null 2>&1; then
      printf '%s\n' "$mgr"
      return 0
    fi
  done
  return 1
}

# install_git_cmd echoes the exact command bootstrap would run so consent is informed before it
# is ever asked for; empty output means no supported package manager was found on this platform.
install_git_cmd() {
  case "$(pkg_manager 2>/dev/null || true)" in
    apt-get) printf 'sudo apt-get update && sudo apt-get install -y git' ;;
    dnf) printf 'sudo dnf install -y git' ;;
    yum) printf 'sudo yum install -y git' ;;
    pacman) printf 'sudo pacman -Sy --noconfirm git' ;;
    apk) printf 'sudo apk add git' ;;
    zypper) printf 'sudo zypper install -y git' ;;
    brew) printf 'brew install git' ;;
  esac
}

# dep_source is the source column the consent prompt shows for a missing foundational dependency.
dep_source() {
  case "$1" in
    git) printf 'your platform package manager' ;;
    treehouse) printf 'kunchenguid/treehouse' ;;
    herdr) printf 'ogulcancelik/herdr' ;;
  esac
}

# resolve_dep_action sets dep_action_kind ("cmd", run directly, or "url", fetched and verified
# before it is ever run) and dep_action_value for dep. A url is never piped straight into a
# shell: `curl ... | sh` treats a failed fetch as an empty, successful script, so run_dep_action
# downloads to a file first and only runs it once the download itself is confirmed to have
# succeeded.
resolve_dep_action() {
  case "$1" in
    git)
      dep_action_kind=cmd
      dep_action_value=$(install_git_cmd)
      ;;
    treehouse)
      dep_action_kind=url
      dep_action_value='https://kunchenguid.github.io/treehouse/install.sh'
      ;;
    herdr)
      if command -v brew >/dev/null 2>&1; then
        dep_action_kind=cmd
        dep_action_value='brew install herdr'
      else
        dep_action_kind=url
        dep_action_value='https://herdr.dev/install.sh'
      fi
      ;;
    *)
      dep_action_kind=cmd
      dep_action_value=''
      ;;
  esac
}

describe_dep_action() {
  resolve_dep_action "$1"
  case "$dep_action_kind" in
    cmd) printf '%s' "$dep_action_value" ;;
    url) printf 'download and verify %s, then run only the completed download' "$dep_action_value" ;;
  esac
}

run_dep_action() {
  resolve_dep_action "$1"
  case "$dep_action_kind" in
    cmd)
      [ -n "$dep_action_value" ] || return 1
      sh -c "$dep_action_value"
      ;;
    url)
      tmp=$(mktemp) || return 1
      if ! curl -fsSL -o "$tmp" "$dep_action_value" || [ ! -s "$tmp" ]; then
        rm -f "$tmp"
        return 1
      fi
      if sh "$tmp"; then
        status=0
      else
        status=$?
      fi
      rm -f "$tmp"
      return "$status"
      ;;
  esac
}

# missing_foundational_deps lists, in the same order hand doctor's foundational tools table
# does, every dependency doctor would also report missing - never a schema bootstrap invents.
missing_foundational_deps() {
  for tool in git treehouse herdr; do
    command -v "$tool" >/dev/null 2>&1 || printf '%s\n' "$tool"
  done
}

# ensure_foundational_deps returns 0 once every foundational dependency is on PATH, and 1
# whenever one remains missing - declined, failed, unsupported platform, or check mode. It never
# treats that as fatal itself: `hand doctor` is the one place a remaining gap is judged blocking.
ensure_foundational_deps() {
  missing=$(missing_foundational_deps)
  [ -z "$missing" ] && return 0

  if [ "$check_only" -eq 1 ]; then
    log "missing foundational dependencies (check mode: no changes made):"
    for dep in $missing; do
      log "  $dep"
    done
    return 1
  fi

  log "Missing foundational runtime dependencies:"
  log ""
  for dep in $missing; do
    desc=$(describe_dep_action "$dep")
    log "  $dep"
    log "    source: $(dep_source "$dep")"
    if [ -n "$desc" ]; then
      log "    install method: $desc"
    else
      log "    install method: none detected for this platform; install $dep manually, then rerun bootstrap.sh"
    fi
    log ""
  done

  proceed=$consent_yes
  if [ "$proceed" -eq 0 ]; then
    if [ "$interactive" -eq 0 ]; then
      log "not running interactively and --yes was not given: declining to install any of the above"
      return 1
    fi
    printf 'Install these dependencies? [y/N] '
    read -r reply || reply=""
    case "$reply" in
      y|Y|yes|YES) proceed=1 ;;
      *) proceed=0 ;;
    esac
  fi
  if [ "$proceed" -eq 0 ]; then
    log "declined: continuing without installing missing foundational dependencies"
    return 1
  fi

  install_failed=0
  for dep in $missing; do
    desc=$(describe_dep_action "$dep")
    if [ -z "$desc" ]; then
      log "$dep: no supported install method detected on this platform; skipping"
      install_failed=1
      continue
    fi
    log "installing $dep: $desc"
    if ! run_dep_action "$dep"; then
      log "$dep: install action failed"
      install_failed=1
      continue
    fi
    command -v "$dep" >/dev/null 2>&1 || { log "$dep: installed but still not on PATH"; install_failed=1; }
  done
  [ "$install_failed" -eq 0 ]
}

ensure_foundational_deps || true

# ---- step 3: choose a safe fleet-home target --------------------------------------------------

# fleet_state never duplicates hand doctor's or hand init's own validation: it only decides the
# one thing bootstrap alone is responsible for before ever invoking hand init - whether this
# target is safe to hand to it at all.
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
    log "hand init has not run here yet; check mode makes no changes, so readiness cannot be evaluated further"
    exit 0
  fi
  if [ "$hand_available" -eq 0 ]; then
    log "hand is not installed; readiness cannot be evaluated further"
    exit 0
  fi
  doctor_out=$(HAND_HOME="$fleet" hand doctor 2>&1) || true
  log ""
  log "$doctor_out"
  exit 0
fi

# ---- step 4: hand init, then hand doctor for the authoritative readiness result ----------------

[ "$state" = "absent" ] && mkdir -p "$fleet"

if ! init_out=$(hand init "$fleet" 2>&1); then
  log "$init_out"
  die "hand init failed against $fleet; recover by resolving the reported error, then rerun: bootstrap.sh --fleet $fleet"
fi
log "$init_out"

doctor_out=$(HAND_HOME="$fleet" hand doctor 2>&1) || true
log ""
log "$doctor_out"

# doctor_field extracts a scalar TOON field ("key: value") from hand doctor's stdout.
doctor_field() {
  printf '%s\n' "$2" | sed -n "s/^$1: //p" | head -n1
}

# doctor_list extracts the "  - item" lines under one TOON list block ("name[N]:"). The block
# header is matched with a plain prefix comparison, never a dynamic regex built from $1: awk
# dialects disagree on escaping a literal "[" inside a string-turned-pattern, and a prefix check
# needs no escaping at all.
doctor_list() {
  printf '%s\n' "$2" | awk -v prefix="$1[" '
    substr($0, 1, length(prefix)) == prefix { in_block=1; next }
    in_block && /^  - / { sub(/^  - /, ""); print; next }
    in_block { in_block=0 }
  '
}

# installed_harnesses reads the harnesses[N]{name,installed} rows hand doctor already computed,
# in the order hand reports them, so bootstrap never re-detects harnesses on its own.
installed_harnesses() {
  printf '%s\n' "$1" | awk '
    /^harnesses\[/ { in_block=1; next }
    in_block && /^  [a-z]/ {
      split($0, cols, ",")
      if (cols[2] == "true") print cols[1]
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
