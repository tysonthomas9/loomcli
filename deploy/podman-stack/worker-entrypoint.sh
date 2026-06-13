#!/bin/sh
# Worker container entrypoint: give every compose replica a distinct agent
# name (worker-<container hostname>) unless the operator pinned one, then
# exec the loom worker loop (exec, not subshell — signals reach the worker).
set -eu

: "${LOOM_WORKER_CONTROL_PLANE:?LOOM_WORKER_CONTROL_PLANE is required}"
: "${LOOM_WORKER_WORKSPACE:?LOOM_WORKER_WORKSPACE is required}"

if [ -z "${LOOM_WORKER_AGENT:-}" ]; then
  LOOM_WORKER_AGENT="worker-$(hostname)"
  export LOOM_WORKER_AGENT
fi

# `loom worker` makes a SINGLE registration attempt and exits 1 if the control
# plane rejects it (internal/cli/serve/worker/worker_cmd.go registerAndGetToken).
# The workspace this worker registers against is created by the test driver a
# moment AFTER loom-serve turns healthy, so a worker that boots first races the
# seed and would exhaust compose's restart budget on "unknown workspace ID".
# Retry registration here with a bounded backoff so the worker survives that
# window; once it registers it execs into the long-running loop. The final
# attempt uses exec so signals reach the worker process.
WORKER_REGISTER_MAX_ATTEMPTS="${LOOM_WORKER_REGISTER_MAX_ATTEMPTS:-60}"
WORKER_REGISTER_BACKOFF="${LOOM_WORKER_REGISTER_BACKOFF:-2}"

attempt=1
while [ "$attempt" -lt "$WORKER_REGISTER_MAX_ATTEMPTS" ]; do
  # `|| rc=$?` keeps `set -e` from aborting the script on the expected
  # non-zero exit while the workspace seed is still in flight. Once `loom
  # worker` stays up (registration succeeded) this command blocks in the
  # worker loop and only returns when the worker itself stops.
  rc=0
  /usr/local/bin/loom worker "$@" || rc=$?
  echo "[worker-entrypoint] loom worker exited rc=$rc (attempt $attempt/$WORKER_REGISTER_MAX_ATTEMPTS); retrying in ${WORKER_REGISTER_BACKOFF}s" >&2
  attempt=$((attempt + 1))
  sleep "$WORKER_REGISTER_BACKOFF"
done

# Final attempt: exec so signals reach the worker; compose restart policy
# remains the outer safety net.
exec /usr/local/bin/loom worker "$@"
