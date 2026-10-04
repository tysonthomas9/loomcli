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
  # Some SQLite builds keep an empty -wal/-shm after a clean close; OpenCode's
  # clean close leaves none, so model that.
  rm -f "$1/opencode.db-wal" "$1/opencode.db-shm"
}
# Name, size, mtime and SHA-256 of everything in a folder.
listing() {
  find "$1" -mindepth 1 | sort | while IFS= read -r f; do
    printf '%s %s %s\n' "$f" "$(stat -f '%z %m' "$f" 2>/dev/null || stat -c '%s %Y' "$f")" \
      "$({ shasum -a 256 < "$f" || sha256sum < "$f"; } 2>/dev/null || echo unreadable)"
  done
}
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
check "host folder byte-identical during a live-writer copy (db, -wal and -shm)" '[ "$before" = "$after" ]'

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

# A -wal with no -shm (crashed or damaged writer): refuse before any read, so
# no -shm is ever created in the host folder.
rm -f "$host/opencode.db-shm"
before="$(listing "$host")"
out="$("$script" make "$state/proj-f/opencode.db" 2>&1)"; rc=$?
check "host -wal without -shm: make refuses with the repair hint" '[ "$rc" = 1 ] && printf "%s" "$out" | grep -q "opencode.db has a -wal but no -shm; open and close OpenCode once, then retry"'
check "host -wal without -shm: no -shm created, host unchanged" '[ ! -e "$host/opencode.db-shm" ] && [ "$(listing "$host")" = "$before" ] && [ ! -e "$state/proj-f" ]'

# A -wal with a bad header (damaged): refuse.
: > "$host/opencode.db-shm"
printf 'garbage-wal-header-and-more-bytes' > "$host/opencode.db-wal"
before="$(listing "$host")"
out="$("$script" make "$state/proj-g/opencode.db" 2>&1)"; rc=$?
check "damaged host WAL header: make refuses" '[ "$rc" = 1 ] && printf "%s" "$out" | grep -q "bad WAL header"'
check "damaged host WAL header: host unchanged, no copy" '[ "$(listing "$host")" = "$before" ] && [ ! -e "$state/proj-g" ]'
mkfake "$host"

# --- 5. The copy may never live in the host folder. -------------------------
mkfake "$host"
"$script" make "$host/sub/opencode.db" >/dev/null 2>&1; rc=$?
check "copy path inside the host folder is refused" '[ "$rc" = 2 ] && [ ! -e "$host/sub" ]'
"$script" make "relative/opencode.db" >/dev/null 2>&1; rc=$?
check "relative copy path is refused" '[ "$rc" = 2 ]'

ln -s "$host" "$T/alias"
"$script" make "$T/alias/seed.db" >/dev/null 2>&1; rc=$?
check "copy path through a symlink alias of the host folder is refused" '[ "$rc" = 2 ] && [ ! -e "$host/seed.db" ]'
"$script" remove "$T/alias/opencode.db" >/dev/null 2>&1; rc=$?
check "remove through a symlink alias of the host folder is refused" '[ "$rc" = 2 ] && [ -f "$host/opencode.db" ]'
mkdir -p "$T/aliasparent"; ln -s "$host" "$T/aliasparent/sub"
"$script" make "$T/aliasparent/sub/new/seed.db" >/dev/null 2>&1; rc=$?
check "not-yet-existing folder under a host alias is refused" '[ "$rc" = 2 ] && [ ! -e "$host/new" ]'
"$script" make "$state/x/../proj-z/opencode.db" >/dev/null 2>&1; rc=$?
check "copy path with .. parts is refused" '[ "$rc" = 2 ]'
mkdir -p "$state/proj-l"; chmod 700 "$state/proj-l"; ln -s "$host/opencode.db" "$state/proj-l/opencode.db"
"$script" make "$state/proj-l/opencode.db" >/dev/null 2>&1; rc=$?
check "copy that is a symlink (into the host folder) is refused" '[ "$rc" = 2 ]'
rm -rf "$state/proj-l"

# --- 5b. An existing copy is kept only when sound and private. --------------
"$script" make "$state/proj-k/opencode.db" >/dev/null 2>&1
chmod 644 "$state/proj-k/opencode.db"
out="$("$script" make "$state/proj-k/opencode.db" 2>&1)"; rc=$?
check "existing copy with mode 644 is refused" '[ "$rc" = 1 ] && printf "%s" "$out" | grep -q "not private"'
chmod 600 "$state/proj-k/opencode.db"
"$script" make "$state/proj-k/opencode.db" >/dev/null 2>&1; rc=$?
check "existing sound mode-600 copy is kept" '[ "$rc" = 0 ]'
python3 - "$state/proj-k/opencode.db" <<'PY'
import sys
with open(sys.argv[1], "r+b") as f:
    f.seek(4096); f.write(b"\xff" * 4096)
PY
"$script" make "$state/proj-k/opencode.db" >/dev/null 2>&1; rc=$?
check "existing corrupt copy is refused" '[ "$rc" = 1 ]'
"$script" remove "$state/proj-k/opencode.db"

# --- 5c. A same-size change with the mtime restored during the copy is seen. -
# A cp shim on PATH runs the real cp, then rewrites one byte of the host file
# in place (same size) and restores its mtime, right after the snapshot read.
real_cp="$(command -v cp)"
mkdir -p "$T/shim"
cat > "$T/shim/cp" <<SH
#!/usr/bin/env bash
"$real_cp" "\$@"; rc=\$?
case "\$*" in *"$host/opencode.db "*)
  touch -r "$host/opencode.db" "$T/mtime"
  python3 -c 'import sys; f=open(sys.argv[1],"r+b"); f.seek(100); b=f.read(1); f.seek(100); f.write(bytes([b[0]^1])); f.close()' "$host/opencode.db"
  touch -r "$T/mtime" "$host/opencode.db" ;;
esac
exit \$rc
SH
chmod +x "$T/shim/cp"
mkfake "$host"
out="$(PATH="$T/shim:$PATH" "$script" make "$state/proj-m/opencode.db" 2>&1)"; rc=$?
check "same-size, mtime-restored host change during the copy is refused" '[ "$rc" = 1 ] && printf "%s" "$out" | grep -q "changed during the copy" && [ ! -e "$state/proj-m" ]'
rm -f "$T/shim/cp"

# Any change to the host folder's file list (names, sizes, mtimes) during the
# copy refuses, even when opencode.db and its -wal are untouched: a cp shim
# bumps the -shm mtime and creates a new file right after the snapshot read.
cat > "$T/shim/cp" <<SH
#!/usr/bin/env bash
"$real_cp" "\$@"; rc=\$?
case "\$*" in *"$host/opencode.db "*)
  touch -t 203001010000 "$host/opencode.db-shm" 2>/dev/null || true
  : > "$host/new-file" ;;
esac
exit \$rc
SH
chmod +x "$T/shim/cp"
mkfake "$host"
: > "$host/opencode.db-shm"
out="$(PATH="$T/shim:$PATH" "$script" make "$state/proj-q/opencode.db" 2>&1)"; rc=$?
check "host folder list change during the copy is refused" '[ "$rc" = 1 ] && printf "%s" "$out" | grep -q "host OpenCode folder changed during the copy" && [ ! -e "$state/proj-q" ]'
rm -f "$T/shim/cp" "$host/new-file"

# --- 5d. sqlite3 is never pointed at a host file. ----------------------------
real_sqlite="$(command -v sqlite3)"
cat > "$T/shim/sqlite3" <<SH
#!/usr/bin/env bash
printf '%s\n' "\$*" >> "$T/sqlite3.log"
exec "$real_sqlite" "\$@"
SH
chmod +x "$T/shim/sqlite3"
mkfake "$host"
python3 - "$host/opencode.db" <<'PY'
import sqlite3, sys, os
c = sqlite3.connect(sys.argv[1]); c.execute("pragma wal_autocheckpoint=0")
c.execute("insert into account(token) values ('y')"); c.commit()
os._exit(0)
PY
before="$(listing "$host")"
PATH="$T/shim:$PATH" "$script" make "$state/proj-n/opencode.db" >/dev/null 2>&1; rc=$?
check "WAL host db (with -shm) copies; host folder byte-identical" '[ "$rc" = 0 ] && [ "$(listing "$host")" = "$before" ]'
check "sqlite3 never receives a host path" '[ -s "$T/sqlite3.log" ] && ! grep -q "$host" "$T/sqlite3.log"'
check "copy includes the WAL-only row" '[ "$(sqlite3 "$state/proj-n/opencode.db" "select count(*) from account where token = '"'"'y'"'"'")" = 1 ]'
check "no snapshot left in the project folder" '[ "$(ls -A "$state/proj-n")" = opencode.db ]'
rm -f "$T/shim/sqlite3"
"$script" remove "$state/proj-n/opencode.db"
mkfake "$host"

# --- 5e. A symlinked project folder is refused (make and remove). -----------
"$script" make "$state/proj-v/opencode.db" >/dev/null 2>&1
ln -s "$state/proj-v" "$state/proj-w"
out="$("$script" make "$state/proj-w/opencode.db" 2>&1)"; rc=$?
check "make through a symlinked project folder is refused" '[ "$rc" = 2 ] && printf "%s" "$out" | grep -q "symlink or not a folder"'
"$script" remove "$state/proj-w/opencode.db" >/dev/null 2>&1; rc=$?
check "remove through a symlinked project folder is refused; the other copy survives" '[ "$rc" = 2 ] && [ -f "$state/proj-v/opencode.db" ]'
rm -f "$state/proj-w"
"$script" remove "$state/proj-v/opencode.db"

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
