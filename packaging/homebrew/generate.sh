#!/usr/bin/env bash
set -euo pipefail

# Builds the formula from source rather than the prebuilt release archive, so the
# installed binary can embed -X main.distribution=brew instead of the "github"
# value already baked into the shared release asset by release.yaml.

tag="${1:-}"
repo="atqamz/hand"
if [ -z "$tag" ]; then
  tag=$(gh release view --repo "$repo" --json tagName --jq .tagName)
fi
version="${tag#v}"

git fetch origin "tag" "$tag" >/dev/null 2>&1 || true
commit=$(git rev-parse "$tag^{commit}")

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
src_url="https://github.com/$repo/archive/refs/tags/$tag.tar.gz"
curl -sL -o "$tmp/src.tar.gz" "$src_url"
sha=$(sha256sum "$tmp/src.tar.gz" | cut -d' ' -f1)

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cat >"$script_dir/hand.rb" <<EOF
class Hand < Formula
  desc "Manage a fleet of coding agents"
  homepage "https://github.com/$repo"
  url "$src_url"
  sha256 "$sha"
  license "MIT"

  depends_on "go" => :build

  def install
    commit = "$commit"
    ldflags = "-s -w -X main.version=#{version} -X main.channel=stable -X main.commit=#{commit} -X main.distribution=brew"
    system "go", "build", *std_go_args(ldflags: ldflags)
  end

  test do
    assert_equal version.to_s, shell_output("#{bin}/hand --version").strip
  end
end
EOF

echo "wrote $script_dir/hand.rb for $tag"
