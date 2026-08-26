#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

for command in curl go playwright-cli python3; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'Required command is missing: %s\n' "$command" >&2
    exit 2
  fi
done

run_id="${SWAPBOOK_INSPECTION_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
output="${SWAPBOOK_INSPECTION_OUTPUT:-$root/.playwright/swapbook-inspection/$run_id}"
target_addr="${SWAPBOOK_INSPECTION_TARGET_ADDR:-127.0.0.1:8082}"
gallery_port="${SWAPBOOK_INSPECTION_PORT:-7008}"
launcher_pid=""
mkdir -p "$output"

cleanup() {
  local status=$?
  if [[ -n "$launcher_pid" ]] && kill -0 "$launcher_pid" 2>/dev/null; then
    kill "$launcher_pid" 2>/dev/null || true
    for _ in $(seq 1 30); do
      kill -0 "$launcher_pid" 2>/dev/null || break
      sleep 0.2
    done
    kill -KILL "$launcher_pid" 2>/dev/null || true
    wait "$launcher_pid" 2>/dev/null || true
  fi
  printf '\nSwapbook inspection artifacts: %s\n' "$output"
  exit "$status"
}
trap cleanup EXIT INT TERM

printf 'Starting Swapbook inspection target...\n'
SWAPBOOK_TARGET_ADDR="$target_addr" SWAPBOOK_PORT="$gallery_port" \
  ./scripts/swapbook.sh >"$output/swapbook.log" 2>&1 &
launcher_pid=$!

ready=0
for _ in $(seq 1 120); do
  if curl -fsS "http://127.0.0.1:$gallery_port/__sb/" >/dev/null 2>&1 && \
     curl -fsS "http://$target_addr/_swapbook/inspection.json" >/dev/null 2>&1; then
    ready=1
    break
  fi
  if ! kill -0 "$launcher_pid" 2>/dev/null; then
    printf 'Swapbook launcher exited before becoming ready.\n' >&2
    tail -80 "$output/swapbook.log" >&2 || true
    exit 1
  fi
  sleep 0.25
done
if [[ "$ready" -ne 1 ]]; then
  printf 'Swapbook inspection target did not become ready.\n' >&2
  tail -80 "$output/swapbook.log" >&2 || true
  exit 1
fi

python3 ./scripts/swapbook-inspect.py \
  --gallery "http://127.0.0.1:$gallery_port" \
  --target "http://$target_addr" \
  --output "$output"
