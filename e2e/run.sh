#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

kubeconfig="${OSB_E2E_KUBECONFIG:-${KUBECONFIG:-}}"
if [[ -z "$kubeconfig" ]]; then
  printf 'OSB_E2E_KUBECONFIG or KUBECONFIG must point to an isolated OpenSandbox cluster.\n' >&2
  exit 2
fi
if [[ ! -r "$kubeconfig" ]]; then
  printf 'Kubeconfig is not readable: %s\n' "$kubeconfig" >&2
  exit 2
fi
kubeconfig="$(cd "$(dirname "$kubeconfig")" && pwd)/$(basename "$kubeconfig")"

for command in curl go playwright-cli; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'Required command is missing: %s\n' "$command" >&2
    exit 2
  fi
done

run_id="${OSB_E2E_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
run_dir="$root/.playwright/e2e/$run_id"
mkdir -p "$run_dir"

port="${OSB_E2E_PORT:-$((20000 + RANDOM % 20000))}"
address="127.0.0.1:$port"
base_url="http://$address"
sandbox_image="${OSB_E2E_SANDBOX_IMAGE:-python:3.12-slim}"
keep_sandbox="${OSB_E2E_KEEP_SANDBOX:-0}"
viewport_width="${OSB_E2E_VIEWPORT_WIDTH:-1600}"
viewport_height="${OSB_E2E_VIEWPORT_HEIGHT:-1000}"
session="osb-e2e-$$"
record="${OSB_E2E_RECORD:-1}"
server_pid=""
browser_open=0
recording=0

cat > "$run_dir/cli.config.json" <<EOF
{
  "browser": {
    "browserName": "chromium",
    "launchOptions": {
      "channel": "msedge"
    },
    "contextOptions": {
      "viewport": {
        "width": $viewport_width,
        "height": $viewport_height
      }
    }
  },
  "outputDir": "$run_dir"
}
EOF

pw() {
  playwright-cli -s="$session" "$@"
}

stop_recording() {
  if [[ "$recording" -ne 1 ]]; then
    return
  fi
  pw video-hide-actions >>"$run_dir/recording.log" 2>&1 || true
  pw video-stop >>"$run_dir/recording.log" 2>&1 || true
  pw tracing-stop >>"$run_dir/recording.log" 2>&1 || true
  recording=0
  if [[ ! -s "$run_dir/run.webm" ]]; then
    printf 'Video recording is empty: %s\n' "$run_dir/run.webm" >&2
    return 1
  fi
}

cleanup() {
  local status=$?
  set +e
  if [[ "$browser_open" -eq 1 ]]; then
    pw console >"$run_dir/console.log" 2>&1 || true
    pw requests >"$run_dir/network.log" 2>&1 || true
    if [[ "$status" -ne 0 ]]; then
      pw screenshot --filename "$run_dir/final-failure.png" >"$run_dir/failure-screenshot.log" 2>&1 || true
      sandbox_id="$(pw --raw eval "() => localStorage.getItem('osb-e2e-sandbox-id') || ''" 2>/dev/null | tail -n 1)"
      sandbox_id="${sandbox_id#\"}"
      sandbox_id="${sandbox_id%\"}"
      if [[ "$sandbox_id" =~ ^[A-Za-z0-9._-]+$ ]]; then
        curl --silent --show-error --request DELETE \
          --header 'X-OSB-CSRF: 1' \
          "$base_url/dashboard/sandboxes/$sandbox_id" \
          --output "$run_dir/fallback-cleanup-response.html" || true
      fi
    fi
    stop_recording
    pw close >>"$run_dir/browser.log" 2>&1 || true
  fi
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill -TERM "$server_pid" 2>/dev/null || true
    for _ in $(seq 1 20); do
      kill -0 "$server_pid" 2>/dev/null || break
      sleep 0.25
    done
    kill -KILL "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  printf '\nE2E artifacts: %s\n' "$run_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

printf 'Preparing frontend dependencies...\n'
./scripts/fetch-ghostty-web.sh
./scripts/fetch-ui-assets.sh

printf 'Building dashboard...\n'
go build -o "$run_dir/osb-dashboard" .

server_args=(
  --kubeconfig "$kubeconfig"
  --opensandbox-namespace "${OSB_E2E_OPENSANDBOX_NAMESPACE:-opensandbox-system}"
  --sandbox-namespace "${OSB_E2E_SANDBOX_NAMESPACE:-opensandbox}"
  --sandbox-image "$sandbox_image"
)

printf 'Starting dashboard at %s...\n' "$base_url"
HTTP_ADDR="$address" "$run_dir/osb-dashboard" "${server_args[@]}" >"$run_dir/server.log" 2>&1 &
server_pid=$!

ready=0
for _ in $(seq 1 120); do
  if curl --fail --silent --show-error --max-time 1 "$base_url/healthz" >/dev/null 2>&1; then
    ready=1
    break
  fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    printf 'Dashboard exited before becoming ready.\n' >&2
    tail -80 "$run_dir/server.log" >&2 || true
    exit 1
  fi
  sleep 0.25
done
if [[ "$ready" -ne 1 ]]; then
  printf 'Dashboard did not become ready at %s.\n' "$base_url" >&2
  tail -80 "$run_dir/server.log" >&2 || true
  exit 1
fi

start_url="$base_url/?e2eRun=$run_id&e2eImage=$sandbox_image&e2eKeepSandbox=$keep_sandbox"
printf 'Opening Microsoft Edge...\n'
pw open "$start_url" --browser msedge --config "$run_dir/cli.config.json" >"$run_dir/browser.log" 2>&1
browser_open=1
pw resize "$viewport_width" "$viewport_height" >>"$run_dir/browser.log" 2>&1

if [[ "$record" == "1" ]]; then
  printf 'Recording trace and video...\n'
  pw tracing-start >"$run_dir/recording.log" 2>&1
  pw video-start "$run_dir/run.webm" >>"$run_dir/recording.log" 2>&1
  pw video-show-actions >>"$run_dir/recording.log" 2>&1
  recording=1
fi

printf 'Running live-cluster E2E suites...\n'
: > "$run_dir/suite.log"
suite_status=0
for suite in e2e/tests/*.js; do
  suite_name="$(basename "$suite" .js)"
  printf '\n[%s]\n' "$suite_name" | tee -a "$run_dir/suite.log"
  if [[ "$recording" -eq 1 ]]; then
    pw video-chapter "$suite_name" >>"$run_dir/recording.log" 2>&1 || true
  fi
  suite_output="$run_dir/$suite_name.log"
  set +e
  pw --raw run-code --filename "$suite" 2>&1 | tee "$suite_output" | tee -a "$run_dir/suite.log"
  command_status=${PIPESTATUS[0]}
  set -e
  if [[ "$command_status" -ne 0 ]] || grep -q '^### Error' "$suite_output"; then
    suite_status=1
    break
  fi
done

if [[ "$suite_status" -ne 0 ]]; then
  printf 'E2E suite failed.\n' >&2
  exit "$suite_status"
fi

stop_recording
printf 'E2E suites passed.\n'
