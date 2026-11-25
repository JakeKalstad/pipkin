#!/bin/sh
set -eu

repo=JakeKalstad/pipkin
version=${PIPKIN_VERSION:-latest}
if [ "$version" != latest ]; then
    if ! printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$'; then
        printf '%s\n' 'PIPKIN_VERSION must be latest or a release tag such as v0.14.0' >&2
        exit 1
    fi
fi

case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) printf '%s\n' 'Pipkin supports Linux and macOS release binaries.' >&2; exit 1 ;;
esac
case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) printf '%s\n' 'Pipkin release binaries support amd64 and arm64.' >&2; exit 1 ;;
esac

asset="pipkin-${os}-${arch}.tar.gz"
dest=${PIPKIN_INSTALL_DIR:-"$HOME/.local/bin"}
case "$dest" in /*) ;; *) dest="$(pwd)/$dest" ;; esac
if [ "$version" = latest ]; then
    release="https://github.com/$repo/releases/latest/download"
else
    release="https://github.com/$repo/releases/download/$version"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' 0
trap 'exit 1' HUP INT TERM
cd "$tmp"
curl -fsSL --retry 3 "$release/$asset" -o "$asset"
curl -fsSL --retry 3 "$release/SHA256SUMS" -o SHA256SUMS
awk -v asset="$asset" '$2 == asset { print }' SHA256SUMS > asset.sha256
if [ "$(wc -l < asset.sha256 | tr -d ' ')" != 1 ]; then
    printf '%s\n' "Missing or ambiguous checksum for $asset" >&2
    exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c asset.sha256
else
    shasum -a 256 -c asset.sha256
fi
tar -xzf "$asset" pipkin
cd /
mkdir -p "$dest"
install -m 755 "$tmp/pipkin" "$dest/pipkin"
printf 'Installed Pipkin at %s/pipkin\n' "$dest"
case ":$PATH:" in
    *":$dest:"*) ;;
    *) printf 'Add %s to PATH to run pipkin by name.\n' "$dest" ;;
esac
