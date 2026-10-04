#!/usr/bin/env bash
# real-opencode-copy_test.sh - Tests for the private OpenCode login copy used
# by LOCAL_MODE_AGENTS_REAL=1 stacks (real-opencode-copy.sh, the REAL compose
# override and the Makefile wiring).
#
# Uses only fake SQLite databases in a temporary folder (HOME is pointed
# there too); never reads the real ~/.local/share/opencode. Starts no
# containers: the compose check only renders `compose config`.
#
# Usage: ./test/local-mode/real-opencode-copy_test.sh
# Checks are single-quoted strings eval'd by check(), and $compose may be two
# words, so:
# shellcheck disable=SC2016,SC2034,SC2086
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
script="$here/real-opencode-copy.sh"

PASS_COUNT=0
FAIL_COUNT=0
pass() { echo "PASS: $1"; PASS_COUNT=$((PASS_COUNT + 1)); }
fail() { echo "FAIL: $1"; FAIL_COUNT=$((FAIL_COUNT + 1)); }
check() { if eval "$2"; then pass "$1"; else fail "$1"; fi; }

command -v sqlite3 >/dev/null || { echo "SKIP: sqlite3 not found"; exit 0; }
command -v python3 >/dev/null || { echo "SKIP: python3 not found"; exit 0; }

T="$(mktemp -d "${TMPDIR:-/tmp}/au2-test.XXXXXX")"
trap 'rm -rf "$T"' EXIT
export HOME="$T/home"
host="$HOME/.local/share/opencode"
state="$T/state"
mkdir -p "$host" "$state"
export LOCAL_MODE_OPENCODE_DATA="$host"
secret="sk-FAKE-TOKEN-$$-do-not-print"

# Fake OpenCode db in WAL mode, closed cleanly (no -wal/-shm), holding a token.
mkfake() {
  rm -f "$1"/opencode.db*
  python3 - "$1/opencode.db" "$secret" <<'PY'
import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.execute("pragma journal_mode=wal")
c.execute("create table account(id integer primary key, token text)")
c.execute("insert into account(token) values (?)", (sys.argv[2],))
c.commit(); c.close()
PY
}
# Names, sizes and mtimes of everything in a folder.
listing() { find "$1" -mindepth 1 -exec stat -f '%N %z %m' {} + 2>/dev/null || find "$1" -mindepth 1 -exec stat -c '%n %s %Y' {} +; }
mode() { stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1"; }

# --- 1. Clean (no -wal) host db is copied; host folder untouched. -----------
mkfake "$host"
before="$(listing "$host")"
copy="$state/proj-a/opencode.db"
out="$("$script" make "$copy" 2>&1)"; rc=$?
check "make succeeds on a cleanly closed WAL db" '[ "$rc" = 0 ]'
check "host folder unchanged (no files created or written)" '[ "$(listing "$host")" = "$before" ]'
check "copy holds the login row" '[ "$(sqlite3 "$copy" "select count(*) from account")" = 1 ]'
check "copy is mode 600" '[ "$(mode "$copy")" = 600 ]'
check "copy folder is mode 700" '[ "$(mode "$state/proj-a")" = 700 ]'
check "copy is one self-contained file" '[ "$(ls -A "$state/proj-a")" = opencode.db ] && [ "$(sqlite3 "$copy" "pragma journal_mode")" = delete ]'
check "output never contains the credential" '! printf "%s" "$out" | grep -q "$secret"'

# --- 2. Re-up keeps the existing copy. ---------------------------------------
sqlite3 "$copy" "insert into account(token) values ('marker')"
"$script" make "$copy" >/dev/null 2>&1; rc=$?
check "re-make keeps the existing copy" '[ "$rc" = 0 ] && [ "$(sqlite3 "$copy" "select count(*) from account")" = 2 ]'

# --- 3. Live host writer: committed WAL frames are included. ----------------
mkfake "$host"
fifo="$T/fifo"; mkfifo "$fifo"
python3 - "$host/opencode.db" "$fifo" <<'PY' &
import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.execute("pragma wal_autocheckpoint=0")
c.execute("insert into account(token) values ('wal-only')"); c.commit()
open(sys.argv[2], "w").write("ready\n")
open(sys.argv[2]).read()
c.close()
PY
writer=$!
read -r _ < "$fifo"
before="$(listing "$host")"
copy_b="$state/proj-b/opencode.db"
"$script" make "$copy_b" >/dev/null 2>&1; rc=$?
after="$(listing "$host")"
echo finish > "$fifo"; wait "$writer"
check "make succeeds while a host writer holds the WAL" '[ "$rc" = 0 ]'
check "copy includes rows only in the host WAL" '[ "$(sqlite3 "$copy_b" "select count(*) from account where token = '"'"'wal-only'"'"'")" = 1 ]'
check "host folder file set unchanged during a live-writer copy" '[ "$(printf "%s\n" "$before" | cut -d" " -f1)" = "$(printf "%s\n" "$after" | cut -d" " -f1)" ]'

# --- 4. A failed backup or quick_check refuses to boot, leaves nothing. -----
printf 'not a database, just junk bytes %.0s' $(seq 1 300) > "$host/opencode.db"
rm -f "$host/opencode.db-wal" "$host/opencode.db-shm"
before="$(listing "$host")"
copy_c="$state/proj-c/opencode.db"
out="$("$script" make "$copy_c" 2>&1)"; rc=$?
check "corrupt host db: make fails" '[ "$rc" != 0 ]'
check "corrupt host db: clear refusal message" 'printf "%s" "$out" | grep -q "not starting the REAL stack"'
check "corrupt host db: no copy or temp files left" '[ ! -e "$state/proj-c" ]'
check "corrupt host db: host folder unchanged" '[ "$(listing "$host")" = "$before" ]'
rm -f "$host/opencode.db"
"$script" make "$state/proj-d/opencode.db" >/dev/null 2>&1; rc=$?
check "missing host db: make fails" '[ "$rc" != 0 ] && [ ! -e "$state/proj-d" ]'
check "missing host db: nothing created in the host folder" '[ -z "$(ls -A "$host")" ]'

# A -wal that exists but cannot be read (damaged WAL pair): refuse, with no
# immutable fallback and nothing written to the host folder.
mkfake "$host"
python3 - "$host/opencode.db" <<'PY'
import sqlite3, sys
c = sqlite3.connect(sys.argv[1]); c.execute("pragma wal_autocheckpoint=0")
c.execute("insert into account(token) values ('x')"); c.commit()
import os; os._exit(0)
PY
chmod 000 "$host/opencode.db-wal"
before="$(listing "$host")"
out="$("$script" make "$state/proj-e/opencode.db" 2>&1)"; rc=$?
check "unreadable host WAL: make refuses" '[ "$rc" != 0 ] && printf "%s" "$out" | grep -q "not starting the REAL stack"'
check "unreadable host WAL: no copy left" '[ ! -e "$state/proj-e" ]'
check "unreadable host WAL: host folder unchanged" '[ "$(listing "$host")" = "$before" ]'
chmod 600 "$host/opencode.db-wal"

# --- 5. The copy may never live in the host folder. -------------------------
mkfake "$host"
"$script" make "$host/sub/opencode.db" >/dev/null 2>&1; rc=$?
check "copy path inside the host folder is refused" '[ "$rc" = 2 ] && [ ! -e "$host/sub" ]'
"$script" make "relative/opencode.db" >/dev/null 2>&1; rc=$?
check "relative copy path is refused" '[ "$rc" = 2 ]'

# --- 6. remove deletes that project's copy only. ----------------------------
"$script" remove "$copy"; rc=$?
check "remove succeeds and deletes the project's copy folder" '[ "$rc" = 0 ] && [ ! -e "$state/proj-a" ]'
check "remove leaves other projects' copies" '[ -f "$copy_b" ]'
"$script" remove "$copy"; rc=$?
check "remove is idempotent" '[ "$rc" = 0 ]'

# --- 7. Compose files never bind the host OpenCode folder. ------------------
check "no compose file references the host OpenCode folder" \
  '! grep -hvE "^[[:space:]]*#" "$here"/docker-compose*.yml | grep -nE "\.local/share/opencode|LOCAL_MODE_OPENCODE_DATA|opencode-host-data"'
compose=""
if docker-compose version >/dev/null 2>&1; then compose="docker-compose"
elif docker compose version >/dev/null 2>&1; then compose="docker compose"
elif podman-compose version >/dev/null 2>&1; then compose="podman-compose"; fi
if [ -n "$compose" ]; then
  rendered="$(cd "$repo" && LOCAL_MODE_OPENCODE_COPY="$copy_b" LOCAL_MODE_COMPOSE_PROJECT=au2-config-test \
    $compose -p au2-config-test -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml \
    -f test/local-mode/docker-compose.agents-real.yml config 2>&1)"; rc=$?
  check "REAL compose config renders" '[ "$rc" = 0 ]'
  check "REAL compose config has no bind under the host OpenCode folder" '! printf "%s" "$rendered" | grep -q "$host"'
  check "REAL compose config mounts the private copy read-only" \
    'printf "%s" "$rendered" | grep -q "$copy_b" && printf "%s" "$rendered" | grep -A6 "$copy_b" | grep -qE "read_only: true|:ro"'
  (cd "$repo" && env -u LOCAL_MODE_OPENCODE_COPY $compose -p au2-config-test -f test/local-mode/docker-compose.yml \
    -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml config >/dev/null 2>&1); rc=$?
  check "REAL compose config refuses to render without a private copy path" '[ "$rc" != 0 ]'
else
  echo "SKIP: no compose CLI; rendered-config checks not run"
fi

# --- 8. Makefile wiring (dry run only). -------------------------------------
mk() { make -s -n -C "$repo" LOCAL_MODE_COMPOSE=docker-compose LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-au2t \
  LOCAL_MODE_STATE_DIR="$state" "$@" 2>/dev/null; }
up="$(mk local-mode-agents-up LOCAL_MODE_AGENTS_REAL=1)"
check "REAL up makes the private copy before compose up" \
  'printf "%s" "$up" | grep -q "real-opencode-copy.sh make \"$state/loomcli-local-mode-au2t/opencode.db\"" &&
   [ "$(printf "%s\n" "$up" | grep -n "real-opencode-copy.sh make" | cut -d: -f1 | head -1)" -lt "$(printf "%s\n" "$up" | grep -n " up " | cut -d: -f1 | tail -1)" ]'
check "REAL up has no one-stack-at-a-time guard" '! printf "%s" "$up" | grep -q "real-opencode-guard"'
check "non-REAL up makes no copy" 'mk local-mode-agents-up | grep -qF "if [ -n \"\" ]; then test/local-mode/real-opencode-copy.sh make"'
for t in local-mode-agents-down local-mode-down; do
  check "$t removes this project's copy" \
    'mk '"$t"' | grep -q "real-opencode-copy.sh remove \"$state/loomcli-local-mode-au2t/opencode.db\""'
done
check "AU1 guard and override are gone" \
  '[ ! -e "$here/real-opencode-guard.sh" ] && ! grep -rn --exclude="$(basename "$0")" "LOCAL_MODE_OPENCODE_SHARED_OK\|loom-backup" "$repo/Makefile" "$here"'

echo
echo "Results: $PASS_COUNT passed, $FAIL_COUNT failed"
[ "$FAIL_COUNT" -eq 0 ]
