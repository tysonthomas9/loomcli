#!/usr/bin/env bash
# Live-tier preflight: can the controlled Codex lead runtime actually start?
#
# Loom launches leads as `codex app-server --listen ws://127.0.0.1:<port>`
# (internal/leadcontrol/codex_runtime.go codexAppServerArgs) and waits 60s for a
# websocket dial. If the app server cannot come up, every live interactive case fails
# ~4 minutes in with an empty app-server.log — codex prints startup problems only under
# --strict-config. This reproduces the same launch in seconds, for free, before the
# stack boots, so that failure mode costs 15s instead of a rate window.
#
# History: loom used to append `-c sqlite_home="<fresh dir>"`, which on codex 0.145.0
# blocks forever on a state-db backfill in the DEFAULT home. That flag is gone and
# pinned by TestCodexAppServerArgsDoNotOverrideSQLiteHome; this preflight now mirrors
# the shipped argv, so it catches whatever the NEXT incompatibility turns out to be.
#
# Usage: live-preflight-codex-appserver.sh <codex-binary-path> [wait-seconds]
set -uo pipefail

BIN="${1:?usage: live-preflight-codex-appserver.sh <codex-binary> [seconds]}"
WAIT_SECS="${2:-15}"

TMP="$(mktemp -d)"
LOG="$TMP/app-server.log"
PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()' 2>/dev/null)"
if [[ -z "$PORT" ]]; then
    # Fail closed. Skipping would let a live, billed run start on the strength of a
    # check that never actually ran.
    echo "[preflight] could not allocate a probe port; refusing to start the live tier unchecked" >&2
    rm -rf "$TMP"
    exit 1
fi

# Launch codex DIRECTLY, not inside `( cd … && … ) &`. With a subshell, `$!` is the
# subshell and `kill $!` reaps only the wrapper — codex survives, reparented to init.
# That leaked an app-server on every preflight and, since the PID baseline is taken
# before this runs, the post-run sweep correctly blamed the run for it.
cd "$TMP" || { echo "[preflight] cannot enter probe dir $TMP" >&2; exit 1; }
"$BIN" app-server --listen "ws://127.0.0.1:$PORT" >"$LOG" 2>&1 &
pid=$!
ready=""
for _ in $(seq 1 "$WAIT_SECS"); do
    kill -0 "$pid" 2>/dev/null || break
    # /readyz, not a bare TCP listen: codex advertises it alongside the ws endpoint
    # ("readyz: http://127.0.0.1:<port>/readyz"), and it reports the server finished
    # startup initialization rather than merely bound a socket. Still weaker than what
    # loom does (a websocket client init), so readiness here stays necessary-not-sufficient.
    if curl -sf --max-time 3 "http://127.0.0.1:$PORT/readyz" >/dev/null 2>&1; then
        ready=1
        break
    fi
    sleep 1
done
# Escalate and VERIFY. A probe that leaks the process it started poisons the run's
# PID baseline and gets blamed on the product, which is exactly what happened once.
if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null
    for _ in 1 2 3 4 5; do
        kill -0 "$pid" 2>/dev/null || break
        sleep 1
    done
    if kill -0 "$pid" 2>/dev/null; then
        kill -9 "$pid" 2>/dev/null
        sleep 1
    fi
fi
wait "$pid" 2>/dev/null
if kill -0 "$pid" 2>/dev/null; then
    echo "[preflight] FAILED to reap its own probe app-server (pid $pid); kill it before rerunning" >&2
    rm -rf "$TMP"
    exit 1
fi

if [[ -n "$ready" ]]; then
    echo "[preflight] codex app-server reached ready"
    rm -rf "$TMP"
    exit 0
fi

echo "[preflight] codex app-server did NOT become ready in ${WAIT_SECS}s." >&2
echo "[preflight] The controlled Codex lead runtime cannot start, so a live lead/custom-prompt" >&2
echo "[preflight] case would burn ~4 minutes and fail with an empty app-server log." >&2
if [[ -s "$LOG" ]]; then
    echo "[preflight] app-server output:" >&2
    sed 's/^/[preflight]   /' "$LOG" >&2
else
    echo "[preflight] (app-server printed nothing; rerun with --strict-config to see the reason)" >&2
fi
echo "[preflight] reproduce: $BIN app-server --listen ws://127.0.0.1:PORT --strict-config" >&2
echo "[preflight] note: this proves a TCP listener only; loom additionally requires a" >&2
echo "[preflight] successful websocket initialization, so readiness here is necessary, not sufficient." >&2
rm -rf "$TMP"
exit 1
