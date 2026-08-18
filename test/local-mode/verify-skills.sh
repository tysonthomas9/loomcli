#!/usr/bin/env bash
# Deterministic end-to-end verification of the skills vertical against a
# running local-mode stack: fleet-db CRUD -> loom CLI -> materializer ->
# INDEX.md/catalog projection, including update (shrink), delete (prune), and
# the baked backend hook/pointer config. No model calls; safe to run anywhere
# a stack is up.
#
#   LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode make local-mode-skills-verify
set -euo pipefail

PROJECT="${LOCAL_MODE_COMPOSE_PROJECT:-loomcli-local-mode}"
CONTAINER="${LOCAL_MODE_LOOM_CONTAINER:-${PROJECT}-loom-local-1}"

log() { echo "[verify-skills] $*"; }
fatal() {
  echo "[verify-skills] FATAL: $*" >&2
  exit 1
}

ENGINE=""
for candidate in podman docker; do
  if command -v "$candidate" >/dev/null 2>&1; then
    ENGINE="$candidate"
    break
  fi
done
[ -n "$ENGINE" ] || fatal "podman or docker is required"

cexec() { "$ENGINE" exec "$CONTAINER" sh -c "$1"; }

"$ENGINE" inspect --format '{{.State.Running}}' "$CONTAINER" 2>/dev/null | grep -q true \
  || fatal "container ${CONTAINER} is not running (set LOCAL_MODE_COMPOSE_PROJECT)"

RUN_ID="$(date +%s)-$$"
NAME_A="vs-${RUN_ID}-a"
NAME_B="vs-${RUN_ID}-b"
DIR="/tmp/verify-skills-${RUN_ID}"
PHRASE_A="VERIFY-SKILLS-A-${RUN_ID}"
PHRASE_A2="SHRUNK-${RUN_ID}"
PHRASE_B="VERIFY-SKILLS-B-${RUN_ID}"

cleanup() {
  cexec "loom skill delete ${NAME_A} >/dev/null 2>&1; loom skill delete ${NAME_B} >/dev/null 2>&1; rm -rf ${DIR}" || true
}
trap cleanup EXIT

assert_in_dir() {
  # assert_in_dir <label> <shell condition evaluated in $DIR>
  cexec "cd ${DIR} && $2" >/dev/null 2>&1 || fatal "$1"
  log "ok: $1"
}

log "engine=${ENGINE} container=${CONTAINER} run=${RUN_ID}"

# 1. Create + materialize into a fresh directory.
cexec "printf 'The phrase is ${PHRASE_A}. This line pads the body so the later update genuinely shrinks the file.\n' | loom skill create ${NAME_A} --description 'verify-skills seed A' >/dev/null 2>&1"
cexec "mkdir -p ${DIR} && cd ${DIR} && loom skill materialize >/dev/null 2>&1"
assert_in_dir "skill A materialized with content" "grep -q ${PHRASE_A} .agents/skills/${NAME_A}/SKILL.md"
assert_in_dir "INDEX.md lists skill A" "grep -q '\*\*${NAME_A}\*\*' .agents/skills/INDEX.md"
assert_in_dir "catalog meta-skill materialized" "grep -q loom-skill-catalog .agents/skills/loom-skill-catalog/SKILL.md"
assert_in_dir "INDEX.md lists the catalog itself" "grep -q '\*\*loom-skill-catalog\*\*' .agents/skills/INDEX.md"
assert_in_dir "claude compat symlink resolves" "grep -q ${PHRASE_A} .claude/skills/${NAME_A}/SKILL.md"

# 2. Update with SHORTER content (regression: managed files must be allowed to
# shrink during reconcile) and re-materialize.
cexec "printf '${PHRASE_A2}\n' | loom skill update ${NAME_A} --content - >/dev/null 2>&1"
cexec "cd ${DIR} && loom skill materialize >/dev/null 2>&1"
assert_in_dir "shrunken update applied" "grep -q ${PHRASE_A2} .agents/skills/${NAME_A}/SKILL.md"
assert_in_dir "old content gone" "! grep -q ${PHRASE_A} .agents/skills/${NAME_A}/SKILL.md"

# 3. Add B, delete A, re-materialize: prune + live INDEX.
cexec "printf 'The phrase is ${PHRASE_B}.\n' | loom skill create ${NAME_B} --description 'verify-skills seed B' >/dev/null 2>&1"
cexec "loom skill delete ${NAME_A} >/dev/null 2>&1"
cexec "cd ${DIR} && loom skill materialize >/dev/null 2>&1"
assert_in_dir "deleted skill pruned from disk" "! test -e .agents/skills/${NAME_A}"
assert_in_dir "INDEX.md dropped deleted skill" "! grep -q '\*\*${NAME_A}\*\*' .agents/skills/INDEX.md"
assert_in_dir "INDEX.md lists new skill B" "grep -q '\*\*${NAME_B}\*\*' .agents/skills/INDEX.md"
assert_in_dir "skill B materialized" "grep -q ${PHRASE_B} .agents/skills/${NAME_B}/SKILL.md"

# 4. Backend advertisement/hook config baked into the image (per variant).
if cexec "command -v codex >/dev/null 2>&1"; then
  cexec "grep -q 'loom skill materialize' /etc/codex/requirements.toml && grep -q 'allow_managed_hooks_only = true' /etc/codex/requirements.toml" \
    || fatal "codex installed but /etc/codex/requirements.toml is missing the managed materialize hook"
  log "ok: codex managed hook policy present"
  cexec "grep -q 'INDEX.md' /root/.codex/AGENTS.md" \
    || fatal "codex installed but /root/.codex/AGENTS.md catalog pointer is missing"
  log "ok: codex global catalog pointer present"
fi
if cexec "command -v claude >/dev/null 2>&1"; then
  cexec "grep -q 'INDEX.md' /root/.claude/CLAUDE.md" \
    || fatal "claude installed but /root/.claude/CLAUDE.md catalog pointer is missing"
  log "ok: claude global catalog pointer present"
fi

log "PASS: skills vertical verified end-to-end (create/update-shrink/delete, INDEX+catalog, backend config)"
