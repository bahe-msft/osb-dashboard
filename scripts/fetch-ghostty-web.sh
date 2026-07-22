#!/usr/bin/env bash
set -euo pipefail

version="0.4.0"
sha256="90bf473b6c7f43ab5e52ee98d8295e04fb1c6b07b928e9795489df1e8cb8802e"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
destination="$root/web/assets/third-party/ghostty-web"
version_file="$destination/.version"

if [[ -f "$version_file" ]] \
  && [[ "$(cat "$version_file")" == "$version" ]] \
  && [[ -f "$destination/ghostty-web.js" ]] \
  && [[ -f "$destination/ghostty-vt.wasm" ]] \
  && [[ -f "$destination/vite-browser-external.js" ]]; then
  exit 0
fi

temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
archive="$temporary/ghostty-web.tgz"
url="https://registry.npmjs.org/ghostty-web/-/ghostty-web-${version}.tgz"

printf 'Downloading ghostty-web %s...\n' "$version"
curl --fail --location --silent --show-error --retry 3 "$url" --output "$archive"
printf '%s  %s\n' "$sha256" "$archive" | sha256sum --check --status

tar --extract --gzip --file "$archive" --directory "$temporary" \
  package/dist/ghostty-web.js \
  package/dist/ghostty-vt.wasm \
  package/dist/__vite-browser-external-2447137e.js

mkdir -p "$destination"
sed 's|\./__vite-browser-external-2447137e\.js|./vite-browser-external.js|g' \
  "$temporary/package/dist/ghostty-web.js" > "$destination/ghostty-web.js"
install -m 0644 "$temporary/package/dist/ghostty-vt.wasm" "$destination/ghostty-vt.wasm"
install -m 0644 "$temporary/package/dist/__vite-browser-external-2447137e.js" "$destination/vite-browser-external.js"
printf '%s\n' "$version" > "$version_file"
printf 'ghostty-web %s ready.\n' "$version"
