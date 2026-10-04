#!/usr/bin/env bash
# Guard for LOCAL_MODE_AGENTS_REAL=1 make local-mode-agents-up: REAL stacks
# mount the host's live OpenCode data folder (opencode.db) read-write, so only
# one may run at a time. Fails when a container of another compose project
# carries the loom.local-mode.opencode-host-data label; a re-up of the same
# project passes. Also warns when a host process has that opencode.db open,
# since both writing it at once risks corruption. Prints no credential data.
#
# Usage: real-opencode-guard.sh <compose project>
set -euo pipefail

project="${1:?usage: real-opencode-guard.sh <compose project>}"
data="${LOCAL_MODE_OPENCODE_DATA:-${HOME}/.local/share/opencode}"

if [ ! -f "$data/opencode.db" ]; then
  echo "local-mode: no OpenCode database at $data/opencode.db; run \`opencode auth login\` on the host first (or set LOCAL_MODE_OPENCODE_DATA)" >&2
  exit 1
fi

engine=""
for e in podman docker; do
  if command -v "$e" >/dev/null 2>&1; then engine="$e"; break; fi
done
if [ -n "$engine" ]; then
  others="$("$engine" ps --filter label=loom.local-mode.opencode-host-data=1 \
    --format '{{.Names}} {{index .Labels "com.docker.compose.project"}}' 2>/dev/null \
    | awk -v p="$project" '$2 != p { print "  " $1 " (project " $2 ")" }')"
  if [ -n "$others" ]; then
    {
      echo "local-mode: another REAL stack already shares the host OpenCode data folder:"
      echo "$others"
      echo "Only one REAL stack may run at a time; tear that one down first."
    } >&2
    exit 1
  fi
fi

if command -v lsof >/dev/null 2>&1 && lsof -t "$data/opencode.db" >/dev/null 2>&1; then
  echo "local-mode: WARNING: a host process has $data/opencode.db open (your desktop OpenCode?); it and this REAL stack writing at the same time risks corrupting it." >&2
fi
