#!/usr/bin/env bash
# Build skoed browser extension packages.
# Outputs: dist/skoed-firefox.zip  (Manifest V2)
#          dist/skoed-chrome.zip   (Manifest V3)
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p ../../dist

build() {
  local browser="$1"
  local manifest="manifest-${browser}.json"
  local out="../../dist/skoed-${browser}.zip"
  local tmp
  tmp=$(mktemp -d)
  trap "rm -rf $tmp" EXIT

  cp -r background popup icons "$tmp/"
  cp "$manifest" "$tmp/manifest.json"

  (cd "$tmp" && zip -r - .) > "$out"
  echo "Built $out"
}

build firefox
build chrome
