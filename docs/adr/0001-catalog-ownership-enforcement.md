# Catalog file ownership: soft enforcement, not deny rules

**Status:** accepted (2026-05-14)

Each YAML file in `catalog/` is owned by exactly one cataloger agent (`features.yaml` → `feature-cataloger`, etc.) and the contract is enforced by three soft layers — strict prompt language in each agent's `.md`, a `PreToolUse` advisory hook (`.claude/hooks/catalog-write-guard.sh`) that surfaces the owner on every write, and a `catalog-curator` audit mode that reports drift via `git diff`. No hard permission deny rules.

## Considered options

- **Hard `deny` rules in `.claude/settings.local.json`** — rejected. Claude Code permissions are session-scoped; subagents inherit the same rules. Denying `Edit(catalog/features.yaml)` blocks the legitimate `feature-cataloger` from doing its job as much as it blocks a rogue write.
- **Structured-diff gatekeeping** (catalogers emit diffs, curator validates and applies) — rejected as overkill at current scale (5 files, 6 agents). The diff format would be its own schema to maintain.

## Consequences

- A determined rogue agent can still write the wrong file; we accept this. The audit mode catches it after the fact.
- If hard blocking becomes necessary later, the hook's final `exit 0` can flip to `exit 2` — documented in `catalog/OWNERSHIP.md`.
- The `catalog-curator` is the only agent that may legitimately write multiple catalog files in a single run (cross-file migrations). Every such override is logged in its summary.

See `catalog/OWNERSHIP.md`, `catalog/.ownership.yaml`, `.claude/hooks/catalog-write-guard.sh`.
