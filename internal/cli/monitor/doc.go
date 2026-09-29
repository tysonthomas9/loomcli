// Package monitor renders loom's terminal dashboard.
//
// It gathers what an operator watching a fleet needs — the daemon's managed
// agents from its state file, ready-task counts bucketed by priority, and each
// worktree's branch and ahead/behind position against its default branch — and
// lays it out in a fixed DashboardWidth.
//
// The layout helpers are deliberately width-aware rather than length-aware:
// DisplayWidth measures rendered columns, so CenterText and PadRight align
// correctly for branch names and task titles containing wide or multi-byte
// characters instead of drifting by a column per glyph.
//
// Branch and ref state is read straight from the filesystem (ReadBranchFromFS,
// ReadRefSHA). Ahead/behind counts are not: GetWorktreeGitSyncStatus shells
// out per worktree, so a dashboard refresh is not process-free.
package monitor
