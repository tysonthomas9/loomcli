# Agent Instructions

This project uses fleet-db-backed `loom data` commands for issue tracking.

## Shared Agent Runbooks

Agent-specific skill loaders are optional. All agent CLIs can use the repo
runbooks directly:

- `.agent-skills/loom-pr-test/SKILL.md` - real Loom PR runtime testing with
  local-mode stacks, browser validation, FleetDB compatibility checks, and
  real backend sandbox runs.

When testing Loom runtime behavior, follow the runbook above. Do not manually
create lock files, FleetDB state, sessions, transcripts, diffs, or other fake
state as test evidence.

## Quick Reference

```bash
loom data ready --limit 10     # Find available work
loom data show <id>            # View issue details
loom data claim <id>           # Claim work
loom data close <id> --reason "done"  # Complete work
```

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
