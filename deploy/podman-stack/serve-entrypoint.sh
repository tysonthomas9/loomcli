#!/bin/sh
# loom-serve entrypoint.
#
# Seeds the /work named volume's node_modules so server-side workflow-bundle
# builds can resolve their runtime dependency closure, then execs loom.
#
# Why this is needed: serve builds each submitted workflow under
# /work/.loom/workflow-builds/<name>-<n>/ and seeds only @loom/sdk into that
# build directory's own node_modules (internal/workflows/workflows.go
# writeWorkflowBuildProject). The flue node-target build, however, generates a
# server entry that imports @hono/node-server / hono, which it resolves by
# walking up from the build root for node_modules/@flue/runtime and then
# adding that package's node_modules subtree to the Vite NODE_PATH
# (flue packages/cli/src/lib/build.ts collectNodePaths/resolveRuntimeDir).
# Nothing on the walk-up chain provides @flue/runtime in a container, so the
# build fails with "Rolldown failed to resolve import @hono/node-server".
#
# /work is a named volume mounted at runtime, so it must be seeded here (a
# Containerfile-time mkdir under /work is shadowed by the empty volume).
# Symlinking @flue/runtime -> the in-image flue workspace package puts its
# already-installed node_modules (@hono/node-server, hono, ...) on the build's
# resolution chain; @loom/sdk is symlinked too for parity / belt-and-braces.
set -eu

WORK_DIR="${LOOM_WORK_DIR:-/work}"
NM="$WORK_DIR/node_modules"
FLUE_RUNTIME="${LOOM_FLUE_RUNTIME_ROOT:-/opt/flue/packages/runtime}"
SDK_ROOT="${LOOM_SDK_ROOT:-/opt/loom-sdk}"

# ── Codex auth: mirror the read-only host ~/.codex into a writable copy ──────
# The A1 github-review-agent task runner spawns the codex CLI. The host's
# ~/.codex is mounted READ-ONLY (compose) at LOOM_STACK_CODEX_RO_DIR so the
# operator's auth is never mutated; codex, however, writes session/log state
# under CODEX_HOME at runtime and crashes with "Read-only file system" if
# pointed at the RO mount. Mirror the auth + benign config into the writable
# CODEX_HOME, omitting host MCP/plugin/notify config and runtime state DBs
# (platform-specific; a stale state DB makes codex print "file is not a
# database"). This is the same sanitized mirror the proven single-container path
# performs (scripts/dev-container-start.sh mirror_codex_rw). Only runs when the
# RO mount is present, so the stack still boots without codex for non-A1 runs.
CODEX_RO_DIR="${LOOM_STACK_CODEX_RO_DIR:-$HOME/.codex}"
CODEX_RW_DIR="${LOOM_STACK_CODEX_RW_DIR:-$HOME/.codex-rw}"

sanitize_codex_config() {
  # Drop notify hooks, [mcp_servers.*] and [plugins.*] sections (host desktop
  # binaries are not present/executable in this container).
  awk '
    /^notify[[:space:]]*=/ { next }
    /^\[mcp_servers(\.|])/ { skip = 1; next }
    /^\[plugins\./        { skip = 1; next }
    /^\[/                 { skip = 0 }
    !skip { print }
  ' "$1" >"$2" 2>/dev/null || true
}

mirror_codex_rw() {
  src="$1"
  dst="$2"
  [ -d "$src" ] || return 0
  # auth.json is the only must-have; if the RO mount has none there is nothing
  # to mirror and codex would fail at runtime regardless — warn and move on.
  if [ ! -e "$src/auth.json" ]; then
    echo "serve-entrypoint: WARNING ${src}/auth.json absent — codex review TaskRuns will fail until host ~/.codex is mounted with valid auth" >&2
    return 0
  fi
  mkdir -p "$dst"
  for f in auth.json AGENTS.md installation_id internal_storage.json \
           version.json .codex-global-state.json .personality_migration; do
    [ -e "$src/$f" ] && cp -p "$src/$f" "$dst/$f" 2>/dev/null || true
  done
  [ -e "$src/config.toml" ] && sanitize_codex_config "$src/config.toml" "$dst/config.toml"
  for d in rules skills plugins memories vendor_imports; do
    [ -d "$src/$d" ] && cp -R "$src/$d" "$dst/$d" 2>/dev/null || true
  done
  echo "serve-entrypoint: mirrored codex auth ${src} -> ${dst} (writable CODEX_HOME)" >&2
}

mirror_codex_rw "$CODEX_RO_DIR" "$CODEX_RW_DIR"

if [ -d "$FLUE_RUNTIME" ]; then
  mkdir -p "$NM/@flue" "$NM/@loom"
  ln -sfn "$FLUE_RUNTIME" "$NM/@flue/runtime"
  [ -d "$SDK_ROOT" ] && ln -sfn "$SDK_ROOT" "$NM/@loom/sdk"
else
  echo "serve-entrypoint: WARNING @flue/runtime not found at $FLUE_RUNTIME — workflow node builds may fail to resolve @hono/node-server" >&2
fi

# Seed the per-machine state cache ($HOME/.loom/state.json) with a local path
# for the stack workspace. The `loom worker` registration handler accepts a
# worker only when storeadapter.ResolveWorkspacePath(workspace) != "" in
# ADDITION to the workspace existing in fleet-db (internal/webui/app/
# server_workspace.go workerValidateWorkspace, line ~150). ResolveWorkspacePath
# reads the local state cache (bootstrap.LoadStateCache -> sc.Workspaces[key]
# .Path), which the fleet-db admin workspace create does NOT populate — that
# path is normally established by `loom workspace use`/clone on a dev machine.
# Without it the worker register POST returns 400 "unknown workspace ID" forever
# (and serve logs "periodic workspace reconcile: register failed: workspace path
# must not be empty"). Seed it here so the worker plane can register on this
# fleet-mode container stack. Only writes when absent, so serve's own state-cache
# writes (workspace-management endpoints) are never clobbered.
STATE_WS="${LOOM_STACK_WORKSPACE:-}"
if [ -n "$STATE_WS" ]; then
  STATE_DIR="${HOME:-/home/node}/.loom"
  STATE_FILE="$STATE_DIR/state.json"
  WS_PATH="$WORK_DIR/workspaces/$STATE_WS"
  mkdir -p "$STATE_DIR" "$WS_PATH/worktrees"
  if [ ! -f "$STATE_FILE" ]; then
    printf '{"version":1,"workspaces":{"%s":{"path":"%s"}}}\n' "$STATE_WS" "$WS_PATH" >"$STATE_FILE"
    echo "serve-entrypoint: seeded state cache workspace path ${STATE_WS} -> ${WS_PATH}" >&2
  fi
fi

exec /usr/local/bin/loom "$@"
