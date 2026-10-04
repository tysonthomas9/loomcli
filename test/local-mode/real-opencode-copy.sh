#!/usr/bin/env bash
# Private OpenCode login for LOCAL_MODE_AGENTS_REAL=1 Agent API stacks.
#
#   real-opencode-copy.sh make <copy>    before `up`
#   real-opencode-copy.sh remove <copy>  after `down`
#
# `make` copies the host's opencode.db (and its -wal) with plain reads into a
# private snapshot, accepted only when the host files hash the same before and
# after and the host folder's file list (names, sizes, mtimes) is unchanged;
# SQLite never opens a host file, so nothing in the host folder is
# created or written. SQLite then takes one online backup of the snapshot into
# <copy>, a mode-600 file in a mode-700 folder owned by one compose project,
# and runs quick_check on it. Any failure (a host write during the copy, a
# damaged -wal, a failed backup or check) removes the partial copy and exits
# nonzero, so the stack does not boot; there is no retry and no other mode.
# When <copy> already exists (a re-up of the same project) it is kept if it
# is still sound and private. The container mounts only <copy>, read-only, and
# seeds its own database on the loom-data volume from it; nothing is written
# back to the host.
#
# `remove` deletes <copy> and its folder (that project only).
#
# Prints paths only, never database contents.
# Knob: LOCAL_MODE_OPENCODE_DATA (host OpenCode data folder).
set -euo pipefail

usage() { echo "usage: real-opencode-copy.sh make|remove <copy path>" >&2; exit 2; }
[ "$#" -eq 2 ] || usage
cmd="$1" copy="$2"
case "$copy" in /*) ;; *) echo "local-mode: the OpenCode copy path must be absolute: $copy" >&2; exit 2 ;; esac
case "/$copy/" in */../*|*/./*) echo "local-mode: the OpenCode copy path must not contain . or .. parts: $copy" >&2; exit 2 ;; esac
dir="$(dirname "$copy")"

data="${LOCAL_MODE_OPENCODE_DATA:-${HOME}/.local/share/opencode}"
db="$data/opencode.db"

# canon PATH: PATH with every symlink in its longest existing prefix resolved
# (the parts that do not exist yet are appended as given).
canon() {
  local p="$1" rest=""
  while [ ! -d "$p" ]; do
    rest="/$(basename "$p")$rest"
    p="$(dirname "$p")"
  done
  printf '%s%s\n' "$(cd -P -- "$p" && pwd -P)" "$rest"
}
# The copy must never land in (or under) the host OpenCode folder, through any
# alias or symlink, and must not itself be a symlink.
outside_host() {
  local d h
  d="$(canon "$dir")/" h="$(canon "$data")/"
  case "$d" in "$h"*) echo "local-mode: the OpenCode copy must live outside $data (resolved: $h)" >&2; exit 2 ;; esac
  if [ -L "$copy" ] || { [ -e "$copy" ] && [ ! -f "$copy" ]; }; then
    echo "local-mode: $copy is not a regular file; remove it by hand" >&2; exit 2
  fi
}
outside_host

case "$cmd" in
  remove)
    rm -f -- "$copy" "$copy-wal" "$copy-shm" "$copy.tmp" "$copy.tmp-wal" "$copy.tmp-shm" "$copy.tmp-journal"
    rm -rf -- "$dir"/snap.*
    rmdir -- "$dir" 2>/dev/null || true
    exit 0
    ;;
  make) ;;
  *) usage ;;
esac

command -v sqlite3 >/dev/null 2>&1 \
  || { echo "local-mode: sqlite3 is needed to copy $db for a REAL stack" >&2; exit 1; }
mode() { stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1"; }

if [ -f "$copy" ]; then
  # A re-up keeps the existing copy, but only a sound, private one.
  if [ "$(mode "$copy")" != 600 ] || [ "$(mode "$dir")" != 700 ] \
    || [ "$(sqlite3 "file:$copy?mode=ro" "PRAGMA quick_check;" 2>/dev/null || true)" != ok ]; then
    echo "local-mode: this project's OpenCode copy at $copy is damaged or not private (want mode 600 in a 700 folder); run make local-mode-agents-down first; not starting the REAL stack" >&2
    exit 1
  fi
  echo "local-mode: keeping this project's private OpenCode copy at $copy"
  exit 0
fi
if [ ! -f "$db" ]; then
  echo "local-mode: no OpenCode database at $db; run \`opencode auth login\` on the host first (or set LOCAL_MODE_OPENCODE_DATA)" >&2
  exit 1
fi
# SQLite never opens a host file: even a read-only reader writes read marks
# into an existing -shm and creates a missing one. Instead, take a byte
# snapshot of opencode.db and its -wal (plain reads) into this project's
# folder, and accept it only when both files hash the same before and after
# the copy. SQLite then recovers the snapshot privately and takes the online
# backup from it. A -wal with a bad header, or a -wal with no -shm (a crashed
# or damaged writer), refuses the boot: repair the host database first.
digest() {
  if [ ! -e "$1" ]; then echo absent
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 < "$1"
  else sha256sum < "$1"; fi
}
if [ -e "$db-wal" ] && [ ! -e "$db-shm" ]; then
  echo "local-mode: opencode.db has a -wal but no -shm; open and close OpenCode once, then retry; not starting the REAL stack" >&2
  exit 1
fi
if [ -s "$db-wal" ]; then
  magic="$(od -An -tx1 -N4 "$db-wal" 2>/dev/null | tr -d ' \n' || true)"
  case "$magic" in
    377f0682|377f0683) ;;
    *) echo "local-mode: $db-wal is unreadable or damaged (bad WAL header); repair the host database with OpenCode first; not starting the REAL stack" >&2; exit 1 ;;
  esac
fi

(umask 077; mkdir -p "$dir")
chmod 700 "$dir"
outside_host
tmp="$copy.tmp"
snap="$(mktemp -d "$dir/snap.XXXXXX")"
fail() {
  rm -rf -- "$snap"
  rm -f -- "$tmp" "$tmp-wal" "$tmp-shm" "$tmp-journal"
  rmdir -- "$dir" 2>/dev/null || true
  echo "local-mode: $1; not starting the REAL stack" >&2
  exit 1
}
rm -f -- "$tmp" "$tmp-wal" "$tmp-shm" "$tmp-journal"

# Names, sizes and mtimes of the host folder's entries; must not change.
hostlist() {
  find "$data" -mindepth 1 -maxdepth 1 | LC_ALL=C sort | while IFS= read -r f; do
    printf '%s %s\n' "$f" "$(stat -f '%z %m' "$f" 2>/dev/null || stat -c '%s %Y' "$f")"
  done
}
listbefore="$(hostlist)"
before="$(digest "$db") $(digest "$db-wal")"
(
  umask 077
  cp -- "$db" "$snap/opencode.db"
  if [ -e "$db-wal" ]; then cp -- "$db-wal" "$snap/opencode.db-wal"; fi
) || fail "could not read $db"
after="$(digest "$db") $(digest "$db-wal")"
[ "$before" = "$after" ] \
  || fail "$db changed during the copy (host OpenCode is writing); try again"
[ "$(hostlist)" = "$listbefore" ] \
  || fail "the host OpenCode folder changed during the copy (host OpenCode is writing); try again"
snapped="$(digest "$snap/opencode.db") $(digest "$snap/opencode.db-wal")"
[ "$snapped" = "$before" ] || fail "the snapshot of $db does not match the host file"

(umask 077; sqlite3 "$snap/opencode.db" ".backup '$tmp'" >/dev/null 2>&1) \
  || fail "the online backup of $db failed"
rm -rf -- "$snap"
# The backup keeps the source's WAL mode; switch the copy to a plain rollback
# journal so it is one self-contained file (the stack's OpenCode picks its own
# mode on its volume copy).
check="$(sqlite3 "$tmp" "PRAGMA journal_mode=DELETE;" "PRAGMA quick_check;" 2>/dev/null || true)"
check="${check#delete
}"
[ "$check" = ok ] || fail "quick_check failed on the copy of $db"
chmod 600 "$tmp"
mv -f -- "$tmp" "$copy"
rm -f -- "$tmp-wal" "$tmp-shm" "$tmp-journal"
echo "local-mode: copied the OpenCode login to $copy (private to this project)"
