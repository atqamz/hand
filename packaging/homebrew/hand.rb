class Hand < Formula
  desc "Manage a fleet of coding agents"
  homepage "https://github.com/atqamz/hand"
  url "https://github.com/atqamz/hand/archive/refs/tags/v0.5.0.tar.gz"
  sha256 "bb122f2129160c0568d331363c9c5249490553c8dde4909602c7023ab07e1418"
  license "MIT"

  depends_on "go" => :build

  def install
    commit = "81eb3e8d5a32c59f5d68e2605a9a09f9495a6a13"
    ldflags = "-s -w -X main.version=#{version} -X main.channel=stable -X main.commit=#{commit} -X main.distribution=brew"
    system "go", "build", *std_go_args(ldflags: ldflags)
  end

  test do
    assert_equal version.to_s, shell_output("#{bin}/hand --version").strip
  end
end
