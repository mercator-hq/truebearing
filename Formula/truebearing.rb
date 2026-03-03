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
  version "0.1.0"
  license "Apache-2.0"

  if OS.mac? && Hardware::CPU.arm?
    url "https://github.com/mercator-hq/truebearing/releases/download/v0.1.0/truebearing-darwin-arm64"
    sha256 "4b5198d1909b47ceb02beea9386ec465a741573cb4af153eb11ff3cf6f2ee4dd"
  elsif OS.mac? && Hardware::CPU.intel?
    url "https://github.com/mercator-hq/truebearing/releases/download/v0.1.0/truebearing-darwin-amd64"
    sha256 "83fdcfe5d480e44af6592cd6f1460bdddb531a00f8bdb238b781744d85aaad33"
  end

  # Install from HEAD with: brew install --HEAD mercator-hq/truebearing/truebearing
  head "https://github.com/mercator-hq/truebearing.git", branch: "master"

  def install
    # The downloaded file is the raw binary, so we just need to install it to the
    # bin directory and rename it to `truebearing`.
    if OS.mac? && Hardware::CPU.arm?
      bin.install "truebearing-darwin-arm64" => "truebearing"
    elsif OS.mac? && Hardware::CPU.intel?
      bin.install "truebearing-darwin-amd64" => "truebearing"
    end
  end

  test do
    assert_match "truebearing version 0.1.0", shell_output("#{bin}/truebearing --version")
  end
end
