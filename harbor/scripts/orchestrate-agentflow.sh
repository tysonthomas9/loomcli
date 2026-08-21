#!/usr/bin/env bash
# SWE-Marathon orchestrator for the AGENTFLOW-LEAD arm (EXPERIMENTS.md B4a):
# a codex LEAD session generates and drives agentflow pipelines; this script
# provides only [O]/[H] machinery — baseline, auth, the uniform lifecycle
# relaunch (rule 4), spend/time rails, sanitation (rule 5), and evidence.
# Paths are overridable so the same script runs in the host micro-smoke.
set -uo pipefail

APP="${AGENTFLOW_APP_DIR:-/app}"
LOGD="${AGENTFLOW_LOGD:-/logs/agent}"
MH="${AGENTFLOW_HOME:-/installed-agent/agentflow-marathon}"
BUDGET="${AGENTFLOW_BUDGET_SECS:-14400}"
RESERVE="${AGENTFLOW_RESERVE_SECS:-2400}"
SPEND_CAP="${AGENTFLOW_SPEND_CAP_USD:-200}"
MODEL="${AGENTFLOW_MODEL:-gpt-5.5}"
MAX_RELAUNCH="${AGENTFLOW_MAX_RELAUNCH:-20}"
export HOME="${HOME:-/root}"
export CODEX_HOME="${AGENTFLOW_CODEX_HOME:-$LOGD/codex-home}"
DONE_FILE="$APP/.harness-done"
START=$(date +%s); DEADLINE=$((START + BUDGET - RESERVE))

mkdir -p "$LOGD" "$CODEX_HOME/sessions"
log() { printf '[agentflow-orch %s] %s\n' "$(date -u +%H:%M:%S)" "$*"; }

git config --global user.name  "agentflow-marathon" 2>/dev/null || true
git config --global user.email "agentflow@localhost" 2>/dev/null || true
git config --global init.defaultBranch main 2>/dev/null || true
git config --global --add safe.directory '*' 2>/dev/null || true

cd "$APP" || exit 1
git rev-parse --git-dir >/dev/null 2>&1 || git init -q -b main
git rev-parse HEAD >/dev/null 2>&1 || { git add -A; git commit -q --allow-empty -m "marathon baseline"; }
[ -d "$LOGD/app-mirror.git" ] || git init -q --bare -b main "$LOGD/app-mirror.git"

# codex auth + trust ([O])
if [ -s /installed-agent/codex-auth/auth.json ]; then
  ln -sf /installed-agent/codex-auth/auth.json "$CODEX_HOME/auth.json"
elif [ -s "$HOME/.codex/auth.json" ] && [ ! -e "$CODEX_HOME/auth.json" ]; then
  ln -sf "$HOME/.codex/auth.json" "$CODEX_HOME/auth.json"
fi
{ printf '\n[projects."%s"]\ntrust_level = "trusted"\n' "$APP"; } >> "$CODEX_HOME/config.toml"

command -v agentflow >/dev/null || { log "FATAL: agentflow not on PATH"; exit 1; }
command -v codex >/dev/null || { log "FATAL: codex not on PATH"; exit 1; }
rm -f "$DONE_FILE"

# Lead prompt ([O]: verbatim instruction + one completion sentence + tool pointer)
LEAD_PROMPT="$LOGD/lead-prompt.md"
{
  cat "$MH/instruction.md"
  # BINDING framing (agentflow-lead-2 fix): tool use is the arm's DEFINITION
  # ([O]), not an option — the lead-1 "available" phrasing let the lead skip
  # the engine entirely and degenerate into a plain single session.
  printf '\n\n---\n\nPerform this work by authoring and running `agentflow` pipelines: write a pipeline YAML, validate it with `agentflow validate`, execute it with `agentflow run --work-dir=%s`, read the results, and revise or extend the pipeline as needed until done. All implementation work must run through pipeline steps. The engine mechanical reference is at %s — read it before use.\n' "$APP" "$MH/tool-reference.md"
  printf 'When the specification above is satisfied and your work is committed, write the single word DONE to %s and stop.\n' "$DONE_FILE"
} > "$LEAD_PROMPT"

spend_usd() {
  bash "$MH/spend.sh" 2>/dev/null \
    | python3 -c 'import sys,json;print(json.load(sys.stdin).get("est_cost_usd",0))' 2>/dev/null || echo 0
}
over_cap() {
  python3 -c 'import sys;sys.exit(0 if float(sys.argv[1])>=float(sys.argv[2]) else 1)' "$(spend_usd)" "$SPEND_CAP"
}

launch_lead() {
  local n="$1"
  if [ "$n" -eq 0 ]; then
    ( cd "$APP" && codex exec --json --dangerously-bypass-approvals-and-sandbox \
        --model "$MODEL" -- "$(cat "$LEAD_PROMPT")" </dev/null >> "$LOGD/lead.log" 2>&1 ) &
  else
    # Uniform lifecycle relaunch (rule 4): mechanical continue, no new strategy.
    ( cd "$APP" && codex exec resume --last --json \
        -- "continue until the completion condition in your original instructions holds" \
        </dev/null >> "$LOGD/lead.log" 2>&1 ) &
  fi
  CODEX_PID=$!
  log "lead session launched (relaunch=$n pid=$CODEX_PID)"
}

FINALIZE_REASON=unknown
RELAUNCH=0
launch_lead 0
while :; do
  NOW=$(date +%s)
  git -C "$APP" push -q "$LOGD/app-mirror.git" '+refs/*:refs/*' 2>/dev/null || true
  if [ -f "$DONE_FILE" ]; then FINALIZE_REASON=done-signal; log "done-file present"; break; fi
  if [ "$NOW" -ge "$DEADLINE" ]; then FINALIZE_REASON=deadline; break; fi
  if over_cap; then FINALIZE_REASON=spend-cap; log "spend cap: \$$(spend_usd)"; break; fi
  if ! kill -0 "$CODEX_PID" 2>/dev/null; then
    if [ "$RELAUNCH" -ge "$MAX_RELAUNCH" ]; then FINALIZE_REASON=relaunch-cap; break; fi
    RELAUNCH=$((RELAUNCH + 1))
    launch_lead "$RELAUNCH"
  fi
  sleep 30
done

# ---- finalize ---------------------------------------------------------------
kill -9 "$CODEX_PID" 2>/dev/null
pkill -9 -f 'c[o]dex exec' 2>/dev/null
sleep 1

# Uniform sanitation (rule 5) — pre-sweep state logged.
python3 - <<'PY' >> "$LOGD/port-sweep.log" 2>&1
import os, socket, time
PORTS = (8000, 8001, 8002, 6667, 6379)
SELF = {os.getpid(), os.getppid()}
def busy(p):
    s = socket.socket()
    try: s.bind(("127.0.0.1", p)); return False
    except OSError: return True
    finally: s.close()
print("pre-sweep:", {p: ("BUSY" if busy(p) else "free") for p in PORTS})
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
print("post-sweep:", {p: ("BUSY" if busy(p) else "free") for p in PORTS})
PY

git -C "$APP" push -q "$LOGD/app-mirror.git" '+refs/*:refs/*' 2>/dev/null || true
tar -C "$(dirname "$APP")" -czf "$LOGD/app-snapshot.tar.gz" "$(basename "$APP")" 2>/dev/null
git -C "$APP" log --oneline > "$LOGD/app-git-log.txt" 2>/dev/null
python3 - "$FINALIZE_REASON" "$RELAUNCH" <<'PY' > "$LOGD/usage-summary.json"
import json, subprocess, sys, os
try:
    spend = json.loads(subprocess.run(
        ["bash", os.environ.get("AGENTFLOW_HOME", "/installed-agent/agentflow-marathon") + "/spend.sh"],
        capture_output=True, text=True).stdout or "{}")
except Exception:
    spend = {}
for k, v in (("input_tokens", 0), ("output_tokens", 0), ("est_cost_usd", 0.0)):
    spend.setdefault(k, v)
spend.update(finalize_reason=sys.argv[1], relaunches=int(sys.argv[2]))
print(json.dumps(spend, indent=1))
PY
log "finalize complete: reason=$FINALIZE_REASON relaunches=$RELAUNCH spend=\$$(spend_usd)"
exit 0
