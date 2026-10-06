#!/usr/bin/env bash
# Opt-in, owned real-OpenCode Agent API AFT tier. No default AFT corpus changes.
set -Eeuo pipefail
TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOURCE_ROOT="$(cd "$TESTS_DIR/../.." && pwd)"
SUITE_DIR="$TESTS_DIR/live-agent-flow-suites"
die() { echo "[aft-agent-flows] $*" >&2; exit 2; }
usage() { die 'usage: run-aft-agent-flows.sh --live --no-agent --real-backend opencode --max-real-cases N [--validate-only]'; }

live=0 no_agent=0 backend='' cap='' validate_only=0
while (($#)); do
  case "$1" in
    --live) ((live+=1)); shift ;;
    --no-agent) ((no_agent+=1)); shift ;;
    --real-backend) (($#>=2)) || usage; backend="$2"; shift 2 ;;
    --max-real-cases) (($#>=2)) || usage; cap="$2"; shift 2 ;;
    --validate-only) ((validate_only+=1)); shift ;;
    *) usage ;;
  esac
done
[[ "$live" == 1 && "$no_agent" == 1 && "$backend" == opencode && "$validate_only" -le 1 ]] || usage
[[ "$cap" =~ ^[1-9][0-9]*$ ]] || usage
((cap <= 10)) || die '--max-real-cases exceeds the absolute ceiling of 10'
[[ -z "${AFT_BASE_URL:-}${AFT_API_URL:-}${AFT_SUITES:-}" ]] || die 'ambient AFT URL or suite override is refused; this runner owns its stack and corpus'
[[ -z "${AFT_REAL_BACKEND:-}" || "$AFT_REAL_BACKEND" == opencode ]] || die 'conflicting ambient real backend'
[[ -z "${AFT_REAL_CODEX:-}" ]] || die 'AFT_REAL_CODEX conflicts with the OpenCode tier'
[[ -d "$SUITE_DIR" ]] || die "missing suite directory: $SUITE_DIR"
shopt -s nullglob
suites=("$SUITE_DIR"/*.test.yaml)
shopt -u nullglob
((${#suites[@]} == 3)) || die "expected exactly three live Agent API suite files; found ${#suites[@]}"
[[ -d "$SUITE_DIR" && ! -L "$SUITE_DIR" ]] || die 'suite directory must not be a symlink'
for suite in "${suites[@]}"; do [[ -f "$suite" && ! -L "$suite" ]] || die "unsafe suite path: $suite"; done

primary_root="$(git -C "$SOURCE_ROOT" worktree list --porcelain | sed -n '1s/^worktree //p')"
[[ -n "$primary_root" ]] || die 'could not locate the shared account lock root'
AFT_DIR="${AFT_DIR:-/Users/tyson/codebase/code-agents/testing-app}"
[[ -f "$AFT_DIR/dist/cli.js" && -f "$AFT_DIR/dist/runner.js" && -d "$AFT_DIR/node_modules" ]] || die "AFT checkout is not built: $AFT_DIR"
command -v node >/dev/null || die 'node is required'
export AFT_BASE_URL=http://127.0.0.1:1 AFT_API_URL=http://127.0.0.1:1 AFT_WS=LOCALMODE
export AFT_AGENT_FLOW_REPO=/workspace/source-repo AFT_REAL_BACKEND=opencode
export AFT_TESTS_DIR="$TESTS_DIR" AFT_WORK_DIR=/private/tmp/aft-agent-flows-validation RUN_ID=validation
export AFT_RESTART_SERVE="$TESTS_DIR/scripts/agent-flows-restart-serve.sh"
export AFT_NATIVE_SESSION_PROBE="$TESTS_DIR/scripts/agent-flows-native-session.sh"
case_count="$(node --input-type=module - "$AFT_DIR/dist/runner.js" "${suites[@]}" <<'NODE'
import {pathToFileURL} from 'node:url';
const [loader, ...files] = process.argv.slice(2);
const {loadSuite} = await import(pathToFileURL(loader).href);
let count = 0;
for (const file of files) {
  const suite = loadSuite(file);
  if (suite.tests.length < 1 || suite.tests.length > 3) throw new Error(`${file}: expected 1-3 cases`);
  count += suite.tests.length;
}
console.log(count);
NODE
)" || die 'suite schema validation failed'
[[ "$case_count" =~ ^[1-9][0-9]*$ ]] || die 'could not count parsed AFT cases'
((case_count <= cap && case_count <= 9)) || die "$case_count selected paid cases exceed cap $cap or the nine-case suite ceiling"
echo "[aft-agent-flows] validated ${#suites[@]} suites and $case_count cases (cap $cap); real OpenCode will consume provider account usage"
((validate_only == 0)) || exit 0

[[ "$SOURCE_ROOT" == /private/tmp/* ]] || die 'run from a separate /private/tmp source worktree'
[[ -z "$(git -C "$SOURCE_ROOT" status --porcelain)" ]] || die 'source worktree must be clean and committed before a live run'
head_sha="$(git -C "$SOURCE_ROOT" rev-parse HEAD)"
for cmd in podman python3 curl jq df shasum; do command -v "$cmd" >/dev/null || die "missing $cmd"; done
podman compose version >/dev/null 2>&1 || die 'podman compose is unavailable'
podman info >/dev/null 2>&1 || die 'podman is unavailable'
available_kib="$(df -Pk /private/tmp | awk 'NR==2 {print $4}')"
if [[ ! "$available_kib" =~ ^[0-9]+$ ]] || ((available_kib < 9*1024*1024)); then
  die 'less than 9 GiB free under /private/tmp'
fi
browser_bin="$(command -v agent-browser)" || die 'agent-browser is required'
fleet_repo="${FLEET_DB_REPO:-/private/tmp/fdb1-fleet}"
[[ -f "$fleet_repo/deploy/docker/Dockerfile" ]] || die "compatible FleetDB checkout missing: $fleet_repo"
[[ "$(git -C "$fleet_repo" rev-parse --is-bare-repository)" == false ]] || die 'FleetDB source must be a worktree; set FLEET_DB_REPO'
[[ -z "$(git -C "$fleet_repo" status --porcelain)" ]] || die 'FleetDB build source must be clean'
fleet_sha="$(git -C "$fleet_repo" rev-parse HEAD)"
export LOCAL_MODE_OPENCODE_DATA="${LOCAL_MODE_OPENCODE_DATA:-$HOME/.local/share/opencode}"
[[ -f "$LOCAL_MODE_OPENCODE_DATA/opencode.db" ]] || die 'host OpenCode login database is missing'

umask 077
run_root="$(mktemp -d /private/tmp/aft-agent-flows.XXXXXXXX)"
run_id="af${run_root##*.}"
project="loom-aft-agents-$run_id"
account_lock="$primary_root/tmp/aft-live.opencode.lock"
mkdir -p "$primary_root/tmp" "$run_root/evidence" "$run_root/bin" "$run_root/profiles" "$run_root/aft-home"
lock_owned=0 stack_attempted=0 build_lock_owned=0
cleanup() {
  status=$?
  trap - EXIT INT TERM
  if ((build_lock_owned)); then rm -f /private/tmp/dryhawk-stack-build.lock/owner; rmdir /private/tmp/dryhawk-stack-build.lock || true; fi
  if ((stack_attempted)); then
    (cd "$SOURCE_ROOT" && make local-mode-agents-down) >> "$run_root/evidence/teardown.log" 2>&1 || status=1
  fi
  for registry in "$run_root"/aft-home/.aft/sessions/aft-*.json; do
    [[ -f "$registry" ]] || continue
    session="$(basename "$registry" .json)"
    [[ "$session" =~ ^aft-[A-Za-z0-9-]+$ ]] || continue
    "$browser_bin" --profile "$run_root/profiles/$session" --session "$session" close >/dev/null 2>&1 || status=1
  done
  if [[ -d "$run_root/profiles" ]]; then rm -rf "$run_root/profiles"; fi
  if [[ -d "$run_root/aft-home" ]]; then rm -rf "$run_root/aft-home"; fi
  if ((lock_owned)) && [[ "$(cat "$account_lock" 2>/dev/null || true)" == "$$" ]]; then rm -f "$account_lock"; fi
  echo "[aft-agent-flows] evidence: $run_root/evidence (exit $status)" >&2
  exit "$status"
}
trap cleanup EXIT INT TERM
if ! (set -o noclobber; printf '%s\n' "$$" > "$account_lock") 2>/dev/null; then
  die "OpenCode account lock exists at $account_lock; do not start a second paid run"
fi
lock_owned=1

ports="$(python3 - <<'PY'
import socket
sockets=[]
for _ in range(3):
    s=socket.socket(); s.bind(('127.0.0.1',0)); sockets.append(s)
print(' '.join(str(s.getsockname()[1]) for s in sockets))
for s in sockets: s.close()
PY
)"
read -r fleet_port api_port ui_port <<< "$ports"
export LOCAL_MODE_COMPOSE_PROJECT="$project" LOCAL_MODE_FLEETDB_PORT="$fleet_port"
export LOCAL_MODE_API_PORT="$api_port" LOCAL_MODE_UI_PORT="$ui_port"
export LOCAL_MODE_AGENTS_REAL=1 LOCAL_MODE_COMPOSE='podman compose'
export LOCAL_MODE_COMPOSE_UP_FLAGS='--build -d' LOCAL_MODE_STATE_DIR="$run_root/state"
export LOCAL_MODE_OPENCODE_COPY="$run_root/state/$project/opencode.db"
export LOCAL_MODE_CODEX_AUTH=/dev/null LOCAL_MODE_CLAUDE_AUTH=/dev/null
export LOCAL_MODE_CLAUDE_TOKEN_FILE=/dev/null
export LOCAL_MODE_LOOM_AGENTS_IMAGE="$project-loom-agents:latest"
export LOCAL_MODE_FLEETDB_IMAGE="$project-fleet-db:latest"
cat > "$run_root/evidence/fleet-override.yml" <<YAML
services:
  fleet-db:
    build:
      context: $fleet_repo
YAML
export LOCAL_MODE_COMPOSE_FILES="$run_root/evidence/fleet-override.yml"
export AFT_OWNED_PROJECT="$project" AFT_SOURCE_ROOT="$SOURCE_ROOT"
export AFT_BASE_URL="http://127.0.0.1:$ui_port" AFT_API_URL="http://127.0.0.1:$api_port"
export AFT_WORK_DIR="$run_root/evidence" RUN_ID="$run_id"
export AFT_BROWSER_BIN="$browser_bin" AFT_BROWSER_PROFILES="$run_root/profiles"
ln -s "$TESTS_DIR/scripts/agent-flows-browser" "$run_root/bin/agent-browser"
export PATH="$run_root/bin:$PATH"
if [[ -n "$(cd "$SOURCE_ROOT" && podman compose -p "$project" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$LOCAL_MODE_COMPOSE_FILES" ps -a -q)" ]]; then
  die "generated project $project already has containers; refusing to attach"
fi

jq -n --arg head "$head_sha" --arg source "$SOURCE_ROOT" --arg fleet "$fleet_repo" --arg fleetSha "$fleet_sha" \
  --arg aft "$AFT_DIR" --arg browser "$browser_bin" --arg project "$project" \
  --arg run "$run_id" --argjson cases "$case_count" --argjson cap "$cap" \
  --argjson fleetPort "$fleet_port" --argjson apiPort "$api_port" --argjson uiPort "$ui_port" \
  --arg aftCliSha "$(shasum -a 256 "$AFT_DIR/dist/cli.js" | awk '{print $1}')" \
  --arg aftLoaderSha "$(shasum -a 256 "$AFT_DIR/dist/runner.js" | awk '{print $1}')" \
  '{source_head:$head,source_root:$source,fleet_source:$fleet,fleet_head:$fleetSha,harness:$aft,aft_cli_sha256:$aftCliSha,aft_loader_sha256:$aftLoaderSha,browser_binary:$browser,run_id:$run,realness:"real OpenCode external model",backend:"opencode",cases:$cases,cap:$cap,owned:{compose_project:$project,ports:[$fleetPort,$apiPort,$uiPort]},evidence:"AFT screenshots every step and all videos"}' \
  > "$run_root/evidence/manifest.json"

if ! mkdir /private/tmp/dryhawk-stack-build.lock 2>/dev/null; then
  die 'shared stack build lock is held; retry when its owner finishes'
fi
build_lock_owned=1
printf 'aft-agent-flows project=%s pid=%s %s\n' "$project" "$$" "$(date -u +%FT%TZ)" > /private/tmp/dryhawk-stack-build.lock/owner
stack_attempted=1
(cd "$SOURCE_ROOT" && make local-mode-agents-up) > "$run_root/evidence/stack-up.log" 2>&1 || die "owned stack failed to start; see $run_root/evidence/stack-up.log"
rm -f /private/tmp/dryhawk-stack-build.lock/owner
rmdir /private/tmp/dryhawk-stack-build.lock
build_lock_owned=0
[[ "$(git -C "$SOURCE_ROOT" rev-parse HEAD)" == "$head_sha" && "$(git -C "$fleet_repo" rev-parse HEAD)" == "$fleet_sha" ]] \
  || die 'build source head changed during stack creation; provenance is invalid'

curl -fsS --max-time 10 "$AFT_API_URL/api/config" > /dev/null || die 'owned API is not ready'
curl -fsS --max-time 10 "$AFT_BASE_URL/" > /dev/null || die 'owned UI is not ready'
catalog="$(curl -fsS --max-time 30 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/harnesses/opencode/models")" || die 'OpenCode model catalog unavailable'
model="${LOCAL_MODE_AGENTS_MODEL:-$(jq -r '[.providers[].models[] | select(.is_default) | .id][0] // empty' <<< "$catalog")}"
[[ -n "$model" && "$model" != aft/* ]] || die 'OpenCode has no selected real model; refusing paid cases'
jq -e --arg model "$model" 'any(.providers[].models[]; .id == $model)' <<< "$catalog" >/dev/null || die "selected OpenCode model $model is unavailable"
jq --arg model "$model" '.model=$model' "$run_root/evidence/manifest.json" > "$run_root/evidence/manifest.tmp"
mv "$run_root/evidence/manifest.tmp" "$run_root/evidence/manifest.json"
for image in "$LOCAL_MODE_LOOM_AGENTS_IMAGE" "$LOCAL_MODE_FLEETDB_IMAGE"; do
  podman image inspect "$image" | jq -e --arg project "$project" '.[0] | select(.Labels["io.loom.local-mode.project"] == $project) | {Id,RepoTags,Labels}' \
    >> "$run_root/evidence/images.jsonl" || die "built image $image lacks owned project provenance"
done

echo "[aft-agent-flows] running $case_count paid cases on owned $project; model $model; screenshots and videos in $run_root/evidence"
HOME="$run_root/aft-home" node "$AFT_DIR/dist/cli.js" run "${suites[@]}" --no-agent --screenshots --record-all \
  --report-dir "$run_root/evidence" --viewport 1920x1080 --timeout 30000
