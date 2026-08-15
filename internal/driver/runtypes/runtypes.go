package runtypes

import "github.com/tysonthomas9/loomcli/internal/domain"

// Package runtypes holds the driver's core execution-run contract (RunRequest /
// RunResult). It lives in this neutral leaf package — imported by both the parent
// driver package and the driver/sandbox package, and aliased back as
// driver.RunRequest / driver.RunResult — so the run/orchestration types and the
// sandbox launchers don't form an import cycle. domain is its only dependency.

type RunRequest struct {
	Run          *domain.DriverRun
	Version      *domain.DriverVersion
	BundleRoot   string
	WorkflowPath string
	ServerPath   string
	Manifest     map[string]string
	// RunToken is the run-scoped bearer token minted at claim time, bound to
	// this run's lease + fencing token, and exported to the workflow runtime
	// as LOOM_RUN_TOKEN. Empty when minting is disabled (no RunTokenKey).
	// Carried on the request — not the runner — so every launcher behind the
	// SB1 sandbox seam injects it the same way.
	RunToken string
	// TrustLevel is the run's driver trust level, loaded server-side by
	// loadRunRequest (SB3 placement policy). Anything but trusted — including
	// empty/unknown — refuses non-isolating launchers (sandbox_required).
	TrustLevel domain.DriverTrustLevel
}

type RunResult struct {
	Status     domain.DriverRunStatus
	Summary    string
	ErrorClass string
	Output     map[string]string
	// Logs carries the runner's raw log bytes to Executor.runClaimed, the
	// single writer that can persist them through store.Store.
	Logs string
}
