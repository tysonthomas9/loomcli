#!/usr/bin/env bash
set -euo pipefail

engine=$1 project=$2
shift 2
[[ $project != loomcli-local-mode && $project =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ ]] || exit 0
[[ $engine == podman || $engine == docker ]] || exit 2

for image in "$@"; do
    [[ $image == "$project"-*:latest ]] || continue
    "$engine" image inspect --format '{{.Id}}' "$image" >/dev/null 2>&1 || continue
    if "$engine" ps -a --format '{{.Image}}' | grep -Fxq "$image"; then
        echo "Keeping in-use image $image" >&2
        continue
    fi
    if "$engine" image rm "$image" >/dev/null; then
        echo "Removed private stack image $image" >&2
    fi
done
