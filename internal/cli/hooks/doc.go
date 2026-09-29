// Package hooks installs and interprets Claude Code lifecycle hooks inside an
// agent's worktree.
//
// InstallClaudeHooks writes the hook configuration, UninstallClaudeHooks
// removes it, and ClaudeHooksStatus reports which hooks are currently present
// — enough for `loom doctor` and the agent launch path to tell a configured
// worktree from a bare one.
//
// On the receiving side, the harness invokes loom with hook input on stdin.
// ParseClaudeHookInput decodes that into a HookEvent tagged with a
// HookEventType (session start and the other lifecycle points), which is what
// lets loom observe an agent's progress from the harness itself rather than
// inferring it from output.
package hooks
