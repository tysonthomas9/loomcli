#!/usr/bin/env bash

set -euo pipefail

readonly docker_context="${LOOM_KERNEL_DOCKER_CONTEXT:-colima-loom-kernel-browser-221}"
readonly duration_seconds="${1:-30}"
readonly interval_seconds="${LOOM_KERNEL_HEALTH_INTERVAL_SECONDS:-1}"

apps=(
  "app-a:61222"
  "app-b:62222"
)

deadline=$((SECONDS + duration_seconds))
checks=0

while (( SECONDS < deadline )); do
  for entry in "${apps[@]}"; do
    app_id="${entry%%:*}"
    cdp_port="${entry##*:}"
    container="loom-kernel-browser-${app_id}"

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
