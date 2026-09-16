// Package automode runs an agent in a loop instead of a single invocation.
//
// RunAutoModeLoop drives successive tasks in the current process;
// RunAutoModeTmux does the same inside tmux panes when it is available, which
// IsTmuxAvailable reports. SetupSignalHandler returns the shutdown channel both
// respect, so a loop stops between tasks rather than mid-agent.
//
// What the loop picks up next is a filtered query, not a queue.
// GetAvailablePlanningTasks and GetAvailableImplementationTasks return the work
// a role is eligible for, and the Has* variants answer the same question
// without paying for the full list — the loop uses them to decide whether to
// idle. BuildRouterTaskCheck turns a role and agent entry into the predicate
// the router evaluates each pass.
//
// CaptureHEADRef records the worktree's commit before an attempt, so a run that
// produces no change can be told from one whose change was lost.
package automode
