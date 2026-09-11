#!/usr/bin/env bash
set -euo pipefail

engine="${1:-}"
project="${2:-}"
[[ -n "$engine" ]] || { echo "container engine command is required" >&2; exit 2; }
[[ "$project" =~ ^loomcli-sse-ui-(redis|postgres)-[a-z0-9][a-z0-9-]{0,47}$ ]] || {
  echo "refusing unsafe SSE UI Compose project: $project" >&2
  exit 2
}

inspect_kind() {
  local kind="$1"
  shift
  local output
  if ! output="$($engine "$@" -q --filter "label=com.docker.compose.project=$project")"; then
    echo "could not inventory $kind for Compose project $project" >&2
    exit 1
  fi
  if [[ -n "$output" ]]; then
    echo "Compose project already has $kind: $project" >&2
    printf '%s\n' "$output" >&2
    exit 1
  fi
}

inspect_kind containers ps -a
inspect_kind volumes volume ls
inspect_kind networks network ls

# Compose labels are the primary ownership proof. Exact generated names close
# the remaining gap where a manually-created or damaged residual resource has
# lost its labels but `down -v` could still resolve the same project name.
containers="$($engine ps -a --format '{{.Names}}')" || {
  echo "could not inventory exact container names for $project" >&2
  exit 1
}
volumes="$($engine volume ls --format '{{.Name}}')" || {
  echo "could not inventory exact volume names for $project" >&2
  exit 1
}
networks="$($engine network ls --format '{{.Name}}')" || {
  echo "could not inventory exact network names for $project" >&2
  exit 1
}
if grep -Eq "^${project}[-_]" <<<"$containers"; then
  echo "Compose project already has a matching container name: $project" >&2
  exit 1
fi
for name in redis-data loom-data loom-workspace postgres-data; do
  if grep -Fqx "${project}_${name}" <<<"$volumes"; then
    echo "Compose project already has exact volume ${project}_${name}" >&2
    exit 1
  fi
done
if grep -Fqx "${project}_local-mode" <<<"$networks"; then
  echo "Compose project already has exact network ${project}_local-mode" >&2
  exit 1
fi
