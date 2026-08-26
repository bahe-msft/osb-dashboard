#!/usr/bin/env bash
set -euo pipefail

TARGET_ADDR="${SWAPBOOK_TARGET_ADDR:-127.0.0.1:8081}"
SWAPBOOK_PORT="${SWAPBOOK_PORT:-7007}"
TMP_DIR="$(mktemp -d)"
TARGET_PID=""
PROXY_PID=""

cleanup() {
  if [[ -n "$PROXY_PID" ]]; then
    kill "$PROXY_PID" 2>/dev/null || true
    wait "$PROXY_PID" 2>/dev/null || true
  fi
  if [[ -n "$TARGET_PID" ]]; then
    kill "$TARGET_PID" 2>/dev/null || true
    wait "$TARGET_PID" 2>/dev/null || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# Build concrete temporary binaries instead of using `go run` so the signal
# trap owns the actual server PIDs. Stopping a `go run` wrapper can otherwise
# leave its compiled child listening after this launcher exits. The EXIT trap
# removes both binaries with TMP_DIR.
go build -tags swapbook -o "$TMP_DIR/osb-dashboard-swapbook" ./cmd/osb-dashboard-swapbook
go build -o "$TMP_DIR/swapbook" github.com/Aejkatappaja/swapbook/cmd/swapbook
SWAPBOOK_TARGET_ADDR="$TARGET_ADDR" "$TMP_DIR/osb-dashboard-swapbook" &
TARGET_PID=$!

for _ in $(seq 1 40); do
  if curl -fsS "http://$TARGET_ADDR/healthz" >/dev/null; then
    break
  fi
  if ! kill -0 "$TARGET_PID" 2>/dev/null; then
    echo "Swapbook target exited before becoming ready" >&2
    exit 1
  fi
  sleep 0.25
done

if ! curl -fsS "http://$TARGET_ADDR/healthz" >/dev/null; then
  echo "Swapbook target did not become ready at http://$TARGET_ADDR" >&2
  exit 1
fi

echo "Opening Swapbook at http://127.0.0.1:$SWAPBOOK_PORT/__sb/"
"$TMP_DIR/swapbook" --target "http://$TARGET_ADDR" --port "$SWAPBOOK_PORT" &
PROXY_PID=$!
wait "$PROXY_PID"
