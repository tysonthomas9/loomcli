// Package lockfile provides the flock primitives behind loom's single-daemon
// guarantee.
//
// TryDaemonLock reports whether a daemon already holds the runtime directory's
// lock and which PID owns it; ReadLockInfo returns the recorded LockInfo
// without competing for the lock, so status commands can inspect a running
// daemon without disturbing it.
//
// TryLockExclusive, FlockExclusiveBlocking, and FlockUnlock are the
// lower-level helpers, exported for callers that manage their own file
// handles. Each has a per-platform implementation selected by build tag.
package lockfile
