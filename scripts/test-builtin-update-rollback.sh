#!/usr/bin/env bash
# test-builtin-update-rollback.sh — DEV-V5-41 (Slice 6) §3j demo / proof.
#
# Proves the built-in workflow version lifecycle end to end against a REAL loom
# binary, a REAL embedded fleet-db store, and REAL staged bundles on disk —
# WITHOUT the Flue compiler (LOOM_REAL_FLUE_CMD=/usr/bin/false throughout; the
# compiler is never invoked). Two packaged "app builds" are simulated with two
# loom binaries that bake different ExpectedIndexDigest values over two
# single-entry builtin-workflows trees whose epic-runner dist differs (stub
# server.mjs A vs B → distinct artifact digests → distinct immutable versions).
#
# Scenarios (spec §3j):
#   A. install      — first sync registers vA and activates it (auto track)
#   B. update       — app build B on the auto track activates vB (D1/D2)
#   C. downgrade    — app build A on the auto track re-activates vA (D6),
#                     while vB's version + staged bundle survive (D3)
#   D. rollback     — `workflow rollback` restores the previously-active vB and
#                     pins the track (D4); previous-version recorded (D4/record)
#   E. pinned keep  — after rollback (pinned), app build A does NOT auto-activate;
#                     the active version is preserved and the packaged version is
#                     surfaced as update_available (D1 pinned branch). This is the
#                     mechanism behind "pinned-custom update"; the custom-authored
#                     variant is proven at the Go/HTTP layer (CustomPinnedPreserved,
#                     PinnedUpdateAvailable, RejectsCustom) because authoring a
#                     custom version requires the compiler, which §3j forbids here.
#   F. tamper       — corrupting a staged bundle is detected (bundle_verified=false)
#                     and a re-sync self-heals a packaged built-in (D3 repair).
#
# Everything observed is appended to $OUT/update-rollback-observed-output.txt.
#
#   usage: scripts/test-builtin-update-rollback.sh
#   env:   FLEET_DB_BIN=/path/to/fleet-db
#            (default: `command -v fleet-db`, else the Loom.app bundle)
#          OUT=dir for observed output (default desktop/dist-skeleton)
#          KEEP=1 keeps the temp dir on success
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${OUT:-$ROOT/desktop/dist-skeleton}"
PKG="github.com/tysonthomas9/loomcli/internal/workflows/packaged"
WF="epic-runner"

# ---- fleet-db binary discovery (no node / no Flue needed) -------------------
FLEET_DB_BIN="${FLEET_DB_BIN:-$(command -v fleet-db 2>/dev/null || true)}"
if [[ -z "$FLEET_DB_BIN" ]]; then
  for cand in \
    "/Applications/Loom Agents.app/Contents/MacOS/fleet-db" \
    "/Applications/Loom.app/Contents/MacOS/fleet-db"; do
    [[ -x "$cand" ]] && FLEET_DB_BIN="$cand" && break
  done
fi
require_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 2; }; }
for c in go jq; do require_cmd "$c"; done
[[ -n "$FLEET_DB_BIN" && -x "$FLEET_DB_BIN" ]] || { echo "fleet-db binary not found; set FLEET_DB_BIN=/path/to/fleet-db" >&2; exit 2; }

TMP="$(mktemp -d -t loom-update-rollback.XXXXXX)"
mkdir -p "$OUT" "$TMP/bin" "$TMP/home" "$TMP/t"
OBS="$OUT/update-rollback-observed-output.txt"
: > "$OBS"

# fleet-db + loom logs stream here, kept OUT of the observed-output artifact so
# it stays a clean proof document; the tail is dumped on failure.
ERRLOG="$TMP/loom-stderr.log"
: > "$ERRLOG"

cleanup() {
  local status=$?
  if [[ $status -ne 0 ]]; then
    echo "FAIL (exit $status); observed output: $OBS; loom log tail:" >&2
    tail -30 "$ERRLOG" >&2 || true
    echo "temp: $TMP" >&2
  elif [[ "${KEEP:-0}" != "1" ]]; then
    rm -rf "$TMP"
  fi
}
trap cleanup EXIT

log()    { printf '==> %s\n' "$*" | tee -a "$OBS"; }
record() { printf -- '--- %s ---\n%s\n' "$1" "$2" >> "$OBS"; }
fail()   { echo "ASSERTION FAILED: $*" | tee -a "$OBS" >&2; exit 1; }
# jq_eq <json> <filter> <expected> <label>
jq_eq() {
  local got; got="$(jq -r "$2" <<<"$1")"
  [[ "$got" == "$3" ]] || fail "$4: expected $2 == '$3'; got '$got' from: $(head -c 800 <<<"$1")"
}
assert_contains()     { grep -qF -- "$2" <<<"$1" || fail "$3: expected to contain '$2'; got: $(head -c 600 <<<"$1")"; }

# loom_run <bin> <artifacts_dir> <data_dir> <runtime_dir> <ws> -- <args...>
# Runs a loom subcommand in a hermetic env: no node/Flue on PATH, the compiler
# pointed at /usr/bin/false, desktop runtime mode, embedded fleet-db.
loom_run() {
  local bin="$1" arts="$2" data="$3" runtime="$4" ws="$5"; shift 5
  [[ "$1" == "--" ]] && shift
  env -i \
    HOME="$TMP/home" TMPDIR="$TMP/t" PATH="/usr/bin:/bin" USER=demo \
    LOOM_CONFIG_DIR="$data" \
    LOOM_WORKSPACE_RUNTIME_DIR="$runtime" \
    LOOM_BUILTIN_ARTIFACTS_DIR="$arts" \
    LOOM_LOCAL_RUNTIME=desktop \
    LOOM_ISSUE_BACKEND=fleetdb \
    FLEET_DB_BIN="$FLEET_DB_BIN" \
    LOOM_WORKSPACE="$ws" \
    LOOM_REAL_FLUE_CMD=/usr/bin/false \
    LOOM_SDK_ROOT= FLUE_REPO= \
    "$bin" "$@" 2>>"$ERRLOG"
}

# ----------------------------------------------------------- 1. stub @loom/sdk
log "1. synthesizing a stub @loom/sdk runtime (no Flue) at $TMP/sdk"
SDK="$TMP/sdk"
mkdir -p "$SDK"
printf '{"name":"@loom/sdk","version":"0.0.0-demo","type":"module"}\n' > "$SDK/package.json"
for f in index.js internal.js driver.js runner.js runtime-adapters.js; do
  printf '// @loom/sdk demo stub (%s); the compiler is never invoked in this demo.\nexport {};\n' "$f" > "$SDK/$f"
done

# ------------------------------------------------------- 2. two stub dists A/B
log "2. building two stub epic-runner dists (A and B) — distinct artifact bytes"
mk_dist() { # mk_dist <dir> <marker>
  mkdir -p "$1"
  printf '// epic-runner demo stub build %s — not executed (no run is created).\nexport const BUILD = "%s";\n' "$2" "$2" > "$1/server.mjs"
}
mk_dist "$TMP/distA" A
mk_dist "$TMP/distB" B

# ------------------------------------------------- 3. package two builtin trees
log "3. loom workflow package-builtin epic-runner → tree A and tree B"
pkg_tree() { # pkg_tree <dist> <out> ; echoes index_digest,artifact_digest
  local json
  json="$(cd "$ROOT" && go run ./cmd/loom workflow package-builtin "$WF" \
    --dist "$1" --out "$2" --loom-sdk "$SDK" --json)" \
    || { echo "$json" >&2; fail "package-builtin failed for $1"; }
  record "package-builtin $2" "$json"
  jq -r '[.index_digest, .artifact_digest] | @tsv' <<<"$json"
}
read -r IDX_A ART_A < <(pkg_tree "$TMP/distA" "$TMP/treeA")
read -r IDX_B ART_B < <(pkg_tree "$TMP/distB" "$TMP/treeB")
log "   tree A: index_digest=$IDX_A artifact_digest=$ART_A"
log "   tree B: index_digest=$IDX_B artifact_digest=$ART_B"
[[ -n "$IDX_A" && -n "$IDX_B" ]] || fail "empty index digests"
[[ "$IDX_A" != "$IDX_B" ]] || fail "tree A and B produced the same index_digest ($IDX_A); dists did not differ"
[[ "$ART_A" != "$ART_B" ]] || fail "tree A and B produced the same artifact_digest; dists did not differ"

# --------------------------------------------------- 4. two packaged loom builds
log "4. building loom-A and loom-B with the two ExpectedIndexDigest values baked"
(cd "$ROOT" && go build -ldflags "-X $PKG.ExpectedIndexDigest=$IDX_A" -o "$TMP/bin/loom-A" ./cmd/loom) || fail "go build loom-A"
(cd "$ROOT" && go build -ldflags "-X $PKG.ExpectedIndexDigest=$IDX_B" -o "$TMP/bin/loom-B" ./cmd/loom) || fail "go build loom-B"

# ------------------------------------------------------------- 5. lifecycle env
DATA="$TMP/data-life"; RUNTIME="$TMP/runtime-life"; WS="BUILTINROLL"
mkdir -p "$DATA" "$RUNTIME"
log "5. creating hermetic workspace $WS (embedded fleet-db, no serve)"
WS_OUT="$(loom_run "$TMP/bin/loom-A" "$TMP/treeA" "$DATA" "$RUNTIME" "$WS" -- workspace add "$WS")" \
  || fail "workspace add (see loom log)"
record "workspace add $WS" "$WS_OUT"
assert_contains "$WS_OUT" "Created workspace $WS" "workspace add"

# ------------------------------------------------------------- A. install (auto)
log "A. install — loom-A + tree A: first sync registers + activates vA (auto)"
A_JSON="$(loom_run "$TMP/bin/loom-A" "$TMP/treeA" "$DATA" "$RUNTIME" "$WS" -- workflow sync "$WF" --json)" \
  || { echo "$A_JSON" >&2; fail "sync A"; }
record "A. sync (install) --json" "$A_JSON"
VA="$(jq -r '.active_version_id' <<<"$A_JSON")"
jq_eq "$A_JSON" '.activated'                 true    "A.activated"
jq_eq "$A_JSON" '.track'                     auto    "A.track"
jq_eq "$A_JSON" '.update_available'          false   "A.update_available"
jq_eq "$A_JSON" '.packaged.registered_new'   true    "A.registered_new"
jq_eq "$A_JSON" '.packaged.artifact_digest'  "$ART_A" "A.packaged.artifact_digest"
jq_eq "$A_JSON" '.active_bundle_available'   true    "A.active_bundle_available"
[[ -n "$VA" && "$VA" != "null" ]] || fail "A: no active version id"
log "   vA=$VA active, track=auto"

# ------------------------------------------------------------- B. update (auto)
log "B. update — loom-B + tree B: auto track activates vB (D1/D2)"
B_JSON="$(loom_run "$TMP/bin/loom-B" "$TMP/treeB" "$DATA" "$RUNTIME" "$WS" -- workflow sync "$WF" --json)" \
  || { echo "$B_JSON" >&2; fail "sync B"; }
record "B. sync (update) --json" "$B_JSON"
VB="$(jq -r '.active_version_id' <<<"$B_JSON")"
jq_eq "$B_JSON" '.activated'                    true    "B.activated"
jq_eq "$B_JSON" '.track'                        auto    "B.track"
jq_eq "$B_JSON" '.packaged.registered_new'      true    "B.registered_new"
jq_eq "$B_JSON" '.packaged.artifact_digest'     "$ART_B" "B.packaged.artifact_digest"
jq_eq "$B_JSON" '.previous_active_version_id'   "$VA"   "B.previous_active_version_id"
[[ -n "$VB" && "$VB" != "null" && "$VB" != "$VA" ]] || fail "B: vB not a new distinct version (vB=$VB vA=$VA)"
log "   vB=$VB active (updated from vA), track=auto"

# ----------------------------------------------------------- C. downgrade (D6)
log "C. downgrade — loom-A + tree A: auto track re-activates vA (D6); vB survives (D3)"
C_JSON="$(loom_run "$TMP/bin/loom-A" "$TMP/treeA" "$DATA" "$RUNTIME" "$WS" -- workflow sync "$WF" --json)" \
  || { echo "$C_JSON" >&2; fail "sync C (downgrade)"; }
record "C. sync (downgrade) --json" "$C_JSON"
jq_eq "$C_JSON" '.active_version_id'            "$VA"   "C.active_version_id"
jq_eq "$C_JSON" '.activated'                    true    "C.activated"
jq_eq "$C_JSON" '.track'                        auto    "C.track"
jq_eq "$C_JSON" '.packaged.registered_new'      false   "C.registered_new(already staged)"
jq_eq "$C_JSON" '.previous_active_version_id'   "$VB"   "C.previous_active_version_id"
log "   active=vA again (downgraded from vB); vB version + bundle retained"

# ------------------------------------------------------------- D. rollback (D4)
log "D. rollback — restore the previously-active vB and pin the track (D4)"
D_TXT="$(loom_run "$TMP/bin/loom-A" "$TMP/treeA" "$DATA" "$RUNTIME" "$WS" -- workflow rollback "$WF")" \
  || fail "rollback (see loom log)"
record "D. rollback (text)" "$D_TXT"
assert_contains "$D_TXT" "Rolled back workflow" "D.rollback line"
assert_contains "$D_TXT" "to version $VB"       "D.rollback target=vB"
assert_contains "$D_TXT" "(previous $VA)"       "D.rollback previous=vA"
# Confirm the state: active=vB, track pinned, selected_by=user (operator rollback).
D_VERS="$(loom_run "$TMP/bin/loom-A" "$TMP/treeA" "$DATA" "$RUNTIME" "$WS" -- workflow versions "$WF" --json)" \
  || { echo "$D_VERS" >&2; fail "versions after rollback"; }
record "D. versions after rollback --json" "$D_VERS"
jq_eq "$D_VERS" ".versions[] | select(.version.version_id==\"$VB\") | .active"      true "D.vB active"
jq_eq "$D_VERS" ".versions[] | select(.version.version_id==\"$VB\") | .selected_by" user "D.vB selected_by=user"
jq_eq "$D_VERS" '.builtin.track'                                                    pinned "D.track pinned"
log "   active=vB, track=pinned, selected_by=user"

# ----------------------------------------------- E. pinned keeps active (D1)
log "E. pinned keep — loom-A + tree A must NOT auto-activate; update_available surfaced"
E_JSON="$(loom_run "$TMP/bin/loom-A" "$TMP/treeA" "$DATA" "$RUNTIME" "$WS" -- workflow sync "$WF" --json)" \
  || { echo "$E_JSON" >&2; fail "sync E (pinned)"; }
record "E. sync (pinned, no activate) --json" "$E_JSON"
jq_eq "$E_JSON" '.activated'          false  "E.activated"
jq_eq "$E_JSON" '.active_version_id'  "$VB"  "E.active preserved=vB"
jq_eq "$E_JSON" '.track'              pinned "E.track pinned"
jq_eq "$E_JSON" '.update_available'   true   "E.update_available"
jq_eq "$E_JSON" '.packaged.version_id' "$VA" "E.packaged=vA"
E_VERS="$(loom_run "$TMP/bin/loom-A" "$TMP/treeA" "$DATA" "$RUNTIME" "$WS" -- workflow versions "$WF" --json)" \
  || { echo "$E_VERS" >&2; fail "versions E"; }
record "E. versions (pinned) --json" "$E_VERS"
jq_eq "$E_VERS" '.builtin.update_available'    true  "E.builtin.update_available"
jq_eq "$E_VERS" '.builtin.packaged_version_id' "$VA" "E.builtin.packaged_version_id"
# newest-first ordering: vB has the higher Version number, so it lists first.
jq_eq "$E_VERS" '.versions[0].version.version_id' "$VB" "E.versions newest-first(vB)"
log "   active preserved=vB (pinned); packaged vA surfaced as update_available"

# --------------------------------------------------- F. tamper + self-heal (D3)
log "F. tamper — corrupt a staged bundle (detected), then re-sync self-heals (D3)"
DATA_T="$TMP/data-tamper"; RUNTIME_T="$TMP/runtime-tamper"; WS_T="TAMPER"
mkdir -p "$DATA_T" "$RUNTIME_T"
loom_run "$TMP/bin/loom-A" "$TMP/treeA" "$DATA_T" "$RUNTIME_T" "$WS_T" -- workspace add "$WS_T" >/dev/null \
  || fail "tamper: workspace add"
F0_JSON="$(loom_run "$TMP/bin/loom-A" "$TMP/treeA" "$DATA_T" "$RUNTIME_T" "$WS_T" -- workflow sync "$WF" --json)" \
  || { echo "$F0_JSON" >&2; fail "tamper: initial sync"; }
record "F. initial sync --json" "$F0_JSON"
VT="$(jq -r '.active_version_id' <<<"$F0_JSON")"
jq_eq "$F0_JSON" '.active_bundle_available' true "F.initial active_bundle_available"

# Locate + corrupt the staged server.mjs for the active bundle.
STAGED="$(find "$RUNTIME_T" -type f -name server.mjs | head -1)"
[[ -n "$STAGED" ]] || fail "tamper: no staged server.mjs found under $RUNTIME_T"
printf '\n// TAMPERED — appended bytes change the bundle digest.\n' >> "$STAGED"
log "   tampered staged bundle: $STAGED"

F_BAD="$(loom_run "$TMP/bin/loom-A" "$TMP/treeA" "$DATA_T" "$RUNTIME_T" "$WS_T" -- workflow versions "$WF" --json)" \
  || { echo "$F_BAD" >&2; fail "tamper: versions after tamper"; }
record "F. versions after tamper --json" "$F_BAD"
jq_eq "$F_BAD" ".versions[] | select(.version.version_id==\"$VT\") | .bundle_verified" false "F.bundle_verified=false after tamper"
log "   detected: bundle_verified=false for the tampered version"

F_FIX="$(loom_run "$TMP/bin/loom-A" "$TMP/treeA" "$DATA_T" "$RUNTIME_T" "$WS_T" -- workflow sync "$WF" --json)" \
  || { echo "$F_FIX" >&2; fail "tamper: repair sync"; }
record "F. repair sync --json" "$F_FIX"
jq_eq "$F_FIX" '.repaired'                 true "F.repaired"
jq_eq "$F_FIX" '.active_bundle_available'  true "F.active_bundle_available after repair"
F_OK="$(loom_run "$TMP/bin/loom-A" "$TMP/treeA" "$DATA_T" "$RUNTIME_T" "$WS_T" -- workflow versions "$WF" --json)" \
  || { echo "$F_OK" >&2; fail "tamper: versions after repair"; }
record "F. versions after repair --json" "$F_OK"
jq_eq "$F_OK" ".versions[] | select(.version.version_id==\"$VT\") | .bundle_verified" true "F.bundle_verified=true after repair"
log "   self-healed: re-sync re-staged the packaged bundle; bundle_verified=true"

# ----------------------------------------------------- disk cost (retention doc)
log "measuring per-version staged bundle disk cost (D3 retention note)"
DISK="$(du -sk "$RUNTIME" 2>/dev/null | awk '{print $1}')"
NBUNDLES="$(find "$RUNTIME" -type f -name server.mjs | wc -l | tr -d ' ')"
record "disk: staged bundles" "runtime_dir=$RUNTIME total_kib=$DISK bundles=$NBUNDLES (two retained versions vA,vB)"
log "   staged bundles: ${DISK} KiB across ${NBUNDLES} retained version bundle(s)"

log "ALL SCENARIOS PASSED — install, update, downgrade, rollback, pinned-keep, tamper/self-heal"
log "observed output: $OBS"
