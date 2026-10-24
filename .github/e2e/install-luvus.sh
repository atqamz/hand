#!/usr/bin/env bash
set -eu
version=$(sed -n 's/^const Tested = "\(.*\)"$/\1/p' internal/luvus/pin.go)
test -n "$version"
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) triple=x86_64-unknown-linux-musl; ext=tar.gz ;;
  Darwin-arm64) triple=aarch64-apple-darwin; ext=tar.gz ;;
  MINGW*-x86_64) triple=x86_64-pc-windows-msvc; ext=zip ;;
  *) echo "no Luvus build for $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac
name=luvus-v$version-$triple
base=https://github.com/RizRiyz/luvus/releases/download/v$version
dir=${LUVUS_DIR:?}
case "$(uname -s)" in MINGW*) dir=$(cygpath -u "$dir") ;; esac
mkdir -p "$dir"
cd "$dir"
if [ ! -x luvus ] && [ ! -x luvus.exe ]; then
  for file in "$name.$ext" "$name.sha256"; do
    for attempt in 1 2 3 4; do
      curl -fsSLO --connect-timeout 20 --max-time 120 "$base/$file" && break
      test "$attempt" -lt 4
      sleep $((attempt * 5))
    done
  done
  if command -v sha256sum >/dev/null; then sha256sum -c "$name.sha256"; else shasum -a 256 -c "$name.sha256"; fi
  if [ "$ext" = zip ]; then unzip -q "$name.zip"; else tar -xzf "$name.tar.gz"; chmod +x luvus; fi
  rm -f "$name.$ext" "$name.sha256"
fi
if [ "$ext" = zip ]; then ./luvus.exe --version; else ./luvus --version; fi
if [ -n "${GITHUB_PATH:-}" ]; then
  case "$(uname -s)" in MINGW*) cygpath -w "$dir" >> "$GITHUB_PATH" ;; *) echo "$dir" >> "$GITHUB_PATH" ;; esac
fi
