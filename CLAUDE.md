# Rules

- NEVER modify `.git/config` or any files under `.git/` directly. Use `git config` commands only when explicitly instructed by the user. Changing git internals (e.g., `core.bare`, `core.worktree`) can break the repository and all worktrees.
- Read `AGENTS.md` for shared agent instructions and repo-local runbooks, including `.agent-skills/loom-pr-test/SKILL.md` for real Loom runtime testing.

## Agent skills

### Issue tracker

Issues live in fleet-db via the `loom data` CLI (not GitHub Issues). See `docs/agents/issue-tracker.md`.

### Triage labels

The five default canonical labels (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` + `docs/adr/` at the repo root; `docs/loom-glossary.md` is the existing vocabulary of record. See `docs/agents/domain.md`.
