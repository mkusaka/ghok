#!/usr/bin/env bash
# Install elan (the Lean toolchain manager) on linux/amd64 CI runners from a
# pinned release, checked against its sha256; no toolchain is installed here.
# Usage: verify/install-elan.sh   (adds ~/.elan/bin to GITHUB_PATH when set)
set -euo pipefail
version=v4.2.4 # bump together with sha256
sha256=42b94d4244e8353142c456ec0e4ca6528fd898a6c604d4059f494e706e431f63
archive=elan-x86_64-unknown-linux-gnu.tar.gz
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -sSfL -o "$tmp/$archive" "https://github.com/leanprover/elan/releases/download/$version/$archive"
echo "$sha256  $tmp/$archive" | sha256sum -c -
tar -xzf "$tmp/$archive" -C "$tmp"
"$tmp/elan-init" -y --no-modify-path --default-toolchain none
if [[ -n "${GITHUB_PATH:-}" ]]; then
  echo "$HOME/.elan/bin" >> "$GITHUB_PATH"
fi
