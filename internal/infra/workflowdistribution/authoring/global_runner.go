package authoring

import (
	"context"
	"errors"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// NewGlobalBuiltinRunnerResolver adapts the app-owned builtin resolution
// workflow to the driver runtime callback type.
func NewGlobalBuiltinRunnerResolver(
	catalog workflowcatalog.API,
	authoring workflowcatalog.VersionAuthoringAPI,
	authorities appworkflowauthoring.ManagedBuiltinAuthorityProvider,
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
			return nil, persistence.ErrNotFound
		}
		if errors.Is(err, workflowcatalog.ErrInvalid) {
			return nil, persistence.ErrInvalid
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
