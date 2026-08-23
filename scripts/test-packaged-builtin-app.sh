#!/usr/bin/env bash
# test-packaged-builtin-app.sh — DEV-V5-37 Definition of Done.
#
# Takes a locally-built (ad-hoc-signed) `Loom Agents.app` and proves, from a
# clean `env -i` shell with NO dev checkout, NO node/flue on PATH, and the Flue
# compiler pointed at /usr/bin/false, that the shipped runtime runs BOTH
# built-ins entirely from embedded artifacts:
#
#    1. ENV     app layout present; copy the app; record node version + sizes;
#               no *.node / symlinks in the artifact tree; index digest
#    2. SMOKE   load-smoke each server.mjs under the app's own bundled node
#    3. START   `loom local start`, poll `local status` to healthy; capture
#               the serve process environment (ps eww)
#    4. READYZ  builtin_runtime_ready over both; node.source=bundled at the
#               app's node; authoring_ready=false; expected_index_digest match
#    5. EPIC    epic-runner dry-run → completed (no compile)
#    6. GH      github-review-agent draft-PR run → completed; summary contains
#               "is a draft; deferring review" (DEFECT-1: output.skipped is not
#               persisted by sdk/driver.js + executor.go — assert the summary)
#    7. VERS    both versions: provenance=packaged_builtin, trust=trusted,
#               packaged_index_digest == the app's expected digest
#    8. LOG     serve log has both packaged-registration lines + the bundled
#               resolver line and NONE of the compile/dev-path tokens
#    9. SYMLINK loom invoked through a symlink still resolves the bundle
#   10. TAMPER  on a SEPARATE copy, fresh data dirs: flip a byte in each
#               server.mjs (mirror) and in index.json (both) → POST fails
#               closed (never 404), no DriverVersion; untampered copy succeeds
#   11. B4      artifact dir moved away, LOOM_LOCAL_RUNTIME unset → the baked
#               digest still forces fail-closed
#   12. PASS
#
# Everything observed is appended to $OUT/observed-output.txt.
#
#   usage: scripts/test-packaged-builtin-app.sh ["/path/to/Loom Agents.app"]
#   env:   APP    override the app path (else arg 1, else the release bundle)
#          OUT    observed-output dir (default desktop/dist-app-test)
#          SMOKE_JS  path to smoke-load-server.mjs (default desktop/scripts/…)
#          KEEP_APP_TEST=1  keep the temp dir on success
#
# Driver-host prerequisites: bash, curl, jq, ps, dd, cp, shasum — nothing else
# (node/flue/go come from the app bundle, never the host).
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP="${APP:-${1:-$ROOT/desktop/src-tauri/target/release/bundle/macos/Loom Agents.app}}"
OUT="${OUT:-$ROOT/desktop/dist-app-test}"
SMOKE_JS="${SMOKE_JS:-$ROOT/desktop/scripts/smoke-load-server.mjs}"

require_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 2; }; }
for c in curl jq ps dd cp shasum; do require_cmd "$c"; done
[[ -f "$SMOKE_JS" ]] || { echo "smoke-load-server.mjs not found at $SMOKE_JS (set SMOKE_JS)" >&2; exit 2; }

# ---- ENV: the app must be a real, artifact-bearing bundle -------------------
for p in loom node fleet-db; do
  [[ -x "$APP/Contents/MacOS/$p" ]] || { echo "not a built app: missing Contents/MacOS/$p in '$APP' — build it first (desktop/README.md)" >&2; exit 1; }
done
[[ -f "$APP/Contents/Resources/builtin-workflows/index.json" ]] \
  || { echo "no builtin-workflows/index.json in '$APP' — built with LOOM_SKIP_BUILTIN_ARTIFACTS=1? rebuild without it" >&2; exit 1; }

TMP="$(mktemp -d -t loom-packaged-app.XXXXXX)"
mkdir -p "$OUT" "$TMP/app" "$TMP/tampered" "$TMP/bin"
OBS="$OUT/observed-output.txt"
: > "$OBS"

SUT_HOME="$(mktemp -d "$TMP/home.XXXXXX")"
SUT_TMP="$(mktemp -d "$TMP/sut-tmp.XXXXXX")"

log() { printf '==> %s\n' "$*" | tee -a "$OBS"; }
record() { printf -- '--- %s ---\n%s\n' "$1" "$2" >> "$OBS"; }
fail() { echo "ASSERTION FAILED: $*" | tee -a "$OBS" >&2; exit 1; }
assert_contains() { grep -qF -- "$2" <<<"$1" || fail "$3: expected to contain '$2'; got: $(head -c 800 <<<"$1")"; }
assert_not_contains() { ! grep -qF -- "$2" <<<"$1" || fail "$3: must NOT contain '$2'"; }

# env -i sandbox: exactly the desktop-service allowlist, nothing inherited.
sut() { env -i HOME="$SUT_HOME" TMPDIR="$SUT_TMP" PATH=/usr/bin:/bin:/usr/sbin:/sbin \
  USER="${USER:-loom}" LOGNAME="${USER:-loom}" SHELL=/bin/sh LANG=en_US.UTF-8 TERM=dumb \
  LOOM_REAL_FLUE_CMD=/usr/bin/false "$@"; }
# readyz/versions locate their store through the config env, not --data-dir.
sut_data() { local data="$1"; shift; sut env LOOM_CONFIG_DIR="$data" \
  LOOM_WORKSPACE_RUNTIME_DIR="$data" LOOM_LOCAL_RUNTIME=desktop LOOM_ISSUE_BACKEND=fleetdb "$@"; }

DATA_DIRS=()
cleanup() {
  local status=$?
  for d in "${DATA_DIRS[@]:-}"; do
    [[ -n "$d" ]] && sut "$APP_MACOS/loom" local stop --data-dir "$d" >/dev/null 2>&1 || true
  done
  pkill -f "$TMP/.*/Contents/MacOS/(loom|fleet-db|node)" >/dev/null 2>&1 || true
  if [[ $status -ne 0 ]]; then
    echo "FAIL (exit $status); observed output: $OBS; temp: $TMP" >&2
  elif [[ "${KEEP_APP_TEST:-0}" != "1" ]]; then
    rm -rf "$TMP"
  fi
}
trap cleanup EXIT

# ---------------------------------------------------------------- 1. ENV
log "1. ENV: copying the app to a scratch dir (the built app is never touched)"
COPY="$TMP/app/Loom Agents.app"
cp -R "$APP" "$COPY"
# macOS symlinks /var -> /private/var; EvalSymlinks in the resolver reports the
# physical path, so compare node.path against the physical copy path.
APP_COPY_REAL="$(cd "$COPY" && pwd -P)"
APP_MACOS="$COPY/Contents/MacOS"
BW="$COPY/Contents/Resources/builtin-workflows"
NODE_REAL="$APP_COPY_REAL/Contents/MacOS/node"

record "node --version" "$(sut "$APP_MACOS/node" --version)"
[[ "$(sut "$APP_MACOS/node" --version)" == v* ]] || fail "bundled node did not report a version"
record "sizes (du -sh)" "$(du -sh "$APP_MACOS/node" "$BW" 2>/dev/null || true)"
NATIVE="$(find "$BW" \( -name '*.node' -o -type l \) -print)"
[[ -z "$NATIVE" ]] || fail "artifact tree has native addons or symlinks: $NATIVE"
INDEX_DIGEST="sha256:$(shasum -a 256 "$BW/index.json" | awk '{print $1}')"
record "index digest" "$INDEX_DIGEST"
[[ "$INDEX_DIGEST" == sha256:* ]] || fail "index digest missing"

# ---------------------------------------------------------------- 2. SMOKE
log "2. SMOKE: load each server.mjs under the bundled node"
for name in epic-runner github-review-agent; do
  SMOKE_OUT="$(sut "$APP_MACOS/node" "$SMOKE_JS" "$BW/$name/dist/server.mjs" "$name")" \
    || fail "load smoke failed for $name: $SMOKE_OUT"
  record "smoke $name" "$SMOKE_OUT"
  assert_contains "$SMOKE_OUT" "execPath=$NODE_REAL" "smoke $name execPath"
done

# ---- HTTP helpers bound to the currently-running runtime's $URL -------------
URL=""
SERVE_PID=""
WS_KEY=""
EPIC=""
api() {
  if [[ -n "${3:-}" ]]; then
    curl -sS -X "$1" -H 'Content-Type: application/json' --data "$3" "$URL$2"
  else
    curl -sS -X "$1" "$URL$2"
  fi
}
api_status() { # api_status <method> <path> <body> → "<code>\n<body>"
  curl -sS -o "$TMP/body" -w '%{http_code}' -X "$1" -H 'Content-Type: application/json' --data "$3" "$URL$2"
  printf '\n'
  cat "$TMP/body"
}
seed_workspace() { # fresh empty workspace + epic + task; sets WS_KEY and EPIC
  local ws_json
  ws_json="$(api POST /api/workspaces "$(jq -nc '{name:"APPTEST",type:"empty",repos:[]}')")"
  WS_KEY="$(jq -r '.data.id // .data.key // empty' <<<"$ws_json")"
  [[ -n "$WS_KEY" ]] || fail "workspace create: $ws_json"
  EPIC="$(api POST "/api/workspaces/$WS_KEY/issues" '{"title":"app epic","issue_type":"epic","priority":1}' | jq -r '.data.id // empty')"
  [[ -n "$EPIC" ]] || fail "epic create failed"
  api POST "/api/workspaces/$WS_KEY/issues" "$(jq -nc --arg p "$EPIC" '{title:"app task",issue_type:"task",priority:1,parent:$p}')" >/dev/null
}
post_epic() {
  api_status POST "/api/workspaces/$WS_KEY/workflows/epic-runner" \
    "$(jq -nc --arg e "$EPIC" '{epicId:$e,dryRun:true,runner:"daytona-task-runner",requestedBy:"app-test"}')"
}
post_gh() {
  api_status POST "/api/workspaces/$WS_KEY/workflows/github-review-agent" \
    "$(jq -nc '{repo:"octo/demo",prNumber:1,headSha:"0123456789abcdef0123456789abcdef01234567",draft:true,requestedBy:"app-test"}')"
}
post_wf() { case "$1" in github-review-agent) post_gh;; *) post_epic;; esac; }
wait_run() { # wait_run <run-id> → terminal run json
  local run status
  for _ in $(seq 1 240); do
    run="$(api GET "/api/workspaces/$WS_KEY/runs/$1")"
    status="$(jq -r .status <<<"$run")"
    case "$status" in completed|failed|needs_review|cancelled) printf '%s' "$run"; return 0;; esac
    sleep 0.5
  done
  printf '%s' "$run"
  fail "run $1 did not reach a terminal status: $run"
}
version_count() { # version_count <loom-bin> <data-dir> <workflow> → integer (0 on any error/not-found)
  sut_data "$2" env LOOM_WORKSPACE="$WS_KEY" "$1" workflow versions "$3" --json 2>/dev/null \
    | jq '.versions | length' 2>/dev/null || echo 0
}

app_start() { # app_start <loom-bin> <data-dir>  → sets URL, SERVE_PID
  local loom="$1" data="$2" st
  mkdir -p "$data"
  DATA_DIRS+=("$data")
  sut "$loom" local start --data-dir "$data" >>"$OUT/local-start.log" 2>&1 &
  for _ in $(seq 1 480); do
    st="$(sut "$loom" local status --data-dir "$data" --json 2>/dev/null || true)"
    if [[ "$(jq -r '.healthy // false' <<<"$st" 2>/dev/null)" == true ]]; then
      URL="$(jq -r '.runtime.url' <<<"$st")"
      SERVE_PID="$(jq -r '.runtime.serve_pid' <<<"$st")"
      [[ -n "$URL" && "$URL" != null ]] || fail "healthy but no runtime.url: $st"
      return 0
    fi
    sleep 0.25
  done
  st="$(sut "$loom" local status --data-dir "$data" --json 2>/dev/null || true)"
  printf 'last status: %s\n' "$st" >&2
  tail -30 "$data/logs/loom-serve.log" 2>/dev/null >&2 || true
  fail "runtime did not become healthy for $data"
}
app_stop() { # app_stop <loom-bin> <data-dir>
  sut "$1" local stop --data-dir "$2" >>"$OUT/local-start.log" 2>&1 || true
  SERVE_PID=""
}

# ---------------------------------------------------------------- 3. START
DATA="$TMP/data1"
log "3. START: loom local start (env -i, no PATH node, compiler=/usr/bin/false)"
app_start "$APP_MACOS/loom" "$DATA"
ps eww "$SERVE_PID" > "$OUT/ps-env.txt" 2>/dev/null || true
PSENV="$(cat "$OUT/ps-env.txt")"
record "serve process env" "$PSENV"
assert_contains "$PSENV" "LOOM_REAL_FLUE_CMD=/usr/bin/false" "serve env tripwire"
for bad in "LOOM_SDK_ROOT=" "FLUE_REPO=" "LOOM_NODE_BIN=" "LOOM_BUILTIN_ARTIFACTS_DIR=" "LOOM_REAL_FLUE_CMD_JSON=" "LOOM_DAEMON_LEAF="; do
  assert_not_contains "$PSENV" "$bad" "serve env"
done

# ---------------------------------------------------------------- 4. READYZ
log "4. READYZ: builtin_runtime_ready over both built-ins, node.source=bundled"
READYZ="$(sut_data "$DATA" "$APP_MACOS/loom" workflow readyz --json)"
record "readyz" "$READYZ"
[[ "$(jq -r .builtin_runtime_ready <<<"$READYZ")" == true ]] || fail "builtin_runtime_ready != true"
[[ "$(jq -c '.builtin_runtime.required' <<<"$READYZ")" == '["epic-runner","github-review-agent"]' ]] || fail "builtin_runtime.required != both names: $(jq -c '.builtin_runtime.required' <<<"$READYZ")"
[[ "$(jq -r '.builtin_runtime.artifacts."epic-runner".verified' <<<"$READYZ")" == true ]] || fail "epic-runner not verified"
[[ "$(jq -r '.builtin_runtime.artifacts."github-review-agent".verified' <<<"$READYZ")" == true ]] || fail "github-review-agent not verified"
[[ "$(jq -r .builtin_runtime.node.source <<<"$READYZ")" == bundled ]] || fail "node.source != bundled"
[[ "$(jq -r .builtin_runtime.node.path <<<"$READYZ")" == "$NODE_REAL" ]] || fail "node.path != $NODE_REAL (got $(jq -r .builtin_runtime.node.path <<<"$READYZ"))"
[[ "$(jq -r .authoring_ready <<<"$READYZ")" == false ]] || fail "authoring_ready should be false"
[[ "$(jq -r .builtin_runtime.expected_index_digest <<<"$READYZ")" == "$INDEX_DIGEST" ]] || fail "expected_index_digest != $INDEX_DIGEST"

# ---------------------------------------------------------------- 5. EPIC
log "5. EPIC-RUNNER: seed workspace, POST dryRun epic → completed"
seed_workspace
RESP="$(post_epic)"; CODE="${RESP%%$'\n'*}"; BODY="${RESP#*$'\n'}"
record "POST epic-runner" "HTTP $CODE $BODY"
[[ "$CODE" == 202 ]] || fail "POST epic-runner: HTTP $CODE $BODY"
RUN="$(wait_run "$(jq -r .run_id <<<"$BODY")")"
record "epic-runner run" "$RUN"
[[ "$(jq -r .status <<<"$RUN")" == completed ]] || fail "epic-runner status: $(jq -r .status <<<"$RUN") summary: $(jq -r .summary <<<"$RUN")"
assert_contains "$(jq -r .summary <<<"$RUN")" "Dry-run validated epic" "epic-runner summary"

# ---------------------------------------------------------------- 6. GH
log "6. GITHUB-REVIEW-AGENT: POST draft → completed + draft summary (offline)"
RESP="$(post_gh)"; CODE="${RESP%%$'\n'*}"; BODY="${RESP#*$'\n'}"
record "POST github-review-agent (draft)" "HTTP $CODE $BODY"
[[ "$CODE" == 202 ]] || fail "POST github-review-agent: HTTP $CODE $BODY"
GH_RUN="$(wait_run "$(jq -r .run_id <<<"$BODY")")"
record "github-review-agent run" "$GH_RUN"
[[ "$(jq -r .status <<<"$GH_RUN")" == completed ]] || fail "gh draft status: $(jq -r .status <<<"$GH_RUN") summary: $(jq -r .summary <<<"$GH_RUN")"
# DEFECT-1: output.skipped is dropped by sdk/driver.js + executor.go; the draft
# short-circuit's summary is the persisted proof the real bundle executed.
assert_contains "$(jq -r .summary <<<"$GH_RUN")" "is a draft; deferring review" "gh draft summary"

# ---------------------------------------------------------------- 7. VERSIONS
log "7. VERSIONS: both provenance=packaged_builtin, trust=trusted, digest match"
for name in epic-runner github-review-agent; do
  V="$(sut_data "$DATA" env LOOM_WORKSPACE="$WS_KEY" "$APP_MACOS/loom" workflow versions "$name" --json)"
  record "versions $name" "$V"
  [[ "$(jq -r '.versions[]|select(.active)|.version.manifest.provenance' <<<"$V")" == packaged_builtin ]] || fail "$name provenance != packaged_builtin"
  [[ "$(jq -r '.versions[]|select(.active)|.version.manifest.trust_level' <<<"$V")" == trusted ]] || fail "$name trust_level != trusted"
  [[ "$(jq -r '.versions[]|select(.active)|.version.manifest.packaged_index_digest' <<<"$V")" == "$INDEX_DIGEST" ]] || fail "$name packaged_index_digest != $INDEX_DIGEST"
done

# ---------------------------------------------------------------- 8. LOG
log "8. LOG: stop, then assert both registrations + bundled resolver, no compile"
app_stop "$APP_MACOS/loom" "$DATA"
cp "$DATA/logs/loom-serve.log" "$OUT/serve-main.log" 2>/dev/null || fail "no serve log at $DATA/logs/loom-serve.log"
SLOG="$(cat "$OUT/serve-main.log")"
assert_contains "$SLOG" "builtin workflow registered from packaged artifact" "serve log registration"
assert_contains "$SLOG" "workflow=epic-runner" "serve log epic-runner registration"
assert_contains "$SLOG" "workflow=github-review-agent" "serve log github-review-agent registration"
assert_contains "$SLOG" "node runtime resolved" "serve log resolver"
assert_contains "$SLOG" "source=bundled" "serve log resolver source"
assert_contains "$SLOG" "path=$NODE_REAL" "serve log resolver path"
for bad in "source=path" "source=override" "flue build" "workflow-builds" "BuildAndRegister" "local @loom/sdk" "LOOM_SDK_ROOT"; do
  assert_not_contains "$SLOG" "$bad" "serve log forbidden token"
done
SRC_PATH_COUNT="$(grep -Ec 'source=(path|override)' "$OUT/serve-main.log" || true)"
[[ "$SRC_PATH_COUNT" == 0 ]] || fail "serve log has $SRC_PATH_COUNT source=path|override line(s)"

# ---------------------------------------------------------------- 9. SYMLINK
log "9. SYMLINK: loom via a symlink still resolves the bundle (EvalSymlinks)"
ln -s "$APP_MACOS/loom" "$TMP/bin/loom"
SL="$(sut env LOOM_CONFIG_DIR="$TMP/data-symlink" LOOM_LOCAL_RUNTIME=desktop "$TMP/bin/loom" workflow readyz --json)"
record "readyz via symlink" "$SL"
[[ "$(jq -r .builtin_runtime.node.source <<<"$SL")" == bundled ]] || fail "symlink: node.source != bundled"
[[ "$(jq -r '.builtin_runtime.artifacts."epic-runner".verified' <<<"$SL")" == true ]] || fail "symlink: epic-runner not verified"
[[ "$(jq -r '.builtin_runtime.artifacts."github-review-agent".verified' <<<"$SL")" == true ]] || fail "symlink: github-review-agent not verified"

# ---------------------------------------------------------------- 10. TAMPER
log "10. TAMPER: on a separate copy, fresh data dirs, byte-flips fail closed"
TAMP="$TMP/tampered/Loom Agents.app"
cp -R "$APP" "$TAMP"
TAMP_LOOM="$TAMP/Contents/MacOS/loom"
TAMP_BW="$TAMP/Contents/Resources/builtin-workflows"

flip64() { printf '\x00' | dd of="$1" bs=1 seek=64 conv=notrunc 2>/dev/null; }

assert_fail_closed() { # assert_fail_closed <loom-bin> <data-dir> <workflow> <want-field>
  local resp code body
  resp="$(post_wf "$3")"; code="${resp%%$'\n'*}"; body="${resp#*$'\n'}"
  record "POST $3 (tampered)" "HTTP $code $body"
  [[ "$code" =~ ^4|^5 ]] && [[ "$code" != 404 ]] || fail "tampered $3: want non-2xx and never 404, got $code $body"
  assert_contains "$body" "builtin_artifact_invalid" "tampered $3 body"
  assert_contains "$body" "$4" "tampered $3 body ($4)"
  assert_contains "$body" "reinstall Loom" "tampered $3 body"
  local vc; vc="$(version_count "$1" "$2" "$3")"
  [[ "$vc" == 0 ]] || fail "tampered $3 created $vc DriverVersion(s); expected none"
}
assert_succeeds() { # assert_succeeds <workflow>
  local resp code body
  resp="$(post_wf "$1")"; code="${resp%%$'\n'*}"; body="${resp#*$'\n'}"
  record "POST $1 (companion, expect success)" "HTTP $code $body"
  [[ "$code" == 202 ]] || fail "companion $1: HTTP $code $body"
  [[ "$(wait_run "$(jq -r .run_id <<<"$body")" | jq -r .status)" == completed ]] || fail "companion $1 did not complete"
}

# 10a. gh server.mjs flipped → gh fails, epic still runs
GH_MJS="$TAMP_BW/github-review-agent/dist/server.mjs"
cp "$GH_MJS" "$TMP/gh.bak"; flip64 "$GH_MJS"
app_start "$TAMP_LOOM" "$TMP/data-gh"; seed_workspace
assert_fail_closed "$TAMP_LOOM" "$TMP/data-gh" github-review-agent artifact_digest
assert_succeeds epic-runner
app_stop "$TAMP_LOOM" "$TMP/data-gh"; cp "$TMP/gh.bak" "$GH_MJS"

# 10b. epic server.mjs flipped → epic fails, gh still runs (mirror)
EP_MJS="$TAMP_BW/epic-runner/dist/server.mjs"
cp "$EP_MJS" "$TMP/ep.bak"; flip64 "$EP_MJS"
app_start "$TAMP_LOOM" "$TMP/data-epic"; seed_workspace
assert_fail_closed "$TAMP_LOOM" "$TMP/data-epic" epic-runner artifact_digest
assert_succeeds github-review-agent
app_stop "$TAMP_LOOM" "$TMP/data-epic"; cp "$TMP/ep.bak" "$EP_MJS"

# 10c. index.json flipped → BOTH fail with index_digest
cp "$TAMP_BW/index.json" "$TMP/idx.bak"; flip64 "$TAMP_BW/index.json"
app_start "$TAMP_LOOM" "$TMP/data-index"; seed_workspace
assert_fail_closed "$TAMP_LOOM" "$TMP/data-index" epic-runner index_digest
assert_fail_closed "$TAMP_LOOM" "$TMP/data-index" github-review-agent index_digest
app_stop "$TAMP_LOOM" "$TMP/data-index"; cp "$TMP/idx.bak" "$TAMP_BW/index.json"

# 10d. restored copy, fresh data dir → both succeed (proves the data dir was
# never the cause of the failures above)
app_start "$TAMP_LOOM" "$TMP/data-restored"; seed_workspace
assert_succeeds epic-runner
assert_succeeds github-review-agent
app_stop "$TAMP_LOOM" "$TMP/data-restored"

# ---------------------------------------------------------------- 11. B4
log "11. B4: artifact dir moved away, LOOM_LOCAL_RUNTIME unset → fail closed"
mv "$TAMP_BW" "$TAMP_BW.moved"
B4="$(sut env LOOM_CONFIG_DIR="$TMP/data-b4" "$TAMP_LOOM" workflow readyz --json)"
record "readyz B4" "$B4"
[[ "$(jq -r .builtin_runtime_ready <<<"$B4")" == false ]] || fail "B4 builtin_runtime_ready should be false"
[[ "$(jq -r .builtin_runtime.fail_closed <<<"$B4")" == true ]] || fail "B4 fail_closed should be true (baked digest)"
for n in epic-runner github-review-agent; do
  ERR="$(jq -r --arg n "$n" '.builtin_runtime.artifacts[$n].error' <<<"$B4")"
  assert_contains "$ERR" "builtin_artifact_missing" "B4 $n error"
  assert_contains "$ERR" "desktop packaging error" "B4 $n error"
done
mv "$TAMP_BW.moved" "$TAMP_BW"

# ---------------------------------------------------------------- 12. PASS
log "12. PASS — observed output: $OBS"
echo PASS
