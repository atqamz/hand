#!/usr/bin/env bash
set -eu
version=${1:?usage: install-claude.sh VERSION|latest}
case "$(uname -s)" in
  MINGW*) bin=$USERPROFILE/.local/bin/claude.exe ;;
  *) bin=$HOME/.local/bin/claude ;;
esac
announce() {
  "$bin" --version
  if [ -n "${GITHUB_PATH:-}" ]; then
    case "$(uname -s)" in MINGW*) cygpath -w "$(dirname "$bin")" >> "$GITHUB_PATH" ;; *) dirname "$bin" >> "$GITHUB_PATH" ;; esac
  fi
}
if [ "$version" != latest ] && [ -x "$bin" ] && "$bin" --version | grep -q "^$version "; then
  announce
  exit 0
fi
case "$(uname -s)" in
  MINGW*)
    powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm https://claude.ai/install.ps1))) $version"
    ;;
  *)
    script=$(mktemp)
    for attempt in 1 2 3; do
      curl -fsSL --connect-timeout 20 --max-time 300 https://claude.ai/install.sh -o "$script" && break
      test "$attempt" -lt 3
      sleep $((attempt * 5))
    done
    bash "$script" "$version"
    rm -f "$script"
    ;;
esac
announce
