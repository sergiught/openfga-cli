#!/usr/bin/env bash
#
# ofga installer
#
# Downloads a GoReleaser archive from GitHub, verifies it against the release's
# SHA-256 checksums, and installs the ofga binary. Set BIN_DIR to install
# somewhere other than /usr/local/bin.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/sergiught/openfga-cli/main/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/sergiught/openfga-cli/main/install.sh | bash -s -- v1.0.0
#   BIN_DIR=$HOME/.local/bin bash install.sh
#
# Variables:
#   VERSION    Release tag to install. Default: latest.
#   BIN_DIR    Installation directory. Default: /usr/local/bin.
#   GH_REPO    GitHub repository to fetch from. Default: sergiught/openfga-cli.

set -euo pipefail

REPO="${GH_REPO:-sergiught/openfga-cli}"
VERSION="${1:-${VERSION:-latest}}"
BIN_DIR="${BIN_DIR:-/usr/local/bin}"

say() { printf '==> %s\n' "$*"; }
die() {
  printf 'ofga install: %s\n' "$*" >&2
  exit 1
}
need() { command -v "$1" >/dev/null 2>&1 || die "required tool '$1' not found on PATH"; }

need awk
need curl
need grep
need install
need mktemp
need tar
need uname

printf '%s\n' "$REPO" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' ||
  die "GH_REPO must be in owner/repository form"

case "$(uname -s)" in
  Linux) OS="Linux" ;;
  Darwin) OS="Darwin" ;;
  *) die "unsupported OS '$(uname -s)' (supported: Linux and macOS)" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) ARCH="x86_64" ;;
  arm64 | aarch64) ARCH="arm64" ;;
  *) die "unsupported architecture '$(uname -m)' (supported: x86_64 and arm64)" ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
  verify_checksum() { sha256sum -c -; }
elif command -v shasum >/dev/null 2>&1; then
  verify_checksum() { shasum -a 256 -c -; }
else
  die "neither sha256sum nor shasum is installed"
fi

if [ "$VERSION" = "latest" ]; then
  say "resolving the latest release for $REPO"
  RESOLVED=$(curl -sSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/${REPO}/releases/latest") ||
    die "could not reach the latest release for $REPO"
  VERSION="${RESOLVED##*/}"
  VERSION="${VERSION%%\?*}"
fi

printf '%s\n' "$VERSION" |
  grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$' ||
  die "VERSION must be a release tag such as v1.2.3 (got '$VERSION')"

ARCHIVE="ofga_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

say "downloading ofga $VERSION for $OS/$ARCH"
curl -fsSL "${BASE_URL}/${ARCHIVE}" -o "${TMP_DIR}/${ARCHIVE}"
curl -fsSL "${BASE_URL}/checksums.txt" -o "${TMP_DIR}/checksums.txt"

CHECKSUM=$(awk -v archive="$ARCHIVE" '$2 == archive { print; found = 1 } END { if (!found) exit 1 }' \
  "${TMP_DIR}/checksums.txt") ||
  die "$ARCHIVE is missing from checksums.txt"

say "verifying SHA-256 checksum"
(cd "$TMP_DIR" && printf '%s\n' "$CHECKSUM" | verify_checksum) ||
  die "checksum verification failed for $ARCHIVE"

say "extracting $ARCHIVE"
tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "$TMP_DIR" ofga
[ -x "${TMP_DIR}/ofga" ] || die "binary 'ofga' was not found in $ARCHIVE"

if [ ! -d "$BIN_DIR" ]; then
  if ! mkdir -p "$BIN_DIR" 2>/dev/null; then
    need sudo
    say "creating $BIN_DIR with sudo"
    sudo mkdir -p "$BIN_DIR"
  fi
fi

INSTALL_PATH="${BIN_DIR}/ofga"
if [ -w "$BIN_DIR" ]; then
  install -m 0755 "${TMP_DIR}/ofga" "$INSTALL_PATH"
else
  need sudo
  say "$BIN_DIR is not writable; installing with sudo"
  sudo install -m 0755 "${TMP_DIR}/ofga" "$INSTALL_PATH"
fi

say "installed $INSTALL_PATH"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) say "note: add $BIN_DIR to PATH to run ofga without its full path" ;;
esac
say "run: ofga version"
