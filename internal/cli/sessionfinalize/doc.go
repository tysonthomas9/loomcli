// Package sessionfinalize closes out an agent session against its worktree.
//
// WithWorktree runs the finalization a session needs while its worktree is
// still present — capturing what changed and recording it on the session —
// and returns a WithWorktreeResult describing the outcome. It exists as its
// own package because finalization must happen on every exit path, successful
// or not, and duplicating that sequence at each call site is how sessions end
// up recorded inconsistently.
package sessionfinalize
