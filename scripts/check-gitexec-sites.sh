#!/usr/bin/env bash
# Only gitexec runs git (Loom Git design §5 rule 1). Production code anywhere
# in the repo (non-test Go files; vendored, generated and testdata trees skipped) outside
# internal/loomgit/internal/gitexec may not call exec.Command("git", ...).
# The list holds the remaining legacy callers with exact site counts; it may
# only shrink. Unit tests are covered by check-no-raw-exec.sh.
set -euo pipefail
cd "$(dirname "$0")/.."
skip=(--exclude-dir=node_modules --exclude-dir=vendor --exclude-dir=third_party --exclude-dir=.git --exclude-dir=worktrees --exclude-dir=dist)

allowed='internal/cli/daemon/seed_worktree_cmd.go:1
internal/cli/exec.go:1
internal/cli/serve/opsimpl/repair_checkout.go:1
internal/cli/agent/tsruntime/tsruntime.go:1
internal/driver/run.go:4
internal/gitbranch/branch.go:1
internal/localworkspace/localworkspace.go:2
internal/skillmat/materialize.go:1
internal/stackpublish/gitutil.go:1'

# Parsed with go/ast (scripts/gitexecsites): any exec.Command/CommandContext
# whose program argument is the constant "git", however ctx is written.
actual=$(go run ./scripts/gitexecsites . | grep -v '^internal/loomgit/internal/gitexec/' || true)
allowed=$(printf '%s\n' "$allowed" | sort)

if [ "$actual" != "$allowed" ]; then
	echo "exec.Command(\"git\", ...) sites outside gitexec changed (want left, got right):" >&2
	diff <(printf '%s\n' "$allowed") <(printf '%s\n' "$actual") >&2 || true
	echo "Run Git through internal/loomgit; remove a deleted site from scripts/check-gitexec-sites.sh." >&2
	exit 1
fi
# No plain force push, clean -f or reset --hard outside gitexec guards (D27).
# P4.11 AC deviation (coordinator, 2026-10-02): only the P1.5 capture-first
# reset remains, until Reset moves into loomgit (design §2.4 follow-up).
allowed_writes='internal/cli/git/git_deps.go:1
internal/cli/git/reset_safety.go:1'
writes=$(grep -rEc --include='*.go' --include='*.ts' --include='*.mjs' --exclude='*_test.go' \
	--exclude='*.test.*' "${skip[@]}" \
	'"push"[^])]*"(--force|-f)"|"clean", *"-[a-z]*f|"reset", *"--hard"' . |
	grep -v ':0$' | sed 's#^\./##' | grep -v '^internal/loomgit/internal/gitexec/' | sort || true)
if [ "$writes" != "$allowed_writes" ]; then
	echo "force push, clean -f or reset --hard sites changed (want left, got right):" >&2
	diff <(printf '%s\n' "$allowed_writes") <(printf '%s\n' "$writes") >&2 || true
	exit 1
fi
echo "gitexec boundary: no new raw Git callers or destructive Git writes."
