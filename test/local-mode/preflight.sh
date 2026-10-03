#!/usr/bin/env bash
# Preflight for the local-mode `make local-mode-*-up` targets.
#
# Fails fast when a chosen host port is already listening, unless the project
# already has containers (a re-up of the same stack holds its own ports).
#
# Usage: preflight.sh <compose command and args...>
#   e.g. preflight.sh podman compose -p loomcli-local-mode-x -f test/local-mode/docker-compose.yml
set -euo pipefail

project="${LOCAL_MODE_COMPOSE_PROJECT:-loomcli-local-mode}"

if [ "$#" -gt 0 ] && [ -n "$("$@" ps -a -q 2>/dev/null || true)" ]; then
  echo "local-mode: project ${project} already has containers; skipping the port check."
  exit 0
fi

port_in_use() {
  local port="$1"
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1
  else
    (exec 3<>"/dev/tcp/127.0.0.1/$port") >/dev/null 2>&1
  fi
}

busy=()
for spec in \
  "LOCAL_MODE_FLEETDB_PORT:${LOCAL_MODE_FLEETDB_PORT:-8280}" \
  "LOCAL_MODE_API_PORT:${LOCAL_MODE_API_PORT:-8282}" \
  "LOCAL_MODE_UI_PORT:${LOCAL_MODE_UI_PORT:-8283}"; do
  name="${spec%%:*}"
  port="${spec#*:}"
  if port_in_use "$port"; then
    busy+=("${name}=${port}")
  fi
done

if [ "${#busy[@]}" -gt 0 ]; then
  {
    echo "local-mode: port(s) already in use: ${busy[*]}"
    echo "  Pick a free block (check with: lsof -nP -iTCP -sTCP:LISTEN) and set"
    echo "  LOCAL_MODE_FLEETDB_PORT, LOCAL_MODE_API_PORT and LOCAL_MODE_UI_PORT."
  } >&2
  exit 1
fi
