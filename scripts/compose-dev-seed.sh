#!/usr/bin/env bash
# Seed the dev compose stack's server container with a local workspace.
#
# Runs INSIDE the server container:
#   docker compose -f docker-compose.dev.yml exec -T server bash -s < scripts/compose-dev-seed.sh
#
# Why this exists: a fleet-db workspace RECORD and a local workspace CHECKOUT
# are two different objects, and the Web UI terminal needs the second one. The
# terminal attach resolves the workspace to an on-disk path first and returns
# early when that path is empty, so the tab reports "Disconnected" and no PTY
# is ever spawned. `mkdir` alone does not fix it either: the self-heal path
# requires at least one repo record whose checkout verifies, and gives up with
# "directory exists but no repo checkout verified" otherwise.
#
# `loom workspace create` is the one command that produces every required
# artifact in a single operation: the git worktrees under the workspace dir,
# the workspace AND its repos registered in fleet-db, and the local paths
# written into state.json. Because it populates that per-machine state cache,
# the later resolve takes the fast path and the self-heal branch never runs.
#
# The name SMOKE is uppercase on purpose. The workspace KEY is derived by
# uppercasing the name, and the URL path segment and terminal API use the key
# while the checkout directory is derived from the name — with SMOKE the two
# are the same string.
#
# Idempotent: the named volume outlives `docker compose down` (without -v), so
# a re-run on a populated volume must be a no-op. Creating a workspace whose
# name or key is taken is an AlreadyExists error, not a success.
set -euo pipefail

WS="${SMOKE_WORKSPACE:-SMOKE}"
LOOM_DIR="${LOOM_CONFIG_DIR:-/var/lib/loom}"
FIXTURE="${LOOM_DIR}/fixtures/smoke-repo"
CHECKOUT="${LOOM_DIR}/workspaces/${WS}/smoke-repo"

if [ -e "${CHECKOUT}/.git" ]; then
  echo "[seed] workspace ${WS} already checked out at ${CHECKOUT} — nothing to do"
  exit 0
fi

# git needs an identity and a writable HOME; the image sets HOME to the volume.
export HOME="${HOME:-$LOOM_DIR}"

if [ ! -d "${FIXTURE}/.git" ]; then
  echo "[seed] creating fixture repo at ${FIXTURE}"
  mkdir -p "${FIXTURE}"
  git -C "${FIXTURE}" init -q -b main
  git -C "${FIXTURE}" config user.email "compose-smoke@local"
  git -C "${FIXTURE}" config user.name "compose-smoke"
  printf '# smoke fixture\n\nCreated by scripts/compose-dev-seed.sh.\n' \
    > "${FIXTURE}/README.md"
  git -C "${FIXTURE}" add README.md
  # `git worktree add` needs a commit to branch from, so the fixture cannot be
  # an empty repo.
  git -C "${FIXTURE}" commit -q -m "smoke fixture"
fi

echo "[seed] creating workspace ${WS}"
loom workspace create "${WS}" --repos "${FIXTURE}" --branch smoke

echo "[seed] done: ${CHECKOUT}"
