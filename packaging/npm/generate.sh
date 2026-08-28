#!/usr/bin/env bash
# Cross-compiles hand from the checked-out release commit for every runtime-qualified
# target and packages the results for npm: one platform package per target #354 leaves
# supported in internal/toolchain/runtime.lock.json, plus one meta package, versioned to
# match the release. Building from source (rather than downloading hand's prebuilt
# release archives, which already embed -X main.distribution=github) is what lets each
# binary correctly self-identify as an npm install.
#
# The published target set is never written down here: it is the exact subset of
# runtime.lock.json's targets carrying no "unsupported" entry, mapped to npm's os/cpu
# names. A target that regresses or newly qualifies changes what this script does with
# no edit to this file - see
# docs/adr/npm-publishes-only-runtime-qualified-targets-behind-one-operator-gate.md.
# A target that drops out of that set is left alone, not deleted: its package.json and
# bin/ may still hold whatever a release last wrote while it was qualified.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PLATFORMS_DIR="$SCRIPT_DIR/platforms"
META_DIR="$SCRIPT_DIR/meta"
RUNTIME_LOCK="${HAND_NPM_RUNTIME_LOCK:-$REPO_ROOT/internal/toolchain/runtime.lock.json}"

usage() {
  cat <<'EOF'
Usage: generate.sh --version X.Y.Z --commit <sha>
       generate.sh --print-targets

Builds hand from the source already checked out at REPO_ROOT (it does not fetch
or switch commits itself) into packaging/npm/platforms/<platform>/bin/, for
exactly the targets internal/toolchain/runtime.lock.json marks runtime-qualified
(no "unsupported" entry), and stamps --version into each platform package.json
and the meta package.json, including its optionalDependencies pins. Refuses to
run if the checkout's HEAD does not match --commit.

--print-targets prints the derived npm platform keys as a sorted JSON array and
exits without building or writing anything; no other flag is needed with it.

Requires `go`, `git` and `jq` on PATH. Re-running is safe: builds and rewrites
are idempotent.
EOF
}

print_targets=0
version=""
commit=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    --print-targets)
      print_targets=1
      shift
      ;;
    --version)
      version="${2:-}"
      shift 2
      ;;
    --commit)
      commit="${2:-}"
      shift 2
      ;;
    *)
      echo "generate.sh: unrecognized argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

npm_os() {
  case "$1" in
    linux) printf 'linux' ;;
    darwin) printf 'darwin' ;;
    windows) printf 'win32' ;;
    *)
      echo "generate.sh: no npm os mapping for GOOS $1" >&2
      exit 1
      ;;
  esac
}

npm_cpu() {
  case "$1" in
    amd64) printf 'x64' ;;
    arm64) printf 'arm64' ;;
    *)
      echo "generate.sh: no npm cpu mapping for GOARCH $1" >&2
      exit 1
      ;;
  esac
}

if [[ ! -f "$RUNTIME_LOCK" ]]; then
  echo "generate.sh: runtime lock not found: $RUNTIME_LOCK" >&2
  exit 1
fi

# A plain while-read loop rather than mapfile/readarray, which macOS's bash 3.2 does not
# have. Each line is also stripped of a trailing CR: a checkout or tool in the chain can
# hand back CRLF line endings (observed on Windows runners), and an un-stripped CR here
# would silently ride along into every GOOS/GOARCH derived from it below.
stable_targets=()
while IFS= read -r line; do
  line="${line%$'\r'}"
  if [[ -n "$line" ]]; then
    stable_targets+=("$line")
  fi
done < <(jq -r '
  .targets
  | to_entries
  | map(select((.value.unsupported // "") == ""))
  | map(.key)
  | sort[]
' "$RUNTIME_LOCK")

if [[ ${#stable_targets[@]} -eq 0 ]]; then
  echo "generate.sh: runtime lock has no runtime-qualified targets" >&2
  exit 1
fi

# Parallel indexed arrays rather than associative arrays (declare -A), another bash-4-only
# feature macOS's bash 3.2 lacks: goos_list/goarch_list line up with npm_keys by position.
declare -a npm_keys=()
declare -a goos_list=()
declare -a goarch_list=()
for target in "${stable_targets[@]}"; do
  goos="${target%%/*}"
  goarch="${target#*/}"
  npmos="$(npm_os "$goos")"
  npmcpu="$(npm_cpu "$goarch")"
  npmkey="${npmos}-${npmcpu}"
  npm_keys+=("$npmkey")
  goos_list+=("$goos")
  goarch_list+=("$goarch")
done

if [[ "$print_targets" -eq 1 ]]; then
  printf '%s\n' "${npm_keys[@]}" | jq -R . | jq -s .
  exit 0
fi

if [[ -z "$version" || -z "$commit" ]]; then
  echo "generate.sh: --version and --commit are both required" >&2
  usage >&2
  exit 1
fi

head_commit="$(git -C "$REPO_ROOT" rev-parse HEAD)"
if [[ "$head_commit" != "$commit" ]]; then
  echo "generate.sh: checked-out HEAD $head_commit does not match --commit $commit" >&2
  exit 1
fi

npm_keys_json="$(printf '%s\n' "${npm_keys[@]}" | jq -R . | jq -sc .)"
LDFLAGS="-s -w -X main.version=$version -X main.channel=stable -X main.commit=$commit -X main.distribution=npm"

for ((i = 0; i < ${#npm_keys[@]}; i++)); do
  npmkey="${npm_keys[$i]}"
  goos="${goos_list[$i]}"
  goarch="${goarch_list[$i]}"
  binname="hand"
  if [[ "$goos" == windows ]]; then
    binname="hand.exe"
  fi
  dest_dir="$PLATFORMS_DIR/$npmkey/bin"
  mkdir -p "$dest_dir"
  (
    cd "$REPO_ROOT"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -ldflags "$LDFLAGS" -o "$dest_dir/$binname" .
  )
  if [[ "$goos" != windows ]]; then
    chmod +x "$dest_dir/$binname"
  fi

  os_word="${npmkey%-*}"
  cpu_word="${npmkey##*-}"
  jq -n \
    --arg name "@atqamz/hand-$npmkey" \
    --arg version "$version" \
    --arg description "hand prebuilt binary for ${os_word} ${cpu_word}" \
    --arg os "$os_word" \
    --arg cpu "$cpu_word" \
    '{
      name: $name,
      version: $version,
      description: $description,
      repository: {type: "git", url: "git+https://github.com/atqamz/hand.git"},
      license: "MIT",
      os: [$os],
      cpu: [$cpu],
      publishConfig: {access: "public"},
      files: ["bin/"]
    }' > "$PLATFORMS_DIR/$npmkey/package.json"
done

meta_pkg="$META_DIR/package.json"
tmp_meta="$(mktemp "$meta_pkg.XXXXXX")"
jq --arg v "$version" \
   --argjson keys "$npm_keys_json" \
  '.version = $v
   | .optionalDependencies = ($keys | map({key: ("@atqamz/hand-" + .), value: $v}) | from_entries)' \
  "$meta_pkg" > "$tmp_meta"
mv "$tmp_meta" "$meta_pkg"

echo "generate.sh: packaged hand $version for ${#npm_keys[@]} runtime-qualified target(s): ${npm_keys[*]}"
