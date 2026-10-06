#!/usr/bin/env bash
# Read-only Git observation inside the runner-owned local-mode container.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -eq 1 && "$1" =~ ^agt_[A-Za-z0-9_-]+$ ]] || { echo 'expected exact Agent ID' >&2; exit 2; }
: "${AFT_AGENT_FLOW_REPO:?}" "${AFT_TESTS_DIR:?}"
jq -e --arg repo "$AFT_AGENT_FLOW_REPO" '
  .selection.batch == "git-files" and
  .fixture_repo.seed_path == "/workspace/source-repo" and
  .fixture_repo.managed_path == $repo and
  any(.selection.agents.leads[];
    .name == "cov-files-${RUN_ID}-git" and .suite == "coverage-agent-git-files" and .model_required == true)
' "$AFT_WORK_DIR/manifest.json" >/dev/null || { echo 'foreign Git Files batch or fixture' >&2; exit 2; }

cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
# shellcheck disable=SC2016 # The single-quoted JavaScript runs inside Node.
"${compose[@]}" exec -T loom-local node -e '
const fs = require("node:fs");
const cp = require("node:child_process");
const {DatabaseSync} = require("node:sqlite");
const [id, run, repo] = process.argv.slice(1);
const origin = "/workspace/source-repo-origin.git";
const worktree = `/root/.loom/worktrees/source-repo/${id}`;
const branch = `loom/agent/${id}`;
const git = (path, ...args) => cp.execFileSync("git", ["-C", path, ...args],
  {encoding:"utf8", stdio:["ignore","pipe","pipe"]}).trim();
const optionalConfig = (...args) => {
  try { return git(worktree,"config",...args); }
  catch (error) { if (error.status !== 1) throw error; return ""; }
};
if (!/^agt_[A-Za-z0-9_-]+$/.test(id) || !/^af[a-z0-9]{8}$/.test(run) ||
    repo !== "/root/.loom/workspaces/LOCALMODE/source-repo" ||
    fs.realpathSync(origin) !== origin || !fs.lstatSync(origin).isDirectory() ||
    fs.realpathSync(worktree) !== worktree || !fs.lstatSync(worktree).isDirectory())
  throw Error("owned path or local bare origin mismatch");
const db = new DatabaseSync("/root/.loom/agents.db", {readOnly:true});
const row = db.prepare(`SELECT agent_id,name,preset,harness,repo,created_by_kind,
  parent_agent_id,root_agent_id,worktree_path,branch FROM agents
  WHERE workspace_id=? AND agent_id=?`).get("LOCALMODE", id);
if (!row || row.agent_id !== id || row.name !== `cov-files-${run}-git` ||
    row.preset !== "lead" || row.harness !== "opencode" || row.repo !== repo ||
    row.created_by_kind !== "user" || row.parent_agent_id !== null ||
    row.root_agent_id !== null || row.worktree_path !== worktree || row.branch !== branch)
  throw Error("not the run-owned Git Lead");
const common = fs.realpathSync(git(worktree,"rev-parse","--path-format=absolute","--git-common-dir"));
const managedCommon = fs.realpathSync(git(repo,"rev-parse","--path-format=absolute","--git-common-dir"));
if (common !== managedCommon || git(worktree,"rev-parse","--show-toplevel") !== worktree ||
    git(origin,"rev-parse","--is-bare-repository") !== "true")
  throw Error("worktree does not belong to managed repository and bare origin");
let originRef = null;
try { originRef = git(origin,"rev-parse","--verify","refs/heads/"+branch); }
catch (error) { if (error.status !== 128) throw error; }
let trackingRef = null;
try { trackingRef = git(worktree,"rev-parse","--verify","refs/remotes/origin/"+branch); }
catch (error) { if (error.status !== 128) throw error; }
process.stdout.write(JSON.stringify({agent_id:id,name:row.name,repo:row.repo,
  worktree_path:row.worktree_path,branch:row.branch,worktree_root:worktree,
  common,managed_common:managedCommon,origin_realpath:fs.realpathSync(origin),
  origin_bare:true,remotes:git(worktree,"remote").split("\n"),
  origin_fetch_urls:git(worktree,"remote","get-url","--all","origin").split("\n"),
  origin_push_urls:git(worktree,"remote","get-url","--push","--all","origin").split("\n"),
  origin_mirror:optionalConfig("--bool","--get","remote.origin.mirror"),
  origin_push_refspecs:optionalConfig("--get-all","remote.origin.push"),
  head:git(worktree,"rev-parse","HEAD"),
  current_branch:git(worktree,"branch","--show-current"),
  porcelain:git(worktree,"status","--porcelain"),origin_ref:originRef,
  tracking_ref:trackingRef})+"\n");
' "$1" "$RUN_ID" "$AFT_AGENT_FLOW_REPO"
