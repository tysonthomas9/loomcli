package sandbox

// Trust placement policy (§7 step 9, SB3): isolation is a platform-enforced
// rule, not topology hygiene. Every Driver carries a trust level; the runner
// REFUSES to launch an untrusted bundle outside an isolating launcher
// (container/gVisor/remote), failing the run with a structured
// sandbox_required error instead of silently degrading to a host process.
// Unknown/missing trust reads as untrusted — fail closed (locked decision:
// the one-time fleet-db backfill stamped pre-existing rows trusted).
//
// driverTrustLevel (resolving a driver version's effective trust) lives in the
// parent driver package next to DriverVersionEffectiveTrust; this package owns
// only the sandbox admission decision once a trust level is known.

import (
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver/runtypes"
)

// ErrorClassSandboxRequired is the structured error class stamped on a run
// the placement policy refused to launch: an untrusted driver resolved to a
// non-isolating launcher. Not retryable — the deployment must enable an
// isolating sandbox (LOOM_DRIVER_SANDBOX=container) or an operator must
// explicitly mark the driver trusted.
const ErrorClassSandboxRequired = "sandbox_required"

// Run-output keys recording the placement decision (§9.6 audit): the trust
// level the policy saw, the launcher it resolved, and — on refusal — the
// structured error code with retryability.
const (
	TrustLevelOutputKey      = "driver_trust_level"
	SandboxLauncherOutputKey = "sandbox_launcher"
	ErrorCodeOutputKey       = "error_code"
	RetryableOutputKey       = "retryable"
)

// IsolatingLauncher marks SandboxLauncher implementations whose runtimes are
// isolated from the host (container/gVisor/remote). Launchers that do not
// implement it — including the default local process launcher — are treated
// as non-isolating, so untrusted bundles are refused.
type IsolatingLauncher interface {
	SandboxLauncher
	// Isolates reports whether launched runtimes are isolated from the host.
	Isolates() bool
}

func launcherIsolates(launcher SandboxLauncher) bool {
	isolating, ok := launcher.(IsolatingLauncher)
	return ok && isolating.Isolates()
}

// LauncherPlacementProvider names the launcher for the audit record. It
// mirrors the placement Provider the launcher would report; custom injected
// launchers without a known provider are labeled by isolation outcome.
func LauncherPlacementProvider(launcher SandboxLauncher) string {
	switch launcher.(type) {
	case ProcessLauncher:
		return SandboxProviderProcess
	case *containerLauncher:
		return SandboxProviderContainer
	default:
		if launcherIsolates(launcher) {
			return "custom-isolating"
		}
		return "custom"
	}
}

// RefuseUntrustedPlacement is the pre-launch gate: it returns a terminal
// failed result (and true) when the run's driver is untrusted and the
// resolved launcher does not isolate. The launcher is never invoked — no
// process is spawned. Trusted drivers and isolating launchers pass through.
func RefuseUntrustedPlacement(req runtypes.RunRequest, launcher SandboxLauncher) (runtypes.RunResult, bool) {
	if req.TrustLevel.Trusted() || launcherIsolates(launcher) {
		return runtypes.RunResult{}, false
	}
	provider := LauncherPlacementProvider(launcher)
	result := runtypes.RunResult{
		Status: domain.DriverRunFailed,
		Summary: fmt.Sprintf(
			"driver %q is untrusted and the resolved %q launcher does not isolate: refusing to launch outside a sandbox (set LOOM_DRIVER_SANDBOX=container or have an operator mark the driver trusted)",
			req.Run.DriverID, provider),
		ErrorClass: ErrorClassSandboxRequired,
		Output: map[string]string{
			ErrorCodeOutputKey: ErrorClassSandboxRequired,
			RetryableOutputKey: "false",
		},
	}
	RecordTrustPlacementDecision(&result, req.TrustLevel, provider)
	return result, true
}

// RecordTrustPlacementDecision stamps the policy inputs onto the run output
// (§9.6 audit): every run records the trust level it executed (or was
// refused) under and the launcher the runner resolved.
func RecordTrustPlacementDecision(result *runtypes.RunResult, trust workflowcatalog.DriverTrustLevel, launcherProvider string) {
	if result.Output == nil {
		result.Output = map[string]string{}
	}
	level := workflowcatalog.DriverTrustUntrusted
	if trust.Trusted() {
		level = workflowcatalog.DriverTrustTrusted
	}
	result.Output[TrustLevelOutputKey] = string(level)
	result.Output[SandboxLauncherOutputKey] = launcherProvider
}
