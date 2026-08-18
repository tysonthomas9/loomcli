package workflowauthoring

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

// ResolveGlobalBuiltinRunner self-heals candidate builtins and resolves the
// requested runner from the active trusted catalog version. Runner manifest
// decoding remains behind BuiltinSupport.
//
//nolint:funlen // Resolution keeps self-healing, trust validation, and exact runner selection in one fail-closed flow.
func (coordinator *Coordinator) ResolveGlobalBuiltinRunner(
	ctx context.Context,
	catalog workflowcatalog.API,
	authoring CatalogCommands,
	authorities ManagedBuiltinAuthorityProvider,
	support BuiltinSupport,
	workspace,
	runnerName string,
) (*GlobalRunnerResolution, error) {
	if coordinator == nil || catalog == nil || authoring == nil ||
		authorities == nil || support == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	runnerName = strings.TrimSpace(runnerName)
	if runnerName == "" {
		return nil, workflowcatalog.ErrNotFound
	}
	candidates := builtinsDeclaringRunner(support, runnerName)
	if len(candidates) == 0 {
		return nil, workflowcatalog.ErrNotFound
	}
	var lastErr error
	for _, name := range candidates {
		if err := coordinator.EnsureBuiltin(
			ctx,
			catalog,
			authoring,
			authorities,
			support,
			workspace,
			name,
		); err != nil {
			lastErr = err
			continue
		}
		result, err := activeTrustedBuiltinRunner(
			ctx,
			catalog,
			support,
			workspace,
			name,
			runnerName,
		)
		if err != nil {
			lastErr = err
			continue
		}
		return result, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, workflowcatalog.ErrNotFound
}

func builtinsDeclaringRunner(support BuiltinSupport, runnerName string) []string {
	var names []string
	for _, name := range support.BuiltinNames() {
		spec, ok := support.Builtin(name)
		if !ok {
			continue
		}
		for _, runner := range spec.Runners {
			if runner.Name == runnerName {
				names = append(names, name)
				break
			}
		}
	}
	return names
}

func activeTrustedBuiltinRunner(
	ctx context.Context,
	catalog workflowcatalog.API,
	support BuiltinSupport,
	workspace,
	workflowName,
	runnerName string,
) (*GlobalRunnerResolution, error) {
	driverRecord, err := catalog.GetDriver(ctx, workspace, workflowName)
	if err != nil {
		return nil, err
	}
	if driverRecord == nil {
		return nil, workflowcatalog.ErrInvalidPersistedState
	}
	versionID := strings.TrimSpace(driverRecord.ActiveVersionID)
	if versionID == "" {
		return nil, fmt.Errorf(
			"builtin workflow %q has no active version: %w",
			workflowName,
			workflowcatalog.ErrNotFound,
		)
	}
	version, err := catalog.GetVersion(ctx, workspace, versionID)
	if err != nil {
		return nil, err
	}
	if version == nil || version.DriverID != driverRecord.DriverID ||
		version.VersionID != versionID {
		return nil, workflowcatalog.ErrInvalidPersistedState
	}
	if !workflowcatalog.EffectiveTrust(driverRecord, version).Trusted() {
		return nil, fmt.Errorf(
			"builtin workflow %q active version %q is not trusted: %w",
			workflowName,
			versionID,
			workflowcatalog.ErrInvalid,
		)
	}
	spec, err := support.DeclaredRunner(version, runnerName)
	if err != nil {
		return nil, err
	}
	return &GlobalRunnerResolution{
		Driver:  driverRecord,
		Version: version,
		Spec:    spec,
	}, nil
}

// ResolveActiveBuiltinRunner exposes the app-owned active-version trust policy
// to infrastructure adapters and their tests.
func ResolveActiveBuiltinRunner(
	ctx context.Context,
	catalog workflowcatalog.API,
	support BuiltinSupport,
	workspace,
	workflowName,
	runnerName string,
) (*GlobalRunnerResolution, error) {
	if catalog == nil || support == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	return activeTrustedBuiltinRunner(
		ctx,
		catalog,
		support,
		workspace,
		workflowName,
		runnerName,
	)
}
