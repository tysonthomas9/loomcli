// Workflow authoring adapters connect the application-owned lifecycle to
// Workflow Distribution's source, build, staging, promotion, and verification
// mechanics without a nested forwarding package.
package workflowdistribution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// ResolveDaytonaSDKRoot resolves the provider SDK shipped with the active Flue
// runtime. Keeping runtime-layout discovery in the distribution adapter lets
// host adapters consume one cohesive authoring and staging boundary.
func ResolveDaytonaSDKRoot() (string, error) {
	runtimeRoot, err := FlueRuntimeRoot()
	if err != nil {
		return "", fmt.Errorf("resolve Flue runtime: %w", err)
	}
	return DaytonaSDKRoot(runtimeRoot)
}

type bundleStager struct{}

func NewBundleStager() appworkflowauthoring.BundleStager {
	return bundleStager{}
}

func NewNativeBundleStager() appworkflowauthoring.NativeBundleStager {
	return bundleStager{}
}

func (bundleStager) RecoverPending(
	_ context.Context,
	version *workflowcatalog.DriverVersion,
) (appworkflowauthoring.StagedBundle, appworkflowauthoring.FailureDisposition, error) {
	staged, err := driver.RecoverStagedFlueRegistration(builtinWorkflowWorkDir(), version)
	if err != nil {
		disposition := appworkflowauthoring.FailureRetryable
		if errors.Is(err, persistence.ErrInvalid) {
			disposition = appworkflowauthoring.FailurePermanent
		}
		return nil, disposition, err
	}
	return &stagedBundle{staged: staged}, appworkflowauthoring.FailureRetryable, nil
}

func (bundleStager) BuildAndStage(
	ctx context.Context,
	options appworkflowauthoring.BuildOptions,
) (appworkflowauthoring.StagedBundle, string, error) {
	files, err := ValidateWorkflowFiles(options.Files)
	if err != nil {
		return nil, "", err
	}
	options.Files = files
	if options.SourceDigest == "" {
		options.SourceDigest, err = SourceDigest(options.Files)
		if err != nil {
			return nil, "", err
		}
	}
	if options.SourceRef == "" {
		options.SourceRef = "api://workflows/" + options.Name + "/versions/" + options.SourceDigest
	}
	if staged, found, err := stagePackagedBuiltin(options, packagedBuiltinFS); found || err != nil {
		return staged, "", err
	}
	built, output, err := Build(ctx, BuildOptions{
		Name: options.Name, Files: options.Files, WorkDir: options.WorkDir,
	})
	if err != nil {
		return nil, output, err
	}
	defer built.Cleanup()
	runnerSpecs := driverRunnerSpecs(options.Runners)
	if len(runnerSpecs) == 0 && options.DeriveRunners {
		runnerSpecs = deriveWorkflowRunnerSpecs(options.Entrypoint, options.Files)
	}
	staged, err := driver.StageFlueDriverBundle(driver.RegisterFlueOptions{
		WorkspaceKey: options.WorkspaceKey, WorkDir: built.WorkDir, DistPath: built.OutputDir,
		DriverName: options.Name, DriverID: options.DriverID,
		WorkflowName: strings.TrimSuffix(
			filepath.Base(options.Entrypoint),
			filepath.Ext(options.Entrypoint),
		),
		SourceRef: options.SourceRef, SourceDigest: options.SourceDigest,
		RunnerSpecs: runnerSpecs, Manifest: options.Manifest,
		BuildDiagnostics: output, Trust: options.Trust,
	})
	if err != nil {
		return nil, output, err
	}
	return &stagedBundle{staged: staged}, output, nil
}

func (bundleStager) StageNative(
	_ context.Context,
	options appworkflowauthoring.NativeOptions,
) (appworkflowauthoring.StagedBundle, error) {
	staged, err := driver.StageFlueDriverBundle(driver.RegisterFlueOptions{
		WorkspaceKey: options.WorkspaceKey, WorkDir: options.WorkDir,
		DistPath: options.DistPath, ManifestPath: options.ManifestPath,
		DriverName: options.DriverName, DriverID: options.DriverID,
		WorkflowName: options.WorkflowName, SourceRef: options.SourceRef,
		SourceDigest: options.SourceDigest, Activate: options.Activate,
		RunnerSpecs: driverRunnerSpecs(options.Runners),
		Manifest:    options.Manifest, Trust: options.Trust,
	})
	if err != nil {
		return nil, err
	}
	return &stagedBundle{staged: staged}, nil
}

type stagedBundle struct {
	staged      *driver.StagedFlueRegistration
	cleanupRoot string
}

func (bundle *stagedBundle) Metadata() appworkflowauthoring.StagedMetadata {
	if bundle == nil || bundle.staged == nil {
		return appworkflowauthoring.StagedMetadata{}
	}
	return appworkflowauthoring.StagedMetadata{
		DriverName: bundle.staged.DriverName, DriverID: bundle.staged.DriverID,
		VersionID: bundle.staged.VersionID, SourceRef: bundle.staged.SourceRef,
		SourceDigest: bundle.staged.SourceDigest, BundleRef: bundle.staged.BundleRef,
		BundleDigest: bundle.staged.BundleDigest, Runtime: bundle.staged.Runtime,
		CatalogManifest: cloneWorkflowManifest(bundle.staged.CatalogManifest),
		Diagnostics:     bundle.staged.BuildDiagnostics,
	}
}

func (bundle *stagedBundle) Bundle() *appworkflowauthoring.Bundle {
	if bundle == nil || bundle.staged == nil || bundle.staged.Bundle == nil {
		return nil
	}
	value := bundle.staged.Bundle
	return &appworkflowauthoring.Bundle{
		Root: value.Root, BundleRef: value.BundleRef, SourceRef: value.SourceRef,
		SourceDigest: value.SourceDigest, BundleDigest: value.BundleDigest,
		Manifest: cloneWorkflowManifest(value.Manifest), Diagnostics: value.Diagnostics,
	}
}

func (bundle *stagedBundle) Promote() error {
	if bundle == nil || bundle.staged == nil {
		return workflowcatalog.ErrInvalidPersistedState
	}
	return bundle.staged.Promote()
}

func (bundle *stagedBundle) Verify() error {
	if bundle == nil || bundle.staged == nil {
		return workflowcatalog.ErrInvalidPersistedState
	}
	return bundle.staged.Verify()
}

func (bundle *stagedBundle) ClassifyFailure(err error) appworkflowauthoring.FailureDisposition {
	if errors.Is(err, persistence.ErrInvalid) {
		return appworkflowauthoring.FailurePermanent
	}
	return appworkflowauthoring.FailureRetryable
}

func (bundle *stagedBundle) Discard() {
	if bundle == nil {
		return
	}
	if bundle.staged != nil {
		bundle.staged.Cleanup()
	}
	if bundle.cleanupRoot != "" {
		_ = os.RemoveAll(bundle.cleanupRoot)
		bundle.cleanupRoot = ""
	}
}

func driverRunnerSpecs(runners []appworkflowauthoring.RunnerSpec) []driver.DriverRunnerSpec {
	out := make([]driver.DriverRunnerSpec, 0, len(runners))
	for _, runner := range runners {
		out = append(out, driver.DriverRunnerSpec{
			Name: runner.Name, Kind: runner.Kind, Entrypoint: runner.Entrypoint,
		})
	}
	return out
}

func applicationRunnerSpecs(runners []driver.DriverRunnerSpec) []appworkflowauthoring.RunnerSpec {
	out := make([]appworkflowauthoring.RunnerSpec, 0, len(runners))
	for _, runner := range runners {
		out = append(out, appworkflowauthoring.RunnerSpec{
			Name: runner.Name, Kind: runner.Kind, Entrypoint: runner.Entrypoint,
		})
	}
	return out
}

func cloneWorkflowManifest(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func deriveWorkflowRunnerSpecs(entrypoint string, files map[string]string) []driver.DriverRunnerSpec {
	return DeriveWorkflowRunnerSpecs(entrypoint, files)
}

func workflowRunnerNameSet(spec Spec) map[string]struct{} {
	return RunnerNameSet(spec)
}

func activeManifestRunnersAreStale(manifest map[string]string, fresh map[string]struct{}) bool {
	return ActiveManifestRunnersAreStale(manifest, fresh)
}

func manifestMissingFreshRunners(manifest map[string]string, fresh map[string]struct{}) []string {
	return ManifestMissingFreshRunners(manifest, fresh)
}
