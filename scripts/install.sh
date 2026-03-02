#!/usr/bin/env bash
# install.sh — Download and install the truebearing binary from GitHub Releases.
#
# Usage (one-liner):
#   curl -sSL https://raw.githubusercontent.com/mercator-hq/truebearing/master/scripts/install.sh | sh
#
# Supported platforms:
#   macOS  arm64  (Apple Silicon)
#   macOS  amd64  (Intel)
#   Linux  amd64
#   Linux  arm64  (aarch64)
#
# Environment overrides:
#   VERSION      — install a specific release, e.g. VERSION=0.1.0
#   INSTALL_DIR  — install to a custom directory, e.g. INSTALL_DIR=$HOME/.local/bin

set -euo pipefail

REPO="mercator-hq/truebearing"
BINARY="truebearing"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${VERSION:-}"

# ── Detect OS ─────────────────────────────────────────────────────────────────

OS=$(uname -s)
case "$OS" in
  Darwin) OS="darwin" ;;
  Linux)  OS="linux"  ;;
  *)
    echo "error: unsupported operating system: ${OS}" >&2
    echo "       supported: macOS (Darwin), Linux" >&2
    exit 1
    ;;
esac

# ── Detect architecture ───────────────────────────────────────────────────────

ARCH=$(uname -m)
case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    echo "error: unsupported architecture: ${ARCH}" >&2
    echo "       supported: x86_64 (amd64), arm64 / aarch64" >&2
    exit 1
    ;;
esac

# ── Resolve version ───────────────────────────────────────────────────────────

if [ -z "$VERSION" ]; then
  echo "Fetching latest release version..."
  # GitHub's releases/latest redirect resolves to the tag; parse tag_name from the API.
  VERSION=$(curl -fsSL \
    "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | sed -E 's/.*"v?([^"]+)".*/\1/')

  if [ -z "$VERSION" ]; then
    echo "error: could not determine latest version from GitHub API" >&2
    echo "       Set VERSION=<version> to install a specific release, e.g.:" >&2
    echo "       VERSION=0.1.0 curl -sSL <install-url> | sh" >&2
    exit 1
  fi
fi

# ── Download binary ───────────────────────────────────────────────────────────

ASSET="${BINARY}_${OS}_${ARCH}"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${ASSET}"

echo "Installing truebearing v${VERSION} (${OS}/${ARCH})..."
echo "Source: ${URL}"
echo ""

TMP=$(mktemp)
# Remove the temp file on exit regardless of success or failure so we never
# leave a partial binary on disk.
trap 'rm -f "$TMP"' EXIT

if ! curl -fsSL "$URL" -o "$TMP"; then
  echo "error: download failed" >&2
  echo "       Check that release v${VERSION} exists at:" >&2
  echo "       https://github.com/${REPO}/releases" >&2
  exit 1
fi

chmod 0755 "$TMP"

# ── Install to INSTALL_DIR ────────────────────────────────────────────────────

# Attempt a direct move; fall back to sudo if the directory is not writable.
# We move rather than copy so that the temp-file trap cannot delete the
# installed binary after a successful install.
if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP" "${INSTALL_DIR}/${BINARY}"
else
  echo "Installing to ${INSTALL_DIR} requires elevated permissions."
  sudo mv "$TMP" "${INSTALL_DIR}/${BINARY}"
fi

echo "Installed: ${INSTALL_DIR}/${BINARY}"
echo ""
echo "Run 'truebearing --help' to get started."
echo "Docs: https://github.com/${REPO}#readme"
