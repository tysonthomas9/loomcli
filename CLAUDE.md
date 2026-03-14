# Rules

- NEVER modify `.git/config` or any files under `.git/` directly. Use `git config` commands only when explicitly instructed by the user. Changing git internals (e.g., `core.bare`, `core.worktree`) can break the repository and all worktrees.
