#!/usr/bin/env bash
# Called only by with-heavy-lock.sh while its machine-wide lock is held.
set -euo pipefail

lock_owner="$HOME/.cache/loom/heavy.lock/owner"
if [[ ! ${LOOM_HEAVY_LOCK_HELD:-} =~ ^[0-9]+$ || $PPID != "$LOOM_HEAVY_LOCK_HELD" || ! -f $lock_owner ]]; then
    echo "Disk cleanup requires the heavy-run lock" >&2
    exit 1
fi
holder=
IFS= read -r holder < "$lock_owner" || true
if [[ $holder != "$LOOM_HEAVY_LOCK_HELD" ]]; then
    echo "Disk cleanup requires the heavy-run lock" >&2
    exit 1
fi

budget_kib=$((40 * 1024 * 1024))
urgent_kib=$((20 * 1024 * 1024))
log=${LOOM_DISK_CLEANUP_LOG:-$HOME/.cache/loom/cleanup.log}
podman=${LOOM_DISK_PODMAN:-podman}
podman_connection="podman-machine-default"

podman_run() {
    "$podman" --connection "$podman_connection" "$@"
}

free_kib() {
    if [[ -n ${LOOM_DISK_FREE_KIB_OVERRIDE:-} ]]; then
        [[ $LOOM_DISK_FREE_KIB_OVERRIDE =~ ^[0-9]+$ ]] || return 1
        printf '%s\n' "$LOOM_DISK_FREE_KIB_OVERRIDE"
    else
        df -Pk "$HOME" | awk 'NR == 2 { print $4 }'
    fi
}

record() {
    printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >> "$log"
}

trim_go_cache() {
    local unused_hours=$1 file bytes=0 count=0 size
    # Go refreshes an entry's mtime at most hourly, so allow that hour of lag.
    local mtime_minutes=$(((unused_hours + 1) * 60))
    local cache=${LOOM_DISK_GOCACHE:-$(go env GOCACHE)}
    [[ -d $cache ]] || return 0
    while IFS= read -r -d '' file; do
        if [[ -d $file ]]; then
            size=$(du -sk "$file" | awk '{ print $1 * 1024 }') || continue
            rm -r -- "$file" || continue
        else
            size=$(wc -c < "$file") || continue
            rm -f -- "$file" || continue
        fi
        if [[ ! -e $file ]]; then
            bytes=$((bytes + size))
            count=$((count + 1))
        fi
    done < <(find "$cache" -mindepth 2 -maxdepth 2 \( -type f -o -type d \) \( -name '*-a' -o -name '*-d' \) -mmin "+$mtime_minutes" -print0)
    record "Go cache: removed $count entries ($bytes bytes), unused over ${unused_hours}h"
}

trim_images() {
    local ref short_ref id labels in_use newest_lab_kept=false
    record "Podman: using connection $podman_connection"
    if ! command -v "$podman" >/dev/null 2>&1 || ! podman_run info >/dev/null 2>&1; then
        record "Podman: connection $podman_connection unavailable; image cleanup and fstrim skipped"
        return 1
    fi
    while IFS='|' read -r ref id; do
        short_ref=${ref##*/}
        [[ $short_ref =~ ^(loomcli|loomgit)-[a-zA-Z0-9_.-]+-(fleet-db|loom|loom-codex|loom-claude):[^[:space:]]+$ || $short_ref =~ ^loomgit-lab:[a-zA-Z0-9_.-]+$ ]] || continue
        if [[ $short_ref == loomgit-lab:* && $newest_lab_kept == false ]]; then
            newest_lab_kept=true
            record "Podman: kept newest Git-lab image $ref"
            continue
        fi
        [[ ! $short_ref =~ ^loomcli-local-mode-(fleet-db|loom|loom-codex|loom-claude): ]] || continue
        [[ ${id#sha256:} =~ ^[a-f0-9]+$ ]] || continue
        labels=$(podman_run image inspect --format '{{json .Labels}}' "$ref" 2>/dev/null) || continue
        [[ $labels != *loomcli-local-mode* ]] || continue
        in_use=$(podman_run ps -a --no-trunc --format '{{.ImageID}}' 2>/dev/null) || return 1
        if printf '%s\n' "$in_use" | grep -Fxq "${id#sha256:}" || printf '%s\n' "$in_use" | grep -Fxq "sha256:${id#sha256:}"; then
            record "Podman: kept in-use image $ref"
            continue
        fi
        if podman_run image rm "$ref" >/dev/null 2>&1; then
            record "Podman: removed image $ref ($id)"
        else
            record "Podman: kept image $ref (in use or removal failed)"
        fi
    done < <(podman_run image ls --sort created --no-trunc --format '{{.Repository}}:{{.Tag}}|{{.ID}}')
    return 0
}

before=$(free_kib)
[[ $before =~ ^[0-9]+$ ]] || { echo "Unable to measure free disk space" >&2; exit 1; }
mkdir -p "${log%/*}"
if (( before >= budget_kib )); then
    record "Budget check: ${before} KiB free; removed nothing (0 KiB freed)"
    exit 0
fi
record "Budget check: ${before} KiB free, target ${budget_kib} KiB"
trim_go_cache 24
after=$(free_kib)
if (( after < urgent_kib )); then
    trim_go_cache 6
fi
after=$(free_kib)
if (( after < budget_kib )); then
    if trim_images; then
        "$podman" machine ssh podman-machine-default sudo fstrim -av >> "$log" 2>&1 || record "Podman: fstrim unavailable"
    fi
fi
after=$(free_kib)
record "Budget check finished: ${after} KiB free; gained $((after - before)) KiB"
