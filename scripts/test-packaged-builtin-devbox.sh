#!/usr/bin/env bash
# test-packaged-builtin-devbox.sh — DEV-V5-36 DoD-2: prove the packaged
# built-in lane end to end on a dev box, with NO app build:
#
#   1. build the epic-runner Flue dist from the embedded sources (pinned Flue)
#   2. `loom workflow package-builtin` → builtin-workflows/ tree + index_digest
#   3. build a `loom` with the digest baked via -ldflags -X
#   4. lay the tree + a sibling `node` + `fleet-db` next to that binary
#   5. start `loom serve` in desktop mode with NO node on PATH and the Flue
#      compiler pointed at /usr/bin/false
#   6. readyz → builtin_runtime_ready=true, node.source=bundled
#   7. register + run an epic-runner DRY-RUN from the packaged artifact
#   8. versions → manifest.provenance=packaged_builtin, trust=trusted
#   9. serve log → packaged registration line, no compile line
#  10. tamper server.mjs / index.json → POST fails closed (HTTP 500 body
#      names builtin_artifact_invalid + the field; never a 404), restore → OK
#  11. B4: tree moved away + LOOM_LOCAL_RUNTIME unset → still fails closed
#
# Everything observed is appended to $OUT/observed-output.txt.
#
#   usage: scripts/test-packaged-builtin-devbox.sh
#   env:   FLUE_REPO=/path/to/flue at internal/workflows/FLUE_COMMIT (default ../flue)
#          FLEET_DB_BIN=/path/to/fleet-db (default: `command -v fleet-db`)
#          OUT=dir for observed output (default desktop/dist-devbox)
#          KEEP_DEVBOX=1 keeps the temp dir on success
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLUE_REPO="${FLUE_REPO:-$(cd "$ROOT/../flue" 2>/dev/null && pwd || true)}"
FLEET_DB_BIN="${FLEET_DB_BIN:-$(command -v fleet-db || true)}"
OUT="${OUT:-$ROOT/desktop/dist-devbox}"
PORT="${PORT:-18117}"
WS="${WS:-DEVBOX}"
PKG="github.com/tysonthomas9/loomcli/internal/workflows/packaged"

require_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 2; }; }
for c in go jq curl node git; do require_cmd "$c"; done
[[ -n "$FLUE_REPO" && -f "$FLUE_REPO/packages/cli/bin/flue.mjs" ]] || { echo "FLUE_REPO not found; set FLUE_REPO=/path/to/flue (built, at the pin)" >&2; exit 2; }
[[ -n "$FLEET_DB_BIN" && -x "$FLEET_DB_BIN" ]] || { echo "fleet-db binary not found; set FLEET_DB_BIN" >&2; exit 2; }
node -e 'const [M,m]=process.versions.node.split(".").map(Number); process.exit((M>22||(M===22&&m>=18))?0:1)' \
  || { echo "host node $(node --version) < 22.18" >&2; exit 2; }

TMP="$(mktemp -d -t loom-packaged-devbox.XXXXXX)"
mkdir -p "$OUT" "$TMP/bin" "$TMP/home" "$TMP/t"
OBS="$OUT/observed-output.txt"
: > "$OBS"
SERVE_LOG="$OUT/serve.log"
: > "$SERVE_LOG"

PIDS=()
cleanup() {
  local status=$?
  for pid in "${PIDS[@]:-}"; do
    [[ -n "$pid" ]] && kill "$pid" >/dev/null 2>&1 || true
  done
  if [[ $status -ne 0 ]]; then
    echo "FAIL (exit $status); observed output: $OBS; temp: $TMP" >&2
  elif [[ "${KEEP_DEVBOX:-0}" != "1" ]]; then
    rm -rf "$TMP"
  fi
}
trap cleanup EXIT

log() { printf '==> %s\n' "$*" | tee -a "$OBS"; }
record() { printf -- '--- %s ---\n%s\n' "$1" "$2" >> "$OBS"; }
fail() { echo "ASSERTION FAILED: $*" | tee -a "$OBS" >&2; exit 1; }
assert_contains() { grep -qF -- "$2" <<<"$1" || fail "$3: expected to contain '$2'; got: $(head -c 600 <<<"$1")"; }
assert_not_contains() { ! grep -qF -- "$2" <<<"$1" || fail "$3: must NOT contain '$2'"; }

# ---------------------------------------------------------------- 1. dist
log "1. building epic-runner dist via scripts/rebuild-builtin-bundle.sh (FLUE_REPO=$FLUE_REPO)"
FLUE_REPO="$FLUE_REPO" BUILTIN_DIST_DEST="$TMP/epic-runner-dist" "$ROOT/scripts/rebuild-builtin-bundle.sh" >"$OUT/rebuild.log" 2>&1 \
  || { tail -40 "$OUT/rebuild.log" >&2; fail "rebuild-builtin-bundle.sh failed"; }
[[ -f "$TMP/epic-runner-dist/server.mjs" ]] || fail "no server.mjs in built dist"

# ---------------------------------------------------------- 2. package-builtin
log "2. loom workflow package-builtin epic-runner --require-all --json"
PKG_JSON="$(cd "$ROOT" && go run ./cmd/loom workflow package-builtin epic-runner --dist "$TMP/epic-runner-dist" --out "$TMP/bw" --require-all --json)"
record "package-builtin" "$PKG_JSON"
tee "$OUT/package.json" <<<"$PKG_JSON" >/dev/null
INDEX_DIGEST="$(jq -r .index_digest <<<"$PKG_JSON")"
[[ "$INDEX_DIGEST" == sha256:* ]] || fail "index_digest missing"
[[ "$(jq -r '.audit.native_files|length' <<<"$PKG_JSON")" == 0 ]] || fail "audit.native_files not empty"
[[ "$(jq -r '.audit.bare_specifiers|length' <<<"$PKG_JSON")" == 0 ]] || fail "audit.bare_specifiers not empty"
[[ "$(jq -r '.audit.dlopen' <<<"$PKG_JSON")" == false ]] || fail "audit.dlopen true"
[[ "$(jq -r '.audit.symlinks|length' <<<"$PKG_JSON")" == 0 ]] || fail "audit.symlinks not empty"
[[ -f "$TMP/bw/index.json" && -f "$TMP/bw/epic-runner/dist/server.mjs" && -f "$TMP/bw/epic-runner/dist/node_modules/@loom/sdk/driver.js" ]] || fail "packaged tree incomplete"
[[ -z "$(find "$TMP/bw" \( -name '*.node' -o -type l \) -print)" ]] || fail "packaged tree contains .node files or symlinks"
PKG_JSON2="$(cd "$ROOT" && go run ./cmd/loom workflow package-builtin epic-runner --dist "$TMP/epic-runner-dist" --out "$TMP/bw2" --require-all --json)"
cmp -s "$TMP/bw/index.json" "$TMP/bw2/index.json" || fail "re-packaging is not byte-identical"
[[ "$(jq -r .index_digest <<<"$PKG_JSON2")" == "$INDEX_DIGEST" ]] || fail "re-packaging changed index_digest"
record "server.mjs externals" "$(grep -c '"@loom/sdk' "$TMP/bw/epic-runner/dist/server.mjs") @loom/sdk refs; $(grep -cE 'from "(@daytona/sdk|@flue/runtime|hono|node-liblzma|@mongodb-js/zstd)"' "$TMP/bw/epic-runner/dist/server.mjs" || true) forbidden static externals"
[[ "$(grep -cE 'from "(@daytona/sdk|@flue/runtime|hono|node-liblzma|@mongodb-js/zstd)"' "$TMP/bw/epic-runner/dist/server.mjs" || true)" == 0 ]] || fail "server.mjs still imports a forbidden external"

# ------------------------------------------------------- 3./4. packaged loom
log "3. building loom with -X $PKG.ExpectedIndexDigest=$INDEX_DIGEST"
(cd "$ROOT" && go build -ldflags "-X $PKG.ExpectedIndexDigest=$INDEX_DIGEST" -o "$TMP/bin/loom" ./cmd/loom)
log "4. laying out builtin-workflows/, sibling node, fleet-db next to the binary"
cp -R "$TMP/bw" "$TMP/bin/builtin-workflows"
ln -s "$(command -v node)" "$TMP/bin/node"
ln -s "$FLEET_DB_BIN" "$TMP/bin/fleet-db"

# A desktop-shaped env: no node on PATH, compiler = /usr/bin/false.
CLEAN_PATH="/usr/bin:/bin:/usr/sbin:/sbin"
[[ -z "$(env -i PATH="$CLEAN_PATH" /bin/sh -c 'command -v node' 2>/dev/null)" ]] || fail "a node is reachable on the clean PATH ($CLEAN_PATH)"
run_env() { # run_env <data-dir> <LOOM_LOCAL_RUNTIME> <cmd...>
  local data="$1" mode="$2"; shift 2
  env -i HOME="$TMP/home" TMPDIR="$TMP/t" PATH="$CLEAN_PATH" USER=devbox \
    LOOM_CONFIG_DIR="$data" LOOM_LOCAL_RUNTIME="$mode" LOOM_ISSUE_BACKEND=fleetdb \
    LOOM_WORKSPACE_RUNTIME_DIR="$data" LOOM_WEBUI_URL="http://127.0.0.1:$PORT" \
    LOOM_SERVER_URL= LOOM_WORKSPACE= LOOM_FLEET_DB_URL= LOOM_FLEET_URL= \
    FLEET_DB_BIN="$TMP/bin/fleet-db" LOOM_REAL_FLUE_CMD=/usr/bin/false LOOM_REAL_FLUE_CMD_JSON= \
    LOOM_SDK_ROOT= FLUE_REPO= "$@"
}

SERVE_PID=""
start_serve() { # start_serve <data-dir>
  local data="$1"
  mkdir -p "$data"
  # The desktop launcher runs serve with cwd = data dir (internal/cli/local
  # local_cmd.go: serveCmd.Dir = cfg.dataDir); the driver executor resolves
  # bundle roots against cwd, so mirror that here.
  (cd "$data" && exec env -i HOME="$TMP/home" TMPDIR="$TMP/t" PATH="$CLEAN_PATH" USER=devbox \
    LOOM_CONFIG_DIR="$data" LOOM_LOCAL_RUNTIME=desktop LOOM_ISSUE_BACKEND=fleetdb \
    LOOM_WORKSPACE_RUNTIME_DIR="$data" LOOM_WEBUI_URL="http://127.0.0.1:$PORT" \
    LOOM_SERVER_URL= LOOM_WORKSPACE= LOOM_FLEET_DB_URL= LOOM_FLEET_URL= \
    FLEET_DB_BIN="$TMP/bin/fleet-db" LOOM_REAL_FLUE_CMD=/usr/bin/false LOOM_REAL_FLUE_CMD_JSON= \
    LOOM_SDK_ROOT= FLUE_REPO= "$TMP/bin/loom" serve --port "$PORT") >>"$SERVE_LOG" 2>&1 &
  SERVE_PID=$!
  PIDS+=("$SERVE_PID")
  for _ in $(seq 1 240); do
    curl -fsS "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && return 0
    kill -0 "$SERVE_PID" 2>/dev/null || { tail -40 "$SERVE_LOG" >&2; fail "loom serve exited early"; }
    sleep 0.25
  done
  tail -40 "$SERVE_LOG" >&2
  fail "loom serve did not become healthy"
}
stop_serve() {
  [[ -n "$SERVE_PID" ]] || return 0
  kill "$SERVE_PID" >/dev/null 2>&1 || true
  wait "$SERVE_PID" 2>/dev/null || true
  SERVE_PID=""
}

api() { # api <method> <path> [body]
  if [[ -n "${3:-}" ]]; then
    curl -sS -X "$1" -H 'Content-Type: application/json' --data "$3" "http://127.0.0.1:$PORT$2"
  else
    curl -sS -X "$1" "http://127.0.0.1:$PORT$2"
  fi
}
api_status() { # api_status <method> <path> <body> → "<code>\n<body>"
  curl -sS -o "$TMP/body" -w '%{http_code}' -X "$1" -H 'Content-Type: application/json' --data "$3" "http://127.0.0.1:$PORT$2"
  printf '\n'
  cat "$TMP/body"
}

seed_workspace() { # seed_workspace → sets WS_KEY and EPIC
  local ws_json
  ws_json="$(api POST /api/workspaces "$(jq -nc --arg n "$WS" '{name:$n,type:"empty",repos:[]}')")"
  WS_KEY="$(jq -r '.data.id // .data.key // empty' <<<"$ws_json")"
  [[ -n "$WS_KEY" ]] || fail "workspace create: $ws_json"
  EPIC="$(api POST "/api/workspaces/$WS_KEY/issues" '{"title":"devbox epic","issue_type":"epic","priority":1}' | jq -r '.data.id // empty')"
  [[ -n "$EPIC" ]] || fail "epic create failed"
  api POST "/api/workspaces/$WS_KEY/issues" "$(jq -nc --arg p "$EPIC" '{title:"devbox task",issue_type:"task",priority:1,parent:$p}')" >/dev/null
}

post_run() { # post_run <epic> → "<code>\n<body>"
  api_status POST "/api/workspaces/$WS_KEY/workflows/epic-runner" \
    "$(jq -nc --arg e "$1" '{epicId:$e,dryRun:true,runner:"daytona-task-runner",requestedBy:"devbox-script"}')"
}

wait_run() { # wait_run <run-id> → run json (terminal)
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

# ------------------------------------------------------------- 5./6. serve
DATA="$TMP/data1"
log "5. starting loom serve (desktop mode, no PATH node, LOOM_REAL_FLUE_CMD=/usr/bin/false)"
start_serve "$DATA"

log "6. loom workflow readyz --json"
READYZ="$(run_env "$DATA" desktop "$TMP/bin/loom" workflow readyz --json)"
record "readyz" "$READYZ"
[[ "$(jq -r .builtin_runtime_ready <<<"$READYZ")" == true ]] || fail "builtin_runtime_ready != true"
[[ "$(jq -r .builtin_runtime.node.source <<<"$READYZ")" == bundled ]] || fail "builtin_runtime.node.source != bundled"
[[ "$(jq -r '.builtin_runtime.artifacts."epic-runner".verified' <<<"$READYZ")" == true ]] || fail "epic-runner not verified"
[[ "$(jq -r '.builtin_runtime.artifacts."github-review-agent".required' <<<"$READYZ")" == false ]] || fail "github-review-agent must be required=false"
[[ "$(jq -r .authoring_ready <<<"$READYZ")" == false ]] || fail "authoring_ready should be false (no toolchain)"
[[ "$(jq -r .ok <<<"$READYZ")" == false ]] || fail "ok should equal authoring_ready=false"

# ---------------------------------------------------------- 7. dry-run
log "7. workspace + epic, then POST workflows/epic-runner (dryRun)"
seed_workspace
RESP="$(post_run "$EPIC")"
CODE="${RESP%%$'\n'*}"; BODY="${RESP#*$'\n'}"
record "POST epic-runner (packaged)" "HTTP $CODE $BODY"
[[ "$CODE" == 202 ]] || fail "POST epic-runner: HTTP $CODE $BODY"
RUN_ID="$(jq -r .run_id <<<"$BODY")"
RUN="$(wait_run "$RUN_ID")"
record "DriverRun $RUN_ID" "$RUN"
[[ "$(jq -r .status <<<"$RUN")" == completed ]] || fail "run status: $(jq -r .status <<<"$RUN") summary: $(jq -r .summary <<<"$RUN")"
assert_contains "$(jq -r .summary <<<"$RUN")" "Dry-run validated epic" "run summary"

# ---------------------------------------------------------- 8. versions
log "8. loom workflow versions epic-runner --json"
VERSIONS="$(run_env "$DATA" desktop env LOOM_WORKSPACE="$WS_KEY" "$TMP/bin/loom" workflow versions epic-runner --json)"
record "versions" "$VERSIONS"
[[ "$(jq -r '.versions[] | select(.active) | .version.manifest.provenance' <<<"$VERSIONS")" == packaged_builtin ]] || fail "active version provenance != packaged_builtin"
[[ "$(jq -r '.versions[] | select(.active) | .version.manifest.trust_level' <<<"$VERSIONS")" == trusted ]] || fail "active version trust_level != trusted"
[[ "$(jq -r '.versions[] | select(.active) | .version.manifest.packaged_index_digest' <<<"$VERSIONS")" == "$INDEX_DIGEST" ]] || fail "packaged_index_digest mismatch"

# ---------------------------------------------------------- 9. serve log
log "9. serve log assertions"
stop_serve
SERVE_TEXT="$(cat "$SERVE_LOG")"
assert_contains "$SERVE_TEXT" "builtin workflow registered from packaged artifact" "serve log"
assert_contains "$SERVE_TEXT" "node runtime resolved" "serve log"
assert_contains "$SERVE_TEXT" "source=bundled" "serve log"
assert_contains "$SERVE_TEXT" "path=$TMP/bin/node" "serve log"
for bad in "flue build" "workflow-builds" "local @loom/sdk" "LOOM_SDK_ROOT"; do assert_not_contains "$SERVE_TEXT" "$bad" "serve log"; done
record "serve log (phase 1, grep)" "$(grep -E 'packaged artifact|node runtime resolved' "$SERVE_LOG" || true)"

# ---------------------------------------------------------- 10. tamper
tamper_case() { # tamper_case <label> <file> <want-field>
  local label="$1" file="$2" field="$3" backup="$TMP/backup-$RANDOM"
  log "10. tamper $label → expect builtin_artifact_invalid/$field, then restore"
  cp "$file" "$backup"
  printf 'x' | dd of="$file" bs=1 seek=$(( $(wc -c <"$file") - 2 )) conv=notrunc 2>/dev/null
  local data="$TMP/data-$label"
  : > "$SERVE_LOG.tmp"; start_serve "$data"
  seed_workspace
  local resp code body
  resp="$(post_run "$EPIC")"; code="${resp%%$'\n'*}"; body="${resp#*$'\n'}"
  record "POST epic-runner (tampered $label)" "HTTP $code $body"
  # VerificationError wraps domain.ErrInvalid (DEV-V5-31 §3c) → 400; the one
  # thing it must never be is a 404 "workflow not found" that hides the cause.
  [[ "$code" =~ ^4|^5 && "$code" != 404 ]] || fail "tampered $label: want non-2xx and never 404, got $code $body"
  assert_contains "$body" "builtin_artifact_invalid" "tampered $label body"
  assert_contains "$body" "$field" "tampered $label body"
  assert_contains "$body" "reinstall Loom" "tampered $label body"
  stop_serve
  cp "$backup" "$file"
  data="$TMP/data-$label-restored"
  start_serve "$data"
  seed_workspace
  resp="$(post_run "$EPIC")"; code="${resp%%$'\n'*}"; body="${resp#*$'\n'}"
  record "POST epic-runner (restored $label)" "HTTP $code $body"
  [[ "$code" == 202 ]] || fail "restored $label: HTTP $code $body"
  [[ "$(wait_run "$(jq -r .run_id <<<"$body")" | jq -r .status)" == completed ]] || fail "restored $label run did not complete"
  stop_serve
}
tamper_case server-mjs "$TMP/bin/builtin-workflows/epic-runner/dist/server.mjs" artifact_digest
tamper_case index-json "$TMP/bin/builtin-workflows/index.json" index_digest

# ---------------------------------------------------------- 11. B4
log "11. B4: tree moved away, LOOM_LOCAL_RUNTIME unset → readyz fails closed"
mv "$TMP/bin/builtin-workflows" "$TMP/bin/builtin-workflows.moved"
B4="$(run_env "$TMP/data-b4" "" "$TMP/bin/loom" workflow readyz --json)"
record "readyz B4" "$B4"
[[ "$(jq -r .builtin_runtime_ready <<<"$B4")" == false ]] || fail "B4 builtin_runtime_ready should be false"
assert_contains "$(jq -r '.builtin_runtime.artifacts."epic-runner".error' <<<"$B4")" "builtin_artifact_missing" "B4 error"
assert_contains "$(jq -r '.builtin_runtime.artifacts."epic-runner".error' <<<"$B4")" "desktop packaging error" "B4 error"
[[ "$(jq -r .builtin_runtime.fail_closed <<<"$B4")" == true ]] || fail "B4 fail_closed should be true (baked digest)"

log "11b. missing artifact: POST epic-runner → HTTP 500 with 'reinstall Loom' (never a 404)"
start_serve "$TMP/data-missing"
seed_workspace
RESP="$(post_run "$EPIC")"; CODE="${RESP%%$'\n'*}"; BODY="${RESP#*$'\n'}"
record "POST epic-runner (artifact missing)" "HTTP $CODE $BODY"
[[ "$CODE" == 500 ]] || fail "missing artifact: want HTTP 500 (plain ErrNotPackaged sentinel, never 404), got $CODE $BODY"
assert_contains "$BODY" "builtin_artifact_missing" "missing artifact body"
assert_contains "$BODY" "reinstall Loom" "missing artifact body"
assert_not_contains "$BODY" "LOOM_SDK_ROOT" "missing artifact body"
assert_not_contains "$BODY" "local @loom/sdk" "missing artifact body"
stop_serve
mv "$TMP/bin/builtin-workflows.moved" "$TMP/bin/builtin-workflows"

grep -qE 'flue build|workflow-builds|local @loom/sdk|LOOM_SDK_ROOT' "$SERVE_LOG" && fail "serve log mentions the compile lane" || true
log "PASS — observed output: $OBS"
echo PASS
