#!/usr/bin/env bash
set -eu
version=${1:?usage: install-codex.sh VERSION|latest}
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) triple=x86_64-unknown-linux-musl; asset=codex-$triple.tar.gz; exe=codex ;;
  Darwin-arm64) triple=aarch64-apple-darwin; asset=codex-$triple.tar.gz; exe=codex ;;
  MINGW*-x86_64) triple=x86_64-pc-windows-msvc; asset=codex-$triple.exe.zip; exe=codex.exe ;;
  *) echo "no Codex build for $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac
repo=openai/codex
if [ "$version" = latest ]; then
  version=$(gh release view --repo "$repo" --json tagName --jq .tagName)
  version=${version#rust-v}
fi
tag=rust-v$version
var=CODEX_SHA256_${triple//-/_}
if [ "$version" = "${CODEX_VERSION:-}" ] && [ -n "${!var:-}" ]; then
  want=${!var}
else
  want=$(gh release view "$tag" --repo "$repo" --json assets --jq ".assets[] | select(.name == \"$asset\") | .digest")
  want=${want#sha256:}
fi
test -n "$want"
dir=${CODEX_DIR:?}
case "$(uname -s)" in MINGW*) dir=$(cygpath -u "$dir") ;; esac
mkdir -p "$dir/bin"
if [ ! -x "$dir/bin/$exe" ] || ! env -u GH_TOKEN "$dir/bin/$exe" --version | grep -qx "codex-cli $version"; then
  cd "$dir"
  for attempt in 1 2 3 4; do
    curl -fsSL --connect-timeout 20 --max-time 900 -o "$asset" "https://github.com/$repo/releases/download/$tag/$asset" && break
    test "$attempt" -lt 4
    sleep $((attempt * 5))
  done
  if command -v sha256sum >/dev/null; then got=$(sha256sum "$asset"); else got=$(shasum -a 256 "$asset"); fi
  if [ "${got%% *}" != "$want" ]; then
    echo "$asset: sha256 ${got%% *}, want $want" >&2
    exit 1
  fi
  if [ "${asset%.zip}" != "$asset" ]; then unzip -q -o "$asset"; else tar -xzf "$asset"; fi
  rm -f "$asset"
  mv "codex-$triple${exe#codex}" "bin/$exe"
  chmod +x "bin/$exe"
fi
env -u GH_TOKEN "$dir/bin/$exe" --version
if [ -n "${GITHUB_PATH:-}" ]; then
  case "$(uname -s)" in MINGW*) cygpath -w "$dir/bin" >> "$GITHUB_PATH" ;; *) echo "$dir/bin" >> "$GITHUB_PATH" ;; esac
fi
