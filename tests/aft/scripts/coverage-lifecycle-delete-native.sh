#!/usr/bin/env bash
# Read-only OpenCode session and checkout proof inside this run's owned service.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -eq 3 && "$1" =~ ^(capture|present|deleted)$ && "$2" =~ ^agt_[A-Za-z0-9_-]+$ ]] || exit 2
[[ -f "$3" && ! -L "$3" ]] || exit 2
case "$3" in "$AFT_WORK_DIR"/coverage-lifecycle-delete/*) ;; *) exit 2;; esac
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
receipt="$(base64 < "$3" | tr -d '\n')"
result="$("${compose[@]}" exec -T loom-local node -e "$(cat "$AFT_TESTS_DIR/scripts/coverage-lifecycle-delete-native.js")" "$1" "$2" "$receipt" "$RUN_ID" "$AFT_AGENT_FLOW_REPO")"
printf '%s\n' "$result"
