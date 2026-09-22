#!/usr/bin/env bash
# run-aft.sh — start the isolated e2e stack (loom serve + fleet-db + vite preview),
# run the aft YAML suites against it, tear down.
#
# Usage: tests/aft/run-aft.sh [aft options...]        # e.g. --no-agent, --strict, --heal, --record
# Env:   E2E_PORT (API, default 8090)   E2E_FRONTEND_PORT (browser target, default 3100)
#        FLEET_DB_REPO (default: sibling ../fleet-db)  AFT_DIR (default: sibling ../testing-app)
#        AFT_SUITES (default: product + surface suite directories; when set,
#                    overrides them with exactly one YAML file or directory)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Optional machine-local overrides (untracked; see .gitignore) so `make test-aft`
# works without per-invocation env vars. Write entries in `: "${VAR:=value}"` form
# so explicitly exported env still wins over the file.
if [[ -f "$SCRIPT_DIR/local.env" ]]; then
    # shellcheck source=/dev/null
    . "$SCRIPT_DIR/local.env"
fi

# --- harness-owned flags ---------------------------------------------------
# Everything this script does not recognize is forwarded to aft verbatim. These
# four configure the STACK rather than the test runner, so they are consumed
# here and removed from the forwarded args. `set --` rewrites the positional
# args afterwards, so every later "$@" use stays correct without edits.
AFT_LIVE=""
AFT_WITH_DAEMON=""
AFT_MAX_REAL_CASES=""
AFT_PASSTHRU=()
LIVE_LOCK_WRITTEN=""
LIVE_ACCOUNT_LOCK=""
# Absolute ceiling on paid cases per run, independent of --max-real-cases. Backstop
# for a bad cap or a miscount; raise it deliberately, not in passing.
LIVE_MAX_CASES_CEILING=10
while [[ $# -gt 0 ]]; do
    case "$1" in
        --live)            AFT_LIVE=1; shift ;;
        --with-daemon)     AFT_WITH_DAEMON=1; shift ;;
        --real-backend)
            [[ $# -ge 2 ]] || { echo "[aft] --real-backend needs a value" >&2; exit 1; }
            AFT_REAL_BACKEND="$2"; shift 2 ;;
        --real-backend=*)  AFT_REAL_BACKEND="${1#*=}"; shift ;;
        --max-real-cases)
            [[ $# -ge 2 ]] || { echo "[aft] --max-real-cases needs a value" >&2; exit 1; }
            AFT_MAX_REAL_CASES="$2"; shift 2 ;;
        --max-real-cases=*) AFT_MAX_REAL_CASES="${1#*=}"; shift ;;
        *)                 AFT_PASSTHRU+=("$1"); shift ;;
    esac
done
set -- "${AFT_PASSTHRU[@]+"${AFT_PASSTHRU[@]}"}"

if [[ -n "$AFT_MAX_REAL_CASES" && ! "$AFT_MAX_REAL_CASES" =~ ^[1-9][0-9]*$ ]]; then
    echo "[aft] --max-real-cases needs a positive integer (got '$AFT_MAX_REAL_CASES')" >&2
    exit 1
fi
if [[ -n "$AFT_WITH_DAEMON" && -z "$AFT_LIVE" ]]; then
    echo "[aft] --with-daemon is reserved for --live worker suites" >&2
    exit 1
fi
# Everything the live tier is allowed to do is decided HERE — before the stack, the
# real-binary lookup, or any credential read. Each check is exact rather than
# substring/glob based: a bypass here spends money on the wrong corpus.
if [[ -n "$AFT_LIVE" ]]; then
    if [[ -z "${AFT_REAL_BACKEND:-}" && "${AFT_REAL_CODEX:-}" != "1" ]]; then
        echo "[aft] --live needs --real-backend <codex|claude|opencode|cursor>" >&2
        exit 1
    fi
    live_saw_no_agent=""
    # `--filter <name>` takes a VALUE, and the positional check below cannot tell a
    # flag's value from a suite path — it rejected `--filter "Task Runner"` as an
    # extra corpus. Consume the value with the flag so filtering a live run to one
    # case stays possible (which is how a paid tier gets iterated on cheaply)
    # WITHOUT weakening the real guard: an unaccompanied bare word is still refused.
    expect_value=""
    for arg in "$@"; do
        if [[ -n "$expect_value" ]]; then
            expect_value=""
            continue
        fi
        case "$arg" in
            --strict|--heal)
                # aft's recovery agent is a second, unaccounted model invocation,
                # and healing a live case rewrites the assertions the tier exists
                # to enforce.
                echo "[aft] --live refuses $arg: the recovery agent is an unaccounted model call" >&2
                exit 1
                ;;
            --no-agent) live_saw_no_agent=1 ;;
            --filter|--real-backend|--max-real-cases) expect_value=1 ;;
            -*) ;;
            *)
                # aft treats every non-option argument as another suite path, and
                # those paths are invisible to the case cap below.
                echo "[aft] --live refuses the positional argument '$arg': extra suite paths bypass --max-real-cases" >&2
                exit 1
                ;;
        esac
    done
    if [[ -z "$live_saw_no_agent" ]]; then
        echo "[aft] --live requires --no-agent so no recovery agent runs alongside the paid backend" >&2
        exit 1
    fi

    # Canonicalize before comparing: a glob on the raw string accepts
    # `live-interactive-suites/../suites`, which resolves into the free corpus.
    live_root="$SCRIPT_DIR"
    live_target="${AFT_SUITES:-}"
    if [[ -z "$live_target" ]]; then
        echo "[aft] --live needs AFT_SUITES pointed at a live-* suite path" >&2
        exit 1
    fi
    if [[ ! -e "$live_target" ]]; then
        echo "[aft] --live: AFT_SUITES path does not exist: $live_target" >&2
        exit 1
    fi
    # realpath resolves symlinks in the FINAL component too. Canonicalizing only the
    # parent and re-appending the basename let a symlinked .yaml inside live-*/ point
    # at the deterministic corpus and pass the containment check below.
    live_canon="$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$live_target" 2>/dev/null)"
    if [[ -z "$live_canon" ]]; then
        echo "[aft] --live: could not canonicalize AFT_SUITES path: $live_target" >&2
        exit 1
    fi
    live_root_canon="$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$live_root")"
    case "$live_canon" in
        "$live_root_canon"/live-*) : ;;
        *)
            echo "[aft] --live needs AFT_SUITES under $live_root_canon/live-* (resolved to '$live_canon')" >&2
            exit 1
            ;;
    esac
    live_rel="${live_canon#"$live_root_canon"/}"
    live_top="${live_rel%%/*}"
    case "$live_top" in
        live-worker*)    AFT_LIVE_SUITE_KIND=worker ;;
        live-pr-review*) AFT_LIVE_SUITE_KIND=prreview ;;
        *)               AFT_LIVE_SUITE_KIND=interactive ;;
    esac
    if [[ "$AFT_LIVE_SUITE_KIND" == "worker" && -z "$AFT_WITH_DAEMON" ]]; then
        echo "[aft] live worker suites require --with-daemon" >&2
        exit 1
    fi
    if [[ -n "$AFT_WITH_DAEMON" && "$AFT_LIVE_SUITE_KIND" != "worker" ]]; then
        echo "[aft] --with-daemon only accepts suites under live-worker* (got $live_top)" >&2
        exit 1
    fi
    if [[ "$AFT_LIVE_SUITE_KIND" == "worker" ]]; then
        case "${AFT_REAL_BACKEND:-codex}" in
            codex|claude) : ;;
            *)
                echo "[aft] live worker suites currently support codex or claude, not ${AFT_REAL_BACKEND:-unknown}" >&2
                exit 1
                ;;
        esac
    fi
    # The cap is MANDATORY. An optional bound on paid work is not a bound: the whole
    # point is that nobody can start this tier without stating how much they are
    # willing to spend.
    if [[ -z "$AFT_MAX_REAL_CASES" ]]; then
        echo "[aft] --live requires --max-real-cases <n>: paid runs must declare an upper bound" >&2
        exit 1
    fi
    # Counting is a grep for the one case spelling these suites use. It could
    # UNDERcount exotic YAML (flow style, inline maps), so a hard ceiling backs it up —
    # a miscount can then bound the damage rather than uncap it.
    LIVE_CASE_COUNT="$( { grep -rhE '^[[:space:]]*-[[:space:]]+name:' "$live_canon" 2>/dev/null || true; } | wc -l | tr -d ' ')"
    if [[ "${LIVE_CASE_COUNT:-0}" -eq 0 ]]; then
        echo "[aft] --live found no test cases under '$live_canon'" >&2
        exit 1
    fi
    if [[ "$LIVE_CASE_COUNT" -gt "$AFT_MAX_REAL_CASES" ]]; then
        echo "[aft] refusing to start: $LIVE_CASE_COUNT live case(s) exceeds --max-real-cases $AFT_MAX_REAL_CASES" >&2
        exit 1
    fi
    if [[ "$AFT_MAX_REAL_CASES" -gt "$LIVE_MAX_CASES_CEILING" ]]; then
        echo "[aft] refusing to start: --max-real-cases $AFT_MAX_REAL_CASES exceeds the live-tier ceiling of $LIVE_MAX_CASES_CEILING" >&2
        exit 1
    fi

    # Account-scoped lock. The stack lock is keyed by PORT, so two runs on different
    # ports would happily consume the same provider account at once — and, since leads
    # now share one codex home, race each other's thread discovery.
    LIVE_ACCOUNT_LOCK="$REPO_ROOT/tmp/aft-live.${AFT_REAL_BACKEND:-codex}.lock"
    mkdir -p "$REPO_ROOT/tmp"
    # Acquire atomically. The previous shape was test-then-write: two runs starting
    # together could both see no lock (or both see the same stale one) and both write
    # their pid, so the guard that exists to keep two paid runs off one provider
    # account would let exactly that through. `set -o noclobber` makes the create
    # O_EXCL, so precisely one contender wins.
    if ! ( set -o noclobber; echo "$$" > "$LIVE_ACCOUNT_LOCK" ) 2>/dev/null; then
        live_lock_pid="$(cat "$LIVE_ACCOUNT_LOCK" 2>/dev/null || true)"
        if [[ -n "$live_lock_pid" ]] && kill -0 "$live_lock_pid" 2>/dev/null; then
            echo "[aft] refusing to start: another live ${AFT_REAL_BACKEND:-codex} run holds the account lock (pid $live_lock_pid)" >&2
            exit 1
        fi
        # Stale owner. Rename (atomic) rather than deleting in place, then retry the
        # exclusive create: if several runs race the reclaim, only one mv sees the
        # original and only one create succeeds — the losers re-read a live pid above.
        echo "[aft] clearing a stale live account lock (pid ${live_lock_pid:-unknown} is gone)"
        mv -f "$LIVE_ACCOUNT_LOCK" "${LIVE_ACCOUNT_LOCK}.stale" 2>/dev/null || true
        if ! ( set -o noclobber; echo "$$" > "$LIVE_ACCOUNT_LOCK" ) 2>/dev/null; then
            echo "[aft] refusing to start: lost the race to reclaim the stale ${AFT_REAL_BACKEND:-codex} account lock" >&2
            exit 1
        fi
    fi
    LIVE_LOCK_WRITTEN=1
    echo "[aft] live tier: $LIVE_CASE_COUNT case(s) against real ${AFT_REAL_BACKEND:-codex}${AFT_MAX_REAL_CASES:+ (cap $AFT_MAX_REAL_CASES)}"
fi

: "${E2E_PORT:=8090}"
: "${E2E_FRONTEND_PORT:=3100}"
# fleet-db must include the driver-runs domain (Engine B / epic-runner); prefer a
# sibling fleet-db-main checkout (e.g. a `git worktree` of origin/main) when the
# primary sibling checkout is older
if [[ -z "${FLEET_DB_REPO:-}" && -d "$REPO_ROOT/../fleet-db-main" ]]; then
    FLEET_DB_REPO="$REPO_ROOT/../fleet-db-main"
fi
: "${FLEET_DB_REPO:=$REPO_ROOT/../fleet-db}"
: "${AFT_DIR:=$REPO_ROOT/../testing-app}"
# flue runtime for building the builtin epic-runner workflow bundle (sibling checkout
# at internal/workflows/FLUE_COMMIT, built with: pnpm install && pnpm --filter
# @flue/runtime --filter @flue/cli build). Empty when absent: agent-flow tests fail
# with a clear workflow error, everything else runs.
: "${FLUE_REPO:=$REPO_ROOT/../flue}"
[[ -d "$FLUE_REPO/packages/runtime" ]] || FLUE_REPO=""
# bfcache retains unloaded documents' SSE sockets ~60s and saturates Chrome's
# 6-socket pool; the SPA pagehide fix addresses the product, this keeps suite timing deterministic regardless.
export AGENT_BROWSER_ARGS="--disable-features=BackForwardCache"
BASE_URL="http://127.0.0.1:${E2E_FRONTEND_PORT}"   # browser entry (vite preview, proxies /api)
API_URL="http://127.0.0.1:${E2E_PORT}"             # loom serve API
REPORT_DIR="$SCRIPT_DIR/reports"
mkdir -p "$REPORT_DIR"
STACK_LOCK="$REPO_ROOT/tmp/aft-stack.${E2E_FRONTEND_PORT}.lock"
LOCK_WRITTEN=""

port_listener_pids() {
    command -v lsof >/dev/null 2>&1 || return 0
    lsof -ti "TCP:$1" -sTCP:LISTEN 2>/dev/null || true
}

is_our_stack_cmd() {
    local cmd="$1"
    case "$cmd" in
        *"$REPO_ROOT/tmp/loom-e2e"*|*"$REPO_ROOT/tmp/fleet-db"*) return 0 ;;
        *"vite preview"*"--port ${E2E_FRONTEND_PORT}"*) return 0 ;;
        *) return 1 ;;
    esac
}

lock_owner_alive() {
    [[ -f "$STACK_LOCK" ]] || return 1
    local pid
    pid="$(cat "$STACK_LOCK" 2>/dev/null || true)"
    [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null
}

preflight_port() {
    local port="$1"
    local role="$2"
    local pid cmd owner ours=0 foreign=0
    local mine=()

    for pid in $(port_listener_pids "$port"); do
        cmd="$(ps -o command= -p "$pid" 2>/dev/null || true)"
        if is_our_stack_cmd "$cmd"; then
            ours=1
            mine+=("$pid")
        else
            foreign=1
            echo "[aft] port $port ($role) held by foreign process: pid $pid - $cmd" >&2
        fi
    done

    if [[ "$foreign" == "1" ]]; then
        echo "[aft] refusing to start: $role port $port is owned by a process that is not this harness's stack." >&2
        echo "[aft] stop it, or rerun with E2E_PORT=/E2E_FRONTEND_PORT= on a free port (demos: 'make demo' uses 8190/3190)." >&2
        exit 1
    fi

    if [[ "$ours" == "1" ]]; then
        if lock_owner_alive; then
            owner="$(cat "$STACK_LOCK" 2>/dev/null || true)"
            echo "[aft] refusing to start: another run-aft owns port $port (lock pid $owner)." >&2
            exit 1
        fi

        echo "[aft] reaping our own leftover stack on $role port $port (pids: ${mine[*]})..."
        kill "${mine[@]}" 2>/dev/null || true
        for _ in $(seq 1 10); do
            [[ -z "$(port_listener_pids "$port")" ]] && break
            sleep 1
        done
        if [[ -n "$(port_listener_pids "$port")" ]]; then
            echo "[aft] port $port still busy after reap" >&2
            exit 1
        fi
    fi
}

preflight_ports() {
    command -v lsof >/dev/null 2>&1 || return 0
    preflight_port "$E2E_PORT" api
    preflight_port "$E2E_FRONTEND_PORT" frontend
}

# Real-backend tiers: AFT_REAL_BACKEND=<codex|claude|opencode|cursor> lets ONE real
# agent CLI resolve on the server's PATH while every other agent CLI stays stubbed.
# AFT_REAL_CODEX=1 is the back-compat alias for AFT_REAL_BACKEND=codex; a conflicting
# AFT_REAL_BACKEND is a hard error — never guess which paid backend to run.
if [[ "${AFT_REAL_CODEX:-}" == "1" ]]; then
    if [[ -n "${AFT_REAL_BACKEND:-}" && "$AFT_REAL_BACKEND" != "codex" ]]; then
        echo "[aft] conflicting real-tier selection: AFT_REAL_CODEX=1 means codex, but AFT_REAL_BACKEND=$AFT_REAL_BACKEND" >&2
        echo "[aft] unset one of them (is AFT_REAL_BACKEND exported in your shell?)" >&2
        exit 1
    fi
    AFT_REAL_BACKEND=codex
fi

REAL_BIN=""
REAL_STUB_DIR=""
REAL_UNSET_FLAGS=""   # `env` flags applied to the server launch, e.g. "-u OPENAI_API_KEY"
if [[ -n "${AFT_REAL_BACKEND:-}" ]]; then
    case "$AFT_REAL_BACKEND" in
        codex)
            REAL_BIN=codex
            REAL_STUB_DIR="stubs-real-codex"
            # unset so codex uses the ChatGPT-account auth.json, not API billing
            REAL_UNSET_FLAGS="-u OPENAI_API_KEY"
            ;;
        claude)
            REAL_BIN=claude
            REAL_STUB_DIR="stubs-real-claude"
            # unset so claude uses the subscription OAuth login, not API billing
            REAL_UNSET_FLAGS="-u ANTHROPIC_API_KEY"
            ;;
        opencode)
            REAL_BIN=opencode
            REAL_STUB_DIR="stubs-real-opencode"
            REAL_UNSET_FLAGS=""   # opencode reads its own provider auth config
            ;;
        cursor)
            REAL_BIN=cursor-agent
            REAL_STUB_DIR="stubs-real-cursor"
            # unset so cursor-agent uses the account login, not API billing
            REAL_UNSET_FLAGS="-u CURSOR_API_KEY"
            ;;
        *)
            echo "[aft] unknown AFT_REAL_BACKEND '$AFT_REAL_BACKEND' (want codex|claude|opencode|cursor)" >&2
            exit 1
            ;;
    esac
    # Strip host GitHub credentials from serve: an inherited PAT flips the degraded
    # 503 egress_unavailable contract into a live connector-seed + egress attempt
    # with the operator's real PAT.
    # Also strip the SSH agent socket and force git to fail rather than prompt or
    # silently authenticate. Stripping the three GitHub token vars and stubbing `gh`
    # closes the obvious egress paths, but a real model in a live tier still inherits
    # HOME, so `git push https://github.com/...` could authenticate through the
    # operator's credential helper, or `git push git@github.com:...` through their
    # ssh-agent — and NEITHER shows up in the fake upstream's /__requests log or in
    # the local bare repo's refs, so LP-1's read-only proof would stay green while a
    # real repository was mutated. Every remote these suites use is a file:// path,
    # so nothing legitimate needs an agent or a credential prompt.
    REAL_UNSET_FLAGS="${REAL_UNSET_FLAGS:+$REAL_UNSET_FLAGS }-u LOOM_WEBUI_GITHUB_TOKEN -u GH_TOKEN -u GITHUB_TOKEN -u GEMINI_API_KEY -u GOOGLE_API_KEY -u SSH_AUTH_SOCK"

    echo "[aft] ====================================================================="
    echo "[aft] REAL $(printf '%s' "$AFT_REAL_BACKEND" | tr '[:lower:]' '[:upper:]') MODE -- server $REAL_BIN is the operator's real CLI"
    echo "[aft] Every other agent CLI stays stubbed (e2e/$REAL_STUB_DIR)."
    echo "[aft] The run consumes the $AFT_REAL_BACKEND account's rate-limit window."
    echo "[aft] ====================================================================="
    if ! REAL_BIN_PATH="$(command -v "$REAL_BIN" 2>/dev/null)"; then
        echo "[aft] real $AFT_REAL_BACKEND tier needs $REAL_BIN on PATH" >&2
        exit 1
    fi
    case "$REAL_BIN_PATH" in
        "$REPO_ROOT"/e2e/stubs*)
            echo "[aft] $REAL_BIN resolved to a test stub ($REAL_BIN_PATH); real tier needs the operator's real CLI" >&2
            exit 1
            ;;
    esac
    case "$AFT_REAL_BACKEND" in
        codex)
            # CODEX_HOME is what the backend itself honors (backend_codex.go) and it
            # rides the subprocess allowlist, so the preflight must validate the same
            # home the server will hand the child — not a hard-coded ~/.codex.
            codex_home="${CODEX_HOME:-$HOME/.codex}"
            if [[ ! -f "$codex_home/auth.json" ]]; then
                echo "[aft] $codex_home/auth.json missing -- run 'codex login' before make test-aft-real" >&2
                exit 1
            fi
            ;;
        claude)
            if [[ ! -f "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/.credentials.json" ]]; then
                echo "[aft] ${CLAUDE_CONFIG_DIR:-$HOME/.claude}/.credentials.json missing -- log in to Claude Code first" >&2
                exit 1
            fi
            ;;
        cursor)
            if ! "$REAL_BIN_PATH" status >/dev/null 2>&1; then
                echo "[aft] cursor-agent is not logged in -- run 'cursor-agent login' before make test-aft-real-cursor" >&2
                exit 1
            fi
            ;;
        opencode)
            : # binary-only HealthCheck; provider auth lives in opencode's own config
            ;;
    esac
    # Inject the selected real binary through a per-run symlink dir instead of
    # relying on the host PATH tail to supply it. The stub farm deliberately has
    # no entry for the selected backend, so the farm still shadows every OTHER
    # agent CLI while this dir supplies exactly one real one — and the closure
    # assertion below can then name where each CLI came from.
    # `command -v` can return a relative path; a relative symlink target would be
    # resolved against the injection dir and dangle.
    case "$REAL_BIN_PATH" in
        /*) : ;;
        *)  REAL_BIN_PATH="$(cd "$(dirname "$REAL_BIN_PATH")" && pwd -P)/$(basename "$REAL_BIN_PATH")" ;;
    esac
    if [[ ! -x "$REAL_BIN_PATH" ]]; then
        echo "[aft] resolved real backend '$REAL_BIN_PATH' is not executable" >&2
        exit 1
    fi
    REAL_INJECT_DIR="$REPORT_DIR/real-bin/$AFT_REAL_BACKEND"
    rm -rf "$REAL_INJECT_DIR"
    mkdir -p "$REAL_INJECT_DIR"
    ln -sf "$REAL_BIN_PATH" "$REAL_INJECT_DIR/$REAL_BIN"

    # Baseline of pre-existing processes for this binary. Anything matching that is
    # NOT in this set after the run was started by us, which is what makes the
    # post-run leak check precise instead of tripping on the operator's own CLI.
    # Baseline on the BASENAME, matching live-sweep.sh: loom execs the backend by name
    # (codex_runtime.go defaultCodexBinary), so the child's argv is `codex app-server …`
    # and a baseline keyed on the resolved path would never line up with what the sweep
    # can see. Both sides must use the same matcher or the diff is meaningless.
    LIVE_PID_BASELINE="$REPORT_DIR/live-pid-baseline.txt"
    # Must stay byte-identical to live-sweep.sh's matcher: anchored to argv[0] so
    # `--backend codex` and ~/Library/Caches/com.openai.codex/* are not mistaken for
    # backend processes. A baseline built with a different matcher than the sweep
    # makes the diff meaningless in both directions.
    pgrep -f "^([^ ]*/)?$(basename "$REAL_BIN_PATH")([[:space:]]|$)" > "$LIVE_PID_BASELINE" 2>/dev/null || : > "$LIVE_PID_BASELINE"

    : "${AFT_TIMEOUT:=600000}"
    if [[ "$AFT_REAL_BACKEND" == "codex" ]]; then
        : "${AFT_SUITES:=$SCRIPT_DIR/real-suites}"
    else
        : "${AFT_SUITES:=$SCRIPT_DIR/real-suites-$AFT_REAL_BACKEND}"
    fi
fi

# Live + codex: prove the controlled lead runtime can start before spending a stack
# boot and a rate window on discovering it cannot. Interactive live cases are the
# only ones that need it, and they are exactly the ones this tier exists for.
if [[ -n "$AFT_LIVE" && -z "$AFT_WITH_DAEMON" && "${AFT_REAL_BACKEND:-}" == "codex" ]]; then
    if ! bash "$SCRIPT_DIR/scripts/live-preflight-codex-appserver.sh" "$REAL_BIN_PATH"; then
        echo "[aft] refusing to start the live tier: the codex lead runtime cannot come up on this machine" >&2
        exit 1
    fi
fi

# Every agent CLI the server can resolve must come from the stub farm or, in a
# real tier, from the one injected real binary. Full PATH closure is deliberately
# NOT attempted — serve legitimately needs the host toolchain (git, node, python3)
# — so this asserts the property that actually matters: no un-stubbed agent CLI is
# reachable. A backend added without a stub fails the run instead of silently
# invoking the operator's install.
assert_server_cli_closure() {
    local server_path="$1" farm="$2" inject="${3:-}" tool resolved
    for tool in codex claude cursor-agent opencode gemini gh; do
        resolved="$(PATH="$server_path" command -v "$tool" 2>/dev/null || true)"
        [[ -n "$resolved" ]] || continue
        case "$resolved" in
            "$farm"/*) ;;
            *) if [[ -n "$inject" && "$resolved" == "$inject"/* ]]; then continue; fi
               echo "[aft] refusing to start: the server would resolve '$tool' to $resolved" >&2
               echo "[aft] that is outside the stub farm ($farm)${inject:+ and the injected real binary ($inject)}; add a stub in e2e/stubs/ first." >&2
               return 1 ;;
        esac
    done
    return 0
}

# dist/cli.js alone is not enough: it imports from node_modules at runtime (zod
# etc.), and cleanup tools sweep node_modules while keeping dist.
if [[ ! -f "$AFT_DIR/dist/cli.js" || ! -d "$AFT_DIR/node_modules" ]]; then
    echo "[aft] installing/building aft in $AFT_DIR..."
    (cd "$AFT_DIR" && npm install --silent && npm run build --silent)
fi

# The browser itself gets swept too: verify agent-browser can launch Chrome,
# self-healing via `agent-browser install` + config repair when it can't (macOS
# only; no-op on Linux/CI, which provisions its own browser).
bash "$SCRIPT_DIR/scripts/ensure-agent-browser.sh"

# Same story for flue: the serve-side epic-runner bundle build needs the runtime
# package's deps. Non-fatal on failure — matching the FLUE_REPO-absent behavior,
# where agent-flow suites fail with a clear workflow error and everything else runs.
if [[ -n "$FLUE_REPO" && ! -d "$FLUE_REPO/packages/runtime/node_modules" ]]; then
    if command -v pnpm >/dev/null 2>&1; then
        echo "[aft] flue node_modules missing — running pnpm install in $FLUE_REPO..."
        (cd "$FLUE_REPO" && pnpm install --frozen-lockfile) \
            || echo "[aft] flue install failed; agent-flow suites will fail with a workflow error" >&2
    else
        echo "[aft] flue node_modules missing and pnpm not on PATH; agent-flow suites will fail with a workflow error" >&2
    fi
fi

# The e2e script builds tmp/fleet-db only when MISSING — switching FLEET_DB_REPO (or
# advancing its checkout) would silently reuse a binary with different behavior
# (driver-runs domain, title upsert). Stamp the source repo+SHA and rebuild on change.
FLEET_DB_BIN="$REPO_ROOT/tmp/fleet-db"
FLEET_DB_STAMP="$REPO_ROOT/tmp/fleet-db.source"
FLEET_DB_SRC="$FLEET_DB_REPO@$(git -C "$FLEET_DB_REPO" rev-parse HEAD 2>/dev/null || echo unknown)"
if [[ -x "$FLEET_DB_BIN" && "$(cat "$FLEET_DB_STAMP" 2>/dev/null || true)" != "$FLEET_DB_SRC" ]]; then
    echo "[aft] fleet-db source changed ($FLEET_DB_SRC) — rebuilding binary..."
    (cd "$FLEET_DB_REPO" && CGO_ENABLED=0 go build -o "$FLEET_DB_BIN" ./cmd/fleet-db)
fi
mkdir -p "$REPO_ROOT/tmp" && printf '%s\n' "$FLEET_DB_SRC" > "$FLEET_DB_STAMP"

SERVER_PID=""
FAKE_GH_PID=""
FAKE_GH_BASE=""

# A GitHub-shaped REST fixture, so the PR-review tier can exercise the real
# connector path without touching github.com. Only started for the live PR tier —
# every other tier keeps the degraded/no-credential contract the existing suites
# assert. Started BEFORE serve so its base URL can be injected into the server env.
start_fake_github() {
    local log="$REPORT_DIR/fake-github.log" port
    : > "$log"
    node "$SCRIPT_DIR/fixtures/fake-github/server.mjs" >>"$log" 2>&1 &
    FAKE_GH_PID=$!
    for _ in $(seq 1 30); do
        port="$(grep -oE 'listening [0-9]+' "$log" 2>/dev/null | awk '{print $2}' | head -1)"
        [[ -n "$port" ]] && break
        if ! kill -0 "$FAKE_GH_PID" 2>/dev/null; then
            echo "[aft] fake-github exited during startup" >&2
            tail -20 "$log" >&2 || true
            return 1
        fi
        sleep 0.5
    done
    if [[ -z "$port" ]]; then
        echo "[aft] fake-github never reported a port" >&2
        tail -20 "$log" >&2 || true
        return 1
    fi
    FAKE_GH_BASE="http://127.0.0.1:$port"
    export AFT_FAKE_GH_BASE="$FAKE_GH_BASE"
    echo "[aft] fake-github ready at $FAKE_GH_BASE (log: $log)"
}

stop_fake_github() {
    [[ -n "$FAKE_GH_PID" ]] || return 0
    kill "$FAKE_GH_PID" 2>/dev/null || true
    wait "$FAKE_GH_PID" 2>/dev/null || true
    FAKE_GH_PID=""
}

DAEMON_PID=""
DAEMON_PID_FILE="$REPORT_DIR/daemon.pid"
DAEMON_LOG="$REPORT_DIR/daemon.log"

stop_owned_daemon() {
    local pid rc pgid
    pid="${DAEMON_PID:-}"
    [[ -n "$pid" ]] || return 0

    if ! kill -0 "$pid" 2>/dev/null; then
        if wait "$pid"; then rc=0; else rc=$?; fi
        echo "[aft] daemon exited before harness teardown (pid $pid, exit $rc)" >&2
        tail -40 "$DAEMON_LOG" 2>/dev/null || true
        DAEMON_PID=""
        rm -f "$DAEMON_PID_FILE"
        return 1
    fi

    echo "[aft] stopping owned daemon (pid $pid)..."
    # SIGINT, not SIGTERM. `loom daemon` cannot shut down gracefully on SIGTERM:
    # root.go's trace-flush handler calls signal.Reset(SIGTERM) then re-raises, which
    # kills the process before the daemon's own handler drains anything — measured
    # exit 143 with zero shutdown markers, while SIGINT and SIGHUP both exit 0 with a
    # full graceful shutdown (FINDINGS §1.24, reproduced 3/3). Sending TERM here made
    # every worker-tier run report daemon=failed for a product bug the harness cannot
    # fix, masking whether cleanup actually worked. Revert to TERM once §1.24 lands.
    kill -INT "$pid" 2>/dev/null || true
    # runDaemonMainLoop allows a bounded 30s supervisor drain plus 10s for its
    # state writer. Give that real graceful path room before declaring a leak.
    for _ in $(seq 1 45); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 1
    done
    if kill -0 "$pid" 2>/dev/null; then
        # The daemon isolates itself into a process group. Kill the group only
        # after proving the group id is exactly the explicit owned pid; otherwise
        # fall back to the one process rather than risking an unrelated group.
        pgid="$(ps -o pgid= -p "$pid" 2>/dev/null | tr -d ' ' || true)"
        if [[ "$pid" =~ ^[1-9][0-9]*$ && "$pgid" == "$pid" ]]; then
            kill -KILL -- "-$pid" 2>/dev/null || true
        else
            kill -KILL "$pid" 2>/dev/null || true
        fi
        wait "$pid" 2>/dev/null || true
        echo "[aft] daemon did not stop gracefully; forced termination was required" >&2
        tail -40 "$DAEMON_LOG" 2>/dev/null || true
        DAEMON_PID=""
        rm -f "$DAEMON_PID_FILE"
        return 1
    fi
    if wait "$pid"; then rc=0; else rc=$?; fi
    DAEMON_PID=""
    rm -f "$DAEMON_PID_FILE"
    if [[ "$rc" != "0" ]] || ! grep -q "Daemon stopped\." "$DAEMON_LOG" 2>/dev/null; then
        echo "[aft] daemon teardown was not clean (exit $rc or missing stop marker)" >&2
        tail -40 "$DAEMON_LOG" 2>/dev/null || true
        return 1
    fi
    echo "[aft] owned daemon stopped cleanly"
    return 0
}

start_owned_daemon() {
    local daemon_cwd="$REPO_ROOT/tmp/e2e-workspace" rc
    : > "$DAEMON_LOG"
    rm -f "$DAEMON_PID_FILE"
    echo "[aft] starting owned daemon for E2E-WS (log: $DAEMON_LOG)..."
    (
        cd "$daemon_cwd"
        # Keep the daemon on the same isolated FleetDB/config home and the same
        # closed agent-CLI PATH as serve. LOOM_SERVER_URL makes leaf `loom data`
        # calls traverse the live server API while daemon config still comes from
        # the shared FleetDB store.
        # $REAL_UNSET_FLAGS expands to trusted `env -u NAME` pairs assembled above.
        # shellcheck disable=SC2086
        exec env $REAL_UNSET_FLAGS \
            -u LOOM_WORKSPACE_RUNTIME_DIR -u LOOM_AGENT_NAME -u LOOM_AGENT_ROLE \
            -u LOOM_AGENT_TERMINAL_ID -u LOOM_SESSION_ID -u LOOM_NOTIFY_TOKEN \
            -u LOOM_DESKTOP_DATA_DIR -u LOOM_FRONTEND_DIR -u LOOM_WEBUI_URL \
            -u LOOM_LOCAL_RUNTIME -u LOOM_FLEET_DB_URL \
            PATH="$SERVER_PATH" LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" \
            LOOM_WORKSPACE="E2E-WS" LOOM_SERVER_URL="$API_URL" \
            LOOM_ISSUE_BACKEND="fleetdb" LOOM_FLEET_DB_ACTOR="loom-aft-daemon" \
            LOOM_MAX_BUDGET_USD="${AFT_LIVE_MAX_BUDGET_USD:-5.00}" \
            "$AFT_LOOM_BIN" daemon
    ) >>"$DAEMON_LOG" 2>&1 &
    DAEMON_PID=$!
    printf '%s\n' "$DAEMON_PID" > "$DAEMON_PID_FILE"

    for _ in $(seq 1 30); do
        if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
            if wait "$DAEMON_PID"; then rc=0; else rc=$?; fi
            echo "[aft] daemon exited during startup (exit $rc)" >&2
            tail -40 "$DAEMON_LOG" 2>/dev/null || true
            DAEMON_PID=""
            rm -f "$DAEMON_PID_FILE"
            return 1
        fi
        if grep -q "Loom Agent Supervisor" "$DAEMON_LOG" 2>/dev/null; then
            # The banner is printed immediately before daemon.Start. A short
            # stability margin catches post-banner initialization failures.
            sleep 2
            if kill -0 "$DAEMON_PID" 2>/dev/null; then
                export AFT_DAEMON_PID="$DAEMON_PID"
                export AFT_DAEMON_LOG="$DAEMON_LOG"
                echo "[aft] daemon ready: pid $DAEMON_PID, initial agents 0"
                return 0
            fi
        fi
        sleep 1
    done
    echo "[aft] daemon did not reach its startup banner" >&2
    stop_owned_daemon || true
    return 1
}

cleanup() {
    stop_fake_github
    local port pid cmd
    stop_owned_daemon || true
    if [[ "$LIVE_LOCK_WRITTEN" == "1" && -n "$LIVE_ACCOUNT_LOCK" ]]; then
        rm -f "$LIVE_ACCOUNT_LOCK"
    fi
    if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
        echo "[aft] stopping e2e stack (pid $SERVER_PID)..."
        kill "$SERVER_PID" 2>/dev/null || true   # fires the script's own trap (kills loom + preview)
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    if [[ "$LOCK_WRITTEN" == "1" ]]; then
        # belt-and-braces, but only our-signature listeners on this run's ports
        for port in "$E2E_PORT" "$E2E_FRONTEND_PORT"; do
            for pid in $(port_listener_pids "$port"); do
                cmd="$(ps -o command= -p "$pid" 2>/dev/null || true)"
                is_our_stack_cmd "$cmd" && kill "$pid" 2>/dev/null || true
            done
        done
        rm -f "$STACK_LOCK"
    fi
}
trap cleanup EXIT INT TERM

preflight_ports
echo "$$" > "$STACK_LOCK"
LOCK_WRITTEN=1


# PR-review live tier only: every other tier keeps the degraded-connector contract.
if [[ -n "$AFT_LIVE" && "${AFT_LIVE_SUITE_KIND:-}" == "prreview" ]]; then
    start_fake_github || exit 1
fi
echo "[aft] starting e2e stack (api :${E2E_PORT}, frontend :${E2E_FRONTEND_PORT}; log: $REPORT_DIR/server.log)..."
# Stub AI backends — scoped to the SERVER process only, never this script's env:
#  - e2e/stubs first on serve's PATH makes `codex`/`claude` resolve to the harmless
#    stubs, so agent runs (and the terminal view's auto-spawned lead) never invoke a
#    real LLM. The driver env allowlist strips LOOM_CODEX_BIN, so PATH is the lever.
#  - OPENAI_API_KEY satisfies the codex HealthCheck in the workflow-run preflight;
#    the stub never reads it.
# aft itself must NOT see this PATH: the recovery agent's `claude` is the real one.
# The builtin-workflow bundle build also needs the flue CLI itself; point serve at
# the built cli entry (node script) so nothing has to be on PATH.
FLUE_CMD_JSON=""
if [[ -n "$FLUE_REPO" && -f "$FLUE_REPO/packages/cli/bin/flue.mjs" ]]; then
    FLUE_CMD_JSON="[\"node\",\"$FLUE_REPO/packages/cli/bin/flue.mjs\"]"
fi
if [[ -n "${AFT_REAL_BACKEND:-}" ]]; then
    SERVER_PATH="$REPO_ROOT/e2e/$REAL_STUB_DIR:$REAL_INJECT_DIR:$PATH"
    assert_server_cli_closure "$SERVER_PATH" "$REPO_ROOT/e2e/$REAL_STUB_DIR" "$REAL_INJECT_DIR" || exit 1
    # Pin the controlled lead runtime. An inherited LOOM_LEAD_CONTROLLED=0|false|no|off
    # makes loom fall back to a plain interactive launch (backends/harness_lead_runtime.go),
    # which would let a live case produce its artifact WITHOUT exercising the app-server
    # path the tier exists to test — a green run proving the wrong thing.
    LIVE_LEAD_CONTROLLED="LOOM_LEAD_CONTROLLED=1"
    # $REAL_UNSET_FLAGS is deliberately unquoted: it expands to "-u VAR" pairs or nothing.
    # shellcheck disable=SC2086
    env $REAL_UNSET_FLAGS E2E_PORT="$E2E_PORT" E2E_FRONTEND_PORT="$E2E_FRONTEND_PORT" FLEET_DB_REPO="$FLEET_DB_REPO" \
        PATH="$SERVER_PATH" FLUE_REPO="$FLUE_REPO" \
        "$LIVE_LEAD_CONTROLLED" \
        ${FAKE_GH_BASE:+LOOM_CONNECTOR_GITHUB_BASE_URL="$FAKE_GH_BASE"} \
        GIT_TERMINAL_PROMPT=0 GIT_ASKPASS=/usr/bin/false SSH_ASKPASS=/usr/bin/false \
        LOOM_REAL_FLUE_CMD_JSON="$FLUE_CMD_JSON" \
        bash "$REPO_ROOT/scripts/start-e2e-server.sh" >"$REPORT_DIR/server.log" 2>&1 &
else
    # Strip host GitHub credentials from serve: an inherited PAT flips the degraded
    # 503 egress_unavailable contract into a live connector-seed + egress attempt
    # with the operator's real PAT. The deterministic tier also needs no Gemini keys.
    # Load-bearing: the seed resolver's fallback reads a sealed settings credential; that is only safe because scripts/start-e2e-server.sh exports LOOM_CONFIG_DIR to tmp/e2e-workspace/.loom-config (wiped per run) — do not remove that export.
    SERVER_PATH="$REPO_ROOT/e2e/stubs:$PATH"
    assert_server_cli_closure "$SERVER_PATH" "$REPO_ROOT/e2e/stubs" || exit 1
    env -u LOOM_WEBUI_GITHUB_TOKEN -u GH_TOKEN -u GITHUB_TOKEN \
        -u GEMINI_API_KEY -u GOOGLE_API_KEY \
        E2E_PORT="$E2E_PORT" E2E_FRONTEND_PORT="$E2E_FRONTEND_PORT" FLEET_DB_REPO="$FLEET_DB_REPO" \
        PATH="$SERVER_PATH" OPENAI_API_KEY="stub-e2e" FLUE_REPO="$FLUE_REPO" \
        ${FAKE_GH_BASE:+LOOM_CONNECTOR_GITHUB_BASE_URL="$FAKE_GH_BASE"} \
        LOOM_REAL_FLUE_CMD_JSON="$FLUE_CMD_JSON" \
        bash "$REPO_ROOT/scripts/start-e2e-server.sh" >"$REPORT_DIR/server.log" 2>&1 &
fi
SERVER_PID=$!

READY=""
for _ in $(seq 1 180); do
    if curl -sf "$BASE_URL/api/config" >/dev/null 2>&1; then READY=1; break; fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        echo "[aft] server exited early — tail of server.log:"
        tail -30 "$REPORT_DIR/server.log"
        exit 1
    fi
    sleep 1
done
if [[ -z "$READY" ]]; then
    echo "[aft] stack not ready after 180s — tail of server.log:"
    tail -30 "$REPORT_DIR/server.log"
    exit 1
fi
# settle: the preview must serve the app shell consistently before browsers connect
for _ in $(seq 1 30); do
    if curl -sf "$BASE_URL/" >/dev/null 2>&1; then break; fi
    sleep 1
done
sleep 1
echo "[aft] stack ready: browser $BASE_URL, api $API_URL"

export AFT_BASE_URL="$BASE_URL"
export AFT_API_URL="$API_URL"
export AFT_WS="E2E-WS"   # primary workspace id seeded by start-e2e-server.sh
export AFT_LOOM_BIN="$REPO_ROOT/tmp/loom-e2e"
export AFT_LOOM_CONFIG_DIR="$REPO_ROOT/tmp/e2e-workspace/.loom-config"
export LOOM_BASE_URL="$API_URL"
export RUN_ID="${RUN_ID:-$(date +%s)}"
export AFT_TESTS_DIR="$SCRIPT_DIR"
export AFT_REPORT_DIR="$REPORT_DIR"
# Load-bearing for the live tier: its suite is parameterized by backend (the modal
# select value, the teardown script's argument), and aft interpolates ${VAR} from
# its OWN process env. A backend chosen with --real-backend is a plain shell
# variable until this line, so without the export `--real-backend claude` would
# silently select codex inside the suite while the server ran real claude.
if [[ -n "${AFT_REAL_BACKEND:-}" ]]; then
    export AFT_REAL_BACKEND
fi
# `_work`, not `work`: the go tool ignores directories beginning with `_` or `.`.
# Live suites seed throwaway git repos here, and the PR-review fixture seeds a Go
# file — under a plain `work/` those became real packages in `go test ./...`, whose
# coverage lines corrupted the profile and failed `make check` at the coverage gate
# with a decidedly unhelpful "line \"1 1\" doesn't match expected format".
export AFT_WORK_DIR="$REPORT_DIR/_work/$RUN_ID"  # scratch space for run-step state (issue ids etc.)
mkdir -p "$AFT_WORK_DIR"

if [[ -n "$AFT_WITH_DAEMON" ]]; then
    start_owned_daemon
fi

AFT_NO_AGENT=""
for arg in "$@"; do
    if [[ "$arg" == "--no-agent" ]]; then
        AFT_NO_AGENT=1
        break
    fi
done
if [[ -n "$AFT_NO_AGENT" && -z "${AFT_REAL_BACKEND:-}" ]]; then
    # TSK-D10 incident guard, 2026-07-29: deterministic run: steps that miss the
    # stub PATH must fail on empty auth homes instead of reaching an operator CLI.
    AFT_EMPTY_AUTH_DIR="$REPORT_DIR/empty-auth"
    mkdir -p "$AFT_EMPTY_AUTH_DIR/codex" "$AFT_EMPTY_AUTH_DIR/claude"
    export CODEX_HOME="$AFT_EMPTY_AUTH_DIR/codex"
    export CLAUDE_CONFIG_DIR="$AFT_EMPTY_AUTH_DIR/claude"
    unset CURSOR_API_KEY
fi

# Regenerate the coverage census from the frontend source so it always matches
# this checkout; aft joins run traces against it and reports untouched surface.
CENSUS="$AFT_WORK_DIR/census.json"
python3 "$SCRIPT_DIR/scripts/gen-census.py" --frontend "$REPO_ROOT/internal/webui/frontend/src" --out "$CENSUS" \
    || CENSUS=""   # census is reporting-only; never fail the run over it

# Loom's six-column board is dense — the agent-browser default viewport (1280x577)
# cuts it off; 1920x1080 shows the full board in screenshots and recordings.
# macOS: a sleeping display freezes Chrome's compositor — rendering frames stop, CSS
# @starting-style transitions never complete (elements stuck at opacity:0 read as
# invisible), Playwright actionability waits time out, and screenshots fail. Verified:
# 0 rAF frames/3s with the display asleep vs 182 awake. Keep the display awake for
# the duration of the run; -u wakes it at start. No-op on Linux/CI.
CAFFEINATE=""
command -v caffeinate >/dev/null 2>&1 && CAFFEINATE="caffeinate -dimsu"

# The deterministic default is one aft invocation over both tiers so reporting and
# census joins stay combined. Real-* tiers set AFT_SUITES above and therefore keep
# their exact single-directory override.
if [[ -n "${AFT_SUITES+x}" ]]; then
    AFT_SUITE_PATHS=("$AFT_SUITES")
else
    AFT_SUITE_PATHS=("$SCRIPT_DIR/suites" "$SCRIPT_DIR/surface-suites")
fi

# 15s step timeout (aft default is 8s): the Loom SPA leans on SSE + polling stores,
# and under CI/host load its reactions legitimately stretch past 8s — measured as
# whole-suite flake storms on a busy machine.
RUN_STARTED_AT="$(date +%s)"
set +e
$CAFFEINATE node "$AFT_DIR/dist/cli.js" run "${AFT_SUITE_PATHS[@]}" --report-dir "$REPORT_DIR" \
    --viewport "${AFT_VIEWPORT:-1920x1080}" --timeout "${AFT_TIMEOUT:-15000}" \
    ${CENSUS:+--census "$CENSUS"} \
    ${AFT_MAX_BROWSERS:+--max-browsers "$AFT_MAX_BROWSERS"} "$@"
AFT_EXIT=$?
set -e

# Live cleanup runs while the stack is still up (the EXIT trap has not fired), pass
# or fail. It is the authoritative one: aft skips a test's own cleanup step after
# any earlier failure, and treats teardown failure as report-only. Its verdict can
# only make the run fail, never pass — a green suite that leaked a paid process is
# not a green run. Cleanup goes first; the ledger is bookkeeping and must not delay it.
if [[ -n "$AFT_LIVE" ]]; then
    DAEMON_CLEANUP_VERDICT="not-requested"
    if [[ -n "$AFT_WITH_DAEMON" ]]; then
        if stop_owned_daemon; then
            DAEMON_CLEANUP_VERDICT="clean"
        else
            DAEMON_CLEANUP_VERDICT="failed"
            echo "[aft] daemon cleanup could not be proven — failing the run" >&2
            if [[ "$AFT_EXIT" == "0" ]]; then
                AFT_EXIT=1
            fi
        fi
    fi
    LIVE_SWEEP_VERDICT="clean"
    if ! bash "$SCRIPT_DIR/scripts/live-sweep.sh" "${REAL_BIN_PATH:-}" "${LIVE_PID_BASELINE:-}"; then
        LIVE_SWEEP_VERDICT="failed"
        echo "[aft] live cleanup could not be proven — failing the run" >&2
        # `[[ ... ]] && x=1` would return 1 when the guard is false and, under
        # `set -e`, exit here — skipping the ledger entirely.
        if [[ "$AFT_EXIT" == "0" ]]; then
            AFT_EXIT=1
        fi
    fi
    AFT_DAEMON_CLEANUP_VERDICT="$DAEMON_CLEANUP_VERDICT" \
    AFT_LIVE_SWEEP_VERDICT="$LIVE_SWEEP_VERDICT" \
    bash "$SCRIPT_DIR/scripts/live-ledger.sh" \
        "${AFT_REAL_BACKEND:-unknown}" "${REAL_BIN_PATH:-unknown}" \
        "${LIVE_CASE_COUNT:-0}" "$AFT_EXIT" "$(( $(date +%s) - RUN_STARTED_AT ))" || true
fi

exit "$AFT_EXIT"
