// Package cli is the root of loom's command tree and the home of the state its
// subcommands share.
//
// Command wiring lives in root.go; the rest of the package is the shared
// substrate underneath it. The main concerns are:
//
// Backend resolution. A loom command has to decide which issue backend it is
// talking to — local, fleet, fleet-db, or the daemon's IPC socket — before it
// can do anything. The issue_backend_* files resolve that choice, scope it to
// a workspace, and wrap it in tracing.
//
// Daemon liaison. Commands prefer to route mutations through a running daemon
// rather than touch storage directly. daemon_ensure.go starts one if needed
// and daemon_ipc_client.go speaks to it, with the IPCOp* constants naming the
// operations. ErrInteractiveInDaemonMode marks the commands that cannot run
// unattended.
//
// Worktree locking. AcquireLock takes the .agent.lock in a worktree so two
// agents cannot work the same checkout at once. ProtectedRuntimePaths names
// the paths loom excludes from cleanup and recovery deletion, so live daemon
// state survives a reset; it is not a write barrier — nothing stops an agent
// writing to those paths directly.
//
// Task routing. task_router.go and taskfilter.go decide which task an agent
// should pick up next, given its role's filters.
package cli
