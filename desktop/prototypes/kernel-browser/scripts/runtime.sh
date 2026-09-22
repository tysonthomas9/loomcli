#!/usr/bin/env bash

set -euo pipefail

readonly docker_context="${LOOM_KERNEL_DOCKER_CONTEXT:-colima-loom-kernel-browser-221}"
readonly runtime_id="${LOOM_KERNEL_RUNTIME_ID:-LOOMCLI-221}"
readonly image="onkernel/chromium-headful@sha256:7aa6dc616440fbe3f8886cec700dc7533aa2a0ec29b999102aa6cad4ac3e6f50"
readonly prototype_label="io.loom.prototype=local-kernel-browser"
readonly runtime_label="io.loom.runtime-id=${runtime_id}"
readonly chromium_flags="--user-data-dir=/home/kernel/user-data --disable-dev-shm-usage --disable-gpu --start-maximized --disable-software-rasterizer --remote-allow-origins=* --no-sandbox --no-zygote"

docker_cmd=(docker --context "$docker_context")

require_runtime() {
  "${docker_cmd[@]}" info >/dev/null
}

container_name() {
  printf 'loom-kernel-browser-%s\n' "$1"
}

start_app() {
  local app_id="$1"
  local live_port="$2"
  local cdp_port="$3"
  local webdriver_port="$4"
  local api_port="$5"
  local media_port="$6"
  local name
  name="$(container_name "$app_id")"

  if "${docker_cmd[@]}" container inspect "$name" >/dev/null 2>&1; then
    printf 'status=blocked reason=container-name-in-use name=%s\n' "$name" >&2
    exit 2
  fi

  "${docker_cmd[@]}" run -d \
    --name "$name" \
    --platform linux/amd64 \
    --privileged \
    --tmpfs /dev/shm:size=2g \
    --memory 8192m \
    --label "$prototype_label" \
    --label "$runtime_label" \
    --label "io.loom.browser-app=$app_id" \
    -p "127.0.0.1:${live_port}:8080" \
    -p "127.0.0.1:${cdp_port}:9222" \
    -p "127.0.0.1:${webdriver_port}:9224" \
    -p "127.0.0.1:${api_port}:10001" \
    -p "127.0.0.1:${media_port}:${media_port}/tcp" \
    -e DISPLAY_NUM=1 \
    -e HEIGHT=900 \
    -e WIDTH=1440 \
    -e TZ=America/Los_Angeles \
    -e ENABLE_WEBRTC=true \
    -e "NEKO_WEBRTC_TCPMUX=${media_port}" \
    -e NEKO_WEBRTC_NAT1TO1=127.0.0.1 \
    -e RUN_AS_ROOT=true \
    -e "CHROMIUM_FLAGS=$chromium_flags" \
    "$image" >/dev/null

  for _ in {1..45}; do
    if curl --silent --show-error --fail --max-time 2 "http://127.0.0.1:${cdp_port}/json/version" >/dev/null 2>&1; then
      printf 'status=ready app=%s live=http://127.0.0.1:%s cdp=http://127.0.0.1:%s\n' "$app_id" "$live_port" "$cdp_port"
      return
    fi
    sleep 1
  done

  printf 'status=failed reason=cdp-not-ready app=%s container=%s\n' "$app_id" "$name" >&2
  "${docker_cmd[@]}" logs --tail 80 "$name" >&2
  exit 1
}

stop_owned() {
  local ids
  ids="$("${docker_cmd[@]}" ps --all --quiet --filter "label=$prototype_label" --filter "label=$runtime_label")"
  if [[ -z "$ids" ]]; then
    printf 'status=clean runtime=%s\n' "$runtime_id"
    return
  fi
  while IFS= read -r id; do
    [[ -n "$id" ]] || continue
    "${docker_cmd[@]}" rm --force "$id" >/dev/null
  done <<< "$ids"
  printf 'status=stopped runtime=%s\n' "$runtime_id"
}

case "${1:-}" in
  start)
    require_runtime
    for app_id in app-a app-b; do
      name="$(container_name "$app_id")"
      if "${docker_cmd[@]}" container inspect "$name" >/dev/null 2>&1; then
        printf 'status=blocked reason=container-name-in-use name=%s\n' "$name" >&2
        exit 2
      fi
    done
    trap stop_owned ERR
    start_app app-a 61080 61222 61224 61101 56000
    start_app app-b 62080 62222 62224 62101 56100
    trap - ERR
    ;;
  status)
    require_runtime
    "${docker_cmd[@]}" ps --all --filter "label=$prototype_label" --filter "label=$runtime_label" --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'
    ;;
  stop)
    require_runtime
    stop_owned
    ;;
  *)
    printf 'usage: %s {start|status|stop}\n' "$0" >&2
    exit 2
    ;;
esac
