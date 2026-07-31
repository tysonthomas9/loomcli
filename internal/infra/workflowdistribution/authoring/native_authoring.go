package authoring

import (
	"context"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

type NativeAuthoringAuthorities = appworkflowauthoring.NativeAuthoringAuthorities

// AuthorNativeFlueDriver is the legacy infrastructure facade. Catalog
// orchestration belongs to the app coordinator; this facade only translates
// legacy driver staging DTOs at the composition edge.
func AuthorNativeFlueDriver(
	ctx context.Context,
	catalog workflowcatalog.API,
	authoring workflowcatalog.VersionAuthoringAPI,
	authorities NativeAuthoringAuthorities,
	options driver.RegisterFlueOptions,
) (*driver.RegisterFlueResult, error) {
	coordinator, err := appworkflowauthoring.NewWithNative(
		legacyBundleStager{},
		legacyBundleStager{},
	)
	if err != nil {
		return nil, err
	}
	result, err := coordinator.AuthorNative(
		ctx,
		catalog,
		authoring,
		authorities,
		applicationNativeOptions(options),
	)
	return legacyAuthoringResult(result), err
}

func applicationNativeOptions(
	options driver.RegisterFlueOptions,
) appworkflowauthoring.NativeOptions {
	runners := make([]appworkflowauthoring.RunnerSpec, 0, len(options.RunnerSpecs))
	for _, runner := range options.RunnerSpecs {
		runners = append(runners, appworkflowauthoring.RunnerSpec{
			Name: runner.Name, Kind: runner.Kind, Entrypoint: runner.Entrypoint,
		})
	}
	return appworkflowauthoring.NativeOptions{
		WorkspaceKey: options.WorkspaceKey, WorkDir: options.WorkDir,
		DistPath: options.DistPath, ManifestPath: options.ManifestPath,
		DriverName: options.DriverName, DriverID: options.DriverID,
		WorkflowName: options.WorkflowName, SourceRef: options.SourceRef,
		SourceDigest: options.SourceDigest, Activate: options.Activate,
		Runners: runners, Manifest: options.Manifest, Trust: options.Trust,
	}
}
