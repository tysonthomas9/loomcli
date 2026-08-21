#!/usr/bin/env bash
# Model-free lifecycle test for the template-backed team integration gate.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
HARBOR="$(cd "$HERE/.." && pwd)"
LOOMCLI="$(cd "$HARBOR/.." && pwd)"
BIN_DIR="${LOOM_BIN_DIR:-$LOOMCLI}"
T="${TMPDIR:-/tmp}/teamgate-test.$$"
mkdir -p "$T"
export PATH="$BIN_DIR:$PATH"
export LOOM_FLEET_DB_BIN="$BIN_DIR/fleet-db"
export LOOM_CONFIG_DIR="$T/loom-state"
mkdir -p "$LOOM_CONFIG_DIR"
export LOOM_WORKSPACE=TEAMGATE
export LOOM_FLEET_DB_ACTOR=teamgate-test

FAILS=0
ok() { printf 'PASS: %s\n' "$*"; }
bad() { printf 'FAIL: %s\n' "$*"; FAILS=$((FAILS + 1)); }
assert() {
  local desc="$1"; shift
  if "$@" >/dev/null 2>&1; then ok "$desc"; else bad "$desc"; fi
}

cleanup() {
  git -C "$T/app" worktree prune >/dev/null 2>&1 || true
  rm -rf -- "$T"
}
trap cleanup EXIT

[ -x "$BIN_DIR/loom" ] || { echo "FATAL: loom binary missing at $BIN_DIR/loom" >&2; exit 1; }
[ -x "$BIN_DIR/fleet-db" ] || { echo "FATAL: fleet-db binary missing at $BIN_DIR/fleet-db" >&2; exit 1; }

# --- git fixtures -------------------------------------------------------------
git init -q --bare "$T/origin.git"
mkdir -p "$T/app"
git -C "$T/app" init -q -b main
git -C "$T/app" config user.name teamgate
git -C "$T/app" config user.email teamgate@localhost
printf 'base\n' > "$T/app/base.txt"
git -C "$T/app" add base.txt
git -C "$T/app" commit -qm base
git -C "$T/app" remote add origin "$T/origin.git"
git -C "$T/app" push -q -u origin main

git clone -q "$T/origin.git" "$T/devA"
git -C "$T/devA" checkout -qb agents/TEAMGATE/devA
git -C "$T/devA" config user.name devA
git -C "$T/devA" config user.email devA@localhost
git -C "$T/devA" push -q -u origin agents/TEAMGATE/devA

git clone -q "$T/origin.git" "$T/devB"
git -C "$T/devB" checkout -qb agents/TEAMGATE/devB
git -C "$T/devB" config user.name devB
git -C "$T/devB" config user.email devB@localhost
git -C "$T/devB" push -q -u origin agents/TEAMGATE/devB

commit_from() {
  local wt="$1" file="$2" content="$3" branch
  printf '%s\n' "$content" > "$wt/$file"
  git -C "$wt" add "$file"
  git -C "$wt" commit -qm "$content"
  git -C "$wt" push -q
  branch=$(git -C "$wt" rev-parse --abbrev-ref HEAD)
  # Production agent worktrees share /app's object store. These independent
  # clones model that by importing the pushed objects without moving /app.
  git -C "$T/app" fetch -q origin "$branch"
  git -C "$wt" rev-parse HEAD
}

# --- gatelib env --------------------------------------------------------------
export LOGD="$T/logs"
mkdir -p "$LOGD"
export INTEG_LOG="$LOGD/integration.log"
: > "$INTEG_LOG"
export MARATHON_APP_DIR="$T/app"
export MARATHON_TEAM=fullstack-app
export MARATHON_TEAM_WTS="$T/devA $T/devB"
export MARATHON_CODER_WT="$T/devA"
export MH="$T/mh-pass"
mkdir -p "$MH/scripts"
printf '#!/usr/bin/env bash\nexit 0\n' > "$MH/scripts/integration-check.sh"
MH_FAIL="$T/mh-fail"
mkdir -p "$MH_FAIL/scripts"
printf '#!/usr/bin/env bash\necho deliberately failing\nexit 1\n' > "$MH_FAIL/scripts/integration-check.sh"
PASS=1
log() { :; }
record() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >> "$INTEG_LOG"; }
. "$HARBOR/scripts/gatelib.sh"

# --- board helpers ------------------------------------------------------------
loom workspace create teamgate --repos "$T/app" --path "$T/ws" --branch work >/dev/null 2>&1
mkid() { python3 -c 'import json,sys;print(json.load(sys.stdin).get("id", ""))'; }
mktask() {
  loom data create --type task --source-repo app --title "$1" -o json 2>/dev/null | mkid
}
impl_ready() {
  local tid="$1" attempt="$2" sha="$3"
  loom data update "$tid" --design "d" >/dev/null 2>&1
  loom data comment "$tid" "IMPL-DONE attempt=$attempt commit=$sha" >/dev/null 2>&1
  loom data update "$tid" --status review >/dev/null 2>&1
}
labels_of() {
  loom data show "$1" -o json 2>/dev/null \
    | python3 -c 'import json,sys;print(",".join(json.load(sys.stdin).get("labels") or []))'
}
status_of() {
  loom data show "$1" -o json 2>/dev/null \
    | python3 -c 'import json,sys;print(json.load(sys.stdin).get("status", ""))'
}
comments_of() {
  loom data show "$1" -o json 2>/dev/null \
    | python3 -c 'import json,sys;print("\n".join(c.get("text") or "" for c in json.load(sys.stdin).get("comments") or []))'
}
not_has_label() { ! printf '%s' "$(labels_of "$1")" | grep -q "$2"; }
has_label() { printf '%s' "$(labels_of "$1")" | grep -q "$2"; }

# S1: a candidate based on /app fast-forwards through the gate.
TA=$(mktask tA)
SHA_A=$(commit_from "$T/devA" a.txt devA-A)
impl_ready "$TA" 1 "$SHA_A"
integrate_team "$TA" 1 "$SHA_A"
assert "S1 INTEGRATED mode=ff" grep -q "INTEGRATED task=$TA attempt=1 .* mode=ff " "$INTEG_LOG"
assert "S1 /app HEAD equals candidate" test "$(git -C "$T/app" rev-parse HEAD)" = "$SHA_A"
assert "S1 task closed" test "$(status_of "$TA")" = closed

# S2: an independent delivery branch merges onto the integrated head.
TB=$(mktask tB)
SHA_B=$(commit_from "$T/devB" shared.txt devB-B)
impl_ready "$TB" 1 "$SHA_B"
APP_BEFORE_B=$(git -C "$T/app" rev-parse HEAD)
integrate_team "$TB" 1 "$SHA_B"
APP_AFTER_B=$(git -C "$T/app" rev-parse HEAD)
PARENTS_B=$(git -C "$T/app" rev-list --parents -n 1 "$APP_AFTER_B")
assert "S2 INTEGRATED mode=merge" grep -q "INTEGRATED task=$TB attempt=1 .* mode=merge " "$INTEG_LOG"
assert "S2 merge first parent is app_before" test "$(printf '%s' "$PARENTS_B" | awk '{print $2}')" = "$APP_BEFORE_B"
assert "S2 merge second parent is candidate" test "$(printf '%s' "$PARENTS_B" | awk '{print $3}')" = "$SHA_B"
assert "S2 task closed" test "$(status_of "$TB")" = closed

# S3: a branch that conflicts with the integrated head is stale, not revision work.
TC=$(mktask tC)
SHA_C=$(commit_from "$T/devA" shared.txt devA-conflict)
impl_ready "$TC" 1 "$SHA_C"
APP_BEFORE_C=$(git -C "$T/app" rev-parse HEAD)
integrate_team "$TC" 1 "$SHA_C"
assert "S3 INTEGRATION-STALE recorded" grep -q "INTEGRATION-STALE task=$TC attempt=1" "$INTEG_LOG"
assert "S3 task reopened" test "$(status_of "$TC")" = open
assert "S3 needs-revision removed" not_has_label "$TC" needs-revision
assert "S3 architect removed" not_has_label "$TC" architect
assert "S3 STALE-BASE comment" grep -q STALE-BASE <<< "$(comments_of "$TC")"
assert "S3 /app unchanged" test "$(git -C "$T/app" rev-parse HEAD)" = "$APP_BEFORE_C"
assert "S3 attempt handled" attempt_handled "$TC" 1

# S4: a clean merge whose integration check fails leaves /app untouched.
TD=$(mktask tD)
SHA_D=$(commit_from "$T/devB" fail.txt devB-failing-check)
impl_ready "$TD" 1 "$SHA_D"
APP_BEFORE_D=$(git -C "$T/app" rev-parse HEAD)
MH="$MH_FAIL" integrate_team "$TD" 1 "$SHA_D"
assert "S4 INTEGRATION-FAILED recorded" grep -q "INTEGRATION-FAILED task=$TD attempt=1" "$INTEG_LOG"
assert "S4 /app unchanged" test "$(git -C "$T/app" rev-parse HEAD)" = "$APP_BEFORE_D"
assert "S4 task reopened" test "$(status_of "$TD")" = open
assert "S4 no needs-revision" not_has_label "$TD" needs-revision
assert "S4 FEEDBACK names candidate" grep -q "$SHA_D" <<< "$(comments_of "$TD")"

# S5: a commit not reachable from either delivery worktree is rejected.
TE=$(mktask tE)
git -C "$T/app" worktree add --detach "$T/stale" HEAD >/dev/null 2>&1
printf 'stale\n' > "$T/stale/stale.txt"
git -C "$T/stale" add stale.txt
git -C "$T/stale" commit -qm stale-candidate
SHA_E=$(git -C "$T/stale" rev-parse HEAD)
git -C "$T/app" worktree remove --force "$T/stale" >/dev/null 2>&1
impl_ready "$TE" 1 "$SHA_E"
integrate_team "$TE" 1 "$SHA_E"
assert "S5 stale-candidate gate skip" grep -q "GATE-SKIP task=$TE attempt=1 reason=stale-candidate" "$INTEG_LOG"
assert "S5 task reopened through reopen_dev" test "$(status_of "$TE")" = open
assert "S5 no architect label" not_has_label "$TE" architect

# S6: rejected architecture work routes back to the architect lane.
TF=$(mktask tF)
reopen_arch "$TF" "design needs revision"
assert "S6 status open" test "$(status_of "$TF")" = open
assert "S6 architect label present" has_label "$TF" architect
assert "S6 needs-revision label present" has_label "$TF" needs-revision

# S7: an unattended design review auto-approves after two passes.
TG=$(mktask tG)
loom data update "$TG" --design "design" --status review --add-label architect >/dev/null 2>&1
PASS=1; team_design_fail_open
assert "S7 pass 1 remains review" test "$(status_of "$TG")" = review
assert "S7 pass 1 keeps architect" has_label "$TG" architect
PASS=3; team_design_fail_open
assert "S7 pass 3 auto-opens" test "$(status_of "$TG")" = open
assert "S7 architect removed" not_has_label "$TG" architect
assert "S7 DESIGN-AUTO-APPROVED recorded" grep -q "DESIGN-AUTO-APPROVED task=$TG waited_passes=2" "$INTEG_LOG"

# S8: orphan and revision routing, while QA work remains isolated.
TH=$(mktask tH-orphan)
TI=$(mktask tI-revision)
loom data update "$TI" --design "design" --add-label needs-revision >/dev/null 2>&1
TJ=$(mktask tJ-qa)
loom data update "$TJ" --add-label qa >/dev/null 2>&1
team_orphan_sweep
assert "S8 orphan gains architect" has_label "$TH" architect
assert "S8 ORPHAN-ROUTED recorded" grep -q "ORPHAN-ROUTED task=$TH" "$INTEG_LOG"
assert "S8 revision gains architect" has_label "$TI" architect
assert "S8 REVISION-ROUTED recorded" grep -q "REVISION-ROUTED task=$TI" "$INTEG_LOG"
assert "S8 QA keeps qa" has_label "$TJ" qa
assert "S8 QA does not gain architect" not_has_label "$TJ" architect

# S9: failed QA delivery reopens into the QA lane, retaining its qa label.
TK=$(mktask tK-qa-delivery)
loom data update "$TK" --add-label qa >/dev/null 2>&1
SHA_K=$(commit_from "$T/devB" qa-fail.txt qa-failing-check)
impl_ready "$TK" 1 "$SHA_K"
APP_BEFORE_K=$(git -C "$T/app" rev-parse HEAD)
MH="$MH_FAIL" integrate_team "$TK" 1 "$SHA_K"
assert "S9 QA integration failure recorded" grep -q "INTEGRATION-FAILED task=$TK attempt=1" "$INTEG_LOG"
assert "S9 /app unchanged" test "$(git -C "$T/app" rev-parse HEAD)" = "$APP_BEFORE_K"
assert "S9 QA task reopened" test "$(status_of "$TK")" = open
assert "S9 qa label preserved" has_label "$TK" qa
assert "S9 no needs-revision" not_has_label "$TK" needs-revision

echo "----"
if [ "$FAILS" = 0 ]; then
  echo "== TEAM-GATE LIFECYCLE: ALL SCENARIOS PROVEN =="
else
  echo "== TEAM-GATE LIFECYCLE: $FAILS FAILURES =="
fi
exit "$FAILS"
