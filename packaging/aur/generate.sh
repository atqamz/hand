#!/usr/bin/env bash
set -euo pipefail

repo="atqamz/hand"
aur_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
pkgbuild="${aur_dir}/PKGBUILD"
srcinfo="${aur_dir}/.SRCINFO"

tag="${1:-}"
if [[ -z "${tag}" ]]; then
	tag="$(gh release view --repo "${repo}" --json tagName --jq .tagName)"
fi
pkgver="${tag#v}"

workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT

gh release download "${tag}" --repo "${repo}" --pattern checksums.txt --dir "${workdir}" --clobber

sha_amd64="$(awk '$2 == "hand-linux-amd64.tar.gz" { print $1 }' "${workdir}/checksums.txt")"
sha_arm64="$(awk '$2 == "hand-linux-arm64.tar.gz" { print $1 }' "${workdir}/checksums.txt")"

if [[ -z "${sha_amd64}" || -z "${sha_arm64}" ]]; then
	echo "generate.sh: ${tag}'s checksums.txt is missing hand-linux-amd64.tar.gz or hand-linux-arm64.tar.gz" >&2
	exit 1
fi

url_amd64="https://github.com/${repo}/releases/download/${tag}/hand-linux-amd64.tar.gz"
url_arm64="https://github.com/${repo}/releases/download/${tag}/hand-linux-arm64.tar.gz"

sed -i \
	-e "s|^pkgver=.*|pkgver=${pkgver}|" \
	-e "s|^source_x86_64=.*|source_x86_64=(\"https://github.com/${repo}/releases/download/v\${pkgver}/hand-linux-amd64.tar.gz\")|" \
	-e "s|^source_aarch64=.*|source_aarch64=(\"https://github.com/${repo}/releases/download/v\${pkgver}/hand-linux-arm64.tar.gz\")|" \
	-e "s|^sha256sums_x86_64=.*|sha256sums_x86_64=('${sha_amd64}')|" \
	-e "s|^sha256sums_aarch64=.*|sha256sums_aarch64=('${sha_arm64}')|" \
	"${pkgbuild}"

pkgdesc="$(sed -n 's/^pkgdesc="\(.*\)"$/\1/p' "${pkgbuild}")"
pkgrel="$(sed -n 's/^pkgrel=\(.*\)$/\1/p' "${pkgbuild}")"

# Stand-in for `makepkg --printsrcinfo`, which needs a real Arch/makepkg
# environment this sandbox does not have.
# Replace this hand-rolled block with the real generator at AUR-push time.
cat > "${srcinfo}" <<EOF
pkgbase = hand-bin
	pkgdesc = ${pkgdesc}
	pkgver = ${pkgver}
	pkgrel = ${pkgrel}
	url = https://github.com/${repo}
	arch = x86_64
	arch = aarch64
	license = MIT
	provides = hand
	conflicts = hand
	source_x86_64 = ${url_amd64}
	sha256sums_x86_64 = ${sha_amd64}
	source_aarch64 = ${url_arm64}
	sha256sums_aarch64 = ${sha_arm64}

pkgname = hand-bin
EOF

echo "generate.sh: updated PKGBUILD and .SRCINFO for ${tag} (pkgver=${pkgver}, pkgrel=${pkgrel})"
