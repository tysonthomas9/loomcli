#!/usr/bin/env bash
# Only gitexec runs git (Loom Git design §5 rule 1). Production code outside
# internal/loomgit/internal/gitexec may not call exec.Command("git", ...).
# The list holds the remaining legacy callers with exact site counts; it may
# only shrink. Unit tests are covered by check-no-raw-exec.sh.
set -euo pipefail
cd "$(dirname "$0")/.."

allowed='internal/cli/daemon/seed_worktree_cmd.go:1
internal/cli/exec.go:1
internal/cli/serve/opsimpl/repair_checkout.go:1
internal/cli/agent/tsruntime/tsruntime.go:1
internal/driver/run.go:4
internal/gitbranch/branch.go:1
internal/localworkspace/localworkspace.go:2
internal/skillmat/materialize.go:1
internal/stackpublish/gitutil.go:1'

actual=$(grep -rEc --include='*.go' --exclude='*_test.go' \
	'exec\.Command(Context)?\(([A-Za-z_][A-Za-z0-9_.]*(\(\))?, )?"git"' internal cmd |
	grep -v ':0$' | grep -v '^internal/loomgit/internal/gitexec/' | sort || true)
allowed=$(printf '%s\n' "$allowed" | sort)

if [ "$actual" != "$allowed" ]; then
	echo "exec.Command(\"git\", ...) sites outside gitexec changed (want left, got right):" >&2
	diff <(printf '%s\n' "$allowed") <(printf '%s\n' "$actual") >&2 || true
	echo "Run Git through internal/loomgit; remove a deleted site from scripts/check-gitexec-sites.sh." >&2
	exit 1
fi
echo "gitexec boundary: no new raw Git callers."
