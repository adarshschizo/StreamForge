#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
api_log="$repo_root/api-smoke.log"
external_api_url="${STREAMFORGE_API_URL:-}"
api_pid=""
api_binary=""

export STREAMFORGE_DATABASE_MODE=memory
export STREAMFORGE_STORAGE_MODE=memory
export STREAMFORGE_QUEUE_MODE=memory
export STREAMFORGE_JWT_SECRET="${STREAMFORGE_JWT_SECRET:-development-only-secret-change-me-32chars}"

cleanup() {
  if [[ -n "$api_pid" ]] && kill -0 "$api_pid" 2>/dev/null; then
    kill "$api_pid"
    wait "$api_pid" 2>/dev/null || true
  fi
  if [[ -n "$api_binary" ]]; then
    rm -f -- "$api_binary"
  fi
}
trap cleanup EXIT

if [[ -n "$external_api_url" ]]; then
  api_url="${external_api_url%/}"
else
  api_addr="${STREAMFORGE_API_ADDR:-127.0.0.1:8080}"
  api_port="${api_addr##*:}"
  api_url="http://127.0.0.1:$api_port"
  export STREAMFORGE_API_ADDR="$api_addr"

  api_binary="$(mktemp "${TMPDIR:-/tmp}/streamforge-api-smoke.XXXXXX")"
  (cd "$repo_root" && go build -o "$api_binary" ./apps/api)
  "$api_binary" > "$api_log" 2>&1 &
  api_pid=$!

  healthy=false
  for i in $(seq 1 30); do
    if ! kill -0 "$api_pid" 2>/dev/null; then
      cat "$api_log" >&2
      echo "API process exited before becoming healthy" >&2
      exit 1
    fi
    if curl --fail --silent "$api_url/health" >/dev/null 2>&1; then
      healthy=true
      break
    fi
    sleep 1
  done
  if [[ "$healthy" != true ]]; then
    echo "API did not become healthy on $api_url" >&2
    exit 1
  fi
fi

export STREAMFORGE_API_URL="$api_url"
k6 run "$repo_root/tests/load/api-smoke.js"
