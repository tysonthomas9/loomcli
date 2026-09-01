#!/usr/bin/env bash
# Test runner that automatically skips known broken tests

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SKIP_FILE="$REPO_ROOT/.test-skip"

# Scrub the inherited loom runtime environment before anything else runs
# (PUPPET-332). A fleet agent shell exports LOOM_WORKSPACE_RUNTIME_DIR pointing
# at the live workspace, and `go test` inheriting it appends real rows to the
# production session and usage ledgers. The in-process guard in
# internal/cli.GetWorkspaceRuntimeDir covers the test binary itself; this covers
# every subprocess a test spawns, which the in-process guard cannot. Re-exec is
# self-limiting via LOOM_TEST_ENV_CLEAN, and it happens BEFORE the
# LOOM_CONFIG_DIR setup below because the scrubber unsets that variable too.
if [[ -z "${LOOM_TEST_ENV_CLEAN:-}" ]]; then
    export LOOM_TEST_ENV_CLEAN=1
    exec "$SCRIPT_DIR/with-clean-loom-env.sh" "$SCRIPT_DIR/test.sh" "$@"
fi

# Isolate tests from the real ~/.loom so state-cache writes never clobber
# the user's workspace path registry (LOOMDEV-14).
if [[ -z "${LOOM_CONFIG_DIR:-}" ]]; then
    LOOM_CONFIG_DIR="$(mktemp -d)"
    export LOOM_CONFIG_DIR
    trap 'rm -rf "$LOOM_CONFIG_DIR"' EXIT
fi

# Build skip pattern from .test-skip file
build_skip_pattern() {
    if [[ ! -f "$SKIP_FILE" ]]; then
        echo ""
        return
    fi

    # Read non-comment, non-empty lines and join with |
    local pattern=$(grep -v '^#' "$SKIP_FILE" | grep -v '^[[:space:]]*$' | paste -sd '|' -)
    echo "$pattern"
}

# Default values
TIMEOUT="${TEST_TIMEOUT:-3m}"
SKIP_PATTERN=$(build_skip_pattern)
VERBOSE="${TEST_VERBOSE:-}"
RUN_PATTERN="${TEST_RUN:-}"
COVERAGE="${TEST_COVER:-}"
TAGS="${TEST_TAGS:-}"
COVERPROFILE="${TEST_COVERPROFILE:-/tmp/loom.coverage.out}"
COVERPKG="${TEST_COVERPKG:-}"
RACE="${TEST_RACE:-}"

# Parse arguments
PACKAGES=()
while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--verbose)
            VERBOSE="-v"
            shift
            ;;
        -timeout)
            TIMEOUT="$2"
            shift 2
            ;;
        -run)
            RUN_PATTERN="$2"
            shift 2
            ;;
        -skip)
            # Allow additional skip patterns
            if [[ -n "$SKIP_PATTERN" ]]; then
                SKIP_PATTERN="$SKIP_PATTERN|$2"
            else
                SKIP_PATTERN="$2"
            fi
            shift 2
            ;;
        -tags)
            TAGS="$2"
            shift 2
            ;;
        *)
            PACKAGES+=("$1")
            shift
            ;;
    esac
done

# Default to all packages if none specified
if [[ ${#PACKAGES[@]} -eq 0 ]]; then
    PACKAGES=("./...")
fi

# Build go test command
CMD=(go test -timeout "$TIMEOUT")

if [[ -n "$VERBOSE" ]]; then
    CMD+=(-v)
fi

if [[ -n "$SKIP_PATTERN" ]]; then
    CMD+=(-skip "$SKIP_PATTERN")
fi

if [[ -n "$RUN_PATTERN" ]]; then
    CMD+=(-run "$RUN_PATTERN")
fi

if [[ -n "$TAGS" ]]; then
    CMD+=(-tags "$TAGS")
fi

if [[ -n "$RACE" ]]; then
    CMD+=(-race)
fi

if [[ -n "$COVERAGE" ]]; then
    CMD+=(-covermode=atomic -coverprofile "$COVERPROFILE")
    if [[ -n "$COVERPKG" ]]; then
        CMD+=(-coverpkg "$COVERPKG")
    fi
fi

CMD+=("${PACKAGES[@]}")

echo "Running: ${CMD[*]}" >&2
if [[ -n "$SKIP_PATTERN" ]]; then
    echo "Skipping: $SKIP_PATTERN" >&2
fi
echo "" >&2

"${CMD[@]}"
status=$?

if [[ -n "$COVERAGE" ]]; then
    total=$(go tool cover -func="$COVERPROFILE" | awk '/^total:/ {print $NF}')
    echo "Total coverage: ${total} (profile: ${COVERPROFILE})" >&2
fi

exit $status
