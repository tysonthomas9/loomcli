// Package sandbox decides where and how a driver run is isolated.
//
// A SandboxLauncher provides an execution environment; SandboxModeProcess runs
// in a plain subprocess, while the container provider runs the workload in an
// image (DefaultSandboxImage) with a chosen egress mechanism. The mode and
// egress policy are read from SandboxModeEnvVar and SandboxEgressEnvVar.
//
// The security-relevant decision is trust. RefuseUntrustedPlacement rejects a
// run whose trust level is not satisfied by the available launcher, returning
// ErrorClassSandboxRequired rather than silently falling back to weaker
// isolation. RecordTrustPlacementDecision and RecordSandboxPlacement write the
// decision and the resulting placement onto the RunResult under the
// TrustLevelOutputKey and SandboxPlacementOutputKey outputs, so the choice is
// auditable after the fact instead of being inferred from logs.
//
// IsolatingLauncher marks the launchers that genuinely isolate, which is what
// distinguishes a real sandbox from a process launcher wearing the same
// interface.
package sandbox
