#!/usr/bin/env bash
# scripts/clean-stale-tmp.sh — sweep orphaned loom/go temp dirs from $TMPDIR.
#
# In-script cleanup (trap ... EXIT INT TERM, see scripts/lib/sandbox.sh) removes
# sandboxes on normal exit and on Ctrl-C / timeout. It CANNOT fire on SIGKILL
# (OOM killer, `podman machine` force-stop, laptop hard sleep). This script is
# the backstop for that case: it removes loom sandboxes and orphaned `go`
# build/link scratch that are older than an age threshold and have no open file
# handles, so a killed run's leftovers get reclaimed on the next sweep.
#
# Safe by construction: only touches known-regenerable temp dirs, skips anything
# newer than --age-hours, and skips anything with an open handle (lsof).
#
# Usage:
#   scripts/clean-stale-tmp.sh                 # sweep dirs older than 12h
#   scripts/clean-stale-tmp.sh --dry-run       # show what would be removed
#   scripts/clean-stale-tmp.sh --age-hours 2   # more aggressive threshold
#   scripts/clean-stale-tmp.sh --root /tmp     # sweep a specific temp root

set -uo pipefail

AGE_HOURS=12
DRY_RUN=0
ROOT="${TMPDIR:-/tmp}"
ROOT="${ROOT%/}"

while [ $# -gt 0 ]; do
  case "$1" in
    --age-hours) AGE_HOURS="$2"; shift 2 ;;
    --dry-run)   DRY_RUN=1; shift ;;
    --root)      ROOT="${2%/}"; shift 2 ;;
    -h|--help)   sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)           echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

# Globs of reclaimable entries under $ROOT:
#   loom/           — predictable sandbox root (scripts/lib/sandbox.sh)
#   loom-*.*        — legacy sandboxes from `mktemp -d -t loom-<name>.XXXXXX`
#   go-build* / go-link* — `go` scratch orphaned when a build/test was killed
#     (only the uncontained ones land here; GOTMPDIR-contained scratch dies with
#      its sandbox, so this mostly catches non-loom or pre-fix runs)
patterns=(
  "$ROOT"/loom/*
  "$ROOT"/loom-*.*
  "$ROOT"/go-build*
  "$ROOT"/go-link*
)

now=$(date +%s)
age_secs=$(( AGE_HOURS * 3600 ))
removed=0
freed_kb=0
skipped_open=0
skipped_young=0

in_use() { lsof +D "$1" >/dev/null 2>&1 || lsof "$1" >/dev/null 2>&1; }

for entry in "${patterns[@]}"; do
  [ -e "$entry" ] || continue           # unmatched glob → literal, skip
  # mtime-based age (BSD stat on macOS; GNU stat fallback for Linux/CI).
  mtime=$(stat -f %m "$entry" 2>/dev/null || stat -c %Y "$entry" 2>/dev/null) || continue
  age=$(( now - mtime ))
  if [ "$age" -lt "$age_secs" ]; then
    skipped_young=$(( skipped_young + 1 )); continue
  fi
  if in_use "$entry"; then
    echo "skip (open handle): $entry"
    skipped_open=$(( skipped_open + 1 )); continue
  fi
  size_kb=$(du -sk "$entry" 2>/dev/null | awk '{print $1}')
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "would remove: $entry (${size_kb:-0} KB)"
  else
    chmod -R u+w "$entry" 2>/dev/null || true   # Go module cache paths are 0444
    rm -rf "$entry" && { removed=$(( removed + 1 )); freed_kb=$(( freed_kb + ${size_kb:-0} )); }
  fi
done

if [ "$DRY_RUN" -eq 1 ]; then
  echo "dry-run: $skipped_young too new, $skipped_open in use"
else
  echo "removed $removed entries, freed $(( freed_kb / 1024 )) MB (skipped: $skipped_young too new, $skipped_open in use)"
fi
