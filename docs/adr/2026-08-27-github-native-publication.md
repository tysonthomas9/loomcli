# ADR: GitHub-native publication and merge commands

Status: accepted

## Decision

`loom push` publishes one feature branch with `git push` and never integrates
branches locally. Pull-request merging is a separate `loom merge` command that
delegates to `gh pr merge` with exactly one explicit method. Stack merging is a
separate `loom stack merge` command delegated to the official `github/gh-stack`
extension, with no internal fallback.

GitHub protection and merge state are authoritative. A repository whose
protection cannot be verified may still commit, publish feature branches, and
create pull requests, but Loom must not perform merges there.

## Consequences

This is a breaking CLI contract: target-branch positional arguments, aliases,
bulk push, workspace fan-out, and automatic stash/checkout/merge behavior are
removed. Published work remains in review until its GitHub pull request is
merged.
