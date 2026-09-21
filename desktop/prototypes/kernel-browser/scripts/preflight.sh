#!/usr/bin/env bash

set -euo pipefail

readonly prototype_label="io.loom.prototype=local-kernel-browser"

if ! command -v podman >/dev/null 2>&1; then
  printf 'status=blocked reason=podman-not-installed\n'
  exit 2
fi

printf 'podman=%s\n' "$(command -v podman)"
printf '%s\n' 'machines:'
podman machine list --format json

if ! podman info --format json >/dev/null 2>&1; then
  printf 'status=blocked reason=podman-machine-unreachable\n'
  exit 2
fi

printf '%s\n' 'prototype-owned-containers:'
podman ps --all --filter "label=${prototype_label}" --format json

printf 'status=ready\n'
