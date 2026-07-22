#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
destination="$root/web/assets/third-party/ui"
version_file="$destination/.version"
versions="basecoat-css=1.0.2 htmx=2.0.10 lucide=0.468.0"

if [[ -f "$version_file" ]] \
  && [[ "$(cat "$version_file")" == "$versions" ]] \
  && [[ -f "$destination/basecoat.min.css" ]] \
  && [[ -f "$destination/htmx.min.js" ]] \
  && [[ -f "$destination/lucide.min.js" ]]; then
  exit 0
fi

mkdir -p "$destination"
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT

fetch() {
  local name="$1"
  local url="$2"
  local checksum="$3"
  local target="$temporary/$name"
  printf 'Downloading %s...\n' "$name"
  curl --fail --location --silent --show-error --retry 3 "$url" --output "$target"
  printf '%s  %s\n' "$checksum" "$target" | sha256sum --check --status
  install -m 0644 "$target" "$destination/$name"
}

fetch \
  basecoat.min.css \
  https://cdn.jsdelivr.net/npm/basecoat-css@1.0.2/dist/basecoat.cdn.min.css \
  8123677adb9bba43be3298e1543bcc5fc763e8cda3d32dc74c806046a3537ca0
fetch \
  htmx.min.js \
  https://unpkg.com/htmx.org@2.0.10/dist/htmx.min.js \
  71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de
fetch \
  lucide.min.js \
  https://cdn.jsdelivr.net/npm/lucide@0.468.0/dist/umd/lucide.min.js \
  3411692820cb8d47543f69496aa25fd603a358f4498046f41c508a5a3342210e

printf '%s\n' "$versions" > "$version_file"
printf 'Pinned UI assets ready.\n'
