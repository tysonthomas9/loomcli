#!/usr/bin/env bash

set -euo pipefail

readonly docker_context="${LOOM_KERNEL_DOCKER_CONTEXT:-colima-loom-kernel-browser-221}"
readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly state_dir="${LOOM_KERNEL_STATE_DIR:-$(dirname "$script_dir")/.runtime}"
readonly runtime_id_file="$state_dir/runtime-id"
readonly image="onkernel/chromium-headful@sha256:7aa6dc616440fbe3f8886cec700dc7533aa2a0ec29b999102aa6cad4ac3e6f50"
readonly prototype_label="io.loom.prototype=local-kernel-browser"
readonly app_a_live_port="${LOOM_KERNEL_APP_A_LIVE_PORT:-61080}"
readonly app_a_cdp_port="${LOOM_KERNEL_APP_A_CDP_PORT:-61222}"
readonly app_a_webdriver_port="${LOOM_KERNEL_APP_A_WEBDRIVER_PORT:-61224}"
readonly app_a_api_port="${LOOM_KERNEL_APP_A_API_PORT:-61101}"
readonly app_a_media_port="${LOOM_KERNEL_APP_A_MEDIA_PORT:-56000}"
readonly app_b_live_port="${LOOM_KERNEL_APP_B_LIVE_PORT:-62080}"
readonly app_b_cdp_port="${LOOM_KERNEL_APP_B_CDP_PORT:-62222}"
readonly app_b_webdriver_port="${LOOM_KERNEL_APP_B_WEBDRIVER_PORT:-62224}"
readonly app_b_api_port="${LOOM_KERNEL_APP_B_API_PORT:-62101}"
readonly app_b_media_port="${LOOM_KERNEL_APP_B_MEDIA_PORT:-56100}"
readonly chromium_flags="--user-data-dir=/home/kernel/user-data --disable-dev-shm-usage --start-maximized --remote-allow-origins=* --no-sandbox --no-zygote"
# Human input is dispatched through Chromium CDP instead of Neko's emulated
# amd64 Xorg input driver, which deadlocks after pointer activity on Apple silicon.
readonly video_pipeline="ximagesrc display-name={display} show-pointer=false use-damage=true ! video/x-raw,framerate=25/1 ! videoconvert ! queue ! vp8enc name=encoder deadline=1 target-bitrate=1996800 cpu-used=4 threads=4 ! appsink name=appsink"
readonly video_pipelines="{\"main\":{\"gst_pipeline\":\"${video_pipeline}\"},\"legacy\":{\"gst_pipeline\":\"${video_pipeline}\"}}"

docker_cmd=(docker --context "$docker_context")
runtime_id=""
runtime_token=""
runtime_label=""

init_runtime_id() {
  local create="${1:-false}"
  if [[ -n "${LOOM_KERNEL_RUNTIME_ID:-}" ]]; then
    runtime_id="$LOOM_KERNEL_RUNTIME_ID"
  elif [[ -f "$runtime_id_file" ]]; then
    runtime_id="$(<"$runtime_id_file")"
  elif [[ "$create" == true ]]; then
    runtime_id="kernel-$(date -u +%Y%m%d%H%M%S)-$$-${RANDOM}"
    mkdir -p "$state_dir"
    (umask 077; printf '%s\n' "$runtime_id" > "$runtime_id_file")
  else
    return 1
  fi
  runtime_token="${runtime_id//[^a-zA-Z0-9_.-]/-}"
  runtime_label="io.loom.runtime-id=${runtime_id}"
}

require_runtime() {
  "${docker_cmd[@]}" info >/dev/null
}

container_name() {
  printf 'loom-kernel-browser-%s-%s\n' "$runtime_token" "$1"
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
    -e NEKO_DESKTOP_INPUT_ENABLED=false \
    -e NEKO_CAPTURE_VIDEO_IDS=main \
    -e "NEKO_CAPTURE_VIDEO_PIPELINES=$video_pipelines" \
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

cleanup_failed_start() {
  local status=$?
  trap - EXIT
  if (( status != 0 )); then
    set +e
    stop_owned
  fi
  exit "$status"
}

validate_add_argument() {
  local value="$1"
  local pattern="$2"
  [[ "$value" =~ $pattern ]] || {
    printf 'status=blocked reason=invalid-add-argument value=%s\n' "$value" >&2
    exit 2
  }
}

case "${1:-}" in
  start)
    init_runtime_id true
    require_runtime
    for app_id in app-a app-b; do
      name="$(container_name "$app_id")"
      if "${docker_cmd[@]}" container inspect "$name" >/dev/null 2>&1; then
        printf 'status=blocked reason=container-name-in-use name=%s\n' "$name" >&2
        exit 2
      fi
    done
    trap cleanup_failed_start EXIT
    start_app app-a "$app_a_live_port" "$app_a_cdp_port" "$app_a_webdriver_port" "$app_a_api_port" "$app_a_media_port"
    start_app app-b "$app_b_live_port" "$app_b_cdp_port" "$app_b_webdriver_port" "$app_b_api_port" "$app_b_media_port"
    trap - EXIT
    ;;
  add)
    [[ $# -eq 7 ]] || {
      printf 'usage: %s add <app-id> <live-port> <cdp-port> <webdriver-port> <api-port> <media-port>\n' "$0" >&2
      exit 2
    }
    validate_add_argument "$2" '^app-[a-z]$'
    for port_value in "${@:3}"; do
      validate_add_argument "$port_value" '^[0-9]{4,5}$'
    done
    init_runtime_id false || {
      printf 'status=blocked reason=no-runtime-id\n' >&2
      exit 2
    }
    require_runtime
    name="$(container_name "$2")"
    if "${docker_cmd[@]}" container inspect "$name" >/dev/null 2>&1; then
      printf 'status=blocked reason=container-name-in-use name=%s\n' "$name" >&2
      exit 2
    fi
    start_app "$2" "$3" "$4" "$5" "$6" "$7"
    ;;
  status)
    if ! init_runtime_id false; then
      printf 'status=clean reason=no-runtime-id\n'
      exit 0
    fi
    require_runtime
    "${docker_cmd[@]}" ps --all --filter "label=$prototype_label" --filter "label=$runtime_label" --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'
    ;;
  stop)
    if ! init_runtime_id false; then
      printf 'status=clean reason=no-runtime-id\n'
      exit 0
    fi
    require_runtime
    stop_owned
    if [[ -z "${LOOM_KERNEL_RUNTIME_ID:-}" ]]; then
      rm -f "$runtime_id_file"
    fi
    ;;
  *)
    printf 'usage: %s {start|add|status|stop}\n' "$0" >&2
    exit 2
    ;;
esac
