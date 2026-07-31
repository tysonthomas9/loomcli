package authoring

import (
	"context"
	"errors"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

// NewGlobalBuiltinRunnerResolver adapts the app-owned builtin resolution
// workflow to the legacy driver runtime callback type.
func NewGlobalBuiltinRunnerResolver(
	catalog workflowcatalog.API,
	authoring workflowcatalog.VersionAuthoringAPI,
	authorities ManagedBuiltinAuthorityProvider,
) driver.GlobalRunnerResolver {
	if catalog == nil || authoring == nil || authorities == nil {
		return nil
	}
	coordinator, err := appworkflowauthoring.New(NewBundleStager())
	if err != nil {
		return nil
	}
	support := NewBuiltinSupport()
	return func(
		ctx context.Context,
		workspace,
		runnerName string,
	) (*driver.GlobalRunnerResolution, error) {
		result, err := coordinator.ResolveGlobalBuiltinRunner(
			ctx,
			catalog,
			authoring,
			authorities,
			support,
			workspace,
			runnerName,
		)
		if errors.Is(err, workflowcatalog.ErrNotFound) {
			return nil, domain.ErrNotFound
		}
		if errors.Is(err, workflowcatalog.ErrInvalid) {
			return nil, domain.ErrInvalid
		}
		if err != nil {
			return nil, err
		}
		if result == nil || result.Driver == nil || result.Version == nil {
			return nil, workflowcatalog.ErrInvalidPersistedState
		}
		return &driver.GlobalRunnerResolution{
			Driver:  result.Driver,
			Version: result.Version,
			Spec: driver.DriverRunnerSpec{
				Name:       result.Spec.Name,
				Kind:       result.Spec.Kind,
				Entrypoint: result.Spec.Entrypoint,
			},
		}, nil
	}
}

// The following helpers keep legacy package tests readable while delegating
// all catalog policy to the application coordinator.
func resolveGlobalBuiltinRunner(
	ctx context.Context,
	catalog workflowcatalog.API,
	authoring workflowcatalog.VersionAuthoringAPI,
	authorities ManagedBuiltinAuthorityProvider,
	workspace,
	runnerName string,
) (*driver.GlobalRunnerResolution, error) {
	resolver := NewGlobalBuiltinRunnerResolver(catalog, authoring, authorities)
	if resolver == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	return resolver(ctx, workspace, runnerName)
}

func builtinWorkflowsDeclaringRunner(runnerName string) []string {
	support := legacyBuiltinSupport{}
	names := []string{}
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
	workspace,
	workflowName,
	runnerName string,
) (*driver.GlobalRunnerResolution, error) {
	result, err := appworkflowauthoring.ResolveActiveBuiltinRunner(
		ctx,
		catalog,
		NewBuiltinSupport(),
		workspace,
		workflowName,
		runnerName,
	)
	if errors.Is(err, workflowcatalog.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if errors.Is(err, workflowcatalog.ErrInvalid) {
		return nil, domain.ErrInvalid
	}
	if err != nil {
		return nil, err
	}
	return &driver.GlobalRunnerResolution{
		Driver:  result.Driver,
		Version: result.Version,
		Spec: driver.DriverRunnerSpec{
			Name:       result.Spec.Name,
			Kind:       result.Spec.Kind,
			Entrypoint: result.Spec.Entrypoint,
		},
	}, nil
}
