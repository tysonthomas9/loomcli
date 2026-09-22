// Package driver registers the `loom driver` command tree — the operator and
// runtime surface for workflow driver runs.
//
// The subcommands split by what they act on: driver-level registration,
// individual runs (exec), the tasks a run produces, the epics it advances, and
// the agent messages it delivers. run_context.go assembles the context a
// subcommand needs from flags and environment.
//
// Two invariants shape this package. Registration defaults a driver to
// untrusted, so a workflow must be explicitly promoted before it can run with
// weaker isolation. And completing a task run is authenticated: the caller
// presents lease credentials, and completion is refused when the presented
// task ID does not match the run being completed — a sandboxed runner calling
// back must not be able to complete a task that is not its own.
package driver
