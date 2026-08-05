package authoring

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	workflowdistribution "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// DriverCatalog and the helpers in this file exist only in the package's test
// build. They preserve historical fixtures while production authoring uses the
// typed Workflow Catalog ports.
type DriverCatalog interface {
	Drivers() store.DriverStore
	DriverVersions() store.DriverVersionStore
}

type driverLifecycleTestCatalog interface {
	ApproveDriverVersionForTest(context.Context, string, string, string) (*domain.Driver, error)
	UnapproveDriverVersionForTest(context.Context, string, string, string) (*domain.Driver, error)
	ActivateDriverVersionForTest(context.Context, string, string, string) (*domain.Driver, error)
}

func submissionTrust(trust domain.DriverTrustLevel) domain.DriverTrustLevel {
	if trust == "" {
		return domain.DriverTrustUntrusted
	}
	return trust
}

func BuildAndRegister(
	ctx context.Context,
	catalog DriverCatalog,
	opts BuildAndRegisterOptions,
) (*driver.RegisterFlueResult, string, error) {
	var err error
	if opts.SourceDigest == "" {
		opts.SourceDigest, err = SourceDigest(opts.Files)
		if err != nil {
			return nil, "", err
		}
	}
	if opts.SourceRef == "" {
		opts.SourceRef = "api://workflows/" + opts.Name + "/versions/" + opts.SourceDigest
	}
	if opts.CreatedBy == "" {
		opts.CreatedBy = "api"
	}
	built, output, err := workflowdistribution.Build(ctx, workflowdistribution.BuildOptions{
		Name: opts.Name, Files: opts.Files, WorkDir: opts.WorkDir,
	})
	if err != nil {
		return nil, output, err
	}
	defer built.Cleanup()
	result, err := registerFlueDriverForLegacyTest(ctx, catalog, driver.RegisterFlueOptions{
		WorkspaceKey: opts.WorkspaceKey, WorkDir: built.WorkDir, DistPath: built.OutputDir,
		DriverName: opts.Name, DriverID: opts.DriverID,
		WorkflowName: strings.TrimSuffix(filepath.Base(opts.Entrypoint), filepath.Ext(opts.Entrypoint)),
		SourceRef:    opts.SourceRef, SourceDigest: opts.SourceDigest, CreatedBy: opts.CreatedBy,
		Activate: opts.Activate, RunnerSpecs: workflowRunnerSpecs(opts), Manifest: opts.Manifest,
		BuildDiagnostics: output, Trust: submissionTrust(opts.Trust),
	})
	return result, output, err
}

func registerFlueDriverForLegacyTest(
	ctx context.Context,
	catalog DriverCatalog,
	opts driver.RegisterFlueOptions,
) (*driver.RegisterFlueResult, error) {
	if catalog == nil {
		return nil, fmt.Errorf("test Driver catalog is required: %w", domain.ErrInvalid)
	}
	staged, err := driver.StageFlueDriverBundle(opts)
	if err != nil {
		return nil, err
	}
	defer staged.Cleanup()

	trust := opts.Trust
	if trust == "" {
		trust = domain.DriverTrustTrusted
	}
	driverRecord, err := catalog.Drivers().Get(ctx, opts.WorkspaceKey, staged.DriverID)
	createdDriver := false
	switch {
	case errors.Is(err, domain.ErrNotFound):
		driverRecord, err = catalog.Drivers().Create(ctx, store.DriverCreate{
			WorkspaceKey: opts.WorkspaceKey,
			DriverID:     staged.DriverID,
			Name:         staged.DriverName,
			OwnerType:    domain.DriverOwnerUser,
			Description:  "Native Flue driver registered from " + staged.SourceRef,
			Status:       domain.DriverStatusDraft,
			TrustLevel:   trust,
			Metadata: map[string]string{
				"source_ref": staged.SourceRef, "runtime": driver.RuntimeFlueNode,
				"entrypoint": driver.EntrypointRun, "artifact_kind": driver.NativeFlueArtifactKind,
			},
		})
		createdDriver = err == nil
	case err == nil && !trust.Trusted() && driverRecord.TrustLevel.Trusted():
		driverRecord, err = catalog.Drivers().Update(
			ctx,
			opts.WorkspaceKey,
			staged.DriverID,
			store.DriverUpdate{TrustLevel: &trust},
		)
	}
	if err != nil {
		return nil, err
	}

	result := &driver.RegisterFlueResult{
		Driver: driverRecord, CreatedDriver: createdDriver, Bundle: staged.Bundle,
	}
	version, err := catalog.DriverVersions().Get(ctx, opts.WorkspaceKey, staged.VersionID)
	switch {
	case err == nil:
		if version.DriverID != staged.DriverID || version.BundleDigest != staged.BundleDigest {
			return nil, domain.ErrAlreadyExists
		}
		result.Version, result.ReusedVersion = version, true
	case errors.Is(err, domain.ErrNotFound):
		versions, listErr := catalog.DriverVersions().List(
			ctx,
			opts.WorkspaceKey,
			store.DriverVersionFilter{DriverID: staged.DriverID},
		)
		if listErr != nil {
			return nil, listErr
		}
		sequence := 1
		for _, current := range versions {
			if current != nil && current.Version >= sequence {
				sequence = current.Version + 1
			}
		}
		if err := staged.Promote(); err != nil {
			return nil, err
		}
		version, err = catalog.DriverVersions().Create(ctx, store.DriverVersionCreate{
			WorkspaceKey:     opts.WorkspaceKey,
			VersionID:        staged.VersionID,
			DriverID:         staged.DriverID,
			Version:          sequence,
			SourceRef:        staged.SourceRef,
			SourceDigest:     staged.SourceDigest,
			BundleRef:        staged.BundleRef,
			BundleDigest:     staged.BundleDigest,
			Runtime:          staged.Runtime,
			Manifest:         cloneLegacyMap(staged.Bundle.Manifest),
			BuildDiagnostics: staged.BuildDiagnostics,
			ValidationStatus: domain.DriverVersionValidationPassed,
			CreatedBy:        opts.CreatedBy,
		})
		if err != nil {
			return nil, err
		}
		result.Version, result.CreatedVersion = version, true
	default:
		return nil, err
	}
	if result.ReusedVersion {
		if err := staged.Promote(); err != nil {
			return nil, err
		}
	}
	if opts.Activate && result.Driver.ActiveVersionID != result.Version.VersionID {
		lifecycle, ok := catalog.(driverLifecycleTestCatalog)
		if !ok {
			return nil, fmt.Errorf("legacy test catalog lacks typed Workflow Catalog lifecycle fixtures: %w", domain.ErrInvalid)
		}
		wasUntrusted := driver.DriverVersionEffectiveTrust(result.Driver, result.Version) == domain.DriverTrustUntrusted
		if _, err = lifecycle.ApproveDriverVersionForTest(ctx, opts.WorkspaceKey, staged.DriverID, result.Version.VersionID); err != nil {
			return nil, fmt.Errorf("approve driver version: %w", err)
		}
		result.Driver, err = lifecycle.ActivateDriverVersionForTest(ctx, opts.WorkspaceKey, staged.DriverID, result.Version.VersionID)
		if err != nil {
			return nil, err
		}
		if wasUntrusted {
			result.Driver, err = lifecycle.UnapproveDriverVersionForTest(ctx, opts.WorkspaceKey, staged.DriverID, result.Version.VersionID)
			if err != nil {
				return nil, fmt.Errorf("restore active untrusted version: %w", err)
			}
		}
		result.Activated = true
	}
	return result, nil
}

func EnsureAndResolveDriver(ctx context.Context, catalog DriverCatalog, workspace, name string) (*domain.Driver, error) {
	if IsBuiltinWorkflow(name) {
		if err := EnsureBuiltinWorkflow(ctx, catalog, workspace, name); err != nil {
			return nil, err
		}
	}
	return ResolveDriver(ctx, catalog, workspace, name)
}

func ResolveDriver(ctx context.Context, catalog DriverCatalog, workspace, name string) (*domain.Driver, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("workflow name is required: %w", domain.ErrInvalid)
	}
	record, err := catalog.Drivers().Get(ctx, workspace, name)
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	records, err := catalog.Drivers().List(ctx, workspace, store.DriverFilter{Name: name, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, domain.ErrNotFound
	}
	return records[0], nil
}

func ResolveDriverID(ctx context.Context, catalog DriverCatalog, workspace, name string) (string, error) {
	record, err := ResolveDriver(ctx, catalog, workspace, name)
	if err != nil {
		return "", err
	}
	return record.DriverID, nil
}

func EnsureBuiltinWorkflow(ctx context.Context, catalog DriverCatalog, workspace, name string) error {
	return ensureBuiltinWorkflow(ctx, catalog, workspace, name, false)
}

func ensureBuiltinWorkflow(
	ctx context.Context,
	catalog DriverCatalog,
	workspace, name string,
	requireManagedRefresh bool,
) error {
	spec, ok := BuiltinWorkflow(name)
	if !ok {
		return domain.ErrNotFound
	}
	builtinMu.Lock()
	defer builtinMu.Unlock()
	digest, err := SourceDigest(spec.Files)
	if err != nil {
		return err
	}
	reuse, current, missing, preserve, err := builtinReuseDecision(
		ctx,
		catalog,
		workspace,
		name,
		workflowRunnerNameSet(spec),
	)
	if err != nil {
		return err
	}
	if preserve || (reuse && current == digest) {
		return nil
	}
	err = registerBuiltinWorkflow(ctx, catalog, workspace, name, spec, digest)
	if err == nil {
		return nil
	}
	return handleBuiltinWorkflowRegistrationError(
		err, name, workspace, current, digest, reuse, missing, requireManagedRefresh,
	)
}

func registerBuiltinWorkflow(
	ctx context.Context,
	catalog DriverCatalog,
	workspace, name string,
	spec Spec,
	digest string,
) error {
	if found, err := registerPackagedBuiltinWorkflow(ctx, catalog, workspace, name, spec, digest); found || err != nil {
		return err
	}
	_, _, err := BuildAndRegister(ctx, catalog, BuildAndRegisterOptions{
		WorkspaceKey: workspace, Name: name, Entrypoint: spec.Entrypoint, Files: spec.Files,
		Activate: true, SourceRef: "builtin://workflows/" + name + "/versions/" + digest,
		SourceDigest: digest, CreatedBy: "system", WorkDir: builtinWorkflowWorkDir(),
		DeriveRunners: true, Trust: domain.DriverTrustTrusted,
	})
	return err
}

func builtinReuseDecision(
	ctx context.Context,
	catalog DriverCatalog,
	workspace, name string,
	fresh map[string]struct{},
) (bool, string, []string, bool, error) {
	driverID, err := ResolveDriverID(ctx, catalog, workspace, name)
	if errors.Is(err, domain.ErrNotFound) {
		return false, "", nil, false, nil
	}
	if err != nil {
		return false, "", nil, false, err
	}
	current, available, manifest, preserve, err := activeBuiltInWorkflowState(
		ctx, catalog, workspace, driverID, name,
	)
	if err != nil {
		return false, "", nil, false, err
	}
	if preserve {
		if !available {
			return false, current, nil, false,
				fmt.Errorf("operator-managed active version for built-in workflow %q has no usable staged bundle; refusing to replace it", name)
		}
		return true, current, nil, true, nil
	}
	if !available || activeManifestRunnersAreStale(manifest, fresh) {
		return false, current, nil, false, nil
	}
	if missing := manifestMissingFreshRunners(manifest, fresh); len(missing) > 0 {
		return false, current, missing, false, nil
	}
	return true, current, nil, false, nil
}

func activeBuiltInWorkflowState(
	ctx context.Context,
	catalog DriverCatalog,
	workspace, driverID, builtinName string,
) (string, bool, map[string]string, bool, error) {
	record, err := catalog.Drivers().Get(ctx, workspace, driverID)
	if err != nil {
		return "", false, nil, false, err
	}
	if strings.TrimSpace(record.ActiveVersionID) == "" {
		return "", false, nil, false, nil
	}
	version, err := catalog.DriverVersions().Get(ctx, workspace, record.ActiveVersionID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", false, nil, false, nil
	}
	if err != nil {
		return "", false, nil, false, err
	}
	prefix := "builtin://workflows/" + strings.TrimSpace(builtinName) + "/versions/"
	operatorManaged := strings.TrimSpace(version.CreatedBy) != "system" ||
		!strings.HasPrefix(strings.TrimSpace(version.SourceRef), prefix)
	return strings.TrimSpace(version.SourceDigest), builtInWorkflowBundleAvailable(version),
		version.Manifest, operatorManaged, nil
}

type BoundPromptAgentCatalog interface {
	DriverCatalog
	Workspaces() store.WorkspaceStore
	TriggerBindings() store.TriggerBindingStore
}

func EnsureBoundPromptAgentWorkflows(ctx context.Context, catalog BoundPromptAgentCatalog) error {
	if catalog == nil {
		return fmt.Errorf("prompt-agent workflow catalog is required: %w", domain.ErrInvalid)
	}
	workspaces, err := catalog.Workspaces().List(ctx)
	if err != nil {
		return err
	}
	var joined []error
	for _, workspace := range workspaces {
		if workspace == nil || strings.TrimSpace(workspace.Key) == "" {
			continue
		}
		enabled := true
		bindings, err := catalog.TriggerBindings().List(ctx, workspace.Key, store.TriggerBindingFilter{
			DriverID: BuiltinPromptAgentWorkflowName, Enabled: &enabled, Limit: 1,
		})
		if err != nil {
			joined = append(joined, err)
			continue
		}
		if len(bindings) > 0 {
			if err := ensureBuiltinWorkflow(
				ctx, catalog, workspace.Key, BuiltinPromptAgentWorkflowName, true,
			); err != nil {
				joined = append(joined, err)
			}
		}
	}
	return errors.Join(joined...)
}

func registerPackagedBuiltinWorkflow(
	ctx context.Context,
	catalog DriverCatalog,
	workspace, name string,
	spec Spec,
	digest string,
) (bool, error) {
	return registerPackagedBuiltinWorkflowFromFS(
		ctx, catalog, workspace, name, spec, digest, packagedBuiltinFS,
	)
}

func registerPackagedBuiltinWorkflowFromFS(
	ctx context.Context,
	catalog DriverCatalog,
	workspace, name string,
	spec Spec,
	digest string,
	source fs.FS,
) (bool, error) {
	distPath := filepath.ToSlash(filepath.Join("builtin-dist", name, "dist"))
	if _, err := fs.Stat(source, filepath.ToSlash(filepath.Join(distPath, "server.mjs"))); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return true, err
	}
	matches, err := packagedBuiltinDigestMatches(source, distPath, digest)
	if err != nil || !matches {
		return false, err
	}
	workDir := builtinWorkflowWorkDir()
	buildParent := filepath.Join(workDir, ".loom", "workflow-builds")
	if err := os.MkdirAll(buildParent, 0o755); err != nil {
		return true, err
	}
	buildRoot, err := os.MkdirTemp(buildParent, name+"-packaged-*")
	if err != nil {
		return true, err
	}
	defer os.RemoveAll(buildRoot) //nolint:errcheck
	outputDir := filepath.Join(buildRoot, "dist")
	if err := copyPackagedBuiltinTree(source, distPath, outputDir); err != nil {
		return true, err
	}
	_, err = registerFlueDriverForLegacyTest(ctx, catalog, driver.RegisterFlueOptions{
		WorkspaceKey: workspace, WorkDir: workDir, DistPath: outputDir,
		DriverName: name, DriverID: name,
		WorkflowName: strings.TrimSuffix(filepath.Base(spec.Entrypoint), filepath.Ext(spec.Entrypoint)),
		SourceRef:    "builtin://workflows/" + name + "/versions/" + digest,
		SourceDigest: digest, CreatedBy: "system", Activate: true,
		RunnerSpecs: deriveWorkflowRunnerSpecs(spec.Entrypoint, spec.Files),
		Trust:       domain.DriverTrustTrusted,
	})
	return true, err
}

func cloneLegacyMap(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
