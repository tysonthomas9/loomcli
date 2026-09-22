#!/usr/bin/env bash

set -euo pipefail

readonly docker_context="${LOOM_KERNEL_DOCKER_CONTEXT:-colima-loom-kernel-browser-221}"
readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly state_dir="${LOOM_KERNEL_STATE_DIR:-$(dirname "$script_dir")/.runtime}"
readonly runtime_id_file="$state_dir/runtime-id"
readonly duration_seconds="${1:-30}"
readonly interval_seconds="${LOOM_KERNEL_HEALTH_INTERVAL_SECONDS:-1}"
readonly app_a_cdp_port="${LOOM_KERNEL_APP_A_CDP_PORT:-61222}"
readonly app_b_cdp_port="${LOOM_KERNEL_APP_B_CDP_PORT:-62222}"

if [[ -n "${LOOM_KERNEL_RUNTIME_ID:-}" ]]; then
  runtime_id="$LOOM_KERNEL_RUNTIME_ID"
elif [[ -f "$runtime_id_file" ]]; then
  runtime_id="$(<"$runtime_id_file")"
else
  printf 'status=failed reason=no-runtime-id\n' >&2
  exit 2
fi
runtime_token="${runtime_id//[^a-zA-Z0-9_.-]/-}"

apps=(
  "app-a:${app_a_cdp_port}"
  "app-b:${app_b_cdp_port}"
)

deadline=$((SECONDS + duration_seconds))
checks=0

while (( SECONDS < deadline )); do
  for entry in "${apps[@]}"; do
    app_id="${entry%%:*}"
    cdp_port="${entry##*:}"
    container="loom-kernel-browser-${runtime_token}-${app_id}"

    if ! curl --silent --show-error --fail --max-time 2 \
      "http://127.0.0.1:${cdp_port}/json/version" >/dev/null; then
      printf 'status=failed app=%s component=cdp check=%d\n' "$app_id" "$checks" >&2
      exit 1
    fi

    if ! docker --context "$docker_context" exec "$container" \
      sh -lc 'DISPLAY=:1 timeout 2 xdotool getmouselocation >/dev/null'; then
      printf 'status=failed app=%s component=x11-input check=%d\n' "$app_id" "$checks" >&2
      exit 1
    fi
  done

  checks=$((checks + 1))
  sleep "$interval_seconds"
done

printf 'status=healthy duration_seconds=%s checks=%s\n' "$duration_seconds" "$checks"
