#!/usr/bin/env bash
# In-container bootstrap for the loom SWE-Marathon ensemble.
# Invoked once by orchestrate.sh. Idempotent. On success writes $MH/env.sh
# (sourced by orchestrate and inherited by every loom/daemon child process).
set -euo pipefail

MH="${LOOM_MARATHON_HOME:-/installed-agent/loom-marathon}"
export HOME="${HOME:-/root}"

STUB="${LOOM_MARATHON_STUB:-0}"
MAX_AGENTS="${LOOM_MARATHON_MAX_AGENTS:-2}"
TEAM="${LOOM_MARATHON_TEAM:-off}"
VERIFY_ROLE="${LOOM_MARATHON_VERIFY_ROLE:-off}"

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
# Daemon-worker backend (codex|cursor) and lead backend (codex|cursor; the
# lead runs loom's headless cursor runtime). spend.sh meters only codex
# rollouts: with cursor roles the spend cap covers codex roles alone and
# budget_secs is the real bound (cursor token usage is in the pane logs).
export LOOM_BACKEND="${LOOM_MARATHON_WORKER_BACKEND:-codex}"
LEAD_BACKEND="${LOOM_MARATHON_LEAD_BACKEND:-codex}"
# cursor spend records (shim-captured per-turn system/result events, priced by
# spend.sh). Host-mounted: evidence survives teardown.
export LOOM_MARATHON_CURSOR_USAGE_DIR=/logs/agent/cursor-usage
NEED_CURSOR=0
{ [ "$LOOM_BACKEND" = cursor ] || [ "$LEAD_BACKEND" = cursor ]; } && NEED_CURSOR=1
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

if [ "$TEAM" != "off" ]; then
  [ "$MAX_AGENTS" -ge 4 ] || die "team mode requires max_agents >= 4 (got $MAX_AGENTS)"
  [ "$VERIFY_ROLE" = "off" ] || die "team mode forbids verify_role=$VERIFY_ROLE"
fi

loom daemon profile set issue_backend fleetdb
# log_dir must resolve INSIDE the project/config dirs or the daemon rejects it;
# it rides to the host via the per-pass loom-state backup copies.
loom daemon profile set log_dir "$LOOM_CONFIG_DIR/daemon-logs"
loom daemon profile set events_dir "$WS_ROOT/.loom/events"
loom daemon profile set max_agents "$MAX_AGENTS"
loom daemon profile set startup_timeout 60

# planner: built-in plan role (task-filter needs_plan) — designs undesigned/needs-revision tasks
# coder-1: built-in task role (task-filter has_design) — ONE serialized implementation coder (v1)
if [ "$TEAM" = "off" ]; then
  loom agentdef add planner --role plan --auto --backend codex --repos app 2>/dev/null \
    || log "agentdef planner already registered"
  loom agentdef add coder-1 --role task --auto --backend codex --repos app 2>/dev/null \
    || log "agentdef coder-1 already registered"

  PLANNER_WT="$WS_ROOT/worktrees/app/planner"
  CODER_WT="$WS_ROOT/worktrees/app/coder-1"
  [ -d "$PLANNER_WT" ] || die "planner worktree missing at $PLANNER_WT"
  [ -d "$CODER_WT" ]   || die "coder-1 worktree missing at $CODER_WT"
else
  apply_json=/logs/agent/template-apply.json
  apply_rc=0
  loom template apply "$TEAM" --json > "$apply_json" || apply_rc=$?
  if ! python3 -c '
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception as e:
    print(f"template apply report is not valid JSON: {e}", file=sys.stderr)
    sys.exit(1)
steps = d.get("steps") or []
failed = int(d.get("failed") or 0)
if failed:
    for step in steps:
        if step.get("action") == "failed" or step.get("error"):
            print("failed step: {} {}: {}".format(step.get("entity"), step.get("name"), step.get("error")), file=sys.stderr)
    sys.exit(1)
if int(d.get("created") or 0) + int(d.get("skipped") or 0) != len(steps):
    print("template apply report is incomplete: created+skipped != len(steps)", file=sys.stderr)
    sys.exit(1)
' "$apply_json"; then
    die "template apply $TEAM failed (exit=$apply_rc)"
  fi

  loom template show "$TEAM" --json | python3 -c '
import json, os, sys
d = json.load(sys.stdin)
ws_root = sys.argv[1]
roles = {r.get("name"): r for r in d.get("roles") or []}
for agent in d.get("agents") or []:
    role_name = agent.get("role_name") or agent.get("role") or ""
    role = roles.get(role_name) or {}
    if role.get("kind") != "worker":
        continue
    labels = set(role.get("labels") or [])
    lane = "architect" if "architect" in labels else "qa" if "qa" in labels else "impl"
    prompt = role.get("prompt_file") or ""
    if prompt.startswith("builtin:"):
        prompt_id = prompt.split(":", 1)[1]
    elif lane == "architect":
        prompt_id = "team-architect"
    elif lane == "qa":
        prompt_id = "team-qa"
    else:
        prompt_id = "team-" + role_name
    name = agent.get("name") or ""
    if not name or not role_name:
        continue
    print("\t".join((name, role_name, lane, prompt_id,
                     os.path.join(ws_root, "worktrees", "app", name))))
' "$WS_ROOT" > "$MH/.team-agent-defs.tsv"
  : > "$MH/team-agents.tsv"
  while IFS=$'\t' read -r agent role lane prompt_id wt; do
    [ -n "$agent" ] || continue
    [ -d "$wt" ] || die "team worktree missing at $wt"
    branch=$(git -C "$wt" rev-parse --abbrev-ref HEAD) \
      || die "cannot read branch for team worktree $wt"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
      "$agent" "$role" "$lane" "$prompt_id" "$wt" "$branch" >> "$MH/team-agents.tsv"
  done < "$MH/.team-agent-defs.tsv"
  rm -f "$MH/.team-agent-defs.tsv"
  [ -s "$MH/team-agents.tsv" ] || die "template $TEAM has no runnable worker agents"
  # Evidence copy: the dry-run assertions and post-run analysis read it from the mount.
  cp "$MH/team-agents.tsv" /logs/agent/team-agents.tsv
  CODER_WT=$(awk -F '\t' '$3 == "impl" { print $5; exit }' "$MH/team-agents.tsv")
  PLANNER_WT=$(awk -F '\t' '$3 == "architect" { print $5; exit }' "$MH/team-agents.tsv")
  [ -n "$CODER_WT" ] || die "template $TEAM has no implementation worktree"
  [ -n "$PLANNER_WT" ] || die "template $TEAM has no architect worktree"
fi

team_worktrees() {
  if [ "$TEAM" != "off" ]; then
    cut -f5 "$MH/team-agents.tsv"
  else
    printf '%s\n%s\n' "$PLANNER_WT" "$CODER_WT"
  fi
}

if [ "$TEAM" = "off" ]; then
  for wt in "$PLANNER_WT" "$CODER_WT" "$WS_ROOT/app"; do
    [ -d "$wt" ] || continue
    git -C "$wt" config user.name  "loom-marathon"
    git -C "$wt" config user.email "loom-marathon@localhost"
    if ! git -C "$wt" remote get-url origin >/dev/null 2>&1; then
      git -C "$wt" remote add origin /work/origin.git 2>/dev/null || true
    fi
  done
else
  while IFS= read -r wt; do
    [ -d "$wt" ] || continue
    git -C "$wt" config user.name  "loom-marathon"
    git -C "$wt" config user.email "loom-marathon@localhost"
    if ! git -C "$wt" remote get-url origin >/dev/null 2>&1; then
      git -C "$wt" remote add origin /work/origin.git 2>/dev/null || true
    fi
  done < <(team_worktrees; printf '%s\n' "$WS_ROOT/app")
fi

# ---- coder prompt override ---------------------------------------------------
# The daemon leaf renders ./loom-prompts/fleet_task.md from the agent worktree cwd
# (internal/cli/agent/prompts.go). Our fork routes completion to
# IMPL-DONE comment + status review (never close, never PR-publish).
if [ "$TEAM" = "off" ]; then
  mkdir -p "$CODER_WT/loom-prompts"
  cp "${LOOM_MARATHON_PROMPTS_DIR:-$MH/prompts}/fleet_task-override.md" "$CODER_WT/loom-prompts/fleet_task.md"
  # Keep harness files out of the coder's commits (untracked .gitignore ignores itself).
  if [ ! -f "$CODER_WT/.gitignore" ] || ! grep -q '^loom-prompts/' "$CODER_WT/.gitignore" 2>/dev/null; then
    printf 'loom-prompts/\n.gitignore\nCRITIC-VERDICT.txt\n' >> "$CODER_WT/.gitignore"
  fi
else
  while IFS=$'\t' read -r agent role lane prompt_id wt branch; do
    override="${LOOM_MARATHON_PROMPTS_DIR:-$MH/prompts}/${prompt_id}-override.md"
    # Team overrides ship only in prompts-generic; tolerate the default profile.
    [ -f "$override" ] || override="$MH/prompts-generic/${prompt_id}-override.md"
    [ -f "$override" ] || die "no override for $prompt_id (looked in ${LOOM_MARATHON_PROMPTS_DIR:-$MH/prompts} and $MH/prompts-generic)"
    mkdir -p "$wt/loom-prompts"
    cp "$override" "$wt/loom-prompts/$prompt_id.md"
    if [ ! -f "$wt/.gitignore" ] || ! grep -q '^loom-prompts/' "$wt/.gitignore" 2>/dev/null; then
      printf 'loom-prompts/\n.gitignore\nCRITIC-VERDICT.txt\n' >> "$wt/.gitignore"
    fi
  done < "$MH/team-agents.tsv"
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
if [ "$TEAM" = "off" ]; then
  for p in /app "$WS_ROOT" "$WS_ROOT/app" "$PLANNER_WT" "$CODER_WT" /work; do
    trust_codex_project_path "$p"
  done
else
  for p in /app "$WS_ROOT" "$WS_ROOT/app" /work; do
    trust_codex_project_path "$p"
  done
  while IFS= read -r p; do trust_codex_project_path "$p"; done < <(team_worktrees)
fi

# ---- verification role support (EXPERIMENTS B2c/B2d) -------------------------
# Shared by both verify arms: a detached checkout the verifier boots the app
# from (never /app itself), plus marathon-freeports — kills stray listeners on
# the app's fixed ports EXCEPT harness infrastructure (the MARATHON-9 fix).
VERIFY_CHECKOUT=""
VERIFY_CHECKOUT_BACKEND=""
VERIFY_CHECKOUT_ARCH=""
install_freeports() {
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
}
if [ "$VERIFY_ROLE" != "off" ]; then
  VERIFY_CHECKOUT="$WS_ROOT/verify-checkout"
  if [ ! -d "$VERIFY_CHECKOUT" ]; then
    git -C /app worktree add --detach "$VERIFY_CHECKOUT" >/dev/null 2>&1 \
      || die "verify-checkout worktree creation failed"
  fi
  trust_codex_project_path "$VERIFY_CHECKOUT"
  if [ "${LOOM_MARATHON_ARCH:-off}" = "on" ]; then
    # B2j architect: its own read-only detached worktree (never runs the app,
    # so no port coordination needed; shares /app's object store for diffs).
    VERIFY_CHECKOUT_ARCH="$WS_ROOT/arch-checkout"
    if [ ! -d "$VERIFY_CHECKOUT_ARCH" ]; then
      git -C /app worktree add --detach "$VERIFY_CHECKOUT_ARCH" >/dev/null 2>&1 \
        || die "arch-checkout worktree creation failed"
    fi
    trust_codex_project_path "$VERIFY_CHECKOUT_ARCH"
  fi
  if [ "$VERIFY_ROLE" = "tasks-dual" ]; then
    # Second verifier gets its own checkout; the two QA sessions never run
    # the app simultaneously (orchestrate alternates passes — spec-pinned
    # ports), but each keeps its own working tree and probe scripts.
    VERIFY_CHECKOUT_BACKEND="$WS_ROOT/verify-checkout-backend"
    if [ ! -d "$VERIFY_CHECKOUT_BACKEND" ]; then
      git -C /app worktree add --detach "$VERIFY_CHECKOUT_BACKEND" >/dev/null 2>&1 \
        || die "verify-checkout-backend worktree creation failed"
    fi
    trust_codex_project_path "$VERIFY_CHECKOUT_BACKEND"
  fi
  install_freeports
  log "verify role=$VERIFY_ROLE checkout=$VERIFY_CHECKOUT freeports installed"
fi

if [ "$TEAM" != "off" ]; then
  install_freeports
  cat > /usr/local/bin/marathon-portlock <<'PLEOF'
#!/usr/bin/env bash
exec 9>/work/.app-ports.lock
flock 9
exec "$@"
PLEOF
  chmod +x /usr/local/bin/marathon-portlock
  log "team portlock and freeports installed"
fi

# NEED_CODEX: stub always (fake codex), real mode only when a role uses codex.
NEED_CODEX=1
[ "$STUB" != "1" ] && [ "${LOOM_MARATHON_NO_CODEX:-0}" = 1 ] && NEED_CODEX=0
if [ "$STUB" != "1" ] && [ "$NEED_CODEX" = 1 ]; then
  [ -s /installed-agent/codex-auth/auth.json ] || die "codex auth.json missing (real mode)"
  ln -sf /installed-agent/codex-auth/auth.json "$CODEX_HOME/auth.json"
fi
mkdir -p "$CODEX_HOME/sessions"

# cursor workers: key lives only in the container-only 600 file; exported into
# this process env (the daemon inherits it; loom's envfilter allowlists
# CURSOR_API_KEY into every agent subprocess). Never echoed, never in env.sh.
if [ "$NEED_CURSOR" = 1 ]; then
  [ "$STUB" != "1" ] || die "cursor backends have no stub"
  [ -s /installed-agent/cursor-auth/api-key ] || die "cursor api-key missing (cursor backend requested)"
  CURSOR_API_KEY="$(tr -d '[:space:]' < /installed-agent/cursor-auth/api-key)"
  export CURSOR_API_KEY
  [ -n "$CURSOR_API_KEY" ] || die "cursor api-key file is empty"
fi

# ---- preflight asserts (fail fast, before any model spend) -------------------
command -v loom >/dev/null || die "loom not on PATH"
[ "$NEED_CODEX" = 0 ] || command -v codex >/dev/null || die "codex not on PATH"
if [ "$NEED_CURSOR" = 1 ]; then
  command -v cursor-agent >/dev/null || die "cursor-agent not on PATH (cursor backend requested)"
  # Spend accounting must be writable before any paid turn: the shim refuses
  # to run an unmetered turn (exit 97), so prove the rail here at \$0.
  mkdir -p "$LOOM_MARATHON_CURSOR_USAGE_DIR" \
    && : > "$LOOM_MARATHON_CURSOR_USAGE_DIR/.preflight" \
    && rm -f "$LOOM_MARATHON_CURSOR_USAGE_DIR/.preflight" \
    || die "cursor usage dir not writable: $LOOM_MARATHON_CURSOR_USAGE_DIR"
  # `cursor-agent status` exits 0 even when unauthenticated. Observed text:
  # logged out -> "Not logged in"; logged in -> "✓ Logged in as <email>".
  # Reject the negative forms first, then require the positive one.
  CURSOR_STATUS=$(timeout 60 cursor-agent status 2>&1 || true)
  case "$CURSOR_STATUS" in
    *"Not logged in"*|*"not logged in"*|*"Authentication required"*|*"Error"*)
      die "cursor-agent not authenticated: ${CURSOR_STATUS}" ;;
    *"Logged in as"*)
      log "cursor-agent $(cursor-agent --version 2>/dev/null) authenticated (workers=$LOOM_BACKEND lead=$LEAD_BACKEND)" ;;
    *) die "cursor-agent status unrecognized: ${CURSOR_STATUS:-<no output>}" ;;
  esac
fi
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
if [ "$TEAM" = "off" ]; then
  for wt in "$PLANNER_WT" "$CODER_WT"; do
    wt_common="$(cd "$wt" && git rev-parse --path-format=absolute --git-common-dir)"
    [ "$wt_common" = "$APP_COMMON" ] \
      || die "worktree $wt common-dir $wt_common != /app common-dir $APP_COMMON"
  done
else
  while IFS= read -r wt; do
    wt_common="$(cd "$wt" && git rev-parse --path-format=absolute --git-common-dir)"
    [ "$wt_common" = "$APP_COMMON" ] \
      || die "worktree $wt common-dir $wt_common != /app common-dir $APP_COMMON"
  done < <(team_worktrees)
fi

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
probe_lane() { # $1 lane -> registers the virtual lane repo and proves create+close
  loom repo add "$1" "" >/dev/null 2>&1 || true  # idempotent; probe is the assertion
  local pj pid
  pj=$(loom data create --type task --source-repo "$1" \
    --title "$1 preflight probe (harness)" -o json) \
    || die "$1 preflight: create failed"
  pid=$(printf '%s' "$pj" \
    | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id",""))' 2>/dev/null)
  [ -n "$pid" ] || die "$1 preflight: create output unparsable: $pj"
  loom data update "$pid" --status closed >/dev/null 2>&1 \
    || die "$1 preflight: close failed"
  log "$1 preflight probe ok ($pid)"
}
case "${LOOM_MARATHON_VERIFY_ROLE:-off}" in
tasks|tasks-dual)
  probe_lane qa-verify
  if [ "${LOOM_MARATHON_VERIFY_ROLE:-off}" = "tasks-dual" ]; then
    probe_lane qa-verify-backend
  fi
  # Hash EVERY prompt file in the active profile (codex B2j-vet finding 10:
  # the run manifest must cover all prompts, arch and lead variants included).
  for pf in "${LOOM_MARATHON_PROMPTS_DIR:-$MH/prompts}"/*.md; do
    log "prompt-hash $(basename "$pf") $( (sha256sum "$pf" 2>/dev/null || shasum -a 256 "$pf" 2>/dev/null) | cut -c1-16)"
  done
  ;;
esac

# Team mode hashes the same active prompt profile; verify roles are forbidden,
# so this never duplicates the legacy tasks/tasks-dual loop above.
if [ "$TEAM" != "off" ]; then
  for pf in "${LOOM_MARATHON_PROMPTS_DIR:-$MH/prompts}"/*.md; do
    log "prompt-hash $(basename "$pf") $( (sha256sum "$pf" 2>/dev/null || shasum -a 256 "$pf" 2>/dev/null) | cut -c1-16)"
  done
fi

TEAM_WTS=""
IMPL_WTS=""
QA_WT=""
ARCH_WT=""
if [ "$TEAM" != "off" ]; then
  TEAM_WTS=$(cut -f5 "$MH/team-agents.tsv" | paste -sd' ' -)
  IMPL_WTS=$(awk -F '\t' '$3 == "impl" { print $5 }' "$MH/team-agents.tsv" | paste -sd' ' -)
  QA_WT=$(awk -F '\t' '$3 == "qa" { print $5; exit }' "$MH/team-agents.tsv")
  ARCH_WT=$(awk -F '\t' '$3 == "architect" { print $5; exit }' "$MH/team-agents.tsv")
fi

# ---- env.sh ------------------------------------------------------------------
cat > "$MH/env.sh" <<EOF
export HOME="$HOME"
export PATH="$PATH"
export LOOM_CONFIG_DIR="$LOOM_CONFIG_DIR"
export LOOM_BACKEND="$LOOM_BACKEND"
export LOOM_WORKSPACE="$WS_KEY"
export LOOM_FLEET_DB_ACTOR="$LOOM_FLEET_DB_ACTOR"
export USER="$USER"
export CODEX_HOME="$CODEX_HOME"
export LOOM_MARATHON_CURSOR_USAGE_DIR="$LOOM_MARATHON_CURSOR_USAGE_DIR"
${LOOM_MARATHON_CURSOR_MODEL:+export LOOM_MARATHON_CURSOR_MODEL="$LOOM_MARATHON_CURSOR_MODEL"}
if [ -s /installed-agent/cursor-auth/api-key ]; then
  CURSOR_API_KEY="\$(tr -d '[:space:]' < /installed-agent/cursor-auth/api-key)"; export CURSOR_API_KEY
fi
export MARATHON_APP_BASE="$APP_BASE"
export MARATHON_APP_DIR=/app
export MARATHON_WS_ROOT="$WS_ROOT"
export MARATHON_CODER_WT="$CODER_WT"
export MARATHON_PLANNER_WT="$PLANNER_WT"
export MARATHON_TEAM="$TEAM"
export MARATHON_TEAM_TSV="$MH/team-agents.tsv"
export MARATHON_TEAM_WTS="$TEAM_WTS"
export MARATHON_IMPL_WTS="$IMPL_WTS"
export MARATHON_QA_WT="$QA_WT"
export MARATHON_ARCH_WT="$ARCH_WT"
export MARATHON_VERIFY_CHECKOUT="$VERIFY_CHECKOUT"
export MARATHON_VERIFY_CHECKOUT_BACKEND="$VERIFY_CHECKOUT_BACKEND"
export MARATHON_ARCH_CHECKOUT="$VERIFY_CHECKOUT_ARCH"
EOF
log "bootstrap complete (stub=$STUB, max_agents=$MAX_AGENTS, team=$TEAM)"
