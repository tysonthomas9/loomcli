// Package authoring adapts local source build/staging to
// Workflow Catalog's atomic authoring API. Static sources, validation, and
// filesystem distribution remain in internal/infra/workflowdistribution.
package authoring

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/driver"
	workflowdistribution "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

const (
	BuiltinEpicRunnerWorkflowName        = workflowcatalog.BuiltinEpicRunnerWorkflowName
	BuiltinGitHubReviewAgentWorkflowName = workflowcatalog.BuiltinGitHubReviewAgentWorkflowName
	BuiltinGitHubReviewTaskRunnerName    = workflowcatalog.BuiltinGitHubReviewTaskRunnerName
	BuiltinBugFixAgentWorkflowName       = workflowcatalog.BuiltinBugFixAgentWorkflowName
	BuiltinReviewLoopAgentWorkflowName   = workflowcatalog.BuiltinReviewLoopAgentWorkflowName
	BuiltinLocalReviewAgentWorkflowName  = workflowcatalog.BuiltinLocalReviewAgentWorkflowName
	BuiltinPromptAgentWorkflowName       = workflowcatalog.BuiltinPromptAgentWorkflowName
	WorkflowSourceSchemaVersion          = workflowdistribution.WorkflowSourceSchemaVersion
)

var ErrBuildToolchainUnavailable = workflowdistribution.ErrBuildToolchainUnavailable

type Spec = workflowdistribution.Spec
type SourceManifest = workflowdistribution.SourceManifest
type LocalSource = workflowdistribution.LocalSource

func BuiltinWorkflowNames() []string {
	return workflowdistribution.BuiltinWorkflowNames()
}

func BuiltinWorkflow(name string) (Spec, bool) {
	return workflowdistribution.BuiltinWorkflow(name)
}

func IsBuiltinWorkflow(name string) bool {
	return workflowcatalog.IsBuiltinWorkflowName(strings.TrimSpace(name))
}

func BuildBuiltinBundle(ctx context.Context, name, destDir string) (string, string, error) {
	return workflowdistribution.BuildBuiltinBundle(ctx, name, destDir)
}

// ResolveDaytonaSDKRoot resolves the provider SDK shipped with the active Flue
// runtime. Keeping runtime-layout discovery in the distribution adapter lets
// host adapters consume one cohesive authoring and staging boundary.
func ResolveDaytonaSDKRoot() (string, error) {
	runtimeRoot, err := workflowdistribution.FlueRuntimeRoot()
	if err != nil {
		return "", fmt.Errorf("resolve Flue runtime: %w", err)
	}
	return workflowdistribution.DaytonaSDKRoot(runtimeRoot)
}

func SourceDigest(files map[string]string) (string, error) {
	return workflowdistribution.SourceDigest(files)
}

func CloneBuiltinSource(name, outDir string) (*SourceManifest, error) {
	return workflowdistribution.CloneBuiltinSource(name, outDir)
}

func ReadLocalSource(workflow, sourceDir string) (*LocalSource, error) {
	return workflowdistribution.ReadLocalSource(workflow, sourceDir)
}

func SourceManifestProvenance(manifest SourceManifest) map[string]string {
	return workflowdistribution.SourceManifestProvenance(manifest)
}

func ValidateWorkflowEntrypoint(name, entrypoint string) error {
	return workflowdistribution.ValidateWorkflowEntrypoint(name, entrypoint)
}

func ValidateWorkflowFiles(in map[string]string) (map[string]string, error) {
	return workflowdistribution.ValidateWorkflowFiles(in)
}

func RedactBuildDiagnostics(input string) string {
	return workflowdistribution.RedactBuildDiagnostics(input)
}

type bundleStager struct{}

func NewBundleStager() appworkflowauthoring.BundleStager {
	return bundleStager{}
}

func NewNativeBundleStager() appworkflowauthoring.NativeBundleStager {
	return bundleStager{}
}

func (bundleStager) BuildAndStage(
	ctx context.Context,
	options appworkflowauthoring.BuildOptions,
) (appworkflowauthoring.StagedBundle, string, error) {
	files, err := workflowdistribution.ValidateWorkflowFiles(options.Files)
	if err != nil {
		return nil, "", err
	}
	options.Files = files
	if options.SourceDigest == "" {
		options.SourceDigest, err = workflowdistribution.SourceDigest(options.Files)
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
	built, output, err := workflowdistribution.Build(ctx, workflowdistribution.BuildOptions{
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

func (bundle *stagedBundle) Cleanup() {
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
	return workflowdistribution.DeriveWorkflowRunnerSpecs(entrypoint, files)
}

func workflowRunnerNameSet(spec Spec) map[string]struct{} {
	return workflowdistribution.RunnerNameSet(spec)
}

func activeManifestRunnersAreStale(manifest map[string]string, fresh map[string]struct{}) bool {
	return workflowdistribution.ActiveManifestRunnersAreStale(manifest, fresh)
}

func manifestMissingFreshRunners(manifest map[string]string, fresh map[string]struct{}) []string {
	return workflowdistribution.ManifestMissingFreshRunners(manifest, fresh)
}
