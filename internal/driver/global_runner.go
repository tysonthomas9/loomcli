package driver

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Workspace-global builtin task-runner resolution (GAP A).
//
// A task runner is normally selectable only when the CALLING driver version's
// own manifest declares it (resolveDriverRunner). That is too strict for the
// prompt-agent / scripted-agent convergence: a CUSTOM (untrusted) workflow
// driver registered through the HTTP path ships no runner specs, so it can
// never dispatch the bundled local-task-runner even though that runner is a
// blessed builtin.
//
// This seam adds a FAIL-CLOSED fallback: when the caller's own manifest does
// not declare a runner (ErrRunnerNotDeclared), the runner name is resolved
// against the workspace's BUILTIN task-runner registry instead — a runner is
// globally resolvable iff it is declared by the ACTIVE version of a TRUSTED
// (domain.DriverTrustTrusted) driver whose spec ships in internal/workflows
// (the builtins).
//
// SECURITY REASONING. Allowing an untrusted custom driver to dispatch the
// trusted local-task-runner is the intended behavior, and it is safe because
// the runner executes under ITS OWN trust, never the caller's:
//
//   - The resolved runner pins RunnerVersionID / RunnerRef / kind / entrypoint
//     to the BUILTIN's OWNING version (see resolveTaskRunRequestRunner), so the
//     bundle the host loads and executes (taskRunnerBundleEnv) is the builtin's
//     blessed server.mjs — NOT anything the caller uploaded. A caller cannot
//     smuggle its own code into a trusted runner.
//   - Trust for sandbox/host-bridge admission derives from the runner's owning
//     version (DriverVersionEffectiveTrust of the builtin driver+version), not
//     the caller. refuseUntrustedTaskRunnerExecution therefore admits the
//     builtin runner on the host bridge exactly as if a builtin workflow had
//     dispatched it.
//   - The caller only ever supplies the task-run INPUT payload (the "prompt =
//     data" boundary). It controls data, never runner code.
//   - Global resolution is restricted to TRUSTED BUILTIN drivers. An untrusted
//     driver's sibling runners are never trusted, so a custom driver can never
//     export its own runner to others (double-checked here in resolveGlobalRunner
//     and again in the workflows-side resolver).
//
// The catalog of which builtins declare which runners lives in internal/workflows,
// which imports internal/driver (not the other way round). The resolver is
// therefore injected via SetGlobalRunnerResolver from an init() in
// internal/workflows; when unset (e.g. an internal/driver unit test that does not
// import workflows), the fallback is disabled and resolution stays exactly as
// before — fail closed.

// GlobalRunnerResolution is a resolved workspace-global runner: the trusted
// builtin driver + active version that OWNS the runner, and the declared spec.
type GlobalRunnerResolution struct {
	Driver  *domain.Driver
	Version *domain.DriverVersion
	Spec    DriverRunnerSpec
}

// GlobalRunnerResolver resolves a task-runner name against the workspace's
// trusted builtin registry. Implemented and registered by internal/workflows.
// It returns domain.ErrNotFound when no trusted builtin declares the runner.
type GlobalRunnerResolver func(ctx context.Context, s store.Store, ws, runnerName string) (*GlobalRunnerResolution, error)

// globalRunnerResolver is the process-wide resolver, injected once at startup
// by internal/workflows. Nil disables the global fallback (fail closed).
var globalRunnerResolver GlobalRunnerResolver

// SetGlobalRunnerResolver registers the workspace-global builtin runner
// resolver. internal/workflows calls this from an init() so any binary that
// links the builtin catalog (the serve process) gains the fallback, while
// internal/driver unit tests that do not import workflows keep the strict,
// caller-manifest-only behavior.
func SetGlobalRunnerResolver(resolver GlobalRunnerResolver) {
	globalRunnerResolver = resolver
}

// DeclaredRunnerSpec exposes resolveDriverRunner so the workflows-side resolver
// can look up a runner in a builtin version's manifest without duplicating the
// decode/guard logic. It returns ErrRunnerNotDeclared (wrapped) when the version
// does not declare the runner.
func DeclaredRunnerSpec(version *domain.DriverVersion, runnerName string) (DriverRunnerSpec, error) {
	return resolveDriverRunner(version, runnerName)
}

// resolveGlobalRunner runs the injected resolver and re-verifies, in this
// package, that the runner's owning version is trusted — defense in depth, so a
// buggy or malicious resolver cannot make an untrusted runner globally
// resolvable. Returns an ErrRunnerNotDeclared-classed error when no eligible
// trusted builtin owns the runner (so the caller preserves the original
// not-declared error and fails exactly as before).
func resolveGlobalRunner(ctx context.Context, s store.Store, ws, runnerName string) (*GlobalRunnerResolution, error) {
	resolver := globalRunnerResolver
	if resolver == nil {
		return nil, fmt.Errorf("no global runner resolver registered for %q: %w", runnerName, ErrRunnerNotDeclared)
	}
	res, err := resolver(ctx, s, ws, runnerName)
	if err != nil {
		return nil, err
	}
	if res == nil || res.Version == nil || res.Driver == nil {
		return nil, fmt.Errorf("global runner %q resolved to an incomplete result: %w", runnerName, ErrRunnerNotDeclared)
	}
	if !DriverVersionEffectiveTrust(res.Driver, res.Version).Trusted() {
		return nil, fmt.Errorf("global runner %q owner %q is not trusted; untrusted drivers cannot export runners: %w", runnerName, res.Driver.DriverID, ErrRunnerNotDeclared)
	}
	return res, nil
}
