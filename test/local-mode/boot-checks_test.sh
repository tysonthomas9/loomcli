#!/usr/bin/env bash
# boot-checks_test.sh - S15 local-mode boot checks against a REAL
# (LOCAL_MODE_AGENTS_REAL=1) Agent API stack booted by `make local-mode-agents-up`.
#
#   K2 (AU2) A host OpenCode folder whose opencode.db-wal is truncated refuses
#            the boot with a clear message: no container, no private copy, and
#            the host folder unchanged. A good folder boots on a private copy
#            (mode 600, mounted read-only, seeded into the stack's own db), and
#            the host db, -wal and -shm keep their bytes and mtimes.
#   K1 (WU1) The moment the boot log prints "[local-mode] ready", the OpenCode
#            catalog already lists LOCAL_MODE_AGENTS_MODEL, and a Create sent
#            within 1 s of that line returns 201 with no unknown-model or
#            catalog error.
#
# No real credentials: the host OpenCode folder is a throwaway fixture made
# here by the host's `opencode auth login` with a fake Anthropic key, in a
# private temp folder (HOME and XDG roots point there). The real
# ~/.local/share/opencode is never read. Nothing calls a model.
#
# Boots its own compose project only; run it under with-heavy-lock.sh:
#   LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-<you> \
#   LOCAL_MODE_FLEETDB_PORT=8x80 LOCAL_MODE_API_PORT=8x82 LOCAL_MODE_UI_PORT=8x83 \
#   test/local-mode/boot-checks_test.sh
# Knobs: FLEET_DB_REPO (a fleet-db checkout to build, when there is no
# ../fleet-db next to this repo), S15_MODEL (default
# anthropic/claude-sonnet-4-5), S15_KEEP=1 keeps the stack up afterwards
# (tear down with make local-mode-agents-down), S15_EVIDENCE=<dir> keeps the
# run's logs.
# Checks are single-quoted strings eval'd by check():
# shellcheck disable=SC2016,SC2034
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"

PASS_COUNT=0
FAIL_COUNT=0
pass() { echo "PASS: $1"; PASS_COUNT=$((PASS_COUNT + 1)); }
fail() { echo "FAIL: $1"; FAIL_COUNT=$((FAIL_COUNT + 1)); }
check() { if eval "$2"; then pass "$1"; else fail "$1"; fi; }
die() { echo "boot-checks: $*" >&2; exit 2; }

project="${LOCAL_MODE_COMPOSE_PROJECT:-}"
case "$project" in
  "" ) die "set LOCAL_MODE_COMPOSE_PROJECT to your own project" ;;
  loomcli-local-mode) die "loomcli-local-mode is the dogfood stack; use your own project" ;;
esac
for v in LOCAL_MODE_FLEETDB_PORT LOCAL_MODE_API_PORT LOCAL_MODE_UI_PORT; do
  [ -n "${!v:-}" ] || die "set $v to an unclaimed port"
  if lsof -nP -iTCP:"${!v}" -sTCP:LISTEN >/dev/null 2>&1; then die "$v=${!v} is already bound"; fi
done
for t in podman opencode sqlite3 python3 jq curl make; do
  command -v "$t" >/dev/null 2>&1 || die "$t is required"
done
# The stack runs on podman (the engine every check below inspects).
[ -z "${LOCAL_MODE_COMPOSE:-}" ] || [ "$LOCAL_MODE_COMPOSE" = "podman compose" ] \
  || die "LOCAL_MODE_COMPOSE must be unset or 'podman compose'"
export LOCAL_MODE_COMPOSE="podman compose"
owned() { podman ps -a --filter "label=com.docker.compose.project=$project" --format '{{.Names}}'; }
# loom-local's container, found by its compose labels (any name separator).
loom_ctr() {
  podman ps -a --filter "label=com.docker.compose.project=$project" \
    --filter "label=com.docker.compose.service=loom-local" --format '{{.Names}}' 2>/dev/null | head -1
}
# The project must be entirely new: teardown runs `down -v`, so refuse any
# container, volume or network already labelled with it. Every query must
# succeed; an error is never read as "nothing there".
leftovers() {
  local c v n
  c="$(owned)" && v="$(podman volume ls -q --filter "label=com.docker.compose.project=$project")" \
    && n="$(podman network ls -q --filter "label=com.docker.compose.project=$project")" || return 1
  printf '%s%s%s' "$c" "$v" "$n"
}
lo="$(leftovers)" || die "could not list podman containers, volumes or networks"
[ -z "$lo" ] || die "project $project already has containers, volumes or networks; pick another name"
# Only this suite's compose override and no Claude login copy may apply.
[ -z "${LOCAL_MODE_COMPOSE_FILES:-}" ] || die "unset LOCAL_MODE_COMPOSE_FILES; the suite brings its own override"
unset LOCAL_MODE_CLAUDE_COPY

model="${S15_MODEL:-anthropic/claude-sonnet-4-5}"
api="http://127.0.0.1:${LOCAL_MODE_API_PORT}"
prefix="$api/api/workspaces/LOCALMODE/v1"
ctr=""
# The Makefile's per-project copy path (LOCAL_MODE_OPENCODE_COPY).
state_dir="${LOCAL_MODE_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/loom-local-mode}"
[ -n "$state_dir" ] || die "could not resolve LOCAL_MODE_STATE_DIR"
copy="$state_dir/$project/opencode.db"
[ ! -e "$state_dir/$project" ] || die "$state_dir/$project already exists; it belongs to an earlier run of $project"
fleet="${FLEET_DB_REPO:-}"
if [ -z "$fleet" ] && [ ! -d "$repo/../fleet-db" ]; then
  die "set FLEET_DB_REPO to a fleet-db checkout (compose builds ../fleet-db by default)"
fi
[ -z "$fleet" ] || [ -f "$fleet/deploy/docker/Dockerfile" ] || die "FLEET_DB_REPO=$fleet is not a fleet-db checkout"

T="$(mktemp -d "${TMPDIR:-/tmp}/s15-boot.XXXXXX")"
host="$T/host-opencode"
# Build fleet-db from FLEET_DB_REPO through a one-run compose override; the
# same list reaches make's up and down.
if [ -n "$fleet" ]; then
  printf 'services:\n  fleet-db:\n    build:\n      context: %s\n' "$fleet" > "$T/fleet-db.yml"
  export LOCAL_MODE_COMPOSE_FILES="$T/fleet-db.yml"
fi
# No real credentials: the REAL override's codex and Claude binds read
# /dev/null instead of the host logins (OpenCode uses the fixture above).
export LOCAL_MODE_CODEX_AUTH=/dev/null LOCAL_MODE_CLAUDE_AUTH=/dev/null LOCAL_MODE_CLAUDE_TOKEN_FILE=/dev/null
brought_up=""
down() {
  [ -n "$brought_up" ] || return 0
  (cd "$repo" && LOCAL_MODE_AGENTS_REAL=1 make -s local-mode-agents-down >"$T/down.log" 2>&1)
}
# Image builds on this host are serialized by a shared mkdir lock (the AFT
# agent-flows runner takes the same one): a concurrent build or prune can drop
# the intermediate stage the agents target builds FROM.
build_lock=/private/tmp/dryhawk-stack-build.lock
lock_held=""
take_build_lock() {
  local i=0
  until mkdir "$build_lock" 2>/dev/null; do
    i=$((i + 1)); [ "$i" -le 1800 ] || die "shared stack build lock $build_lock still held after 1h"
    sleep 2
  done
  lock_held=1
  printf 's15-boot-checks project=%s pid=%s %s\n' "$project" "$$" "$(date -u +%FT%TZ)" > "$build_lock/owner"
}
drop_build_lock() {
  [ -n "$lock_held" ] || return 0
  rm -f "$build_lock/owner"; rmdir "$build_lock" 2>/dev/null; lock_held=""
}
cleanup() {
  local rc=$? lo
  [ -n "${follower:-}" ] && kill "$follower" 2>/dev/null
  drop_build_lock
  # A failed or partial teardown fails the run, with its log.
  if [ "${S15_KEEP:-}" != 1 ] && [ -n "$brought_up" ]; then
    lo="$(down && leftovers)" || lo="(teardown or inventory failed)"
    if [ -n "$lo" ] || [ -e "$state_dir/$project" ]; then
      echo "boot-checks: teardown of $project left resources behind: ${lo:-$state_dir/$project}" >&2
      tail -20 "$T/down.log" >&2
      rc=1
    fi
  fi
  # S15_EVIDENCE=<dir> keeps the logs (never the fixture or any db).
  if [ -n "${S15_EVIDENCE:-}" ]; then
    mkdir -p "$S15_EVIDENCE" && find "$T" -maxdepth 1 \( -name '*.log' -o -name 'k1-*' \) -exec cp {} "$S15_EVIDENCE"/ \;
  fi
  rm -rf "$T"
  exit "$rc"
}
trap cleanup EXIT

up() {
  (cd "$repo" && LOCAL_MODE_AGENTS_REAL=1 LOCAL_MODE_OPENCODE_DATA="$host" \
    LOCAL_MODE_AGENTS_MODEL="$model" LOCAL_MODE_COMPOSE_UP_FLAGS="--build -d" \
    make local-mode-agents-up)
}

# Name, size, mtime (ns where available) and SHA-256 of each host file.
listing() {
  find "$1" -mindepth 1 -maxdepth 1 | LC_ALL=C sort | while IFS= read -r f; do
    printf '%s %s %s\n' "$f" "$(stat -f '%z %Fm' "$f" 2>/dev/null || stat -c '%s %.9Y' "$f")" \
      "$({ shasum -a 256 < "$f" || sha256sum < "$f"; } 2>/dev/null)"
  done
}
mode() { stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1"; }

# --- Fixture: a throwaway OpenCode login (fake key), with a live WAL. --------
gen="$T/gen"
mkdir -p "$gen"/{home,config,data,state,cache}
python3 - "$gen" <<'PY' || die "could not make the throwaway OpenCode login"
import os, pty, re, select, sys, time
g = sys.argv[1]
env = dict(os.environ, HOME=g + "/home", XDG_CONFIG_HOME=g + "/config", XDG_DATA_HOME=g + "/data",
           XDG_STATE_HOME=g + "/state", XDG_CACHE_HOME=g + "/cache")
pid, fd = pty.fork()
if pid == 0:
    os.chdir(g)
    os.execvpe("opencode", ["opencode", "auth", "login", "--standalone", "anthropic", "--method", "key"], env)
buf, typed, seen, end = b"", False, None, time.time() + 90
while time.time() < end:
    r, _, _ = select.select([fd], [], [], 0.3)
    if r:
        try:
            d = os.read(fd, 4096)
        except OSError:
            break
        if not d:
            break
        buf += d
    flat = re.sub(rb"\x1b\[[0-9;?]*[a-zA-Z]|\s", b"", buf)
    if seen is None and b"APIkey" in flat:
        seen = time.time()
    if seen and not typed and time.time() - seen > 1.5:
        for ch in b"sk-ant-s15-throwaway-not-a-real-key":
            os.write(fd, bytes([ch])); time.sleep(0.01)
        time.sleep(0.5); os.write(fd, b"\r"); typed = True
# Reap the child; at the deadline, kill it rather than wait forever.
done, st = os.waitpid(pid, os.WNOHANG)
if done == 0:
    time.sleep(2)
    done, st = os.waitpid(pid, os.WNOHANG)
    if done == 0:
        os.kill(pid, 9)
        os.waitpid(pid, 0)
        sys.exit(1)
sys.exit(0 if typed and st == 0 and b"Connected" in buf else 1)
PY
mkdir -p "$host"
cp "$gen/data/opencode/opencode.db" "$host/opencode.db"
# Leave committed frames in the -wal (with its -shm), like a host OpenCode
# that is still open: a marker row exists only in the WAL.
python3 - "$host/opencode.db" <<'PY' || die "could not write the WAL marker"
import os, sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.execute("pragma journal_mode=wal"); c.execute("pragma wal_autocheckpoint=0")
c.execute("create table s15_marker(v text)"); c.execute("insert into s15_marker values ('wal-only')")
c.commit(); os._exit(0)
PY
[ -s "$host/opencode.db-wal" ] && [ -e "$host/opencode.db-shm" ] || die "fixture has no live WAL"
cp -p "$host/opencode.db-wal" "$T/good.wal"

# --- K2a: a truncated -wal refuses the boot. ---------------------------------
python3 -c 'import os, sys; os.truncate(sys.argv[1], 3)' "$host/opencode.db-wal"
before="$(listing "$host")"
brought_up=1
out="$(up 2>&1)"; rc=$?
printf '%s\n' "$out" > "$T/k2a-up.log"
check "K2 truncated host WAL: make local-mode-agents-up fails" '[ "$rc" != 0 ]'
check "K2 truncated host WAL: clear refusal naming the damaged WAL" \
  'printf "%s" "$out" | grep -q "opencode.db-wal is unreadable or damaged (bad WAL header); repair the host database with OpenCode first; not starting the REAL stack"'
check "K2 truncated host WAL: no container was created" 'c="$(owned)" && [ -z "$c" ]'
check "K2 truncated host WAL: no private copy left" '[ ! -e "$state_dir/$project" ]'
check "K2 truncated host WAL: host folder bytes and mtimes unchanged" '[ "$(listing "$host")" = "$before" ]'

# --- K2b + K1: a good folder boots on a private copy; OpenCode is warm. ------
cp -p "$T/good.wal" "$host/opencode.db-wal"
before="$(listing "$host")"
boot="$T/boot.log"
: > "$boot"
# Follow loom-local's log as soon as the container runs; on the ready line,
# list the catalog and send a Create at once. Timing is measured from the
# line's EMISSION (podman's log timestamp, VM clock), not from when this
# follower reads it: the VM-host clock offset is measured right after, and the
# catalog request and the POST must each start within 1 s of emission.
now() { if [ -n "${EPOCHREALTIME:-}" ]; then printf '%s\n' "$EPOCHREALTIME"; else python3 -c 'import time; print(repr(time.time()))'; fi; }
(
  # Compose creates loom-local before it starts it, and `podman logs -f` on a
  # created container returns at once; attach only once it runs, and again if
  # the stream ends before the ready line (each attach replays from the start).
  while [ ! -s "$T/k1-timing" ]; do
    until ctr="$(loom_ctr)" && [ -n "$ctr" ] && [ "$(podman inspect -f '{{.State.Running}}' "$ctr" 2>/dev/null)" = true ]; do
      sleep 0.25
    done
    : > "$boot"
    podman logs -f --timestamps "$ctr" 2>&1 | while IFS= read -r raw; do
      ts="${raw%% *}" line="${raw#* }"
      printf '%s\n' "$line" >> "$boot"
      if [ "$line" = "[local-mode] ready" ]; then
        # Each request's send time is bounded from above by curl's own timers:
        # it went out at (exit time - time_total + time_pretransfer), and the
        # host clock is read only after curl exits, so the bound is never early.
        mw="$(curl -sS -o "$T/k1-models.json" -w '%{http_code} %{time_pretransfer} %{time_total}' --max-time 1 \
          "$prefix/harnesses/opencode/models" 2>"$T/k1-models.err" || true)"; m_end="$(now)"
        body="$(jq -nc --arg m "$model" '{preset:"lead",name:"s15-wu1-first",repo:"source-repo",base_ref:"main",overrides:{harness:"opencode",model:$m}}')"
        pw="$(curl -sS -o "$T/k1-create.json" -w '%{http_code} %{time_pretransfer} %{time_total}' --max-time 60 -X POST \
          -H 'Content-Type: application/json' -H "Idempotency-Key: s15-wu1-$$" -d "$body" "$prefix/agents" 2>"$T/k1-create.err" || true)"; p_end="$(now)"
        h0="$(now)"; vm="$(podman exec "$ctr" date +%s.%N 2>/dev/null)"; h1="$(now)"
        python3 - "$ts" "$mw" "$m_end" "$pw" "$p_end" "$h0" "$vm" "$h1" > "$T/k1-timing" <<'PY2'
import datetime, json, re, sys
ts, mw, m_end, pw, p_end, h0, vm, h1 = sys.argv[1:]
code, m_pre, m_tot = (mw.split() + ["", "nan", "nan"])[:3]
ccode, p_pre, p_tot = (pw.split() + ["", "nan", "nan"])[:3]
t_models = float(m_end) - float(m_tot) + float(m_pre)
t_post = float(p_end) - float(p_tot) + float(p_pre)
# Python 3.9 fromisoformat: at most 6 fraction digits and no trailing Z.
t = re.sub(r"Z$", "+00:00", ts)
m = re.match(r"(.*\.\d{1,6})\d*(.*)$", t)
emit_vm = datetime.datetime.fromisoformat(m.group(1) + m.group(2) if m else t).timestamp()
# The VM's `date` ran at some host time in [h0, h1], so host = VM + offset
# with offset in [h0 - vm, h1 - vm]. Taking the smallest offset gives the
# LARGEST possible delay after emission: an upper bound, never an understatement.
off_lo = float(h0) - float(vm)
print(json.dumps({"ready_ts": ts, "models_after_max_s": t_models - (emit_vm + off_lo),
                  "post_after_max_s": t_post - (emit_vm + off_lo),
                  "clock_offset_range_s": [off_lo, float(h1) - float(vm)],
                  "models_http": code, "create_http": ccode}))
PY2
        break
      fi
    done
    sleep 0.25
  done
) &
follower=$!
take_build_lock
out="$(up 2>&1)"; rc=$?
drop_build_lock
printf '%s\n' "$out" > "$T/k2b-up.log"
# make returns once the ready marker exists; give the follower's two requests
# up to 90 s more to land, then stop it.
i=0
while [ "$rc" = 0 ] && [ ! -s "$T/k1-timing" ] && kill -0 "$follower" 2>/dev/null && [ "$i" -lt 90 ]; do
  i=$((i + 1)); sleep 1
done
kill "$follower" 2>/dev/null; wait "$follower" 2>/dev/null; follower=""
ctr="$(loom_ctr)"
podman logs "$ctr" > "$T/loom-local.log" 2>&1 || true

check "K2 good host folder: make local-mode-agents-up succeeds and prints STACK UP" \
  '[ "$rc" = 0 ] && printf "%s" "$out" | grep -q "STACK UP"'
check "K2 good host folder: the private copy is a mode-600 file in a mode-700 folder" \
  '[ -f "$copy" ] && [ "$(mode "$copy")" = 600 ] && [ "$(mode "$(dirname "$copy")")" = 700 ]'
check "K2 good host folder: the private copy holds the WAL-only row" \
  '[ "$(sqlite3 "file:$copy?mode=ro" "select v from s15_marker" 2>/dev/null)" = wal-only ]'
mounts="$(podman inspect "$ctr" --format '{{range .Mounts}}{{.Source}} {{.Destination}} {{.RW}}{{"\n"}}{{end}}' 2>/dev/null)"
check "K2 good host folder: the container mounts only the private copy, read-only" \
  'printf "%s\n" "$mounts" | grep -qxF "$copy /run/secrets/opencode-seed.db false" && ! printf "%s" "$mounts" | grep -qF "$host"'
podman cp "$ctr:/root/.loom/agents-opencode/data/opencode/opencode.db" "$T/stack.db" >/dev/null 2>&1
check "K2 good host folder: the stack seeded its own db from the copy" \
  '[ "$(sqlite3 "$T/stack.db" "select v from s15_marker" 2>/dev/null)" = wal-only ]'
check "K2 good host folder: host db, -wal and -shm bytes and mtimes unchanged" '[ "$(listing "$host")" = "$before" ]'

ready_line="$(grep -n -x '\[local-mode\] ready' "$boot" | head -1 | cut -d: -f1)"
warm_line="$(grep -n "^\[local-mode\] OpenCode warm: [0-9][0-9]* models in [0-9][0-9]*s$" "$boot" | head -1 | cut -d: -f1)"
check "K1 the boot log prints \"OpenCode warm\" before \"[local-mode] ready\", with no catalog warning" \
  '[ -n "$ready_line" ] && [ -n "$warm_line" ] && [ "$warm_line" -lt "$ready_line" ] && ! grep -q "WARNING: OpenCode catalog" "$boot"'
tget() { jq -r ".$1 // empty" "$T/k1-timing" 2>/dev/null; }
# An upper-bound delay must be in [0, 1): a negative one means the clocks or
# the timestamps are wrong, so it never passes.
under1() { python3 -c 'import sys; d = float(sys.argv[1]); sys.exit(0 if 0 <= d < 1 else 1)' "$1" 2>/dev/null; }
mcode="$(tget models_http)" ccode="$(tget create_http)"
check "K1 the catalog request is sent within 1 s of the ready line's emission" 'under1 "$(tget models_after_max_s)"'
check "K1 at ready, the catalog answers within 1 s and lists $model" \
  '[ "$mcode" = 200 ] && jq -e --arg m "$model" "any(.providers[].models[]; .id == \$m)" "$T/k1-models.json" >/dev/null 2>&1'
check "K1 the first Create is sent within 1 s of the ready line's emission" 'under1 "$(tget post_after_max_s)"'
check "K1 the first Create returns 201 with no unknown-model or catalog error" \
  '[ "$ccode" = 201 ] && jq -e ".agent_id" "$T/k1-create.json" >/dev/null 2>&1 && ! grep -qiE "unknown model|preset_invalid|catalog|not ready" "$T/k1-create.json"'
created="$(jq -r '.agent_id // empty' "$T/k1-create.json" 2>/dev/null)"
check "K1 the created Lead carries $model" \
  '[ -n "$created" ] && [ "$(curl -fsS --max-time 10 "$prefix/agents/$created" | jq -r .model)" = "$model" ]'

if [ "$FAIL_COUNT" -ne 0 ]; then
  echo "--- evidence (paths only, no db contents) ---"
  for f in k2a-up.log k2b-up.log k1-timing k1-models.err k1-create.json k1-create.err; do
    [ -s "$T/$f" ] && { echo "## $f"; tail -40 "$T/$f"; }
  done
  echo "## boot.log (tail)"; tail -40 "$boot"
fi
echo
echo "Results: $PASS_COUNT passed, $FAIL_COUNT failed"
[ "$FAIL_COUNT" -eq 0 ]
