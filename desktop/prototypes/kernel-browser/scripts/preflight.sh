#!/usr/bin/env bash

set -euo pipefail

readonly prototype_label="io.loom.prototype=local-kernel-browser"
readonly machine_name="${LOOM_KERNEL_PODMAN_MACHINE:-}"

if ! command -v podman >/dev/null 2>&1; then
  printf 'status=blocked reason=podman-not-installed\n'
  exit 2
fi

printf 'podman=%s\n' "$(command -v podman)"
printf '%s\n' 'machines:'
podman machine list --format json

if [[ -z "$machine_name" ]]; then
  printf 'status=blocked reason=dedicated-machine-not-selected env=LOOM_KERNEL_PODMAN_MACHINE\n'
  exit 2
fi

if [[ "$machine_name" == "podman-machine-default" ]]; then
  printf 'status=blocked reason=shared-default-machine-refused machine=%s\n' "$machine_name"
  exit 2
fi

if ! machine_state="$(podman machine inspect --format '{{.State}}' "$machine_name" 2>/dev/null)"; then
  printf 'status=blocked reason=dedicated-machine-not-found machine=%s\n' "$machine_name"
  exit 2
fi

printf 'selected-machine=%s state=%s\n' "$machine_name" "$machine_state"

if [[ "$machine_state" != "running" ]]; then
  printf 'status=blocked reason=dedicated-machine-not-running machine=%s\n' "$machine_name"
  exit 2
fi

if ! podman --connection "$machine_name" info --format json >/dev/null 2>&1; then
  printf 'status=blocked reason=dedicated-machine-unreachable machine=%s\n' "$machine_name"
  exit 2
fi

printf '%s\n' 'prototype-owned-containers:'
podman --connection "$machine_name" ps --all --filter "label=${prototype_label}" --format json

printf 'status=ready machine=%s\n' "$machine_name"
