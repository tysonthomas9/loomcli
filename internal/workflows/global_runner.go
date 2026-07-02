package workflows

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Workspace-global builtin task-runner resolution (GAP A), workflows side.
//
// internal/driver owns the runner-resolution flow but cannot import this
// package (this package imports it). It exposes a seam — SetGlobalRunnerResolver
// — that we register into from init() below, so any binary linking the builtin
// catalog (the serve process) can resolve a builtin task runner declared by a
// DIFFERENT (e.g. custom, untrusted) driver's run. See
// internal/driver/global_runner.go for the security reasoning; the driver side
// re-verifies trust as defense in depth.

func init() {
	driver.SetGlobalRunnerResolver(resolveGlobalBuiltinRunner)
}

// resolveGlobalBuiltinRunner resolves a task-runner name against the builtin
// registry: it finds every builtin workflow whose spec declares the runner,
// self-heals each (registers/activates + stages its bundle on disk) and returns
// the first whose ACTIVE version is TRUSTED and declares the runner. Returns
// domain.ErrNotFound when no trusted builtin owns the runner.
func resolveGlobalBuiltinRunner(ctx context.Context, s store.Store, ws, runnerName string) (*driver.GlobalRunnerResolution, error) {
	runnerName = strings.TrimSpace(runnerName)
	if runnerName == "" {
		return nil, domain.ErrNotFound
	}
	candidates := builtinWorkflowsDeclaringRunner(runnerName)
	if len(candidates) == 0 {
		return nil, domain.ErrNotFound
	}
	var lastErr error
	for _, name := range candidates {
		// Self-heal so the resolved version's server.mjs is actually on disk
		// (a fleet-db row can outlive its .loom/drivers bundle across rebuilds).
		// EnsureBuiltinWorkflow is idempotent: it only rebuilds on a digest
		// mismatch or a missing bundle.
		if err := EnsureBuiltinWorkflow(ctx, s, ws, name); err != nil {
			lastErr = err
			continue
		}
		res, err := activeTrustedBuiltinRunner(ctx, s, ws, name, runnerName)
		if err != nil {
			lastErr = err
			continue
		}
		return res, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, domain.ErrNotFound
}

// builtinWorkflowsDeclaringRunner returns the builtin workflow names whose spec
// declares a task runner with the given name (sorted, via BuiltinWorkflowNames).
// It reads the static builtin specs only — no store access — so it is a pure
// catalog lookup.
func builtinWorkflowsDeclaringRunner(runnerName string) []string {
	runnerName = strings.TrimSpace(runnerName)
	if runnerName == "" {
		return nil
	}
	var names []string
	for _, name := range BuiltinWorkflowNames() {
		spec, ok := BuiltinWorkflow(name)
		if !ok {
			continue
		}
		for _, runner := range deriveWorkflowRunnerSpecs(spec.Entrypoint, spec.Files) {
			if runner.Name == runnerName {
				names = append(names, name)
				break
			}
		}
	}
	return names
}

// activeTrustedBuiltinRunner resolves a single builtin workflow's active version
// and returns the declared runner spec IFF that version is TRUSTED and declares
// the runner. A non-trusted active version (or one that no longer declares the
// runner) is refused — an untrusted driver can never export its sibling runners.
func activeTrustedBuiltinRunner(ctx context.Context, s store.Store, ws, workflowName, runnerName string) (*driver.GlobalRunnerResolution, error) {
	drv, err := ResolveDriver(ctx, s, ws, workflowName)
	if err != nil {
		return nil, err
	}
	versionID := strings.TrimSpace(drv.ActiveVersionID)
	if versionID == "" {
		return nil, fmt.Errorf("builtin workflow %q has no active version: %w", workflowName, domain.ErrNotFound)
	}
	version, err := s.DriverVersions().Get(ctx, ws, versionID)
	if err != nil {
		return nil, err
	}
	if !driver.DriverVersionEffectiveTrust(drv, version).Trusted() {
		return nil, fmt.Errorf("builtin workflow %q active version %q is not trusted: %w", workflowName, versionID, domain.ErrInvalid)
	}
	spec, err := driver.DeclaredRunnerSpec(version, runnerName)
	if err != nil {
		return nil, err
	}
	return &driver.GlobalRunnerResolution{Driver: drv, Version: version, Spec: spec}, nil
}
