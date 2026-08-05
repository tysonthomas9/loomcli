#!/usr/bin/env bash
# In-container bootstrap for the loom SWE-Marathon ensemble.
# Invoked once by orchestrate.sh. Idempotent. On success writes $MH/env.sh
# (sourced by orchestrate and inherited by every loom/daemon child process).
set -euo pipefail

MH="${LOOM_MARATHON_HOME:-/installed-agent/loom-marathon}"
export HOME="${HOME:-/root}"

STUB="${LOOM_MARATHON_STUB:-0}"
MAX_AGENTS="${LOOM_MARATHON_MAX_AGENTS:-2}"

# stubbin (fake codex) must beat everything, then the loom bundle.
if [ "$STUB" = "1" ]; then
  export PATH="$MH/stubbin:$MH/bin:$PATH"
else
  export PATH="$MH/bin:$PATH"
fi

# Durability rule (trial-1 lesson, refined after an A/B on the stub): state
# that matters must reach the HOST-MOUNTED /logs/agent — but LIVE database
# semantics on virtiofs are not trustworthy (comments vanished across
# instance restarts when loom-state lived on the mount, while the identical
# flow round-trips cleanly on a real fs). So:
#   - loom-state (fleet-db) runs on CONTAINER fs for live correctness;
#     orchestrate copies it to /logs/agent/loom-state-backup every pass
#     (crash-safe, at most one pass stale) and at finalize.
#   - codex-home (append-only session transcripts) stays on the mount;
#     auth.json stays a SYMLINK to the container-only credential — the key
#     itself is never written into the host trial dir.
export LOOM_CONFIG_DIR=/installed-agent/loom-state
export LOOM_BACKEND=codex
# fleet-db dev-mode auth still needs an identity (X-Actor). The client falls
# back to $USER, which is UNSET inside docker exec → 401 authentication
# required on every call. Pin an explicit actor (daemon agents override via
# LOOM_AGENT_NAME per agent).
export LOOM_FLEET_DB_ACTOR="${LOOM_FLEET_DB_ACTOR:-marathon-harness}"
export USER="${USER:-root}"
export CODEX_HOME=/logs/agent/codex-home
mkdir -p "$LOOM_CONFIG_DIR" "$CODEX_HOME" /work /logs/agent

# Continuous full-history mirror of /app (all refs incl. agent branches);
# orchestrate pushes to it every pass and at finalize.
[ -d /logs/agent/app-mirror.git ] || git init -q --bare -b main /logs/agent/app-mirror.git

log() { printf '[bootstrap] %s\n' "$*"; }
die() { printf '[bootstrap] FATAL: %s\n' "$*" >&2; exit 1; }

# ---- git identity ------------------------------------------------------------
git config --global user.name  "loom-marathon" 2>/dev/null || true
git config --global user.email "loom-marathon@localhost" 2>/dev/null || true
git config --global init.defaultBranch main 2>/dev/null || true
git config --global --add safe.directory '*' 2>/dev/null || true

# ---- /app baseline -----------------------------------------------------------
# /app is the tree the verifier scores. It stays on branch main; NO agent ever
# runs in it. It only advances via the harness fast-forward integration gate.
[ -d /app ] || die "/app does not exist"
if ! git -C /app rev-parse --git-dir >/dev/null 2>&1; then
  git -C /app init -q -b main
fi
if ! git -C /app rev-parse HEAD >/dev/null 2>&1; then
  git -C /app add -A
  git -C /app commit -q --allow-empty -m "marathon baseline"
fi
APP_BASE="$(git -C /app rev-parse HEAD)"
echo "$APP_BASE" > /logs/agent/app-base.txt
log "/app baseline: $APP_BASE"

# Local bare origin: prompts may say "push"; this satisfies pushes with zero egress.
if [ ! -d /work/origin.git ]; then
  git init -q --bare /work/origin.git
fi
if ! git -C /app remote get-url origin >/dev/null 2>&1; then
  git -C /app remote add origin /work/origin.git
else
  git -C /app remote set-url origin /work/origin.git
fi
git -C /app push -q -u origin main 2>/dev/null || true

# ---- workspace + agents ------------------------------------------------------
WS_NAME=marathon
# Workspace KEYS are uppercased on create (WorkspaceKeyFromName) and LOOM_WORKSPACE
# is resolved verbatim against the key — export the KEY, not the name.
WS_KEY="$(printf '%s' "$WS_NAME" | tr 'a-z' 'A-Z')"
WS_ROOT=/work/ws
if [ ! -d "$WS_ROOT" ]; then
  loom workspace create "$WS_NAME" --repos /app --path "$WS_ROOT" --branch marathon
fi
export LOOM_WORKSPACE="$WS_KEY"

loom daemon profile set issue_backend fleetdb
# log_dir must resolve INSIDE the project/config dirs or the daemon rejects it;
# it rides to the host via the per-pass loom-state backup copies.
loom daemon profile set log_dir "$LOOM_CONFIG_DIR/daemon-logs"
loom daemon profile set events_dir "$WS_ROOT/.loom/events"
loom daemon profile set max_agents "$MAX_AGENTS"
loom daemon profile set startup_timeout 60

# planner: built-in plan role (task-filter needs_plan) — designs undesigned/needs-revision tasks
# coder-1: built-in task role (task-filter has_design) — ONE serialized implementation coder (v1)
loom agentdef add planner --role plan --auto --backend codex --repos app 2>/dev/null \
  || log "agentdef planner already registered"
loom agentdef add coder-1 --role task --auto --backend codex --repos app 2>/dev/null \
  || log "agentdef coder-1 already registered"

PLANNER_WT="$WS_ROOT/worktrees/app/planner"
CODER_WT="$WS_ROOT/worktrees/app/coder-1"
[ -d "$PLANNER_WT" ] || die "planner worktree missing at $PLANNER_WT"
[ -d "$CODER_WT" ]   || die "coder-1 worktree missing at $CODER_WT"

for wt in "$PLANNER_WT" "$CODER_WT" "$WS_ROOT/app"; do
  [ -d "$wt" ] || continue
  git -C "$wt" config user.name  "loom-marathon"
  git -C "$wt" config user.email "loom-marathon@localhost"
  if ! git -C "$wt" remote get-url origin >/dev/null 2>&1; then
    git -C "$wt" remote add origin /work/origin.git 2>/dev/null || true
  fi
done

# ---- coder prompt override ---------------------------------------------------
# The daemon leaf renders ./loom-prompts/fleet_task.md from the agent worktree cwd
# (internal/cli/agent/prompts.go). Our fork routes completion to
# IMPL-DONE comment + status review (never close, never PR-publish).
mkdir -p "$CODER_WT/loom-prompts"
cp "${LOOM_MARATHON_PROMPTS_DIR:-$MH/prompts}/fleet_task-override.md" "$CODER_WT/loom-prompts/fleet_task.md"
# Keep harness files out of the coder's commits (untracked .gitignore ignores itself).
if [ ! -f "$CODER_WT/.gitignore" ] || ! grep -q '^loom-prompts/' "$CODER_WT/.gitignore" 2>/dev/null; then
  printf 'loom-prompts/\n.gitignore\nCRITIC-VERDICT.txt\n' >> "$CODER_WT/.gitignore"
fi

# ---- codex config ------------------------------------------------------------
toml_escape() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }
# Suppress codex's interactive rate-limit model nudge (blocks controlled
# sessions on a modal — B2f POC lesson). Must be a TOP-LEVEL key, so it goes
# in before any [projects] table is appended.
if ! grep -q '^hide_rate_limit_model_nudge' "$CODEX_HOME/config.toml" 2>/dev/null; then
  { printf 'hide_rate_limit_model_nudge = true\n'; cat "$CODEX_HOME/config.toml" 2>/dev/null; } \
    > "$CODEX_HOME/config.toml.tmp" && mv "$CODEX_HOME/config.toml.tmp" "$CODEX_HOME/config.toml"
fi
trust_codex_project_path() {
  local path escaped config="$CODEX_HOME/config.toml"
  path="$1"; escaped="$(toml_escape "$path")"
  touch "$config"
  if grep -Fqx "[projects.\"${escaped}\"]" "$config"; then return 0; fi
  { printf '\n[projects."%s"]\n' "$escaped"; printf 'trust_level = "trusted"\n'; } >> "$config"
}
for p in /app "$WS_ROOT" "$WS_ROOT/app" "$PLANNER_WT" "$CODER_WT" /work; do
  trust_codex_project_path "$p"
done

# ---- verification role support (EXPERIMENTS B2c/B2d) -------------------------
# Shared by both verify arms: a detached checkout the verifier boots the app
# from (never /app itself), plus marathon-freeports — kills stray listeners on
# the app's fixed ports EXCEPT harness infrastructure (the MARATHON-9 fix).
VERIFY_ROLE="${LOOM_MARATHON_VERIFY_ROLE:-off}"
VERIFY_CHECKOUT=""
if [ "$VERIFY_ROLE" != "off" ]; then
  VERIFY_CHECKOUT="$WS_ROOT/verify-checkout"
  if [ ! -d "$VERIFY_CHECKOUT" ]; then
    git -C /app worktree add --detach "$VERIFY_CHECKOUT" >/dev/null 2>&1 \
      || die "verify-checkout worktree creation failed"
  fi
  trust_codex_project_path "$VERIFY_CHECKOUT"
  cat > /usr/local/bin/marathon-freeports <<'FPEOF'
#!/usr/bin/env python3
"""Free the app's fixed ports by killing their listeners, sparing harness
infrastructure. Verifier roles run this before booting the app when leftover
worker test processes hold a port."""
import os, socket, time
PORTS = {8000, 8001, 8002, 6667, 6379}
PROTECT = ("fleet-db", "loom daemon", "loom lead", "codex app-server",
           "codex --remote", "tmux")
SELF = {os.getpid(), os.getppid()}
def busy(p):
    s = socket.socket()
    try:
        s.bind(("127.0.0.1", p)); return False
    except OSError:
        return True
    finally:
        s.close()
def owners(ports):
    inodes = {}
    for path in ("/proc/net/tcp", "/proc/net/tcp6"):
        try:
            lines = open(path).read().splitlines()[1:]
        except OSError:
            continue
        for line in lines:
            f = line.split()
            if len(f) > 9 and f[3] == "0A":
                port = int(f[1].rsplit(":", 1)[1], 16)
                if port in ports:
                    inodes[f[9]] = port
    hits = {}
    for pid in filter(str.isdigit, os.listdir("/proc")):
        if int(pid) in SELF:
            continue
        try:
            cmd = open(f"/proc/{pid}/cmdline", "rb").read().replace(b"\0", b" ").decode(errors="replace")
            if any(pat in cmd for pat in PROTECT):
                continue
            for fd in os.listdir(f"/proc/{pid}/fd"):
                t = os.readlink(f"/proc/{pid}/fd/{fd}")
                if t.startswith("socket:[") and t[8:-1] in inodes:
                    hits.setdefault(pid, set()).add(inodes[t[8:-1]])
        except OSError:
            continue
    return hits
for _ in range(3):
    stale = {p for p in PORTS if busy(p)}
    if not stale:
        break
    h = owners(stale)
    for pid, ports in h.items():
        print(f"freeports: kill {pid} ({sorted(ports)})")
        try:
            os.kill(int(pid), 9)
        except OSError:
            pass
    if not h:
        print(f"freeports: {sorted(stale)} busy but unowned/protected")
        break
    time.sleep(1)
print("freeports:", {p: ("BUSY" if busy(p) else "free") for p in PORTS})
FPEOF
  chmod +x /usr/local/bin/marathon-freeports
  log "verify role=$VERIFY_ROLE checkout=$VERIFY_CHECKOUT freeports installed"
fi

if [ "$STUB" != "1" ]; then
  [ -s /installed-agent/codex-auth/auth.json ] || die "codex auth.json missing (real mode)"
  ln -sf /installed-agent/codex-auth/auth.json "$CODEX_HOME/auth.json"
fi
mkdir -p "$CODEX_HOME/sessions"

# ---- preflight asserts (fail fast, before any model spend) -------------------
command -v loom >/dev/null || die "loom not on PATH"
command -v codex >/dev/null || die "codex not on PATH"
if [ "$STUB" = "1" ]; then
  case "$(command -v codex)" in
    "$MH/stubbin/codex") : ;;
    *) die "stub mode but codex resolves to $(command -v codex)" ;;
  esac
fi
# fleet-db resolves via PATH ($MH/bin is prepended) / sibling-of-loom; probe it.
"$MH/bin/fleet-db" --help >/dev/null 2>&1 || "$MH/bin/fleet-db" -h >/dev/null 2>&1 \
  || log "WARN: fleet-db --help probe nonzero (may still be fine)"

# Every agent checkout must share /app's git object store (same common dir):
# guarantees local merges/FF into /app see agent commits with no remote.
APP_COMMON="$(cd /app && git rev-parse --path-format=absolute --git-common-dir)"
for wt in "$PLANNER_WT" "$CODER_WT"; do
  wt_common="$(cd "$wt" && git rev-parse --path-format=absolute --git-common-dir)"
  [ "$wt_common" = "$APP_COMMON" ] \
    || die "worktree $wt common-dir $wt_common != /app common-dir $APP_COMMON"
done

# v1 runs the Go daemon leaf; the ts leaf is the driver/flue path (not this harness).
case "${LOOM_DAEMON_LEAF:-}" in
  ""|go) : ;;
  *) die "LOOM_DAEMON_LEAF=${LOOM_DAEMON_LEAF} — v1 requires the Go daemon leaf" ;;
esac

# flock must actually CONTEND on this filesystem (virtiofs can no-op it; a
# silent no-op lets concurrent loom CLIs spawn duelling embedded fleet-db
# instances with last-writer-wins state — codex vet-A). Prove it: while one
# process holds the lock, a second nonblocking flock MUST fail.
flock_probe="$LOOM_CONFIG_DIR/.flock-probe"
( exec 9>"$flock_probe" && flock -n 9 && sleep 3 ) &
FLOCK_BG=$!
sleep 1
if ( exec 9>"$flock_probe" && flock -n 9 ) 2>/dev/null; then
  kill "$FLOCK_BG" 2>/dev/null || true
  die "flock does not contend on $LOOM_CONFIG_DIR (virtiofs no-op?) — embedded fleet-db single-instance locking would be unsafe"
fi
wait "$FLOCK_BG" 2>/dev/null || true
log "flock contention verified on $LOOM_CONFIG_DIR"

loom data list >/dev/null 2>&1 || die "loom data list failed (fleet-db not reachable)"

# ---- verification-as-tasks preflight (EXPERIMENTS B2f-revised) ---------------
# fleet-db enforces referential integrity on source_repo (codex B2f-vet
# finding 2 — CONFIRMED: the POC only passed after registering the lane), so
# qa-verify must be a registered workspace repo. Virtual lane: empty remote
# URL, never checked out. The probe then proves create+close works in THIS
# container; it leaves one closed probe task which split metrics exclude.
if [ "${LOOM_MARATHON_VERIFY_ROLE:-off}" = "tasks" ]; then
  loom repo add qa-verify "" >/dev/null 2>&1 || true  # idempotent; probe is the assertion
  PROBE_JSON=$(loom data create --type task --source-repo qa-verify \
    --title "qa-verify preflight probe (harness)" -o json) \
    || die "qa-verify preflight: create failed"
  PROBE_ID=$(printf '%s' "$PROBE_JSON" \
    | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id",""))' 2>/dev/null)
  [ -n "$PROBE_ID" ] || die "qa-verify preflight: create output unparsable: $PROBE_JSON"
  loom data update "$PROBE_ID" --status closed >/dev/null 2>&1 \
    || die "qa-verify preflight: close failed"
  log "qa-verify preflight probe ok ($PROBE_ID)"
  for pf in lead-persistent-verifier-tasks.md qa-persistent-tasks.md; do
    log "prompt-hash $pf $( (sha256sum "${LOOM_MARATHON_PROMPTS_DIR:-$MH/prompts}/$pf" 2>/dev/null || shasum -a 256 "${LOOM_MARATHON_PROMPTS_DIR:-$MH/prompts}/$pf" 2>/dev/null) | cut -c1-16)"
  done
fi

# ---- env.sh ------------------------------------------------------------------
cat > "$MH/env.sh" <<EOF
export HOME="$HOME"
export PATH="$PATH"
export LOOM_CONFIG_DIR="$LOOM_CONFIG_DIR"
export LOOM_BACKEND=codex
export LOOM_WORKSPACE="$WS_KEY"
export LOOM_FLEET_DB_ACTOR="$LOOM_FLEET_DB_ACTOR"
export USER="$USER"
export CODEX_HOME="$CODEX_HOME"
export MARATHON_APP_BASE="$APP_BASE"
export MARATHON_WS_ROOT="$WS_ROOT"
export MARATHON_CODER_WT="$CODER_WT"
export MARATHON_PLANNER_WT="$PLANNER_WT"
export MARATHON_VERIFY_CHECKOUT="$VERIFY_CHECKOUT"
EOF
log "bootstrap complete (stub=$STUB, max_agents=$MAX_AGENTS)"
