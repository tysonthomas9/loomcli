#!/usr/bin/env bash
# SWE-Marathon orchestrator for the FRACTAL arm (plasma-fractal): a recursive
# self-organizing node tree drives codex; this script only bootstraps, caps,
# polls, enforces the timer reserve, merges, and preserves evidence.
set -uo pipefail

LOGD=/logs/agent; mkdir -p "$LOGD"
BUDGET="${FRACTAL_BUDGET_SECS:-14400}"
RESERVE="${FRACTAL_RESERVE_SECS:-2400}"
MAXCOST="${FRACTAL_MAX_COST:-90}"
MODEL="${FRACTAL_MODEL:-gpt-5.5}"
DEPTH="${FRACTAL_MAX_DEPTH:-3}"
CHILDREN="${FRACTAL_MAX_CHILDREN:-4}"
DESC="${FRACTAL_MAX_DESCENDANTS:-10}"
ITERS="${FRACTAL_MAX_ITERS:-60}"
MISSION_MODE="${FRACTAL_MISSION_MODE:-guided}"
RESERVE_BUDGET="${FRACTAL_RESERVE_BUDGET:-}"
MH="${FRACTAL_HOME:-/installed-agent/fractal-marathon}"
export HOME="${HOME:-/root}"
START=$(date +%s); DEADLINE=$((START + BUDGET - RESERVE))

log() { printf '[fractal-orch %s] %s\n' "$(date -u +%H:%M:%S)" "$*"; }

git config --global user.name  "fractal-marathon" 2>/dev/null || true
git config --global user.email "fractal@localhost" 2>/dev/null || true
git config --global init.defaultBranch main 2>/dev/null || true
git config --global --add safe.directory '*' 2>/dev/null || true

cd /app || exit 1
git rev-parse --git-dir >/dev/null 2>&1 || git init -q -b main
git rev-parse HEAD >/dev/null 2>&1 || { git add -A; git commit -q --allow-empty -m "marathon baseline"; }
[ -d /logs/agent/app-mirror.git ] || git init -q --bare -b main /logs/agent/app-mirror.git

# link codex auth into every fractal per-node codex config dir (fractal points
# CODEX_HOME at <node_dir>/.codex; auth lives at ~/.codex/auth.json)
link_auth() {
  find /app/.worktrees -type d -name '.codex' 2>/dev/null | while read -r d; do
    [ -e "$d/auth.json" ] || ln -sf "$HOME/.codex/auth.json" "$d/auth.json"
  done
}

# Priced-alias injection ([O], disclosed): fractal's cost cap requires the
# model in its LiteLLM pricing cache. For models the registry lacks (e.g.
# gpt-5.3-codex-spark), FRACTAL_PRICE_ALIAS="target=source" copies the source
# model's rates to the target id — conservative when the source is the pricier
# family member (the cap binds earlier than truth).
if [ -n "${FRACTAL_PRICE_ALIAS:-}" ]; then
  python3 - "$FRACTAL_PRICE_ALIAS" <<'PY' || { log "FATAL: price alias failed"; exit 1; }
import json, pathlib, sys, urllib.request
target, source = sys.argv[1].split("=", 1)
cache = pathlib.Path.home() / ".fractal" / "pricing.json"
cache.parent.mkdir(parents=True, exist_ok=True)
if not cache.exists():
    url = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
    cache.write_bytes(urllib.request.urlopen(url, timeout=30).read())
d = json.loads(cache.read_text())
src = d.get(source) or d.get(f"openrouter/{source}")
assert src, f"source model {source} not in pricing table"
d[target] = dict(src)
cache.write_text(json.dumps(d))
print(f"priced alias: {target} <- {source}")
PY
  # Kill the refetch at the source: chmod is useless against root
  # (CAP_DAC_OVERRIDE — proven in spark-2), so point the installed package's
  # pricing URL at a dead host; update() then falls back to the existing
  # cache ('stale') by design. Targeted to fractal only; disclosed [O].
  PRICING_MOD=$(/opt/venv/bin/python -c "import fractal.core.pricing as m; print(m.__file__)" 2>/dev/null || python3 -c "import fractal.core.pricing as m; print(m.__file__)")
  [ -n "$PRICING_MOD" ] && sed -i "s|raw.githubusercontent.com|invalid.pricing-refetch-disabled.localhost|" "$PRICING_MOD" \
    && log "pricing refetch disabled in $PRICING_MOD"
  # Children spawned by the agent may omit --model (observed with spark);
  # fractal's cost-cap check accepts a step-frontmatter `model:` — pin it in
  # the PACKAGE seed steps so every node (root and children) inherits it.
  TARGET_MODEL="${FRACTAL_PRICE_ALIAS%%=*}"
  SEED_DIR=$(dirname "$(find /opt/venv -path '*fractal/_node/steps/00-PREPARE.md' 2>/dev/null | head -1)")
  if [ -d "$SEED_DIR" ]; then
    for f in "$SEED_DIR"/*.md; do
      grep -q '^model:' "$f" || sed -i "0,/^---$/{s/^---$/---\nmodel: $TARGET_MODEL/}" "$f"
    done
    log "seed steps pinned to model $TARGET_MODEL"
  fi
fi

# ---- fractal init + root node -----------------------------------------------
if [ ! -d wiki ]; then
  fractal init --agent=codex >> "$LOGD/fractal-init.log" 2>&1 || { log "FATAL: fractal init failed"; exit 1; }
  git add -A && git commit -q -m "fractal scaffold"
fi

NODE=root
if ! fractal node status "$NODE" >/dev/null 2>&1; then
  RESERVE_ARGS=""
  # fractal defaults to a hidden 10% reserve-budget wind-down; generic runs
  # pin it explicitly so the money cap means what the ledger says (codex vet).
  [ -n "$RESERVE_BUDGET" ] && RESERVE_ARGS="--reserve-budget $RESERVE_BUDGET"
  fractal node init "$NODE" --agent codex --model "$MODEL" \
    --max-cost "$MAXCOST" --max-iters "$ITERS" --max-depth "$DEPTH" \
    --max-children "$CHILDREN" --max-descendants "$DESC" $RESERVE_ARGS \
    >> "$LOGD/fractal-init.log" 2>&1 || { log "FATAL: node init failed"; tail -5 "$LOGD/fractal-init.log"; exit 1; }

  NODE_MD=$(find /app/.worktrees -path '*/.fractal/*/NODE.md' 2>/dev/null | head -1)
  [ -n "$NODE_MD" ] || { log "FATAL: NODE.md not found after node init"; exit 1; }
  python3 - "$NODE_MD" "$MH/instruction.md" "$MISSION_MODE" <<'PY' || { echo "FATAL: mission injection failed"; exit 1; }
import sys, pathlib
node_md, instr = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
mode = sys.argv[3]
if mode == "generic":
    # EXPERIMENTS.md B3: verbatim spec + the tool-native completion sentence,
    # nothing else. No subsystem examples, no spawn advice, no port rules.
    mission = instr.read_text()
    done_when = (
        "When the specification is satisfied and your work is committed, run "
        "`fractal node finish --reason='mission complete'`."
    )
else:
    mission = (
        "Build the application described below at the WORKTREE ROOT. Spawn child "
        "nodes for separable subsystems (e.g. backend cluster, IRC gateway, "
        "frontend SPA, resilience hardening) so work proceeds in parallel where "
        "possible; keep every child capped.\n\nRULES: never leave any process "
        "running or port occupied when a step ends; commit all work; do not touch "
        "anything outside your worktree.\n\n----- MISSION SPEC -----\n\n"
        + instr.read_text()
    )
    done_when = (
        "The application at the worktree root satisfies the mission spec, "
        "`bash start.sh` boots services that pass their own health endpoints, "
        "everything is committed, and no processes are left running. Then run "
        "`fractal node finish --reason='mission complete'`."
    )
s = node_md.read_text()
hits = 0
out = []
for line in s.splitlines(keepends=True):
    if "Author the node" in line and "goals and directions" in line:
        out.append(mission + "\n"); hits += 1
    elif "Author the node" in line and "completion conditions" in line:
        out.append(done_when + "\n"); hits += 1
    else:
        out.append(line)
assert hits == 2, f"expected 2 placeholders, replaced {hits}"
node_md.write_text("".join(out))
print(f"mission injected into {node_md}")
PY
fi
link_auth

# ---- start + poll -----------------------------------------------------------
fractal node start "$NODE" >> "$LOGD/fractal-start.log" 2>&1 || { log "FATAL: node start failed"; tail -5 "$LOGD/fractal-start.log"; exit 1; }
log "root node started (model=$MODEL caps: cost=$MAXCOST iters=$ITERS depth=$DEPTH children=$CHILDREN desc=$DESC; deadline t+$((BUDGET - RESERVE))s)"

FINISH_SENT=0; STOP_SENT=0
while :; do
  NOW=$(date +%s)
  ST=$(fractal node status "$NODE" 2>/dev/null | head -3 | tr '\n' ' ')
  printf '%s %s\n' "$(date -u +%FT%TZ)" "$ST" >> "$LOGD/fractal-status.log"
  fractal cost > "$LOGD/fractal-cost.txt" 2>/dev/null
  git -C /app push -q /logs/agent/app-mirror.git '+refs/*:refs/*' 2>/dev/null || true
  link_auth
  case "$ST" in
    *completed*|*merged*|*retired*|*failed*|*killed*) log "settled: $ST"; break ;;
    *stopped*) [ "$STOP_SENT" = 1 ] && { log "stopped per signal"; break; } ; log "node stopped on its own"; break ;;
  esac
  if [ "$NOW" -ge "$DEADLINE" ] && [ "$FINISH_SENT" = 0 ]; then
    log "reserve reached — sending tree-wide finish"
    fractal node finish "$NODE" --reason "time budget reserve reached" >/dev/null 2>&1
    FINISH_SENT=1; DEADLINE=$((NOW + 900))
  elif [ "$NOW" -ge "$DEADLINE" ] && [ "$FINISH_SENT" = 1 ] && [ "$STOP_SENT" = 0 ]; then
    log "finish did not drain — sending stop"
    fractal node stop "$NODE" >/dev/null 2>&1
    STOP_SENT=1; DEADLINE=$((NOW + 300))
  elif [ "$NOW" -ge "$DEADLINE" ]; then
    log "stop did not land — abandoning wait"
    break
  fi
  sleep 60
done

# ---- merge + finalize -------------------------------------------------------
fractal node merge "$NODE" >> "$LOGD/fractal-merge.log" 2>&1 && log "root merged" \
  || log "WARN: merge failed (see fractal-merge.log) — scoring the main tree as-is"
git -C /app checkout -q main 2>/dev/null || true

tmux kill-server 2>/dev/null
pkill -9 -f 'c[o]dex' 2>/dev/null
sleep 1

python3 - <<'PY' >> "$LOGD/port-sweep.log" 2>&1
import os, socket, time
PORTS = (8000, 8001, 8002, 6667, 6379)
SELF = {os.getpid(), os.getppid()}
def busy(p):
    s = socket.socket()
    try: s.bind(("127.0.0.1", p)); return False
    except OSError: return True
    finally: s.close()
def owners(ports):
    inodes = {}
    for path in ("/proc/net/tcp", "/proc/net/tcp6"):
        try: lines = open(path).read().splitlines()[1:]
        except OSError: continue
        for line in lines:
            f = line.split()
            if len(f) > 9 and f[3] == "0A":
                port = int(f[1].rsplit(":", 1)[1], 16)
                if port in ports: inodes[f[9]] = port
    hits = {}
    for pid in filter(str.isdigit, os.listdir("/proc")):
        if int(pid) in SELF: continue
        try:
            for fd in os.listdir(f"/proc/{pid}/fd"):
                t = os.readlink(f"/proc/{pid}/fd/{fd}")
                if t.startswith("socket:[") and t[8:-1] in inodes:
                    hits.setdefault(pid, set()).add(inodes[t[8:-1]])
        except OSError: continue
    return hits
for _ in range(3):
    stale = {p for p in PORTS if busy(p)}
    if not stale: break
    h = owners(stale)
    for pid, ports in h.items():
        print(f"kill {pid} ({sorted(ports)})"); os.kill(int(pid), 9)
    if not h: print(f"stale {sorted(stale)} unowned"); break
    time.sleep(2)
for p in PORTS: print(p, "BUSY" if busy(p) else "free")
PY

git -C /app push -q /logs/agent/app-mirror.git '+refs/*:refs/*' 2>/dev/null || true
tar -C / -czf "$LOGD/app-snapshot.tar.gz" app 2>/dev/null
fractal cost > "$LOGD/fractal-cost-final.txt" 2>/dev/null
fractal node status "$NODE" > "$LOGD/fractal-status-final.txt" 2>/dev/null
find /app -name '*.sqlite*' -path '*fractal*' -exec cp {} "$LOGD/" \; 2>/dev/null
git -C /app log --oneline > "$LOGD/app-git-log.txt" 2>/dev/null
log "finalize complete"
exit 0
