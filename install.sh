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

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Archives are named after the goreleaser project.  That is "busybar" as of
# v0.1.1; v0.1.0 and earlier used the repo name, so fall back to it.
echo "downloading busybar $VERSION ($os/$arch)"
base="https://github.com/$REPO/releases/download/$VERSION"
for name in busybar "${REPO#*/}"; do
  url="$base/${name}_${num}_${os}_${arch}.tar.gz"
  # Not piped into tar: bsdtar exits 0 on empty input, hiding a 404 from curl.
  curl -fsSL -o "$tmp/archive.tar.gz" "$url" 2>/dev/null && break
  url=
done
[ -n "$url" ] || { echo "no release archive for $os/$arch at $base" >&2; exit 1; }
tar -xzf "$tmp/archive.tar.gz" -C "$tmp"

# The binary inside carries the same name as the archive.
if   [ -f "$tmp/busybar" ];      then bin="$tmp/busybar"
elif [ -f "$tmp/${REPO#*/}" ];   then bin="$tmp/${REPO#*/}"
else echo "no busybar binary in $url" >&2; exit 1
fi
install -m 0755 "$bin" "$dest/busybar"

echo "installed $dest/busybar"
case ":$PATH:" in
  *":$dest:"*) ;;
  *) echo "note: $dest is not on your PATH" ;;
esac
