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
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
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

type BuildAndRegisterOptions struct {
	WorkspaceKey string
	Name         string
	// DriverID pins authoring to an existing aggregate whose durable ID differs
	// from its display name. Empty derives the canonical slug from Name.
	DriverID     string
	Entrypoint   string
	Files        map[string]string
	Activate     bool
	SourceRef    string
	SourceDigest string
	CreatedBy    string
	WorkDir      string
	Runners      []driver.DriverRunnerSpec
	Manifest     map[string]string
	// DeriveRunners is reserved for trusted built-in template registration.
	DeriveRunners bool
	// Trust is server-selected. External submissions default untrusted.
	Trust workflowcatalog.DriverTrustLevel
	// RequestID is the durable lost-response replay key submitted to Workflow
	// Catalog's atomic authoring command. When omitted, BuildAndAuthor derives
	// one from the content-addressed driver/version identity.
	RequestID string
	// ExpectedRevision is zero only when the driver is expected not to exist.
	ExpectedRevision uint64
}

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

// BuildAndAuthor builds and stages an operator-authored workflow bundle, then
// submits exactly one atomic Workflow Catalog command. Operator submissions
// are always untrusted and inactive; audit identity is derived by the catalog
// from auth rather than from CreatedBy.
func BuildAndAuthor(
	ctx context.Context,
	api workflowcatalog.VersionAuthoringAPI,
	auth authority.OperatorAuthority,
	opts BuildAndRegisterOptions,
) (*driver.RegisterFlueResult, string, error) {
	coordinator, err := appworkflowauthoring.New(legacyBundleStager{})
	if err != nil {
		return nil, "", err
	}
	result, diagnostics, err := coordinator.AuthorOperator(
		ctx,
		api,
		auth,
		applicationBuildOptions(opts),
	)
	return legacyAuthoringResult(result), diagnostics, err
}

// BuildAndAuthorManaged is the system-only authoring lane for canonical
// embedded workflows. It forces trusted placement and canonical managed
// provenance; activation is applied atomically when requested.
func BuildAndAuthorManaged(
	ctx context.Context,
	api workflowcatalog.VersionAuthoringAPI,
	auth authority.SystemAuthority,
	opts BuildAndRegisterOptions,
) (*driver.RegisterFlueResult, string, error) {
	coordinator, err := appworkflowauthoring.New(legacyBundleStager{})
	if err != nil {
		return nil, "", err
	}
	result, diagnostics, err := coordinator.AuthorManaged(
		ctx,
		api,
		auth,
		applicationBuildOptions(opts),
	)
	return legacyAuthoringResult(result), diagnostics, err
}

type legacyBundleStager struct{}

func NewBundleStager() appworkflowauthoring.BundleStager {
	return legacyBundleStager{}
}

func NewNativeBundleStager() appworkflowauthoring.NativeBundleStager {
	return legacyBundleStager{}
}

func (legacyBundleStager) BuildAndStage(
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
	runnerSpecs := legacyRunnerSpecs(options.Runners)
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
	return &legacyStagedBundle{staged: staged}, output, nil
}

func (legacyBundleStager) StageNative(
	_ context.Context,
	options appworkflowauthoring.NativeOptions,
) (appworkflowauthoring.StagedBundle, error) {
	staged, err := driver.StageFlueDriverBundle(driver.RegisterFlueOptions{
		WorkspaceKey: options.WorkspaceKey, WorkDir: options.WorkDir,
		DistPath: options.DistPath, ManifestPath: options.ManifestPath,
		DriverName: options.DriverName, DriverID: options.DriverID,
		WorkflowName: options.WorkflowName, SourceRef: options.SourceRef,
		SourceDigest: options.SourceDigest, Activate: options.Activate,
		RunnerSpecs: legacyRunnerSpecs(options.Runners),
		Manifest:    options.Manifest, Trust: options.Trust,
	})
	if err != nil {
		return nil, err
	}
	return &legacyStagedBundle{staged: staged}, nil
}

type legacyStagedBundle struct {
	staged      *driver.StagedFlueRegistration
	cleanupRoot string
}

func (bundle *legacyStagedBundle) Metadata() appworkflowauthoring.StagedMetadata {
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

func (bundle *legacyStagedBundle) Bundle() *appworkflowauthoring.Bundle {
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

func (bundle *legacyStagedBundle) Promote() error {
	if bundle == nil || bundle.staged == nil {
		return workflowcatalog.ErrInvalidPersistedState
	}
	return bundle.staged.Promote()
}

func (bundle *legacyStagedBundle) Cleanup() {
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

func applicationBuildOptions(options BuildAndRegisterOptions) appworkflowauthoring.BuildOptions {
	runners := make([]appworkflowauthoring.RunnerSpec, 0, len(options.Runners))
	for _, runner := range options.Runners {
		runners = append(runners, appworkflowauthoring.RunnerSpec{
			Name: runner.Name, Kind: runner.Kind, Entrypoint: runner.Entrypoint,
		})
	}
	return appworkflowauthoring.BuildOptions{
		WorkspaceKey: options.WorkspaceKey, Name: options.Name, DriverID: options.DriverID,
		Entrypoint: options.Entrypoint, Files: options.Files, Activate: options.Activate,
		SourceRef: options.SourceRef, SourceDigest: options.SourceDigest, WorkDir: options.WorkDir,
		Runners: runners, Manifest: options.Manifest, DeriveRunners: options.DeriveRunners,
		Trust: options.Trust, RequestID: options.RequestID, ExpectedRevision: options.ExpectedRevision,
	}
}

func legacyRunnerSpecs(runners []appworkflowauthoring.RunnerSpec) []driver.DriverRunnerSpec {
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

func legacyAuthoringResult(result *appworkflowauthoring.Result) *driver.RegisterFlueResult {
	if result == nil {
		return nil
	}
	var bundle *driver.Bundle
	if result.Bundle != nil {
		bundle = &driver.Bundle{
			Root: result.Bundle.Root, BundleRef: result.Bundle.BundleRef,
			SourceRef: result.Bundle.SourceRef, SourceDigest: result.Bundle.SourceDigest,
			BundleDigest: result.Bundle.BundleDigest,
			Manifest:     cloneWorkflowManifest(result.Bundle.Manifest),
			Diagnostics:  result.Bundle.Diagnostics,
		}
	}
	return &driver.RegisterFlueResult{
		Driver: result.Driver, Version: result.Version, Bundle: bundle,
		CreatedDriver: result.CreatedDriver, CreatedVersion: result.CreatedVersion,
		ReusedVersion: result.ReusedVersion, Activated: result.Activated,
	}
}

func cloneWorkflowManifest(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func workflowRunnerSpecs(opts BuildAndRegisterOptions) []driver.DriverRunnerSpec {
	if len(opts.Runners) > 0 {
		return opts.Runners
	}
	if !opts.DeriveRunners {
		return nil
	}
	return deriveWorkflowRunnerSpecs(opts.Entrypoint, opts.Files)
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

// Test-only compatibility helpers remain while legacy package tests are
// migrated with the final facade deletion.
func flueRuntimeRoot() (string, error) {
	return workflowdistribution.FlueRuntimeRoot()
}

func daytonaSDKRoot(runtimeRoot string) (string, error) {
	return workflowdistribution.DaytonaSDKRoot(runtimeRoot)
}

func linkFlueBuildDependencies(root string) error {
	return workflowdistribution.LinkFlueBuildDependencies(root)
}

func classifyFlueBuildError(cause error, output string) error {
	return workflowdistribution.ClassifyFlueBuildError(cause, output)
}
