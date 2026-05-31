#!/usr/bin/env bash
# Run the opt-in real backend CLI smoke and invalid-model tests.
#
# This intentionally invokes installed claude/codex/opencode binaries and can
# spend real tokens. It does not use the e2e stubs.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

usage() {
    cat <<EOF
Usage: scripts/test-real-clis.sh [OPTIONS]

Options:
  --backend NAME       Run one backend (claude, codex, opencode)
  --backends LIST      Comma/space-separated backend list
  --prompt TEXT        Prompt sent to each real CLI
  --timeout DURATION   Per-backend timeout (default: 3m)
  --keep               Keep the temporary smoke-test root
  --root DIR           Use and keep a specific smoke-test root
  --skip-missing       Skip selected backends whose binary is not on PATH
  --skip-invalid-model Skip invalid-model error classification checks
  --invalid-model NAME Model name used for invalid-model checks
  --require-cost       Require non-zero cost for every selected backend
  -h, --help           Show this help

Environment equivalents:
  LOOM_REAL_CLI_BACKENDS, LOOM_REAL_CLI_PROMPT, LOOM_REAL_CLI_TIMEOUT,
  LOOM_REAL_CLI_KEEP, LOOM_REAL_CLI_ROOT, LOOM_REAL_CLI_SKIP_MISSING,
  LOOM_REAL_CLI_SKIP_INVALID_MODEL, LOOM_REAL_CLI_INVALID_MODEL,
  LOOM_REAL_CLI_REQUIRE_COST

Examples:
  make test-real-clis
  scripts/test-real-clis.sh --backend claude --keep
  scripts/test-real-clis.sh --backends "codex opencode" --timeout 5m
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --backend)
            [[ -n "${2:-}" ]] || { echo "Error: --backend requires a value" >&2; exit 2; }
            export LOOM_REAL_CLI_BACKENDS="$2"
            shift 2
            ;;
        --backends)
            [[ -n "${2:-}" ]] || { echo "Error: --backends requires a value" >&2; exit 2; }
            export LOOM_REAL_CLI_BACKENDS="$2"
            shift 2
            ;;
        --prompt)
            [[ -n "${2:-}" ]] || { echo "Error: --prompt requires a value" >&2; exit 2; }
            export LOOM_REAL_CLI_PROMPT="$2"
            shift 2
            ;;
        --timeout)
            [[ -n "${2:-}" ]] || { echo "Error: --timeout requires a value" >&2; exit 2; }
            export LOOM_REAL_CLI_TIMEOUT="$2"
            shift 2
            ;;
        --keep)
            export LOOM_REAL_CLI_KEEP=1
            shift
            ;;
        --root)
            [[ -n "${2:-}" ]] || { echo "Error: --root requires a value" >&2; exit 2; }
            export LOOM_REAL_CLI_ROOT="$2"
            shift 2
            ;;
        --skip-missing)
            export LOOM_REAL_CLI_SKIP_MISSING=1
            shift
            ;;
        --skip-invalid-model)
            export LOOM_REAL_CLI_SKIP_INVALID_MODEL=1
            shift
            ;;
        --invalid-model)
            [[ -n "${2:-}" ]] || { echo "Error: --invalid-model requires a value" >&2; exit 2; }
            export LOOM_REAL_CLI_INVALID_MODEL="$2"
            shift 2
            ;;
        --require-cost)
            export LOOM_REAL_CLI_REQUIRE_COST=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

cd "$REPO_ROOT"

echo "Running real backend CLI smoke and invalid-model tests. This uses installed CLIs and may spend real tokens."
go test -tags realcli -count=1 -run 'TestRealCLI' -timeout "${GO_TEST_TIMEOUT:-15m}" -v ./internal/cli/backends
