#!/usr/bin/env bash
# gatelib.sh — integration-gate machinery, sourced by orchestrate.sh (and by
# harbor/test/test-arch-gate.sh, which is why it lives in its own file).
# Moved verbatim from orchestrate.sh except: literal /app is parametrized as
# ${MARATHON_APP_DIR:-/app} (bootstrap exports it =/app, so container behavior
# is identical), and integrate() gains an optional 4th arg "gate" implementing
# the B2j architect checkpoint (codex B2j-vet finding 3: validation and merge
# split; approval/timeout re-runs the FULL original path).
# Requires from the sourcing script: record(), log(), $LOGD, $INTEG_LOG,
# $MARATHON_CODER_WT, loom on PATH. $PASS (current pass number) for pending age.

APP_DIR="${MARATHON_APP_DIR:-/app}"
# GNU timeout exists in the task image; macOS (host tests) lacks it — degrade
# to unbounded rather than fail with 127 (codex stage-2-vet portability class).
if command -v timeout >/dev/null 2>&1; then _tmo() { timeout "$@"; }; else _tmo() { shift; "$@"; }; fi
ARCH_PENDING="${ARCH_PENDING:-$LOGD/.arch-pending}"

# current_marker TID -> "attempt|sha" of the LATEST valid IMPL-DONE marker,
# but only while the task is still an UNLABELED review (else empty).
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

# Idempotency ledger: only ever act once per (task, attempt). ARCH-GATED is in
# the set so a pending candidate is not re-gated every pass.
attempt_handled() {
  grep -Eq " (VERDICT-REJECTED|INTEGRATED|INTEGRATION-FAILED|GATE-SKIP|ARCH-GATED) task=$1 attempt=$2( |$)" "$INTEG_LOG"
}

reopen_task() {
  local tid="$1" attempt="$2" reason="$3"
  loom data comment "$tid" "FEEDBACK attempt=$attempt: $reason" >/dev/null 2>&1
  loom data update "$tid" --status open --add-label needs-revision \
    --notes "FEEDBACK attempt=$attempt: $reason" >/dev/null 2>&1
  arch_strip "$tid"   # wiring-vet finding 1: no arch approval survives a reopen
}

# arch_strip TID — remove every architect label (stale-approval hygiene,
# codex B2j-vet finding 4: labels must never survive a phase transition).
arch_strip() {
  loom data update "$1" --remove-label arch-gate --remove-label arch-impl-ok \
    --remove-label arch-rework --remove-label arch-design-ok >/dev/null 2>&1 || true
}

# integrate TID ATTEMPT SHA [gate] — atomic check-BEFORE-fast-forward. /app
# only ever advances by FF to an already-checked candidate; a failed check
# leaves /app byte-identical and the task reopened. With the 4th arg "gate"
# (B2j arch mode) a PASSING candidate is parked for architect ruling instead
# of merged: stale arch labels stripped, arch-gate label added, pending entry
# recorded with full identity (task attempt sha base — vet finding 5). All
# validation and failure paths are IDENTICAL in both modes.
integrate() {
  local tid="$1" attempt="$2" sha="$3" mode="${4:-merge}"
  local app_before coder_head gate_wt app_after fresh
  fresh=$(current_marker "$tid")
  if [ "$fresh" != "$attempt|$sha" ]; then
    record "GATE-SKIP task=$tid attempt=$attempt reason=stale-resweep fresh=${fresh:-none}"
    log "integrate: $tid attempt=$attempt no longer current (now: ${fresh:-gone}) — resweeping next pass"
    return 1
  fi
  app_before=$(git -C "$APP_DIR" rev-parse HEAD)
  coder_head=$(git -C "$MARATHON_CODER_WT" rev-parse HEAD)
  # The single coder branch keeps moving (task N+1 commits land on it while the
  # sweep runs), so require the candidate to be REACHABLE from the coder head,
  # not equal to it; /app is still FF-ed to exactly the candidate below.
  if ! git -C "$MARATHON_CODER_WT" merge-base --is-ancestor "$sha" "$coder_head"; then
    record "GATE-SKIP task=$tid attempt=$attempt reason=stale-candidate sha=$sha coder_head=$coder_head"
    reopen_task "$tid" "$attempt" "stale candidate: IMPL-DONE commit $sha is not reachable from the coder branch head $coder_head; recommit and re-signal"
    return 1
  fi
  if ! git -C "$APP_DIR" merge-base --is-ancestor "$app_before" "$sha"; then
    record "GATE-SKIP task=$tid attempt=$attempt reason=app-not-ancestor app=$app_before sha=$sha"
    reopen_task "$tid" "$attempt" "/app head $app_before is not an ancestor of candidate $sha; rebase onto current /app state"
    return 1
  fi
  gate_wt="/work/gate-$tid-$attempt"
  [ -w /work ] 2>/dev/null || gate_wt="${TMPDIR:-/tmp}/gate-$tid-$attempt"
  git -C "$APP_DIR" worktree remove --force "$gate_wt" >/dev/null 2>&1
  if ! git -C "$APP_DIR" worktree add --detach "$gate_wt" "$sha" >/dev/null 2>&1; then
    record "GATE-SKIP task=$tid attempt=$attempt reason=checkout-failed sha=$sha"
    reopen_task "$tid" "$attempt" "candidate checkout failed for $sha; recommit and re-signal"
    return 1
  fi
  if _tmo 600 bash "$MH/scripts/integration-check.sh" "$gate_wt" \
      > "$LOGD/check-$tid-$attempt.log" 2>&1; then
    if [ "$mode" = "gate" ]; then
      arch_strip "$tid"
      loom data update "$tid" --add-label arch-gate >/dev/null 2>&1
      record "ARCH-GATED task=$tid attempt=$attempt sha=$sha base=$app_before"
      printf '%s %s %s %s %s\n' "$tid" "$attempt" "$sha" "$app_before" "${PASS:-0}" >> "$ARCH_PENDING"
      log "ARCH-GATED $tid attempt=$attempt (awaiting architect ruling)"
    elif git -C "$APP_DIR" merge --ff-only "$sha" >/dev/null 2>&1; then
      app_after=$(git -C "$APP_DIR" rev-parse HEAD)
      record "INTEGRATED task=$tid attempt=$attempt app_before=$app_before app_after=$app_after check=pass"
      loom data comment "$tid" "INTEGRATED attempt=$attempt app_before=$app_before app_after=$app_after" >/dev/null 2>&1
      loom data close "$tid" --reason "integrated into /app by harness gate (attempt=$attempt)" >/dev/null 2>&1
      git -C "$APP_DIR" push -q origin main >/dev/null 2>&1 || true
      log "INTEGRATED $tid attempt=$attempt -> /app now $app_after"
    else
      record "GATE-SKIP task=$tid attempt=$attempt reason=ff-failed app=$app_before sha=$sha"
      reopen_task "$tid" "$attempt" "fast-forward of /app to $sha failed (non-FF history)"
    fi
  else
    app_after=$(git -C "$APP_DIR" rev-parse HEAD)
    record "INTEGRATION-FAILED task=$tid attempt=$attempt app_before=$app_before app_after_unchanged=$app_after candidate=$sha"
    if [ "$app_before" != "$app_after" ]; then
      record "INVARIANT-VIOLATION task=$tid attempt=$attempt /app moved during a failed check"
      log "INVARIANT-VIOLATION: /app moved during failed check for $tid"
    fi
    reopen_task "$tid" "$attempt" "integration check failed: $(tail -3 "$LOGD/check-$tid-$attempt.log" | tr '\n' ' ' | cut -c1-400)"
    log "INTEGRATION-FAILED $tid attempt=$attempt (/app untouched at $app_after)"
  fi
  git -C "$APP_DIR" worktree remove --force "$gate_wt" >/dev/null 2>&1
}

# arch_task_state TID -> "status|labels-csv|marker" (one show call).
arch_task_state() {
  loom data show "$1" -o json 2>/dev/null | python3 -c '
import json, re, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
labels = ",".join(d.get("labels") or [])
status = d.get("status") or ""
best = ("", "")
for c in d.get("comments") or []:
    m = re.search(r"IMPL-DONE\s+attempt=(\d+)\s+commit=([0-9a-fA-F]{7,40})", c.get("text") or "")
    if m:
        best = (m.group(1), m.group(2))
print(f"{status}|{labels}|{best[0]}|{best[1]}")
'
}

# arch_pending_sweep — honor architect rulings on parked candidates.
# FIFO QUEUE DISCIPLINE (wiring-vet finding 2): the single coder branch is
# linear, so a later candidate CONTAINS every earlier parked one. Only the
# queue HEAD may integrate (approve/timeout); when a candidate is rejected,
# every later pending candidate whose history contains it is cascaded back
# to the coder for rebase. Blocked rows have their age clock bumped so the
# fail-open counts from unblocking, not from gating.
# BUSY-PAUSE (finding 3): with ARCH_BUSY=1 (architect turn in flight) the
# sweep only bumps clocks — no timeouts can fire mid-ruling.
arch_pending_sweep() {
  [ -s "$ARCH_PENDING" ] || return 0
  local tmp="$ARCH_PENDING.new"; : > "$tmp"
  if [ "${ARCH_BUSY:-0}" = "1" ]; then
    awk -v OFS=' ' '{ $5 = $5 + 1; print }' "$ARCH_PENDING" > "$tmp"
    mv "$tmp" "$ARCH_PENDING"
    return 0
  fi
  local tid attempt sha base first st status labels mattempt msha
  local blocked=0 rejected_shas=""
  while read -r tid attempt sha base first; do
    [ -n "$tid" ] || continue
    # cascade: does this candidate contain a just-rejected predecessor?
    local casc=""
    for r in $rejected_shas; do
      if git -C "$APP_DIR" merge-base --is-ancestor "$r" "$sha" 2>/dev/null; then casc="$r"; break; fi
    done
    if [ -n "$casc" ]; then
      arch_strip "$tid"
      loom data comment "$tid" "ARCH-FEEDBACK: predecessor commit $casc in this candidate's history was rejected by the architecture review; rebase onto the current integrated head and re-signal." >/dev/null 2>&1
      loom data update "$tid" --status open --add-label arch-rework --assignee "" >/dev/null 2>&1
      record "ARCH-CASCADE-REWORK task=$tid attempt=$attempt contains_rejected=$casc"
      continue
    fi
    st=$(arch_task_state "$tid")
    status="${st%%|*}"; st="${st#*|}"
    labels="${st%%|*}"; st="${st#*|}"
    mattempt="${st%%|*}"; msha="${st#*|}"
    if [ "$status" = "closed" ] || [ -z "$status" ]; then
      record "ARCH-DROP task=$tid attempt=$attempt reason=closed-or-gone"
      continue
    fi
    if [ "$mattempt|$msha" != "$attempt|$sha" ]; then
      arch_strip "$tid"
      record "ARCH-STALE-DROP task=$tid attempt=$attempt superseded_by=${mattempt:-none}"
      continue
    fi
    case ",$labels," in
      *,arch-rework,*)
        if [ "$status" = "review" ]; then
          loom data update "$tid" --status open --assignee "" >/dev/null 2>&1
          record "ARCH-REJECT-REPAIRED task=$tid attempt=$attempt (status forced open)"
        fi
        record "ARCH-REJECTED task=$tid attempt=$attempt"
        rejected_shas="$rejected_shas $sha"
        continue ;;
      *,arch-impl-ok,*)
        if [ "$blocked" = 0 ]; then
          arch_strip "$tid"
          record "ARCH-APPROVED task=$tid attempt=$attempt"
          integrate "$tid" "$attempt" "$sha"
          continue
        fi
        record "ARCH-QUEUE-HOLD task=$tid attempt=$attempt reason=earlier-candidate-unresolved"
        blocked=1
        printf '%s %s %s %s %s\n' "$tid" "$attempt" "$sha" "$base" "$(( first + 1 ))" >> "$tmp"
        continue ;;
    esac
    if [ "$blocked" = 0 ] && [ $(( ${PASS:-0} - first )) -ge 2 ]; then
      arch_strip "$tid"
      record "ARCH-TIMEOUT task=$tid attempt=$attempt waited_passes=$(( ${PASS:-0} - first ))"
      integrate "$tid" "$attempt" "$sha"
      continue
    fi
    if [ "$blocked" = 1 ]; then
      printf '%s %s %s %s %s\n' "$tid" "$attempt" "$sha" "$base" "$(( first + 1 ))" >> "$tmp"
    else
      printf '%s %s %s %s %s\n' "$tid" "$attempt" "$sha" "$base" "$first" >> "$tmp"
    fi
    blocked=1
  done < "$ARCH_PENDING"
  mv "$tmp" "$ARCH_PENDING"
}

# arch_final_sweep — deadline/spend-cap exit must not strand parked
# candidates (vet finding 7): integrate every remaining one as a timeout.
arch_final_sweep() {
  [ -s "$ARCH_PENDING" ] || return 0
  local tid attempt sha base first
  # FIFO file order: integrating in order means each later candidate only
  # ever lands on top of already-integrated predecessors.
  while read -r tid attempt sha base first; do
    [ -n "$tid" ] || continue
    arch_strip "$tid"
    record "ARCH-TIMEOUT-FINAL task=$tid attempt=$attempt"
    integrate "$tid" "$attempt" "$sha"
  done < "$ARCH_PENDING"
  : > "$ARCH_PENDING"
}

# arch_design_list — review tasks for the architect's design checkpoint:
# no needs-revision label, app-lane, and comments containing NO IMPL-DONE
# text at all (malformed markers belong to the anti-wedge valve — finding 8).
arch_design_list() {
  local ids id
  ids=$(loom data list --status review --limit 500 -o json 2>/dev/null \
    | python3 -c '
import sys, json
for i in json.load(sys.stdin):
    if "needs-revision" in (i.get("labels") or []):
        continue
    if i.get("source_repo") and i.get("source_repo") != "app":
        continue
    print(i["id"])
' 2>/dev/null) || return 0
  for id in $ids; do
    loom data show "$id" -o json 2>/dev/null | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
if any("IMPL-DONE" in (c.get("text") or "") for c in d.get("comments") or []):
    sys.exit(0)
if "arch-design-ok" in (d.get("labels") or []):
    sys.exit(0)
print(d["id"])
' 2>/dev/null
  done
}

# arch_cand_list — "task attempt=N sha=S base=B" lines from the pending file.
arch_cand_list() {
  [ -s "$ARCH_PENDING" ] || return 0
  awk '{printf "%s attempt=%s sha=%s base=%s\n", $1, $2, $3, $4}' "$ARCH_PENDING"
}

# arch_refactor_audit — harness-audited <=2 refactor filings per pass
# (finding 11). Attribution via created_by=arch (arch session actor).
arch_refactor_audit() {
  local now prev
  now=$(loom data list --limit 500 -o json 2>/dev/null | python3 -c '
import sys, json
print(len([i for i in json.load(sys.stdin)
           if i.get("issue_type") == "task" and (i.get("created_by") or "") == "arch"]))' 2>/dev/null) || now=0
  prev=$(cat "$LOGD/.arch-created-prev" 2>/dev/null || echo 0)
  case "$prev" in ''|*[!0-9]*) prev=0;; esac
  case "$now" in ''|*[!0-9]*) now=0;; esac
  if [ $(( now - prev )) -gt 2 ]; then
    record "ARCH-REFACTOR-CAP-EXCEEDED filed_this_pass=$(( now - prev )) total=$now"
  fi
  printf '%s' "$now" > "$LOGD/.arch-created-prev"
}
