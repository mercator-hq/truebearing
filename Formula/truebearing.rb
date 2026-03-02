# Formula/truebearing.rb — Homebrew formula for TrueBearing.
#
# This file lives in the main repository and serves as the canonical formula
# source. To publish via a Homebrew tap, copy it to:
#   https://github.com/mercator-hq/homebrew-tap/Formula/truebearing.rb
#
# After cutting a release, update `url` and `sha256`:
#   shasum -a 256 truebearing-<version>.tar.gz
#
# Users install via:
#   brew tap mercator-hq/truebearing https://github.com/mercator-hq/truebearing
#   brew install mercator-hq/truebearing/truebearing

class Truebearing < Formula
  desc "Transparent MCP proxy with sequence-aware behavioral policy enforcement"
  homepage "https://github.com/mercator-hq/truebearing"

  # Update url and sha256 before each release.
  # sha256 is computed over the archive: shasum -a 256 <archive>.tar.gz
  url "https://github.com/mercator-hq/truebearing/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "Apache-2.0"

  # Install from HEAD with: brew install --HEAD mercator-hq/truebearing/truebearing
  head "https://github.com/mercator-hq/truebearing.git", branch: "master"

  depends_on "go" => :build

  def install
    # Design: CGO_ENABLED=0 is required. TrueBearing uses modernc.org/sqlite, a pure-Go
    # SQLite implementation that needs no system libsqlite3. Disabling CGO produces a
    # fully static binary with no runtime dependency on the host C library — this is the
    # "single static binary" guarantee documented in the architecture.
    ENV["CGO_ENABLED"] = "0"

    system "go", "build",
           *std_go_args(ldflags: "-s -w"),
           "-o", bin/"truebearing",
           "./cmd"
  end

  test do
    assert_match "truebearing", shell_output("#{bin}/truebearing --help")
  end
end
