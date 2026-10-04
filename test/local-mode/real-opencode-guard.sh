#!/usr/bin/env bash
# Guard for LOCAL_MODE_AGENTS_REAL=1 make local-mode-agents-up. REAL stacks
# mount the host's live OpenCode data folder (opencode.db) read-write, so
# before each boot this:
#   1. fails when the host has no opencode.db;
#   2. fails when a container of another compose project carries the
#      loom.local-mode.opencode-host-data label (one REAL stack at a time; a
#      re-up of the same project passes);
#   3. fails when a host OpenCode process has opencode.db open, unless
#      LOCAL_MODE_OPENCODE_SHARED_OK=1;
#   4. takes a consistent online backup (sqlite3 .backup, which includes the
#      WAL) to opencode.db.loom-backup-<UTC time> in the same folder, mode 600,
#      keeping the newest LOCAL_MODE_OPENCODE_BACKUPS (default 3).
# Prints no credential data: only paths, process names and PIDs.
#
# Usage: real-opencode-guard.sh <compose project>
set -euo pipefail

project="${1:?usage: real-opencode-guard.sh <compose project>}"
data="${LOCAL_MODE_OPENCODE_DATA:-${HOME}/.local/share/opencode}"
db="$data/opencode.db"
keep="${LOCAL_MODE_OPENCODE_BACKUPS:-3}"

if [ ! -f "$db" ]; then
  echo "local-mode: no OpenCode database at $db; run \`opencode auth login\` on the host first (or set LOCAL_MODE_OPENCODE_DATA)" >&2
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

if command -v lsof >/dev/null 2>&1; then
  holders=""
  for pid in $(lsof -t "$db" "$db-wal" 2>/dev/null | sort -u); do
    comm="$(ps -o comm= -p "$pid" 2>/dev/null || true)"
    case "$(printf '%s' "$comm" | tr '[:upper:]' '[:lower:]')" in
      *opencode*) holders="${holders}  pid ${pid} ${comm}"$'\n' ;;
    esac
  done
  if [ -n "$holders" ]; then
    if [ "${LOCAL_MODE_OPENCODE_SHARED_OK:-}" = 1 ]; then
      echo "local-mode: WARNING: a host OpenCode process has $db open; LOCAL_MODE_OPENCODE_SHARED_OK=1, starting anyway (both writing at once risks corrupting it)." >&2
    else
      {
        echo "local-mode: a host OpenCode process has $db open:"
        printf '%s' "$holders"
        echo "A REAL stack writes the same database; both at once risks corrupting it."
        echo "Quit host OpenCode first, or set LOCAL_MODE_OPENCODE_SHARED_OK=1 to start anyway."
      } >&2
      exit 1
    fi
  fi
fi

command -v sqlite3 >/dev/null 2>&1 || { echo "local-mode: sqlite3 is needed to back up $db before a REAL boot" >&2; exit 1; }
backup="$db.loom-backup-$(date -u +%Y%m%dT%H%M%SZ)"
(
  umask 077
  sqlite3 -readonly "$db" ".backup '$backup'"
)
chmod 600 "$backup"
echo "local-mode: backed up $db to $backup"
# Newest first by name (UTC timestamp); drop all but the newest $keep.
find "$data" -maxdepth 1 -name 'opencode.db.loom-backup-*' -type f | sort -r \
  | tail -n "+$((keep + 1))" | while IFS= read -r old; do rm -f -- "$old"; done
