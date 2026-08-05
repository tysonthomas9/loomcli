#!/usr/bin/env bash
# Heartbeat + deterministic gates for the loom SWE-Marathon ensemble.
#
# Division of labor (codex-vetted R3/R4): LLM agents judge (decompose, design,
# implement, review-verdict); THIS script owns the deterministic plumbing:
#   - lead seed pass + periodic lead orchestrate passes (headless one-shots)
#   - starting/stopping the loom daemon (planner + coder agents)
#   - invoking the CRITIC one-shot per (task, attempt) with a concrete contract
#   - the ATOMIC integration gate: check candidate in a disposable worktree
#     BEFORE fast-forwarding /app; a failed check never touches /app
#   - the harness is the SOLE closer of implementation tasks
#   - aggregate spend rail + timer-reserve finalize + port sweep + evidence dump
set -uo pipefail

MH="${LOOM_MARATHON_HOME:-/installed-agent/loom-marathon}"
PROMPTS="${LOOM_MARATHON_PROMPTS_DIR:-$MH/prompts}"
CRITIC_MODE="${LOOM_MARATHON_CRITIC:-auto}"
LOGD=/logs/agent
mkdir -p "$LOGD"

START_TS=$(date +%s)
BUDGET="${LOOM_MARATHON_BUDGET_SECS:-14400}"
RESERVE="${LOOM_MARATHON_RESERVE_SECS:-2400}"
CADENCE="${LOOM_MARATHON_CADENCE_SECS:-360}"
SPEND_CAP="${LOOM_MARATHON_SPEND_CAP_USD:-90}"
STUB="${LOOM_MARATHON_STUB:-0}"
LEAD_MODE="${LOOM_MARATHON_LEAD_MODE:-oneshot}"
# The stub backend has no app-server; persistent mode is real-codex only.
if [ "$STUB" = "1" ] && [ "$LEAD_MODE" = "persistent" ]; then
  echo "[orchestrate] WARN: stub mode forces lead_mode=oneshot" >&2
  LEAD_MODE=oneshot
fi
# Verification-role fork (EXPERIMENTS B2c lead / B2d qa); requires persistence.
VERIFY_ROLE="${LOOM_MARATHON_VERIFY_ROLE:-off}"
[ "$STUB" = "1" ] && VERIFY_ROLE=off
if [ "$VERIFY_ROLE" != "off" ] && [ "$LEAD_MODE" != "persistent" ]; then
  echo "[orchestrate] FATAL: verify_role=$VERIFY_ROLE requires lead_mode=persistent" >&2
  exit 1
fi
DEADLINE=$((START_TS + BUDGET - RESERVE))

INTEG_LOG="$LOGD/integration.log"
FINALIZE_REASON="unknown"
DAEMON_PID=""
TASKS_SEEDED=0

log() { printf '[orchestrate %s] %s\n' "$(date -u +%H:%M:%S)" "$*"; }
record() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >> "$INTEG_LOG"; }

# ---------------------------------------------------------------- bootstrap --
if ! bash "$MH/scripts/bootstrap.sh"; then
  echo "[orchestrate] FATAL: bootstrap failed" >&2
  exit 1
fi
# shellcheck disable=SC1091
. "$MH/env.sh"
touch "$INTEG_LOG"

# ---------------------------------------------------------------- helpers ----
# lead_pass PROMPT_FILE MESSAGE TIMEOUT LOGFILE [CWD]
lead_pass() {
  local prompt_file="$1" message="$2" tmo="$3" logfile="$4" cwd="${5:-$MARATHON_WS_ROOT/app}"
  (
    cd "$cwd" || exit 1
    LOOM_LEAD_CONTROLLED=0 timeout "$tmo" \
      loom lead --backend codex --prompt "$prompt_file" --message "$message" \
      </dev/null >> "$LOGD/$logfile" 2>&1
  )
}

# ---- persistent lead (LOOM_MARATHON_LEAD_MODE=persistent, EXPERIMENTS B2b) ---
# ONE controlled `loom lead` session (codex app-server + persistent thread)
# lives in tmux for the whole run; seed and passes are injected cross-process
# via leadmsg (bundle bin -> leadcontrol.DeliverLeadMessageWithOptions).
# POC-proven semantics: "delivered" when idle, "pending" (inbox-queued, the
# runtime pump auto-drains in order) when a turn is active.
LEAD_TMUX=marathon-lead
QA_TMUX=marathon-qa
persistent_lead_start() {
  local lead_prompt="$PROMPTS/lead-persistent.md"
  [ "$VERIFY_ROLE" = "lead" ] && lead_prompt="$PROMPTS/lead-persistent-verifier.md"
  [ "$VERIFY_ROLE" = "lead-ui" ] && lead_prompt="$PROMPTS/lead-persistent-verifier-ui.md"
  [ "$VERIFY_ROLE" = "tasks" ] && lead_prompt="$PROMPTS/lead-persistent-verifier-tasks.md"
  cat > "$LOGD/lead-launch.sh" <<LEOF
#!/usr/bin/env bash
. "$MH/env.sh"
cd "\$MARATHON_WS_ROOT/app" || exit 1
exec loom lead --backend codex --prompt "$lead_prompt"
LEOF
  chmod +x "$LOGD/lead-launch.sh"
  tmux kill-session -t "$LEAD_TMUX" 2>/dev/null
  tmux new-session -d -s "$LEAD_TMUX" -x 220 -y 50 "$LOGD/lead-launch.sh" || return 1
  tmux pipe-pane -t "$LEAD_TMUX" -o "cat >> $LOGD/lead-pane.log" 2>/dev/null || true
}

# The QA session (EXPERIMENTS B2d) is the SAME controlled runtime the lead
# uses — a second `loom lead` process registered under agent name `qa` via
# LOOM_AGENT_NAME, with its own app-server thread, addressable via leadmsg.
persistent_qa_start() {
  local qa_prompt="$PROMPTS/qa-persistent.md"
  [ "$VERIFY_ROLE" = "tasks" ] && qa_prompt="$PROMPTS/qa-persistent-tasks.md"
  cat > "$LOGD/qa-launch.sh" <<QEOF
#!/usr/bin/env bash
. "$MH/env.sh"
export LOOM_AGENT_NAME=qa
cd "\$MARATHON_WS_ROOT/app" || exit 1
exec loom lead --backend codex --prompt "$qa_prompt"
QEOF
  chmod +x "$LOGD/qa-launch.sh"
  tmux kill-session -t "$QA_TMUX" 2>/dev/null
  tmux new-session -d -s "$QA_TMUX" -x 220 -y 50 "$LOGD/qa-launch.sh" || return 1
  tmux pipe-pane -t "$QA_TMUX" -o "cat >> $LOGD/qa-pane.log" 2>/dev/null || true
}

# runtime_status AGENT -> controlled-runtime status for that agent name
runtime_status() {
  leadmsg "$LOOM_WORKSPACE" "$1" --status 2>/dev/null \
    | python3 -c 'import sys,json;print(json.load(sys.stdin).get("Status","none"))' 2>/dev/null \
    || echo none
}

# The daemon anchors its pid file/lock/sockets under <cwd>/.loom — pin the cwd
# to the workspace root, then verify it actually came up.
start_daemon() {
  ( cd "$MARATHON_WS_ROOT" && exec nohup loom daemon >> "$LOGD/daemon.out" 2>&1 ) &
  DAEMON_PID=$!
  log "daemon started (pid=$DAEMON_PID)"
  sleep 8
  if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
    FINALIZE_REASON=daemon-failed
    echo "[orchestrate] FATAL: loom daemon exited at startup:" >&2
    tail -20 "$LOGD/daemon.out" >&2
    return 1
  fi
  if ! (cd "$MARATHON_WS_ROOT" && loom daemon status) >> "$LOGD/daemon-status.log" 2>&1; then
    log "WARN: loom daemon status probe nonzero (continuing; see daemon-status.log)"
  fi
  return 0
}

# persistent_pass AGENT MESSAGE -> echoes the delivery state (delivered|pending|...)
persistent_pass() {
  local agent="$1" msg="$2" state
  state=$(leadmsg "$LOOM_WORKSPACE" "$agent" "$msg" 2>>"$LOGD/leadmsg.err" \
    | python3 -c 'import sys,json;print(json.load(sys.stdin).get("State","error"))' 2>/dev/null)
  [ -n "$state" ] || state=error
  printf '%s deliver=%s msg=%.80s\n' "$(date -u +%FT%TZ)" "$state" "$msg" >> "$LOGD/${agent}-passes.log"
  echo "$state"
}

# integ_delta: sets INTEG_DELTA to "TASK@sha TASK@sha ..." for INTEGRATED
# records not yet reported to the verifier role; empty when nothing new.
# Mutates INTEG_SEEN, so it must be CALLED DIRECTLY (never via $(...) — a
# command substitution's subshell would lose the cursor). Mechanical [O] fact.
INTEG_SEEN=0
INTEG_DELTA=""
integ_delta() {
  local total
  INTEG_DELTA=""
  total=$(grep -c "INTEGRATED task=" "$INTEG_LOG" 2>/dev/null) || total=0
  if [ "$total" -gt "$INTEG_SEEN" ]; then
    INTEG_DELTA=$(grep "INTEGRATED task=" "$INTEG_LOG" \
      | sed -n "$((INTEG_SEEN + 1)),${total}p" \
      | sed -E 's/.*INTEGRATED task=([A-Za-z0-9_-]+).*app_after=([0-9a-f]+).*/\1@\2/' \
      | tr '\n' ' ')
    INTEG_SEEN=$total
  fi
}

spend_usd() {
  bash "$MH/scripts/spend.sh" 2>/dev/null \
    | python3 -c 'import sys,json;print(json.load(sys.stdin).get("est_cost_usd",0))' \
    2>/dev/null || echo 0
}

spend_over_cap() {
  python3 - "$(spend_usd)" "$SPEND_CAP" <<'PY'
import sys
spent, cap = float(sys.argv[1]), float(sys.argv[2])
sys.exit(0 if spent >= cap else 1)
PY
}

epic_count() {
  loom data list --type epic -o json 2>/dev/null \
    | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))' 2>/dev/null || echo 0
}

# Emit "id|attempt|sha" for every review-status task that is an IMPLEMENTATION
# review: latest comments contain IMPL-DONE AND the task does NOT carry the
# needs-revision label. A review task WITH the label is a re-design awaiting
# lead approval (the reopen added the label; only lead-approve removes it) —
# treating those as impl-reviews deadlocks the revision loop, because the old
# IMPL-DONE attempt is already in the handled ledger.
impl_reviews() {
  local ids id
  ids=$(loom data list --status review --limit 500 -o json 2>/dev/null \
    | python3 -c '
import sys, json
for i in json.load(sys.stdin):
    if "needs-revision" in (i.get("labels") or []):
        continue
    if i.get("source_repo") and i.get("source_repo") != "app":
        continue  # verification-lane tasks never enter the integration gate
    print(i["id"])
' 2>/dev/null) || return 0
  for id in $ids; do
    # NB: must be python3 -c, NOT `python3 - <<heredoc` — a heredoc would
    # replace the piped JSON on stdin (verified failure mode: silent empty sweep).
    loom data show "$id" -o json 2>/dev/null | python3 -c '
import json, re, sys
issue_id = sys.argv[1]
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
best = None
for c in d.get("comments") or []:
    m = re.search(r"IMPL-DONE\s+attempt=(\d+)\s+commit=([0-9a-fA-F]{7,40})", c.get("text") or "")
    if m:
        best = (int(m.group(1)), m.group(2))
if best:
    print(f"{issue_id}|{best[0]}|{best[1]}")
' "$id"
  done
}

# current_marker TID -> "attempt|sha" of the LATEST valid IMPL-DONE marker,
# but only while the task is still an UNLABELED review (else empty). Used to
# revalidate sweep snapshots immediately before integrate/reopen (a snapshot
# can be a full critic-session stale — codex vet-B).
current_marker() {
  loom data show "$1" -o json 2>/dev/null | python3 -c '
import json, re, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
if d.get("status") != "review" or "needs-revision" in (d.get("labels") or []):
    sys.exit(0)
best = None
for c in d.get("comments") or []:
    m = re.search(r"IMPL-DONE\s+attempt=(\d+)\s+commit=([0-9a-fA-F]{7,40})", c.get("text") or "")
    if m:
        best = (m.group(1), m.group(2))
if best:
    print(f"{best[0]}|{best[1]}")
'
}

# Idempotency ledger: only ever act once per (task, attempt).
attempt_handled() {
  grep -Eq " (VERDICT-REJECTED|INTEGRATED|INTEGRATION-FAILED|GATE-SKIP) task=$1 attempt=$2( |$)" "$INTEG_LOG"
}

reopen_task() {
  local tid="$1" attempt="$2" reason="$3"
  loom data comment "$tid" "FEEDBACK attempt=$attempt: $reason" >/dev/null 2>&1
  loom data update "$tid" --status open --add-label needs-revision \
    --notes "FEEDBACK attempt=$attempt: $reason" >/dev/null 2>&1
}

# run_critic TID ATTEMPT SHA -> prints APPROVED or CHANGES-REQUESTED
run_critic() {
  local tid="$1" attempt="$2" sha="$3"
  local co="/work/critic-$tid-$attempt" verdict="CHANGES-REQUESTED" design vfile
  git -C /app worktree remove --force "$co" >/dev/null 2>&1
  if ! git -C /app worktree add --detach "$co" "$sha" >/dev/null 2>&1; then
    log "critic: cannot checkout $sha for $tid" >&2
    echo "CHANGES-REQUESTED"; return
  fi
  design=$(loom data show "$tid" -o json 2>/dev/null \
    | python3 -c 'import sys,json;d=json.load(sys.stdin);print((d.get("design") or "")[:4000])' 2>/dev/null)
  local msg
  msg=$(printf 'REVIEW CONTRACT\ntask=%s\nattempt=%s\nbase=%s\ncandidate=%s\n\nDESIGN:\n%s\n\nYou are cwd-ed into a DISPOSABLE detached checkout of the candidate commit. Review it against the design and acceptance criteria, then write CRITIC-VERDICT.txt in the current directory containing EXACTLY one first line:\nREVIEW attempt=%s commit=%s APPROVED — <reason>\nor\nREVIEW attempt=%s commit=%s CHANGES-REQUESTED — <reason>\n' \
    "$tid" "$attempt" "$(git -C /app rev-parse HEAD)" "$sha" "$design" "$attempt" "$sha" "$attempt" "$sha")
  ( cd "$co" && rm -f CRITIC-VERDICT.txt && \
    LOOM_LEAD_CONTROLLED=0 timeout 1200 loom lead --backend codex \
      --prompt "$PROMPTS/critic.md" --message "$msg" \
      </dev/null >> "$LOGD/critic-$tid-$attempt.log" 2>&1 )
  vfile="$co/CRITIC-VERDICT.txt"
  if [ -f "$vfile" ]; then
    cp "$vfile" "$LOGD/critic-verdict-$tid-$attempt.txt" 2>/dev/null
    # The verdict must echo back the same attempt + commit; stale/mismatched -> rejected.
    if head -1 "$vfile" | grep -Eq "^REVIEW attempt=$attempt commit=$sha APPROVED( |$|—|-)"; then
      verdict="APPROVED"
    fi
    loom data comment "$tid" "CRITIC $(head -1 "$vfile" | cut -c1-400)" >/dev/null 2>&1
  else
    log "critic: no verdict file for $tid attempt=$attempt — treating as CHANGES-REQUESTED" >&2
    loom data comment "$tid" "CRITIC attempt=$attempt: no verdict produced (auto CHANGES-REQUESTED)" >/dev/null 2>&1
  fi
  git -C /app worktree remove --force "$co" >/dev/null 2>&1
  echo "$verdict"
}

# integrate TID ATTEMPT SHA — atomic check-BEFORE-fast-forward. /app only ever
# advances by FF to an already-checked candidate; a failed check leaves /app
# byte-identical and the task reopened.
integrate() {
  local tid="$1" attempt="$2" sha="$3"
  local app_before coder_head gate_wt app_after fresh
  fresh=$(current_marker "$tid")
  if [ "$fresh" != "$attempt|$sha" ]; then
    record "GATE-SKIP task=$tid attempt=$attempt reason=stale-resweep fresh=${fresh:-none}"
    log "integrate: $tid attempt=$attempt no longer current (now: ${fresh:-gone}) — resweeping next pass"
    return 1
  fi
  app_before=$(git -C /app rev-parse HEAD)
  coder_head=$(git -C "$MARATHON_CODER_WT" rev-parse HEAD)
  # The single coder branch keeps moving (task N+1 commits land on it while the
  # sweep runs), so require the candidate to be REACHABLE from the coder head,
  # not equal to it; /app is still FF-ed to exactly the candidate below.
  if ! git -C "$MARATHON_CODER_WT" merge-base --is-ancestor "$sha" "$coder_head"; then
    record "GATE-SKIP task=$tid attempt=$attempt reason=stale-candidate sha=$sha coder_head=$coder_head"
    reopen_task "$tid" "$attempt" "stale candidate: IMPL-DONE commit $sha is not reachable from the coder branch head $coder_head; recommit and re-signal"
    return 1
  fi
  if ! git -C /app merge-base --is-ancestor "$app_before" "$sha"; then
    record "GATE-SKIP task=$tid attempt=$attempt reason=app-not-ancestor app=$app_before sha=$sha"
    reopen_task "$tid" "$attempt" "/app head $app_before is not an ancestor of candidate $sha; rebase onto current /app state"
    return 1
  fi
  gate_wt="/work/gate-$tid-$attempt"
  git -C /app worktree remove --force "$gate_wt" >/dev/null 2>&1
  if ! git -C /app worktree add --detach "$gate_wt" "$sha" >/dev/null 2>&1; then
    record "GATE-SKIP task=$tid attempt=$attempt reason=checkout-failed sha=$sha"
    reopen_task "$tid" "$attempt" "candidate checkout failed for $sha; recommit and re-signal"
    return 1
  fi
  if timeout 600 bash "$MH/scripts/integration-check.sh" "$gate_wt" \
      > "$LOGD/check-$tid-$attempt.log" 2>&1; then
    if git -C /app merge --ff-only "$sha" >/dev/null 2>&1; then
      app_after=$(git -C /app rev-parse HEAD)
      record "INTEGRATED task=$tid attempt=$attempt app_before=$app_before app_after=$app_after check=pass"
      loom data comment "$tid" "INTEGRATED attempt=$attempt app_before=$app_before app_after=$app_after" >/dev/null 2>&1
      loom data close "$tid" --reason "integrated into /app by harness gate (attempt=$attempt)" >/dev/null 2>&1
      git -C /app push -q origin main >/dev/null 2>&1 || true
      log "INTEGRATED $tid attempt=$attempt -> /app now $app_after"
    else
      record "GATE-SKIP task=$tid attempt=$attempt reason=ff-failed app=$app_before sha=$sha"
      reopen_task "$tid" "$attempt" "fast-forward of /app to $sha failed (non-FF history)"
    fi
  else
    app_after=$(git -C /app rev-parse HEAD)
    record "INTEGRATION-FAILED task=$tid attempt=$attempt app_before=$app_before app_after_unchanged=$app_after candidate=$sha"
    if [ "$app_before" != "$app_after" ]; then
      record "INVARIANT-VIOLATION task=$tid attempt=$attempt /app moved during a failed check"
      log "INVARIANT-VIOLATION: /app moved during failed check for $tid"
    fi
    reopen_task "$tid" "$attempt" "integration check failed: $(tail -3 "$LOGD/check-$tid-$attempt.log" | tr '\n' ' ' | cut -c1-400)"
    log "INTEGRATION-FAILED $tid attempt=$attempt (/app untouched at $app_after)"
  fi
  git -C /app worktree remove --force "$gate_wt" >/dev/null 2>&1
}

open_task_count() {
  loom data list --limit 500 -o json 2>/dev/null | python3 -c '
import sys, json
issues = json.load(sys.stdin)
n = sum(1 for i in issues if i.get("issue_type") != "epic" and i.get("status") not in ("closed", "tombstone"))
print(n)' 2>/dev/null || echo 1
}

nonepic_task_count() {
  loom data list --limit 500 -o json 2>/dev/null | python3 -c '
import sys, json
issues = json.load(sys.stdin)
print(sum(1 for i in issues if i.get("issue_type") != "epic"))' 2>/dev/null || echo 0
}

# ---------------------------------------------------------------- finalize ---
FINALIZED=0
finalize() {
  [ "$FINALIZED" = "1" ] && return
  FINALIZED=1
  log "finalize: reason=$FINALIZE_REASON"

  # Persistent lead: stop it FIRST so no lead mutation races the evidence
  # dump, then archive its runtime home (app-server log; sqlite excluded —
  # the thread transcript already lands in CODEX_HOME/sessions rollouts).
  if [ "$LEAD_MODE" = "persistent" ]; then
    tmux kill-session -t "$LEAD_TMUX" 2>/dev/null
    tmux kill-session -t "$QA_TMUX" 2>/dev/null
    pkill -9 -f 'codex app-server' 2>/dev/null
    pkill -9 -f 'codex --remote' 2>/dev/null
    CACHE_LEADS="${XDG_CACHE_HOME:-$HOME/.cache}/loom/codex-leads"
    [ -d "$CACHE_LEADS" ] && tar --exclude='*.sqlite*' -czf "$LOGD/codex-leads.tar.gz" \
      -C "$(dirname "$CACHE_LEADS")" codex-leads 2>/dev/null
    record "PERSISTENT-LEAD stopped"
  fi

  # Dump evidence from the LIVE instance FIRST: a post-daemon-stop dump runs on
  # a freshly-spawned instance whose comment read-model is NOT rehydrated from
  # the snapshot (G13 — comments go blind across embedded restarts), so
  # dump-after-quiesce loses every comment. The live-instance dump can miss
  # only mutations from workers still mid-write; a second post-quiesce dump is
  # taken below for cross-checking issue statuses.
  loom data list --limit 500 -o json > "$LOGD/final-issues.json" 2>/dev/null

  local stop_ok=1
  timeout 100 loom daemon stop >/dev/null 2>&1 || stop_ok=0
  if [ -n "$DAEMON_PID" ]; then
    for _ in 1 2 3 4 5 6 7 8; do
      kill -0 "$DAEMON_PID" 2>/dev/null || break
      sleep 2
    done
    kill -0 "$DAEMON_PID" 2>/dev/null && stop_ok=0
  fi
  [ "$stop_ok" = 1 ] || record "INCOMPLETE-EVIDENCE daemon did not stop gracefully"
  # Post-quiesce cross-check (comment-blind by design — statuses only).
  loom data list --limit 500 -o json > "$LOGD/final-issues-post-quiesce.json" 2>/dev/null

  [ -n "$DAEMON_PID" ] && kill -9 "$DAEMON_PID" >/dev/null 2>&1
  pkill -9 -f 'loom (task|plan|daemon)' >/dev/null 2>&1
  pkill -9 -f 'codex exec' >/dev/null 2>&1
  sleep 1
  pkill -9 -x fleet-db >/dev/null 2>&1

  # Port sweep: the verifier boots /app/start.sh itself; anything we leave
  # listening on its ports poisons the trial.
  python3 - <<'PY' >> "$LOGD/port-sweep.log" 2>&1
import os, socket, time

PORTS = (8000, 8001, 8002, 6667, 6379)
SELF = {os.getpid(), os.getppid()}

def busy(port):
    s = socket.socket()
    try:
        s.bind(("127.0.0.1", port))
        return False
    except OSError:
        return True
    finally:
        s.close()

def listen_inodes(ports):
    """LISTEN-socket inodes for target ports from /proc/net/tcp{,6} (st 0A)."""
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
    return inodes

def pids_for_inodes(inodes):
    hits = {}
    for pid in filter(str.isdigit, os.listdir("/proc")):
        if int(pid) in SELF:
            continue
        fd_dir = f"/proc/{pid}/fd"
        try:
            for fd in os.listdir(fd_dir):
                try:
                    target = os.readlink(f"{fd_dir}/{fd}")
                except OSError:
                    continue
                if target.startswith("socket:["):
                    ino = target[8:-1]
                    if ino in inodes:
                        hits.setdefault(pid, set()).add(inodes[ino])
        except OSError:
            continue
    return hits

for attempt in range(3):
    stale = [p for p in PORTS if busy(p)]
    if not stale:
        break
    hits = pids_for_inodes(listen_inodes(set(stale)))
    for pid, ports in sorted(hits.items()):
        print(f"killing pid {pid} (listening on {sorted(ports)})")
        try:
            os.kill(int(pid), 9)
        except OSError as e:
            print(f"  kill {pid} failed: {e}")
    if not hits:
        print(f"stale ports {stale} but no owning pid found via /proc")
        break
    time.sleep(2)
for p in PORTS:
    print(f"port {p}: {'BUSY' if busy(p) else 'free'}")
PY

  # Remaining evidence + usage summary (no loom dependency).
  cp -a "$LOOM_CONFIG_DIR" "$LOGD/loom-state-final" 2>/dev/null
  cp -R "$MARATHON_WS_ROOT/.loom/daemon-logs" "$LOGD/daemon-logs" 2>/dev/null
  git -C /app push -q /logs/agent/app-mirror.git '+refs/*:refs/*' 2>/dev/null || true
  git -C /app log --oneline > "$LOGD/app-git-log.txt" 2>/dev/null
  # Teardown-proof code snapshot: /logs/agent is host-mounted, the container
  # filesystem is not. Includes .git (full multi-agent history is evidence).
  tar -C / -czf "$LOGD/app-snapshot.tar.gz" app 2>/dev/null \
    && log "app snapshot: $(du -h "$LOGD/app-snapshot.tar.gz" | cut -f1) -> /logs/agent/app-snapshot.tar.gz"
  git -C /app rev-parse HEAD > "$LOGD/app-final-head.txt" 2>/dev/null
  git -C "$MARATHON_CODER_WT" log --oneline > "$LOGD/coder-git-log.txt" 2>/dev/null

  # grep -c prints "0" AND exits 1 on zero matches, so `|| echo 0` would emit a
  # second line; use || true + a default instead. Spend JSON goes via argv, not
  # a pipe (a heredoc program would steal stdin from the pipe).
  local integrated failures spend_json
  integrated=$(grep -c ' INTEGRATED ' "$INTEG_LOG" 2>/dev/null || true)
  failures=$(grep -c ' INTEGRATION-FAILED ' "$INTEG_LOG" 2>/dev/null || true)
  integrated=${integrated:-0}
  failures=${failures:-0}
  spend_json=$(bash "$MH/scripts/spend.sh" 2>/dev/null || echo '{}')
  python3 -c '
import json, sys
try:
    spend = json.loads(sys.argv[1])
except Exception:
    spend = {}
for k, v in (("input_tokens", 0), ("output_tokens", 0), ("est_cost_usd", 0.0)):
    spend.setdefault(k, v)
spend.update(
    tasks_integrated=int(sys.argv[2]),
    integration_failures=int(sys.argv[3]),
    finalize_reason=sys.argv[4],
    tasks_seeded=int(sys.argv[5]),
    stub=sys.argv[6] == "1",
)
print(json.dumps(spend, indent=1))
' "$spend_json" "$integrated" "$failures" "$FINALIZE_REASON" "$TASKS_SEEDED" "$STUB" > "$LOGD/usage-summary.json"
  log "finalize complete: integrated=$integrated failures=$failures spend=\$$(spend_usd)"
}
trap 'FINALIZE_REASON=signal; finalize' INT TERM
trap finalize EXIT

# ---------------------------------------------------------------- seed -------
log "seed pass (stub=$STUB, lead_mode=$LEAD_MODE, budget=${BUDGET}s, reserve=${RESERVE}s, cap=\$$SPEND_CAP)"
INSTRUCTION="$(cat "$MH/instruction.md")"
if [ "$LEAD_MODE" = "persistent" ]; then
  command -v tmux >/dev/null || { FINALIZE_REASON=lead-tmux-failed; echo "[orchestrate] FATAL: tmux not installed" >&2; exit 1; }
  command -v leadmsg >/dev/null || { FINALIZE_REASON=lead-tmux-failed; echo "[orchestrate] FATAL: leadmsg not on PATH (bundle stale?)" >&2; exit 1; }
  # Daemon FIRST: its long-lived process holds THE canonical embedded
  # fleet-db instance, so the lead and every probe/CLI reuse one store.
  # Trial-1 failure: probe CLIs spawn-and-dying their own instances raced
  # the lead's registration open on embedded.lock -> silent empty
  # registration -> no-session forever (G13-family; also fixed product-side
  # with registration retry).
  start_daemon || exit 1
  persistent_lead_start || { FINALIZE_REASON=lead-tmux-failed; echo "[orchestrate] FATAL: tmux lead launch failed" >&2; exit 1; }
  record "PERSISTENT-LEAD started tmux=$LEAD_TMUX"
  sleep 20
  # Gate A (fail-fast, ~$0 at risk): controlled runtime must reach idle/active
  # (app-server up + thread discovered) before anything is spent.
  READY=0
  for _ in $(seq 1 60); do
    RSTATE="$(runtime_status lead)"
    case "$RSTATE" in idle|active) READY=1; break;; esac
    sleep 10
  done
  if [ "$READY" != 1 ]; then
    FINALIZE_REASON=lead-runtime-failed
    echo "[orchestrate] FATAL: lead runtime never became ready (last=$RSTATE)" >&2
    tail -30 "$LOGD/lead-pane.log" >&2 2>/dev/null
    exit 1
  fi
  record "PERSISTENT-LEAD ready"
  # Gate B: end-to-end delivery proof with a trivial turn BEFORE the spec.
  ACK_STATE="$(persistent_pass lead 'Reply ACK.')"
  case "$ACK_STATE" in delivered|pending) ;; *)
    FINALIZE_REASON=lead-delivery-failed
    echo "[orchestrate] FATAL: lead delivery probe state=$ACK_STATE" >&2
    exit 1;;
  esac
  # Seed: the product instruction verbatim, via the same channel as every
  # later pass. Delivery is async — poll for the epic like oneshot's retry.
  persistent_pass lead "$INSTRUCTION" >/dev/null
  for _ in $(seq 1 90); do [ "$(epic_count)" != "0" ] && break; sleep 10; done
  if [ "$(epic_count)" = "0" ]; then
    log "seed produced no epic — retrying once"
    persistent_pass lead "$INSTRUCTION" >/dev/null
    for _ in $(seq 1 60); do [ "$(epic_count)" != "0" ] && break; sleep 10; done
  fi
else
  lead_pass "$PROMPTS/lead-autonomous.md" "$INSTRUCTION" 1800 lead-seed.log
  if [ "$(epic_count)" = "0" ]; then
    log "seed produced no epic — retrying once"
    lead_pass "$PROMPTS/lead-autonomous.md" "$INSTRUCTION" 1800 lead-seed.log
  fi
fi
if [ "$(epic_count)" = "0" ]; then
  FINALIZE_REASON=seed-failed
  echo "[orchestrate] FATAL: lead seeded no epic after 2 attempts" >&2
  exit 1
fi
TASKS_SEEDED=$(nonepic_task_count)
log "seeded: $(epic_count) epic(s), $TASKS_SEEDED tasks"

# ---- verification role bring-up (EXPERIMENTS B2c/B2d) ------------------------
EPIC_ID=""
if [ "$VERIFY_ROLE" != "off" ]; then
  EPIC_ID=$(loom data list --limit 200 -o json 2>/dev/null | python3 -c '
import sys, json
issues = json.load(sys.stdin)
epics = [i for i in issues if i.get("issue_type") == "epic"]
print(epics[0]["id"] if epics else "")' 2>/dev/null)
  log "verify role=$VERIFY_ROLE epic=$EPIC_ID checkout=${MARATHON_VERIFY_CHECKOUT:-unset}"
fi
if [ "$VERIFY_ROLE" = "qa" ] || [ "$VERIFY_ROLE" = "tasks" ]; then
  persistent_qa_start || { FINALIZE_REASON=qa-tmux-failed; echo "[orchestrate] FATAL: tmux qa launch failed" >&2; exit 1; }
  record "PERSISTENT-QA started tmux=$QA_TMUX"
  QA_READY=0
  for _ in $(seq 1 60); do
    QSTATE="$(runtime_status qa)"
    case "$QSTATE" in idle|active) QA_READY=1; break;; esac
    sleep 10
  done
  if [ "$QA_READY" != 1 ]; then
    FINALIZE_REASON=qa-runtime-failed
    echo "[orchestrate] FATAL: qa runtime never became ready (last=$QSTATE)" >&2
    tail -30 "$LOGD/qa-pane.log" >&2 2>/dev/null
    exit 1
  fi
  record "PERSISTENT-QA ready"
  QA_ACK="$(persistent_pass qa 'Reply ACK.')"
  case "$QA_ACK" in delivered|pending) ;; *)
    FINALIZE_REASON=qa-delivery-failed
    echo "[orchestrate] FATAL: qa delivery probe state=$QA_ACK" >&2
    exit 1;;
  esac
  # QA retains the spec verbatim as its reference (its prompt replies READY).
  persistent_pass qa "$INSTRUCTION" >/dev/null
fi

# ---------------------------------------------------------------- daemon -----
# (persistent lead mode starts the daemon BEFORE the lead — see seed section —
# so this is a no-op there; oneshot keeps the original seed-then-daemon order.)
if [ -z "$DAEMON_PID" ]; then
  start_daemon || exit 1
fi

# ---------------------------------------------------------------- main loop --
PASS=0
while :; do
  NOW=$(date +%s)
  if [ "$NOW" -ge "$DEADLINE" ]; then FINALIZE_REASON=deadline; break; fi
  if spend_over_cap; then
    FINALIZE_REASON=spend-cap
    log "spend cap hit: \$$(spend_usd) >= \$$SPEND_CAP"
    break
  fi

  PASS=$((PASS + 1))
  log "pass $PASS (t+$((NOW - START_TS))s, spend=\$$(spend_usd))"

  # Lead orchestrate: plan-review approvals/rejections only. Implementation
  # reviews, integration, and closes belong to this script (kills the
  # premature-close race — codex R4).
  REMAIN_MIN=$(( (DEADLINE - NOW) / 60 ))
  PASS_MSG="Orchestrate pass $PASS. About $REMAIN_MIN minutes of work time remain. Follow your standing rules: handle PLAN-stage reviews (any review task with the needs-revision label, or without an IMPL-DONE marker) and blocked tasks; never close tasks; never touch implementation reviews (review tasks WITHOUT the needs-revision label that carry a valid 'IMPL-DONE attempt=N commit=SHA' marker)."
  VERIFY_INFO=""
  if [ "$VERIFY_ROLE" != "off" ]; then
    integ_delta
    VERIFY_HEAD=""
    if [ "$VERIFY_ROLE" = "tasks" ]; then
      VERIFY_HEAD=" Current integrated head: $(git -C "${MARATHON_APP_BASE:-/app}" rev-parse --short HEAD 2>/dev/null)."
    fi
    VERIFY_INFO=" Integrated since last pass: ${INTEG_DELTA:-none}.${VERIFY_HEAD} Verification checkout: ${MARATHON_VERIFY_CHECKOUT}. Epic: ${EPIC_ID}."
  fi
  if [ "$LEAD_MODE" = "persistent" ]; then
    BACKLOG_NOTE=""
    if [ "$VERIFY_ROLE" = "tasks" ]; then
      QAV_OPEN=$(loom data list --limit 300 -o json 2>/dev/null | python3 -c '
import sys, json
print(len([i for i in json.load(sys.stdin) if i.get("source_repo") == "qa-verify" and i.get("status") == "open"]))' 2>/dev/null) || QAV_OPEN=0
      if [ "${QAV_OPEN:-0}" -gt 8 ]; then
        BACKLOG_NOTE=" Verification backlog: ${QAV_OPEN} tasks open; do not file new verification tasks this pass."
        record "QAV-BACKLOG open=${QAV_OPEN} (rail engaged)"
      fi
    fi
    case "$VERIFY_ROLE" in
      lead|lead-ui|tasks) persistent_pass lead "${PASS_MSG}${VERIFY_INFO}${BACKLOG_NOTE}" >/dev/null ;;
      *) persistent_pass lead "$PASS_MSG" >/dev/null ;;
    esac
    if [ "$VERIFY_ROLE" = "qa" ] || [ "$VERIFY_ROLE" = "tasks" ]; then
      persistent_pass qa "QA pass $PASS. About $REMAIN_MIN minutes of work time remain.${VERIFY_INFO}" >/dev/null
    fi
  else
    lead_pass "$PROMPTS/lead-orchestrate.md" "$PASS_MSG" 900 "lead-orchestrate.log"
  fi

  # Anti-wedge valve (codex vet-B finding 3): a review task with IMPL-DONE
  # text that does NOT parse as a valid marker, and no needs-revision label,
  # has no owner (lead skips it as impl-review, harness can't parse it).
  # After being observed on two consecutive VALVE passes, reopen it. Runs only
  # every 3rd pass: it is a rare-wedge safety valve, and each extra burst of
  # CLI calls raises the embedded-instance reuse-race pressure (G13).
  if [ $((PASS % 3)) -eq 0 ]; then
  MALFORMED_NOW="$LOGD/.malformed-now"; : > "$MALFORMED_NOW"
  loom data list --status review --limit 500 -o json 2>/dev/null | python3 -c '
import sys, json
for i in json.load(sys.stdin):
    if "needs-revision" in (i.get("labels") or []):
        continue
    if i.get("source_repo") and i.get("source_repo") != "app":
        continue  # verification-lane tasks never enter the integration gate
    print(i["id"])
' 2>/dev/null | while read -r mid; do
    [ -n "$mid" ] || continue
    loom data show "$mid" -o json 2>/dev/null | python3 -c '
import json, re, sys
d = json.load(sys.stdin)
texts = [c.get("text") or "" for c in d.get("comments") or []]
has_sub = any("IMPL-DONE" in t for t in texts)
has_valid = any(re.search(r"IMPL-DONE\s+attempt=\d+\s+commit=[0-9a-fA-F]{7,40}", t) for t in texts)
if has_sub and not has_valid:
    print(sys.argv[1])
' "$mid" >> "$MALFORMED_NOW"
  done
  if [ -s "$MALFORMED_NOW" ] && [ -f "$LOGD/.malformed-prev" ]; then
    while read -r mid; do
      if grep -qx "$mid" "$LOGD/.malformed-prev"; then
        record "MALFORMED-MARKER task=$mid action=reopen"
        reopen_task "$mid" 0 "IMPL-DONE marker malformed; recommit if needed and re-signal EXACTLY: IMPL-DONE attempt=<n> commit=<full-sha>"
        log "anti-wedge: reopened $mid (malformed IMPL-DONE marker, 2 sweeps)"
      fi
    done < "$MALFORMED_NOW"
  fi
  mv "$MALFORMED_NOW" "$LOGD/.malformed-prev" 2>/dev/null
  fi

  # Deterministic critic + integration sweep.
  SWEEP="$(impl_reviews)"
  if [ -n "$SWEEP" ]; then
    while IFS='|' read -r tid attempt sha; do
      [ -n "$tid" ] || continue
      if attempt_handled "$tid" "$attempt"; then continue; fi
      if [ "$CRITIC_MODE" = "off" ]; then
        # Generic mode: the per-review LLM critic is an [S] policy; valid
        # candidates go straight to the deterministic integration gate.
        log "impl-review: $tid attempt=$attempt candidate=$sha — critic=off, gate only"
        VERDICT="APPROVED"
      else
      log "impl-review: $tid attempt=$attempt candidate=$sha — invoking critic"
      VERDICT=$(run_critic "$tid" "$attempt" "$sha")
      fi
      if [ "$VERDICT" = "APPROVED" ]; then
        integrate "$tid" "$attempt" "$sha"
      else
        record "VERDICT-REJECTED task=$tid attempt=$attempt sha=$sha"
        if [ "$(current_marker "$tid")" = "$attempt|$sha" ]; then
          reopen_task "$tid" "$attempt" "critic requested changes; see CRITIC comment"
          log "critic rejected $tid attempt=$attempt"
        else
          log "critic rejected $tid attempt=$attempt but task moved on — not reopening"
        fi
      fi
    done <<< "$SWEEP"
  fi

  # Continuous durability: mirror /app's FULL history (all refs, incl. agent
  # branches) to the host-mounted bare repo every pass — a hard kill can then
  # lose at most one pass of commits.
  git -C /app push -q /logs/agent/app-mirror.git '+refs/*:refs/*' 2>/dev/null || true
  # ... and copy the live fleet-db state to the mount (atomic swap): the live
  # store stays on container fs (virtiofs broke it), the backup is crash-safe.
  if cp -a "$LOOM_CONFIG_DIR" "$LOGD/.loom-state-backup.tmp" 2>/dev/null; then
    rm -rf "$LOGD/loom-state-backup"
    mv "$LOGD/.loom-state-backup.tmp" "$LOGD/loom-state-backup"
  fi

  # Drained? (all non-epic tasks closed and at least one existed)
  OPEN=$(open_task_count)
  TOTAL=$(nonepic_task_count)
  if [ "$TOTAL" -gt 0 ] && [ "$OPEN" = "0" ]; then
    if [ "$VERIFY_ROLE" = "lead-ui" ] || [ "$VERIFY_ROLE" = "tasks" ]; then
      # B2e/B2f: draining is not the end — the lead keeps verifying (including
      # the UI walk) and refiling until the deadline reserve. B2c finalized
      # here and threw away 43 minutes that the ux half needed.
      if [ "${DRAIN_LOGGED:-0}" = "0" ]; then
        record "DRAINED-CONTINUE all $TOTAL tasks closed; verification continues"
        log "epic drained ($TOTAL tasks) — continuing verification passes until deadline"
        DRAIN_LOGGED=1
      fi
    else
      FINALIZE_REASON=drained
      log "epic drained: all $TOTAL tasks closed"
      break
    fi
  fi

  NOW=$(date +%s)
  SLEEP=$((DEADLINE - NOW))
  [ "$SLEEP" -gt "$CADENCE" ] && SLEEP="$CADENCE"
  [ "$SLEEP" -gt 0 ] && sleep "$SLEEP"
done

finalize
exit 0
