#!/bin/sh
# Install the busybar CLI.  curl -fsSL <url>/install.sh | sh
set -e

REPO="${BUSYBAR_REPO:-skidvis/busybar-cli}"
VERSION="${BUSYBAR_VERSION:-latest}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
case "$os" in
  darwin|linux) ;;
  *) echo "unsupported OS: $os (Windows: download the .zip from the releases page)" >&2; exit 1 ;;
esac

if [ "$VERSION" = latest ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
            sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
  [ -n "$VERSION" ] || { echo "could not resolve the latest release" >&2; exit 1; }
fi
num=${VERSION#v}

if [ -w /usr/local/bin ] 2>/dev/null; then dest=/usr/local/bin; else dest="$HOME/.local/bin"; fi
mkdir -p "$dest"

url="https://github.com/$REPO/releases/download/$VERSION/busybar_${num}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "downloading busybar $VERSION ($os/$arch)"
curl -fsSL "$url" | tar -xz -C "$tmp"
install -m 0755 "$tmp/busybar" "$dest/busybar"

echo "installed $dest/busybar"
case ":$PATH:" in
  *":$dest:"*) ;;
  *) echo "note: $dest is not on your PATH" ;;
esac
