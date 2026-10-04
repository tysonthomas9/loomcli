#!/usr/bin/env bash
# Private OpenCode login for LOCAL_MODE_AGENTS_REAL=1 Agent API stacks.
#
#   real-opencode-copy.sh make <copy>    before `up`
#   real-opencode-copy.sh remove <copy>  after `down`
#
# `make` takes one SQLite online backup of the host's opencode.db, opened
# read-only: no file is created in the host folder and the database and its
# -wal are never written (with a -wal present, SQLite's reader locking updates
# only the shared-memory index -shm, as any reader does), into
# <copy>, a mode-600 file in a mode-700 folder owned by one compose project,
# then runs quick_check on the copy. Any failure removes the partial copy and
# exits nonzero, so the stack does not boot; there is no retry and no other
# open mode. When <copy> already exists (a re-up of the same project) it is
# kept as is. The container mounts only <copy>, read-only, and seeds its own
# database on the loom-data volume from it; nothing is written back to the
# host.
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
# A WAL database closed cleanly has no -wal/-shm, and a read-only open cannot
# create them, so it would fail. With no -wal the main file holds every
# commit: read it as immutable (no locks, no -shm) and refuse when a -wal
# appears or the file changes during the copy. With a -wal, use the normal
# read-only reader protocol so committed WAL frames are included.
# URI path: escape the characters SQLite's URI parser treats specially.
upath="$(printf '%s' "$db" | sed -e 's/%/%25/g' -e 's/?/%3f/g' -e 's/#/%23/g' -e 's/ /%20/g')"
if [ -e "$db-wal" ]; then
  src="file:$upath?mode=ro"
  wal=1
else
  src="file:$upath?mode=ro&immutable=1"
  wal=0
fi
# Content digest of the host file (read-only); any byte change alters it.
stamp() { if command -v shasum >/dev/null 2>&1; then shasum -a 256 < "$db"; else sha256sum < "$db"; fi; }
before=""
[ "$wal" = 1 ] || before="$(stamp)"

tmp="$copy.tmp"
fail() {
  rm -f -- "$tmp" "$tmp-wal" "$tmp-shm" "$tmp-journal"
  rmdir -- "$dir" 2>/dev/null || true
  echo "local-mode: $1; not starting the REAL stack" >&2
  exit 1
}
(umask 077; mkdir -p "$dir")
chmod 700 "$dir"
outside_host
rm -f -- "$tmp" "$tmp-wal" "$tmp-shm" "$tmp-journal"
(umask 077; sqlite3 "$src" ".backup '$tmp'" >/dev/null 2>&1) \
  || fail "the online backup of $db failed"
if [ "$wal" = 0 ] && { [ -e "$db-wal" ] || [ -e "$db-shm" ] || [ "$(stamp)" != "$before" ]; }; then
  fail "$db changed during the copy (is host OpenCode running?); try again"
fi
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
