#!/usr/bin/env bash
# FREE lifecycle test of the B2j architect-gate machinery (gatelib.sh) against
# a real loom board and real git repos, with the architect SCRIPTED (its
# judgment is separately proven by the archpoc; this proves the harness).
# Scenarios (codex B2j-vet "cheapest booster" + findings 4/6/7/8):
#   S1 gate-park          S2 approve->integrate     S3 reject->rework->attempt2
#   S4 stale-ruling drop  S5 in-loop ARCH-TIMEOUT   S6 final sweep
#   S7 check-fail path unchanged                    S8 design-list exclusion
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
HARBOR="$(cd "$HERE/.." && pwd)"
LOOMCLI="$(cd "$HARBOR/.." && pwd)"
T="${TMPDIR:-/tmp}/archgate-test.$$"
mkdir -p "$T"
export PATH="$LOOMCLI:$PATH"
export LOOM_CONFIG_DIR="$T/loom-state"; mkdir -p "$LOOM_CONFIG_DIR"
export LOOM_WORKSPACE=ARCHGATE
export LOOM_FLEET_DB_ACTOR=archgate-test

FAILS=0
ok()   { printf 'PASS: %s\n' "$*"; }
bad()  { printf 'FAIL: %s\n' "$*"; FAILS=$((FAILS+1)); }
assert() { # desc cond
  local desc="$1"; shift
  if "$@" >/dev/null 2>&1; then ok "$desc"; else bad "$desc"; fi
}

# --- git fixtures -------------------------------------------------------------
mkdir -p "$T/app"; git -C "$T/app" init -q -b main
echo base > "$T/app/base.txt"; git -C "$T/app" add -A
git -C "$T/app" -c user.email=t@t -c user.name=t commit -qm base
git clone -q "$T/app" "$T/coder"; git -C "$T/coder" checkout -qb work
commit_cand() { # file msg -> echoes sha (on coder work branch, pushed to app)
  echo "$2" > "$T/coder/$1"; git -C "$T/coder" add -A
  git -C "$T/coder" -c user.email=t@t -c user.name=t commit -qm "$2"
  git -C "$T/coder" push -q origin work:refs/heads/work -f
  git -C "$T/coder" rev-parse HEAD
}

# --- gatelib env --------------------------------------------------------------
export LOGD="$T/logs"; mkdir -p "$LOGD"
export INTEG_LOG="$LOGD/integration.log"; : > "$INTEG_LOG"
export MARATHON_APP_DIR="$T/app"
export MARATHON_CODER_WT="$T/coder"
export MH="$T/mh-pass"; mkdir -p "$MH/scripts"
printf '#!/usr/bin/env bash\nexit 0\n' > "$MH/scripts/integration-check.sh"
MH_FAIL="$T/mh-fail"; mkdir -p "$MH_FAIL/scripts"
printf '#!/usr/bin/env bash\necho deliberately failing\nexit 1\n' > "$MH_FAIL/scripts/integration-check.sh"
PASS=1
log() { :; }
record() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >> "$INTEG_LOG"; }
. "$HARBOR/scripts/gatelib.sh"

# --- board fixtures -----------------------------------------------------------
loom workspace create archgate --repos "$T/app" --path "$T/ws" --branch agate >/dev/null 2>&1
mkid() { python3 -c 'import sys,json;print(json.load(sys.stdin).get("id",""))'; }
mktask() { loom data create --type task --source-repo app --title "$1" -o json 2>/dev/null | mkid; }
impl_ready() { # tid attempt sha
  loom data update "$1" --design "d" >/dev/null 2>&1
  loom data comment "$1" "IMPL-DONE attempt=$2 commit=$3" >/dev/null 2>&1
  loom data update "$1" --status review >/dev/null 2>&1
}
labels_of() { loom data show "$1" -o json 2>/dev/null | python3 -c 'import sys,json;print(",".join(json.load(sys.stdin).get("labels") or []))'; }
status_of() { loom data show "$1" -o json 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin).get("status",""))'; }

# ============================ S1: gate-park ===================================
T1=$(mktask t1); S1=$(commit_cand f1.txt c1)
impl_ready "$T1" 1 "$S1"
integrate "$T1" 1 "$S1" gate
assert "S1 arch-gate label set"            grep -q arch-gate <<<"$(labels_of "$T1")"
assert "S1 ARCH-GATED recorded"            grep -q "ARCH-GATED task=$T1 attempt=1" "$INTEG_LOG"
assert "S1 pending entry with base"        grep -q "^$T1 1 $S1 " "$ARCH_PENDING"
assert "S1 /app NOT moved"                 test "$(git -C "$T/app" rev-parse HEAD)" != "$S1"
assert "S1 idempotent (attempt_handled)"   attempt_handled "$T1" 1

# ============================ S2: approve -> integrate ========================
loom data update "$T1" --add-label arch-impl-ok >/dev/null 2>&1
PASS=2; arch_pending_sweep
assert "S2 ARCH-APPROVED recorded"         grep -q "ARCH-APPROVED task=$T1 attempt=1" "$INTEG_LOG"
assert "S2 INTEGRATED recorded"            grep -q "INTEGRATED task=$T1 attempt=1" "$INTEG_LOG"
assert "S2 /app fast-forwarded to sha"     test "$(git -C "$T/app" rev-parse HEAD)" = "$S1"
assert "S2 task closed"                    test "$(status_of "$T1")" = closed
L=$(labels_of "$T1")
if ! printf '%s' "$L" | grep -qE 'arch-gate|arch-impl-ok'; then ok "S2 arch labels stripped"; else bad "S2 arch labels stripped ($L)"; fi
if ! grep -q "^$T1 " "$ARCH_PENDING" 2>/dev/null; then ok "S2 pending drained"; else bad "S2 pending drained"; fi

# ============================ S3: reject -> rework -> attempt2 ================
T2=$(mktask t2); S2A=$(commit_cand f2.txt c2-attempt1)
impl_ready "$T2" 1 "$S2A"
integrate "$T2" 1 "$S2A" gate
# scripted architect rejection
loom data comment "$T2" "ARCH-FEEDBACK: structural problem X" >/dev/null 2>&1
loom data update "$T2" --status open --add-label arch-rework --assignee "" >/dev/null 2>&1
PASS=3; arch_pending_sweep
assert "S3 ARCH-REJECTED recorded"         grep -q "ARCH-REJECTED task=$T2 attempt=1" "$INTEG_LOG"
if ! grep -q "^$T2 " "$ARCH_PENDING" 2>/dev/null; then ok "S3 dropped from pending"; else bad "S3 dropped from pending"; fi
ST=$(status_of "$T2"); L=$(labels_of "$T2")
if [ "$ST" = open ] && ! printf '%s' "$L" | grep -q needs-revision; then ok "S3 coder-routable (open, no needs-revision)"; else bad "S3 coder-routable (st=$ST labels=$L)"; fi
# coder rework: attempt 2
S2B=$(commit_cand f2.txt c2-attempt2)
loom data comment "$T2" "IMPL-DONE attempt=2 commit=$S2B" >/dev/null 2>&1
loom data update "$T2" --status review >/dev/null 2>&1
integrate "$T2" 2 "$S2B" gate
L=$(labels_of "$T2")
if printf '%s' "$L" | grep -q arch-gate && ! printf '%s' "$L" | grep -q arch-rework; then ok "S3 attempt2 re-gated, stale labels stripped"; else bad "S3 attempt2 re-gate (labels=$L)"; fi
loom data update "$T2" --add-label arch-impl-ok >/dev/null 2>&1
PASS=4; arch_pending_sweep
assert "S3 attempt2 integrated"            test "$(git -C "$T/app" rev-parse HEAD)" = "$S2B"

# ============================ S4: stale ruling ================================
T3=$(mktask t3); S3A=$(commit_cand f3.txt c3-attempt1)
impl_ready "$T3" 1 "$S3A"
integrate "$T3" 1 "$S3A" gate
# candidate superseded BEFORE any ruling
S3B=$(commit_cand f3.txt c3-attempt2)
loom data comment "$T3" "IMPL-DONE attempt=2 commit=$S3B" >/dev/null 2>&1
# a stale approval lands on the old attempt
loom data update "$T3" --add-label arch-impl-ok >/dev/null 2>&1
APP_BEFORE_S4=$(git -C "$T/app" rev-parse HEAD)
PASS=5; arch_pending_sweep
assert "S4 stale drop recorded"            grep -q "ARCH-STALE-DROP task=$T3 attempt=1" "$INTEG_LOG"
assert "S4 stale approval NOT merged"      test "$(git -C "$T/app" rev-parse HEAD)" = "$APP_BEFORE_S4"
L=$(labels_of "$T3")
if ! printf '%s' "$L" | grep -qE 'arch-impl-ok|arch-gate'; then ok "S4 labels stripped"; else bad "S4 labels stripped ($L)"; fi
# fresh attempt 2 flows normally
integrate "$T3" 2 "$S3B" gate
loom data update "$T3" --add-label arch-impl-ok >/dev/null 2>&1
PASS=6; arch_pending_sweep
assert "S4 fresh attempt integrated"       test "$(git -C "$T/app" rev-parse HEAD)" = "$S3B"

# ============================ S5: in-loop timeout =============================
T4=$(mktask t4); S4A=$(commit_cand f4.txt c4)
impl_ready "$T4" 1 "$S4A"
PASS=7; integrate "$T4" 1 "$S4A" gate
PASS=9; arch_pending_sweep   # waited 2 passes, no ruling
assert "S5 ARCH-TIMEOUT recorded"          grep -q "ARCH-TIMEOUT task=$T4 attempt=1" "$INTEG_LOG"
assert "S5 timeout candidate integrated"   test "$(git -C "$T/app" rev-parse HEAD)" = "$S4A"

# ============================ S6: final sweep =================================
T5=$(mktask t5); S5A=$(commit_cand f5.txt c5)
impl_ready "$T5" 1 "$S5A"
PASS=10; integrate "$T5" 1 "$S5A" gate
arch_final_sweep
assert "S6 ARCH-TIMEOUT-FINAL recorded"    grep -q "ARCH-TIMEOUT-FINAL task=$T5 attempt=1" "$INTEG_LOG"
assert "S6 final candidate integrated"     test "$(git -C "$T/app" rev-parse HEAD)" = "$S5A"
if ! test -s "$ARCH_PENDING"; then ok "S6 pending emptied"; else bad "S6 pending emptied"; fi

# ============================ S7: check-fail path unchanged ===================
T6=$(mktask t6); S6A=$(commit_cand f6.txt c6)
impl_ready "$T6" 1 "$S6A"
APP_BEFORE_S7=$(git -C "$T/app" rev-parse HEAD)
MH="$MH_FAIL" integrate "$T6" 1 "$S6A" gate
assert "S7 INTEGRATION-FAILED recorded"    grep -q "INTEGRATION-FAILED task=$T6 attempt=1" "$INTEG_LOG"
assert "S7 /app untouched on failed check" test "$(git -C "$T/app" rev-parse HEAD)" = "$APP_BEFORE_S7"
ST=$(status_of "$T6"); L=$(labels_of "$T6")
if [ "$ST" = open ] && printf '%s' "$L" | grep -q needs-revision; then ok "S7 task reopened with needs-revision"; else bad "S7 reopen (st=$ST labels=$L)"; fi
if ! grep -q "^$T6 " "$ARCH_PENDING" 2>/dev/null; then ok "S7 never parked"; else bad "S7 never parked"; fi

# ============================ S8: design-list exclusion =======================
T7=$(mktask design-clean); loom data update "$T7" --design "clean design" --status review >/dev/null 2>&1
T8=$(mktask design-malformed); loom data update "$T8" --design "d" --status review >/dev/null 2>&1
loom data comment "$T8" "IMPL-DONE attempt oops malformed" >/dev/null 2>&1
DL=$(arch_design_list | tr '\n' ' ')
assert "S8 clean design listed"            grep -q "$T7" <<<"$DL"
if ! printf '%s' "$DL" | grep -q "$T8"; then ok "S8 malformed-marker task excluded"; else bad "S8 malformed excluded"; fi

# ============================ S9: FIFO queue hold =============================
T9A=$(mktask t9a); S9A=$(commit_cand f9a.txt c9a)
impl_ready "$T9A" 1 "$S9A"
T9B=$(mktask t9b); S9B=$(commit_cand f9b.txt c9b)   # child of S9A on the branch
impl_ready "$T9B" 1 "$S9B"
PASS=20; integrate "$T9A" 1 "$S9A" gate; integrate "$T9B" 1 "$S9B" gate
loom data update "$T9B" --add-label arch-impl-ok >/dev/null 2>&1   # approve ONLY the later one
PASS=21; arch_pending_sweep
if grep -q "ARCH-QUEUE-HOLD task=$T9B" "$INTEG_LOG"; then ok "S9 later approval held behind unresolved head"; else bad "S9 queue hold missing"; fi
if [ "$(git -C "$T/app" rev-parse HEAD)" != "$S9B" ]; then ok "S9 blocked candidate NOT integrated"; else bad "S9 blocked candidate merged"; fi
loom data update "$T9A" --add-label arch-impl-ok >/dev/null 2>&1
PASS=22; arch_pending_sweep
if [ "$(git -C "$T/app" rev-parse HEAD)" = "$S9B" ] && grep -q "INTEGRATED task=$T9A attempt=1" "$INTEG_LOG" && grep -q "INTEGRATED task=$T9B attempt=1" "$INTEG_LOG"; then ok "S9 both integrated in order after head approval"; else bad "S9 ordered integration failed (head=$(git -C "$T/app" rev-parse HEAD))"; fi

# ============================ S10: rejected-predecessor cascade ===============
T10A=$(mktask t10a); S10A=$(commit_cand f10a.txt c10a)
impl_ready "$T10A" 1 "$S10A"
T10B=$(mktask t10b); S10B=$(commit_cand f10b.txt c10b)  # contains S10A
impl_ready "$T10B" 1 "$S10B"
PASS=23; integrate "$T10A" 1 "$S10A" gate; integrate "$T10B" 1 "$S10B" gate
loom data comment "$T10A" "ARCH-FEEDBACK: bad structure" >/dev/null 2>&1
loom data update "$T10A" --status open --add-label arch-rework --assignee "" >/dev/null 2>&1
APP_S10=$(git -C "$T/app" rev-parse HEAD)
PASS=24; arch_pending_sweep
if grep -q "ARCH-CASCADE-REWORK task=$T10B attempt=1 contains_rejected=$S10A" "$INTEG_LOG"; then ok "S10 descendant cascaded to rework"; else bad "S10 cascade missing"; fi
ST=$(status_of "$T10B"); L=$(labels_of "$T10B")
if [ "$ST" = open ] && printf '%s' "$L" | grep -q arch-rework && ! printf '%s' "$L" | grep -q needs-revision; then ok "S10 descendant coder-routable"; else bad "S10 descendant routing (st=$ST labels=$L)"; fi
if [ "$(git -C "$T/app" rev-parse HEAD)" = "$APP_S10" ]; then ok "S10 nothing merged"; else bad "S10 /app moved"; fi
if ! test -s "$ARCH_PENDING"; then ok "S10 pending fully drained"; else bad "S10 pending residue: $(cat "$ARCH_PENDING")"; fi

# ============================ S11: reopen strips design approval ==============
T11=$(mktask t11)
loom data update "$T11" --design "d" --status review --add-label arch-design-ok >/dev/null 2>&1
loom data comment "$T11" "IMPL-DONE attempt=1 commit=deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" >/dev/null 2>&1
reopen_task "$T11" 1 "some failure"
L=$(labels_of "$T11")
if ! printf '%s' "$L" | grep -q arch-design-ok && printf '%s' "$L" | grep -q needs-revision; then ok "S11 reopen strips arch-design-ok"; else bad "S11 reopen strip (labels=$L)"; fi

# ============================ S12: busy-pause =================================
T12=$(mktask t12); S12A=$(commit_cand f12.txt c12)
impl_ready "$T12" 1 "$S12A"
PASS=30; integrate "$T12" 1 "$S12A" gate
PASS=40; ARCH_BUSY=1 arch_pending_sweep    # way past timeout, but arch busy
if ! grep -q "ARCH-TIMEOUT task=$T12" "$INTEG_LOG"; then ok "S12 no timeout while architect busy"; else bad "S12 timeout fired under busy"; fi
if grep -q "^$T12 1 $S12A" "$ARCH_PENDING"; then ok "S12 still pending, clock bumped"; else bad "S12 pending lost"; fi
loom data update "$T12" --add-label arch-impl-ok >/dev/null 2>&1
PASS=41; arch_pending_sweep
if [ "$(git -C "$T/app" rev-parse HEAD)" = "$S12A" ]; then ok "S12 integrates after busy clears"; else bad "S12 post-busy integrate"; fi

echo "----"
if [ "$FAILS" = 0 ]; then echo "== ARCH-GATE LIFECYCLE: ALL SCENARIOS PROVEN =="; else echo "== ARCH-GATE LIFECYCLE: $FAILS FAILURES =="; fi
rm -rf "$T"
exit "$FAILS"
