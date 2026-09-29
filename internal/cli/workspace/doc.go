// Package workspace registers the `loom workspace` commands and assembles the
// status view behind them.
//
// StatusData is the aggregate an operator asks for: the workspace's backend
// and daemon, its Redis connection, a workspace-wide GitSummary counting how
// many repos need a push or a pull, and the issues currently in play. It is composed here rather than in any one subsystem
// because no single one of them knows the whole picture.
//
// ResolveAgentTarget turns an agent name and repo into a ResolvedTarget,
// settling which worktree a command should act on; EnsureRepoWorktree creates
// that worktree on the requested branch if it does not exist yet, which is
// what makes agent commands work on a fresh checkout.
package workspace
