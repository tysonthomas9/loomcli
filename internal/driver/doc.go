// Package driver executes workflows and bridges them to loom's task model.
//
// A workflow is registered as a flue bundle (register.go) and then run
// (run.go, executor.go). While a run is in flight this package owns the
// machinery around it: env construction, approval gates, epic snapshots, and
// an outbox dispatcher that delivers a run's effects exactly once.
//
// Two behaviors account for most of the surface area:
//
// Suspension. A workflow may await an external event rather than block a
// worker. Await operations suspend a run and resume it on delivery, bounded by
// DefaultAwaitMaxPerRun, DefaultAwaitMaxTimeout, and
// DefaultAwaitTotalSuspendCap (each overridable through the matching
// LOOM_AWAIT_* environment variable) so a stuck workflow cannot hold resources
// forever. A timeout sweeper retires runs that outlive
// those bounds.
//
// The task bridge. Workflow steps become loom tasks and back again — request
// construction, scheduling, worker dispatch, mutation, retry, worktree
// resolution, session and artifact linkage. Child runs created this way carry
// ChildRunSourceKind, and DefaultCompositionMaxDepth bounds how deep workflows
// may nest before loom refuses to compose further.
//
// Isolation is delegated to internal/driver/sandbox: this package decides that
// a run needs a sandbox and records the outcome, while the sandbox package
// decides how to provide one. RunTokenSigningKeyEnv names the key used to sign
// the run tokens that let a sandboxed runner call back into loom.
package driver
