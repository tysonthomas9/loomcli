# shellcheck shell=bash
# Shared setup for the local-mode verify scripts: container engine detection,
# the exec helper, the running check, and logging.
#
# Sourced, not executed. The caller sets VERIFY_LABEL (used as the log prefix)
# before sourcing, and gets ENGINE, CONTAINER, PROJECT, log, fatal and cexec.
#
# Deliberately not a full harness: each script keeps its own fixture names,
# cleanup trap and assertions, because those are what the script is about.

PROJECT="${LOCAL_MODE_COMPOSE_PROJECT:-loomcli-local-mode}"
CONTAINER="${LOCAL_MODE_LOOM_CONTAINER:-${PROJECT}-loom-local-1}"
VERIFY_LABEL="${VERIFY_LABEL:-verify}"

log() { echo "[${VERIFY_LABEL}] $*"; }
fatal() {
  echo "[${VERIFY_LABEL}] FATAL: $*" >&2
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

# require_running fails unless the target container is up. Every check below
# runs inside it, so a stopped container must stop the script rather than
# produce a wall of exec failures.
require_running() {
  "$ENGINE" inspect --format '{{.State.Running}}' "$CONTAINER" 2>/dev/null | grep -q true \
    || fatal "container ${CONTAINER} is not running (set LOCAL_MODE_COMPOSE_PROJECT)"
}

# require_in_container fails unless a command exists inside the container.
require_in_container() {
  cexec "command -v $1 >/dev/null 2>&1" || fatal "$1 is not installed in ${CONTAINER}${2:+; $2}"
}
