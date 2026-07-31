package workflowauthoring

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type Coordinator struct {
	stager       BundleStager
	nativeStager NativeBundleStager
}

func New(stager BundleStager) (*Coordinator, error) {
	if stager == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	return &Coordinator{stager: stager}, nil
}

func NewWithNative(
	stager BundleStager,
	nativeStager NativeBundleStager,
) (*Coordinator, error) {
	coordinator, err := New(stager)
	if err != nil {
		return nil, err
	}
	if nativeStager == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	coordinator.nativeStager = nativeStager
	return coordinator, nil
}

func (coordinator *Coordinator) AuthorOperator(
	ctx context.Context,
	api workflowcatalog.VersionAuthoringAPI,
	auth authority.OperatorAuthority,
	options BuildOptions,
) (*Result, string, error) {
	if options.Activate {
		return nil, "", fmt.Errorf("operator workflow authoring cannot activate a version: %w", workflowcatalog.ErrInvalid)
	}
	if options.Trust == workflowcatalog.DriverTrustTrusted {
		return nil, "", fmt.Errorf("operator workflow authoring cannot select trusted placement: %w", workflowcatalog.ErrInvalid)
	}
	options.Trust = workflowcatalog.DriverTrustUntrusted
	return coordinator.author(ctx, api, auth, authority.SystemAuthority{}, options, false)
}

func (coordinator *Coordinator) AuthorManaged(
	ctx context.Context,
	api workflowcatalog.VersionAuthoringAPI,
	auth authority.SystemAuthority,
	options BuildOptions,
) (*Result, string, error) {
	if !workflowcatalog.IsBuiltinWorkflowName(strings.TrimSpace(options.Name)) {
		return nil, "", fmt.Errorf("managed workflow %q is not a canonical builtin: %w", options.Name, workflowcatalog.ErrInvalid)
	}
	options.Trust = workflowcatalog.DriverTrustTrusted
	options.Manifest = cloneManifest(options.Manifest)
	if provenance := strings.TrimSpace(options.Manifest["provenance"]); provenance != "" &&
		provenance != workflowcatalog.ManagedBuiltinProvenance {
		return nil, "", fmt.Errorf("managed workflow provenance %q is invalid: %w", provenance, workflowcatalog.ErrInvalid)
	}
	options.Manifest["provenance"] = workflowcatalog.ManagedBuiltinProvenance
	return coordinator.author(ctx, api, authority.OperatorAuthority{}, auth, options, true)
}

func (coordinator *Coordinator) author(
	ctx context.Context,
	api workflowcatalog.VersionAuthoringAPI,
	operatorAuth authority.OperatorAuthority,
	systemAuth authority.SystemAuthority,
	options BuildOptions,
	managed bool,
) (*Result, string, error) {
	if coordinator == nil || coordinator.stager == nil || api == nil {
		return nil, "", workflowcatalog.ErrUnavailable
	}
	staged, diagnostics, err := coordinator.stager.BuildAndStage(ctx, options)
	if err != nil {
		return nil, diagnostics, err
	}
	if staged == nil {
		return nil, diagnostics, workflowcatalog.ErrInvalidPersistedState
	}
	defer staged.Cleanup()
	if err := staged.Promote(); err != nil {
		return nil, diagnostics, err
	}
	result, err := coordinator.authorStaged(
		ctx,
		api,
		operatorAuth,
		systemAuth,
		options,
		staged,
		managed,
	)
	return result, diagnostics, err
}

func (coordinator *Coordinator) authorStaged(
	ctx context.Context,
	api workflowcatalog.VersionAuthoringAPI,
	operatorAuth authority.OperatorAuthority,
	systemAuth authority.SystemAuthority,
	options BuildOptions,
	staged StagedBundle,
	managed bool,
) (*Result, error) {
	if api == nil || staged == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	metadata := staged.Metadata()
	command := workflowcatalog.AuthorVersionCommand{
		WorkspaceKey: options.WorkspaceKey, RequestID: options.RequestID,
		ExpectedRevision: options.ExpectedRevision,
		DriverID:         metadata.DriverID, DriverName: metadata.DriverName,
		VersionID: metadata.VersionID, SourceRef: metadata.SourceRef, SourceDigest: metadata.SourceDigest,
		BundleRef: metadata.BundleRef, BundleDigest: metadata.BundleDigest, Runtime: metadata.Runtime,
		Manifest: metadata.CatalogManifest, BuildDiagnostics: metadata.Diagnostics,
	}
	if strings.TrimSpace(command.RequestID) == "" {
		command.RequestID = "workflow-author:" + command.WorkspaceKey + ":" + command.DriverID + ":" + command.VersionID
	}
	var authored *workflowcatalog.AuthorVersionResult
	var err error
	if managed {
		authored, err = api.AuthorManagedVersion(ctx, systemAuth, workflowcatalog.AuthorManagedVersionCommand{
			AuthorVersionCommand: command,
			Activate:             options.Activate,
		})
	} else {
		authored, err = api.AuthorVersion(ctx, operatorAuth, command)
	}
	if err != nil {
		return nil, err
	}
	if authored == nil || authored.Driver == nil || authored.Version == nil {
		return nil, workflowcatalog.ErrInvalidPersistedState
	}
	return &Result{
		Driver: authored.Driver, Version: authored.Version, Bundle: staged.Bundle(),
		CreatedDriver: authored.CreatedDriver, CreatedVersion: authored.CreatedVersion,
		ReusedVersion: authored.ReusedVersion, Activated: authored.Activated,
	}, nil
}

func cloneManifest(input map[string]string) map[string]string {
	output := make(map[string]string, len(input)+1)
	for key, value := range input {
		output[key] = value
	}
	return output
}
