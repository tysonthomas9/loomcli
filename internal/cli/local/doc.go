// Package local manages the single-machine loom runtime behind `loom local`.
//
// StartRuntime, RestartRuntime, and EnsureRuntimeStarted bring up the local
// stack in a data directory on a port, returning a RuntimeStartResult or, for
// the idempotent path, a RuntimeStatusSnapshot describing what is already
// running. ReadRuntimeStatus inspects without starting anything, and
// DefaultDataDir resolves where that state lives when the operator has not
// said.
//
// Starting the process is not the same as being usable, so
// WaitForWorkspaceReady blocks until a named workspace actually answers on the
// API — the check that lets a script proceed straight from start to work.
package local
