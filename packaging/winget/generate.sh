#!/usr/bin/env bash
set -euo pipefail

REPO="atqamz/hand"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFESTS_ROOT="$SCRIPT_DIR/manifests/a/Atqamz/Hand"

TAG="${1:-}"
if [[ -z "$TAG" ]]; then
  TAG="$(gh release view --repo "$REPO" --json tagName --jq .tagName)"
fi
VERSION="${TAG#v}"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

gh release download "$TAG" --repo "$REPO" --pattern checksums.txt --dir "$WORKDIR"

SHA256="$(awk '$2 == "hand-windows-amd64.zip" { print toupper($1) }' "$WORKDIR/checksums.txt")"
if [[ ! "$SHA256" =~ ^[0-9A-F]{64}$ ]]; then
  echo "generate.sh: could not find a valid sha256 for hand-windows-amd64.zip in $TAG's checksums.txt" >&2
  exit 1
fi

if [[ -d "$MANIFESTS_ROOT" ]]; then
  find "$MANIFESTS_ROOT" -mindepth 1 -maxdepth 1 -type d ! -name "$VERSION" -exec rm -rf {} +
fi

TARGET="$MANIFESTS_ROOT/$VERSION"
mkdir -p "$TARGET"

cat > "$TARGET/Atqamz.Hand.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.version.1.9.0.schema.json

PackageIdentifier: Atqamz.Hand
PackageVersion: $VERSION
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.9.0
EOF

cat > "$TARGET/Atqamz.Hand.installer.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.installer.1.9.0.schema.json

PackageIdentifier: Atqamz.Hand
PackageVersion: $VERSION
InstallerType: zip
NestedInstallerType: portable
NestedInstallerFiles:
  - RelativeFilePath: hand.exe
    PortableCommandAlias: hand
Installers:
  - Architecture: x64
    InstallerUrl: https://github.com/$REPO/releases/download/$TAG/hand-windows-amd64.zip
    InstallerSha256: $SHA256
ManifestType: installer
ManifestVersion: 1.9.0
EOF

cat > "$TARGET/Atqamz.Hand.locale.en-US.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.defaultLocale.1.9.0.schema.json

PackageIdentifier: Atqamz.Hand
PackageVersion: $VERSION
PackageLocale: en-US
Publisher: Atqamz
PackageName: Hand
License: MIT
ShortDescription: Manage a fleet of coding agents
PackageUrl: https://github.com/$REPO
ManifestType: defaultLocale
ManifestVersion: 1.9.0
EOF

echo "generated $TARGET"
