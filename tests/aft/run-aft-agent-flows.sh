#!/usr/bin/env bash
# Opt-in, owned real-OpenCode Agent API AFT tier. No default AFT corpus changes.
set -Eeuo pipefail
TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOURCE_ROOT="$(cd "$TESTS_DIR/../.." && pwd)"
die() { echo "[aft-agent-flows] $*" >&2; exit 2; }
usage() { die 'usage: run-aft-agent-flows.sh --live --no-agent --real-backend opencode --max-real-cases N [--coverage-batch NAME] [--validate-only]'; }

live=0 no_agent=0 backend='' cap='' validate_only=0 coverage_batch=default batch_flags=0
while (($#)); do
  case "$1" in
    --live) ((live+=1)); shift ;;
    --no-agent) ((no_agent+=1)); shift ;;
    --real-backend) (($#>=2)) || usage; backend="$2"; shift 2 ;;
    --max-real-cases) (($#>=2)) || usage; cap="$2"; shift 2 ;;
    --coverage-batch) (($#>=2)) || usage; coverage_batch="$2"; ((batch_flags+=1)); shift 2 ;;
    --validate-only) ((validate_only+=1)); shift ;;
    *) usage ;;
  esac
done
[[ "$live" == 1 && "$no_agent" == 1 && "$backend" == opencode && "$validate_only" -le 1 && "$batch_flags" -le 1 ]] || usage
[[ "$cap" =~ ^([1-9]|10)$ ]] || die '--max-real-cases must be an integer from 1 to 10'
[[ -z "${AFT_BASE_URL:-}${AFT_API_URL:-}${AFT_SUITES:-}" ]] || die 'ambient AFT URL or suite override is refused; this runner owns its stack and corpus'
[[ -z "${AFT_AGENT_FLOW_REPO:-}" ]] || die 'ambient Agent flow repository override is refused'
[[ -z "${AFT_REAL_BACKEND:-}" || "$AFT_REAL_BACKEND" == opencode ]] || die 'conflicting ambient real backend'
[[ -z "${AFT_REAL_CODEX:-}" ]] || die 'AFT_REAL_CODEX conflicts with the OpenCode tier'
real_model="${AFT_REAL_MODEL:-openai/gpt-5.5}"
[[ "$real_model" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ && "$real_model" != aft/* ]] || die 'invalid real model ID'
export AFT_REAL_MODEL="$real_model"
AFT_DIR="${AFT_DIR:-/Users/tyson/codebase/code-agents/testing-app}"
[[ -f "$AFT_DIR/dist/cli.js" && -f "$AFT_DIR/dist/runner.js" && -d "$AFT_DIR/node_modules" ]] || die "AFT checkout is not built: $AFT_DIR"
command -v node >/dev/null || die 'node is required'
command -v jq >/dev/null || die 'jq is required'
export AFT_BASE_URL=http://127.0.0.1:1 AFT_API_URL=http://127.0.0.1:1 AFT_WS=LOCALMODE
seed_repo=/workspace/source-repo
export AFT_REAL_BACKEND=opencode
export AFT_TESTS_DIR="$TESTS_DIR" AFT_WORK_DIR=/private/tmp/aft-agent-flows-validation RUN_ID=validation
export AFT_RESTART_SERVE="$TESTS_DIR/scripts/agent-flows-restart-serve.sh"
export AFT_NATIVE_SESSION_PROBE="$TESTS_DIR/scripts/agent-flows-native-session.sh"
export AFT_NATIVE_MODEL_PROBE="$TESTS_DIR/scripts/agent-flows-native-model.sh"
export AFT_SELECT_AGENT_MODEL="$TESTS_DIR/scripts/agent-flows-select-model.sh"
selection="$(node "$TESTS_DIR/scripts/agent-flows-selection.mjs" "$TESTS_DIR" "$AFT_DIR/dist/runner.js" "$coverage_batch")" \
  || die 'suite selection or schema validation failed'
case_count="$(jq -r '.count' <<< "$selection")" || die 'could not read selected case count'
[[ "$case_count" =~ ^[1-9][0-9]*$ ]] || die 'could not count parsed AFT cases'
((case_count <= cap && case_count <= 10)) || die "$case_count selected paid cases exceed cap $cap or the absolute ten-case ceiling"
suites=()
while IFS= read -r suite; do suites+=("$suite"); done < <(jq -r '.suites[].path' <<< "$selection")
if ((validate_only)); then
  echo "[aft-agent-flows] offline loader validation: batch $coverage_batch, ${#suites[@]} suites, $case_count cases (cap $cap); no stack or provider actions" >&2
  jq -c --arg source "$(git -C "$SOURCE_ROOT" rev-parse HEAD)" '. + {source_head:$source}' <<< "$selection"
  exit 0
fi
echo "[aft-agent-flows] validated batch $coverage_batch: ${#suites[@]} suites, $case_count cases (cap $cap); real OpenCode will consume provider account usage"

primary_root="$(git -C "$SOURCE_ROOT" worktree list --porcelain | sed -n '1s/^worktree //p' | tr -d '\r')"
[[ -n "$primary_root" ]] || die 'could not locate the shared account lock root'

[[ "$SOURCE_ROOT" == /private/tmp/* ]] || die 'run from a separate /private/tmp source worktree'
[[ -z "$(git -C "$SOURCE_ROOT" status --porcelain)" ]] || die 'source worktree must be clean and committed before a live run'
head_sha="$(git -C "$SOURCE_ROOT" rev-parse HEAD | tr -d '\r')"
for cmd in podman python3 curl jq df shasum rg; do command -v "$cmd" >/dev/null || die "missing $cmd"; done
[[ -z "${AFT_PODMAN_HOME:-}${AFT_PODMAN_CONNECTION:-}${CONTAINER_HOST:-}${DOCKER_HOST:-}" ]] \
  || die 'ambient Podman helper home/connection or host override is refused'
host_podman_home="$HOME"
[[ "$host_podman_home" == /* && -d "$host_podman_home" && ! -L "$host_podman_home" ]] \
  || die 'host Podman HOME must be an owned absolute directory'
connections="$(podman system connection list --format json)" || die 'host Podman connection registry unavailable'
podman_connection="${CONTAINER_CONNECTION:-$(jq -r '[.[] | select(.Default == true) | .Name] | if length == 1 then .[0] else empty end' <<< "$connections")}"
[[ "$podman_connection" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || die 'no unambiguous host Podman connection'
connection_record="$(jq -cS --arg name "$podman_connection" \
  '[.[] | select(.Name == $name) | {Name,URI,Identity}] | if length == 1 then .[0] else error("selected connection missing") end' \
  <<< "$connections")" || die 'selected host Podman connection is not registered'
connection_fingerprint="$(printf '%s' "$connection_record" | shasum -a 256 | awk '{print $1}')"
export AFT_PODMAN_HOME="$host_podman_home" AFT_PODMAN_CONNECTION="$podman_connection"
export CONTAINER_CONNECTION="$podman_connection"
podman compose version >/dev/null 2>&1 || die 'podman compose is unavailable'
podman --connection "$podman_connection" info >/dev/null 2>&1 || die 'selected host Podman connection is unavailable'
available_kib="$(df -Pk /private/tmp | awk 'NR==2 {print $4}' | tr -d '\r')"
if [[ ! "$available_kib" =~ ^[0-9]+$ ]] || ((available_kib < 9*1024*1024)); then
  die 'less than 9 GiB free under /private/tmp'
fi
browser_bin="$(command -v agent-browser)" || die 'agent-browser is required'
browser_bin="${browser_bin//$'\r'/}"
fleet_repo="${FLEET_DB_REPO:-/private/tmp/fdb1-fleet}"
[[ -f "$fleet_repo/deploy/docker/Dockerfile" ]] || die "compatible FleetDB checkout missing: $fleet_repo"
[[ "$(git -C "$fleet_repo" rev-parse --is-bare-repository | tr -d '\r')" == false ]] || die 'FleetDB source must be a worktree; set FLEET_DB_REPO'
[[ -z "$(git -C "$fleet_repo" status --porcelain)" ]] || die 'FleetDB build source must be clean'
fleet_sha="$(git -C "$fleet_repo" rev-parse HEAD | tr -d '\r')"
export LOCAL_MODE_OPENCODE_DATA="${LOCAL_MODE_OPENCODE_DATA:-$HOME/.local/share/opencode}"
[[ -f "$LOCAL_MODE_OPENCODE_DATA/opencode.db" ]] || die 'host OpenCode login database is missing'

umask 077
run_root="$(mktemp -d /private/tmp/aft-agent-flows.XXXXXXXX | tr -d '\r')"
run_id="af$(printf '%s' "${run_root##*.}" | tr '[:upper:]' '[:lower:]')"
project="loom-aft-agents-$run_id"
account_lock="$primary_root/tmp/aft-live.opencode.lock"
mkdir -p "$primary_root/tmp" "$run_root/evidence" "$run_root/bin" "$run_root/profiles" "$run_root/aft-home"
lock_owned=0 stack_attempted=0 build_lock_owned=0
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$TESTS_DIR/scripts/agent-flows-ownership.sh"
# shellcheck disable=SC2329 # Invoked by the EXIT/INT/TERM cleanup trap.
cleanup_owns_stack() {
  agent_flows_check_manifest || return 1
  local container
  owned_containers="$(cd "$SOURCE_ROOT" && podman compose -p "$AFT_OWNED_PROJECT" \
    -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml \
    -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml" ps -a -q | tr -d '\r')" || return 1
  for container in $owned_containers; do
    podman inspect "$container" | jq -e --arg project "$AFT_OWNED_PROJECT" '
      .[0].Config.Labels as $labels |
      $labels["com.docker.compose.project"] == $project and
      (["redis","fleet-db","loom-local","ui-local"] | index($labels["com.docker.compose.service"])) != null' >/dev/null \
      || return 1
  done
}
# shellcheck disable=SC2329 # EXIT/INT/TERM trap invokes this function.
cleanup() {
  status=$?
  trap - EXIT INT TERM
  if ((build_lock_owned)); then rm -f /private/tmp/dryhawk-stack-build.lock/owner; rmdir /private/tmp/dryhawk-stack-build.lock || true; fi
  if ((stack_attempted)); then
    if cleanup_owns_stack; then
      if ! (cd "$SOURCE_ROOT" && make local-mode-agents-down) >> "$run_root/evidence/teardown.log" 2>&1; then
        status=1
        if [[ -z "$owned_containers" && "$LOCAL_MODE_OPENCODE_COPY" == "$run_root/state/$project/opencode.db" ]]; then
          (cd "$SOURCE_ROOT" && test/local-mode/real-opencode-copy.sh remove "$LOCAL_MODE_OPENCODE_COPY") \
            >> "$run_root/evidence/teardown.log" 2>&1 || status=1
        fi
      fi
    else
      echo 'owned stack check failed; teardown skipped' >> "$run_root/evidence/teardown.log"
      status=1
    fi
  fi
  for registry in "$run_root"/aft-home/.aft/sessions/aft-*.json; do
    [[ -f "$registry" ]] || continue
    session="$(basename "$registry" .json | tr -d '\r')"
    [[ "$session" =~ ^aft-[A-Za-z0-9-]+$ ]] || continue
    "$browser_bin" --profile "$run_root/profiles/$session" --session "$session" close >/dev/null 2>&1 || status=1
  done
  if [[ -d "$run_root/profiles" ]]; then rm -rf "$run_root/profiles"; fi
  if [[ -d "$run_root/aft-home" ]]; then rm -rf "$run_root/aft-home"; fi
  if ((lock_owned)) && [[ "$(cat "$account_lock" 2>/dev/null | tr -d '\r' || true)" == "$$" ]]; then rm -f "$account_lock"; fi
  echo "[aft-agent-flows] evidence: $run_root/evidence (exit $status)" >&2
  exit "$status"
}
trap cleanup EXIT INT TERM
if ! (set -o noclobber; printf '%s\n' "$$" > "$account_lock") 2>/dev/null; then
  die "OpenCode account lock exists at $account_lock; do not start a second paid run"
fi
lock_owned=1

ports="$(python3 - <<'PY' | tr -d '\r'
import socket
sockets=[]
for _ in range(3):
    s=socket.socket(); s.bind(('127.0.0.1',0)); sockets.append(s)
print(' '.join(str(s.getsockname()[1]) for s in sockets))
for s in sockets: s.close()
PY
)"
[[ "$ports" =~ ^[0-9]+\ [0-9]+\ [0-9]+$ ]] || die 'could not parse owned port allocation'
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
if ! existing_containers="$(cd "$SOURCE_ROOT" && podman compose -p "$project" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$LOCAL_MODE_COMPOSE_FILES" ps -a -q | tr -d '\r')"; then
  die "compose preflight failed for $project before auth setup"
fi
if [[ -n "$existing_containers" ]]; then
  die "generated project $project already has containers; refusing to attach"
fi

jq -n --arg head "$head_sha" --arg source "$SOURCE_ROOT" --arg fleet "$fleet_repo" --arg fleetSha "$fleet_sha" \
  --arg aft "$AFT_DIR" --arg browser "$browser_bin" --arg project "$project" \
  --arg apiUrl "$AFT_API_URL" --arg uiUrl "$AFT_BASE_URL" --arg evidence "$AFT_WORK_DIR" \
  --arg podmanHome "$AFT_PODMAN_HOME" --arg podmanConnection "$AFT_PODMAN_CONNECTION" \
  --arg podmanFingerprint "$connection_fingerprint" \
  --arg run "$run_id" --argjson cases "$case_count" --argjson cap "$cap" --argjson selection "$selection" \
  --argjson fleetPort "$fleet_port" --argjson apiPort "$api_port" --argjson uiPort "$ui_port" \
  --arg aftCliSha "$(shasum -a 256 "$AFT_DIR/dist/cli.js" | awk '{print $1}')" \
  --arg aftLoaderSha "$(shasum -a 256 "$AFT_DIR/dist/runner.js" | awk '{print $1}')" \
  '{source_head:$head,source_root:$source,fleet_source:$fleet,fleet_head:$fleetSha,harness:$aft,aft_cli_sha256:$aftCliSha,aft_loader_sha256:$aftLoaderSha,browser_binary:$browser,run_id:$run,realness:"real OpenCode external model",backend:"opencode",cases:$cases,cap:$cap,selection:$selection,owned:{compose_project:$project,evidence_dir:$evidence,api_url:$apiUrl,ui_url:$uiUrl,ports:[$fleetPort,$apiPort,$uiPort],podman_home:$podmanHome,podman_connection:$podmanConnection,podman_connection_fingerprint:$podmanFingerprint},evidence:"AFT screenshots every step and all videos"}' \
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
[[ "$(git -C "$SOURCE_ROOT" rev-parse HEAD | tr -d '\r')" == "$head_sha" && "$(git -C "$fleet_repo" rev-parse HEAD | tr -d '\r')" == "$fleet_sha" ]] \
  || die 'build source head changed during stack creation; provenance is invalid'

curl -fsS --max-time 10 "$AFT_API_URL/api/config" > /dev/null || die 'owned API is not ready'
curl -fsS --max-time 10 "$AFT_BASE_URL/" > /dev/null || die 'owned UI is not ready'
workspace_json="$(curl -fsS --max-time 30 "$AFT_API_URL/api/workspaces/LOCALMODE")" \
  || die 'owned LOCALMODE workspace registry unavailable'
jq '{workspace_id:.data.id,workspace_path:.data.path,repos:[.data.repos[]? | {name,path}]}' \
  <<< "$workspace_json" > "$run_root/evidence/workspace-repo.json" || die 'workspace registry metadata is invalid'
jq -e '.workspace_id == "LOCALMODE" and .workspace_path == "/root/.loom/workspaces/LOCALMODE" and
  (.repos | length) == 1 and .repos[0].name == "source-repo" and
  .repos[0].path == (.workspace_path + "/source-repo")' \
  "$run_root/evidence/workspace-repo.json" >/dev/null || die 'owned LOCALMODE source-repo import is not registered at its managed path'
export AFT_AGENT_FLOW_REPO
AFT_AGENT_FLOW_REPO="$(jq -r '.repos[0].path' "$run_root/evidence/workspace-repo.json" | tr -d '\r')"
jq --arg seed "$seed_repo" --arg managed "$AFT_AGENT_FLOW_REPO" \
  '.fixture_repo={seed_path:$seed,managed_path:$managed}' \
  "$run_root/evidence/manifest.json" > "$run_root/evidence/manifest.tmp"
mv "$run_root/evidence/manifest.tmp" "$run_root/evidence/manifest.json"
catalog="$(curl -fsS --max-time 30 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/harnesses/opencode/models")" || die 'OpenCode model catalog unavailable'
model="${LOCAL_MODE_AGENTS_MODEL:-$(jq -r '[.providers[].models[] | select(.is_default) | .id][0] // empty' <<< "$catalog")}"
model="${model//$'\r'/}"
[[ -n "$model" && "$model" != aft/* ]] || die 'OpenCode has no selected real model; refusing paid cases'
jq -e --arg model "$model" 'any(.providers[].models[]; .id == $model)' <<< "$catalog" >/dev/null || die "selected OpenCode model $model is unavailable"
jq -e --arg model "$real_model" 'any(.providers[].models[]; .id == $model)' <<< "$catalog" >/dev/null \
  || die "required real UI model $real_model is absent from the owned OpenCode catalog"
catalog_default="$(jq -r '[.providers[].models[] | select(.is_default) | .id][0] // empty' <<< "$catalog" | tr -d '\r')"
alternate="$(jq -r --arg target "$real_model" --arg provider "${real_model%%/*}" \
  '[.providers[].models[].id | select(. != $target and (startswith("aft/") | not))] as $others |
   ([$others[] | select(startswith($provider + "/"))][0] // $others[0] // empty)' \
  <<< "$catalog" | tr -d '\r')"
[[ "$catalog_default" != "$real_model" || -n "$alternate" ]] || die 'default model cannot be saved through the picker without another catalog model'
[[ -z "$alternate" || "$alternate" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ ]] || die 'unsafe alternate model ID'
jq -n --arg target "$real_model" --arg displayed "$catalog_default" --arg alternate "$alternate" \
  '{target:$target,displayed_default:$displayed,alternate:$alternate,harness:"opencode"}' \
  > "$run_root/evidence/model-selection.json"
jq --arg model "$model" --arg required "$real_model" \
  '.selected_catalog_model=$model | .required_ui_model=$required' "$run_root/evidence/manifest.json" > "$run_root/evidence/manifest.tmp"
mv "$run_root/evidence/manifest.tmp" "$run_root/evidence/manifest.json"
for image in "$LOCAL_MODE_LOOM_AGENTS_IMAGE" "$LOCAL_MODE_FLEETDB_IMAGE"; do
  podman image inspect "$image" | jq -e --arg project "$project" '.[0] | select(.Labels["io.loom.local-mode.project"] == $project) | {Id,RepoTags,Labels}' \
    >> "$run_root/evidence/images.jsonl" || die "built image $image lacks owned project provenance"
done

echo "[aft-agent-flows] running $case_count paid cases on owned $project; required UI model $real_model; catalog candidate $model; screenshots and videos in $run_root/evidence"
[[ "$(node "$TESTS_DIR/scripts/agent-flows-selection.mjs" "$TESTS_DIR" "$AFT_DIR/dist/runner.js" "$coverage_batch")" == "$selection" ]] \
  || die 'selected suite contents changed after preflight'
aft_status=0
HOME="$run_root/aft-home" node "$AFT_DIR/dist/cli.js" run "${suites[@]}" --no-agent --screenshots --record-all \
  --report-dir "$run_root/evidence" --viewport 1920x1080 --timeout 30000 || aft_status=$?
[[ -f "$run_root/evidence/last-run.json" ]] || die 'AFT produced no original run report'
jq -e --argjson selected "$selection" '
  [.tests[] | {suite,name}] as $actual |
  ($actual | length) == $selected.count and
  ($actual | sort_by(.suite,.name)) == ($selected.cases | sort_by(.suite,.name))' \
  "$run_root/evidence/last-run.json" >/dev/null || die 'AFT report cases differ from the selected batch'

# AgentInfo.model is the model saved on each actual Agent API row. The catalog
# selection above is only a preflight candidate and may differ from UI defaults.
agents_json="$(curl -fsS --max-time 30 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents?include_archived=true&limit=500")" \
  || die 'AFT finished but actual Agent API model readback failed'
jq --arg run "$run_id" '[.agents[] | select(.name | contains($run)) |
  {agent_id,name,preset,created_by_kind,parent_agent_id,repo,harness,model,model_unverified,state}]' <<< "$agents_json" \
  > "$run_root/evidence/actual-agent-models.json" || die 'actual Agent API model snapshot could not be saved'
jq -e '.next == ""' <<< "$agents_json" >/dev/null || die 'Agent API model readback was truncated'
jq -e --arg repo "$AFT_AGENT_FLOW_REPO" --arg target "$real_model" '
  length > 0 and all(.[]; .harness == "opencode" and .repo == $repo and
    (.model == null or (.model | type == "string" and length > 0 and (startswith("aft/") | not)))) and
  any(.[]; .preset == "lead" and .created_by_kind == "user" and .model == $target) and
  all(.[]; if .preset == "lead" and .created_by_kind == "user" then .model == $target and .model_unverified == false else true end)' \
  "$run_root/evidence/actual-agent-models.json" >/dev/null || die 'saved UI Lead model or owned OpenCode identity is missing'
jq -e --slurpfile observed "$run_root/evidence/actual-agent-models.json" \
  '. as $catalog | all($observed[0][] | select(.model != null); .model as $m | any($catalog.providers[].models[]; .id == $m))' \
  <<< "$catalog" >/dev/null || die 'an actual Agent API model is absent from the real OpenCode catalog'
[[ -f "$run_root/evidence/model-selections.jsonl" ]] || die 'no UI model selection evidence was recorded'
jq -se --slurpfile observed "$run_root/evidence/actual-agent-models.json" --arg target "$real_model" '
  . as $selections | [$observed[0][] | select(.preset == "lead" and .created_by_kind == "user")] as $leads |
  ($leads | length) > 0 and all($leads[]; . as $lead |
    any($selections[]; .agent_id == $lead.agent_id and .ui_selected_model == $target and .observed_saved_model == $target))' \
  "$run_root/evidence/model-selections.jsonl" >/dev/null || die 'a UI-created Lead lacks an explicit saved-model selection receipt'
jq --slurpfile observed "$run_root/evidence/actual-agent-models.json" \
  '.observed_agents=$observed[0] |
   .observed_models=($observed[0] | map(.model) | map(select(. != null)) | unique) |
   .agents_without_saved_model=($observed[0] | map(select(.model == null) | {agent_id,name,preset})) |
   .model_proof_scope="UI-saved model on surviving Leads; null child defaults need separate turn-level proof"' \
  "$run_root/evidence/manifest.json" > "$run_root/evidence/manifest.tmp"
mv "$run_root/evidence/manifest.tmp" "$run_root/evidence/manifest.json"
exit "$aft_status"
