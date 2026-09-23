#!/usr/bin/env bash

set -euo pipefail

readonly app_id="${1:-}"
if [[ ! "$app_id" =~ ^app-[a-z]$ ]]; then
  printf 'browser app id must match app-[a-z]\n' >&2
  exit 2
fi
shift
if [[ $# -eq 0 ]]; then
  printf 'usage: %s <app-id> <agent-browser command> [args...]\n' "$0" >&2
  exit 2
fi

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly state_dir="${LOOM_KERNEL_STATE_DIR:-$(dirname "$script_dir")/.runtime}"
readonly runtime_id_file="$state_dir/runtime-id"
readonly control_origin="${LOOM_KERNEL_CONTROL_ORIGIN:-http://127.0.0.1:61300}"
readonly curl_bin="${LOOM_KERNEL_CURL_BIN:-curl}"
readonly node_bin="${LOOM_KERNEL_NODE_BIN:-node}"
readonly agent_browser_bin="${LOOM_KERNEL_AGENT_BROWSER_BIN:-agent-browser}"

runtime_id="${LOOM_KERNEL_RUNTIME_ID:-}"
if [[ -z "$runtime_id" && -f "$runtime_id_file" ]]; then
  runtime_id="$(<"$runtime_id_file")"
fi
if [[ -z "$runtime_id" ]]; then
  printf 'browser runtime id is unavailable; start the POC runtime first\n' >&2
  exit 2
fi

status_json="$($curl_bin -fsS "$control_origin/api/status/$app_id")"
read -r cdp_port target_id < <(printf '%s' "$status_json" | "$node_bin" -e '
  const fs = require("node:fs");
  const status = JSON.parse(fs.readFileSync(0, "utf8"));
  if (!Number.isInteger(status.cdpPort) || status.cdpPort < 1 || status.cdpPort > 65535) process.exit(2);
  if (typeof status.targetId !== "string" || !/^[a-zA-Z0-9_-]+$/.test(status.targetId)) process.exit(2);
  process.stdout.write(`${status.cdpPort} ${status.targetId}\n`);
')
runtime_token="${runtime_id//[^a-zA-Z0-9_.-]/-}"
session_name="loom-${runtime_token}-${app_id}"

tabs_json="$("$agent_browser_bin" \
  --session "$session_name" \
  --cdp "$cdp_port" \
  tab list --json)"
active_target_id="$(printf '%s' "$tabs_json" | "$node_bin" -e '
  const fs = require("node:fs");
  const result = JSON.parse(fs.readFileSync(0, "utf8"));
  const active = result?.data?.tabs?.find((tab) => tab.active);
  if (active && typeof active.targetId === "string") process.stdout.write(active.targetId);
')"
if [[ "$active_target_id" != "$target_id" ]]; then
  "$agent_browser_bin" \
    --session "$session_name" \
    --cdp "$cdp_port" \
    tab "$target_id" >/dev/null
fi

exec "$agent_browser_bin" \
  --session "$session_name" \
  --cdp "$cdp_port" \
  --pin-tab \
  "$@"
