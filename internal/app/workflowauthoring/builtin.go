package workflowauthoring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

var managedBuiltinMu sync.Mutex

// EnsureBuiltin self-heals one canonical builtin through Workflow Catalog's
// atomic managed-authoring command. Bundle construction and inspection remain
// behind BuiltinSupport and BundleStager.
func (coordinator *Coordinator) EnsureBuiltin(
	ctx context.Context,
	catalog workflowcatalog.API,
	authoring workflowcatalog.VersionAuthoringAPI,
	authorities ManagedBuiltinAuthorityProvider,
	support BuiltinSupport,
	workspace,
	name string,
) error {
	return coordinator.ensureBuiltin(
		ctx,
		catalog,
		authoring,
		authorities,
		support,
		workspace,
		name,
		false,
	)
}

//nolint:funlen // Builtin convergence keeps trust, reuse, authoring, and activation decisions in one fail-closed flow.
func (coordinator *Coordinator) ensureBuiltin(
	ctx context.Context,
	catalog workflowcatalog.API,
	authoring workflowcatalog.VersionAuthoringAPI,
	authorities ManagedBuiltinAuthorityProvider,
	support BuiltinSupport,
	workspace,
	name string,
	requireManagedRefresh bool,
) error {
	if coordinator == nil || coordinator.stager == nil ||
		catalog == nil || authoring == nil || authorities == nil || support == nil {
		return workflowcatalog.ErrUnavailable
	}
	spec, ok := support.Builtin(name)
	if !ok {
		return workflowcatalog.ErrNotFound
	}

	managedBuiltinMu.Lock()
	defer managedBuiltinMu.Unlock()

	digest, err := support.SourceDigest(spec.Files)
	if err != nil {
		return fmt.Errorf("digest built-in workflow %q: %w", name, err)
	}
	reuse, current, missing, preserveOperatorVersion, expectedRevision, err :=
		builtinReuseDecision(ctx, catalog, support, workspace, name, spec.Runners)
	if err != nil {
		return err
	}
	if preserveOperatorVersion || (reuse && current == digest) {
		return nil
	}
	auth, err := authorities.AuthorityForManagedBuiltin(
		ctx,
		workspace,
		"author canonical managed builtin "+name,
	)
	if err != nil {
		return err
	}
	_, _, err = coordinator.AuthorManaged(ctx, authoring, auth, BuildOptions{
		WorkspaceKey:     workspace,
		Name:             name,
		DriverID:         name,
		Entrypoint:       spec.Entrypoint,
		Files:            spec.Files,
		Activate:         true,
		SourceRef:        workflowcatalog.BuiltinSourceRef(name, digest),
		SourceDigest:     digest,
		WorkDir:          support.WorkDir(),
		Runners:          spec.Runners,
		ExpectedRevision: expectedRevision,
	})
	if err == nil {
		return nil
	}
	return handleBuiltinRegistrationError(
		err,
		name,
		workspace,
		current,
		digest,
		reuse,
		missing,
		requireManagedRefresh,
	)
}

// RefreshBoundPromptAgentWorkflows refreshes only workspaces with an enabled
// prompt-agent binding. The index port keeps legacy stores outside the
// application workflow.
//
//nolint:funlen // The bounded sweep keeps per-workspace filtering and joined error semantics explicit.
func (coordinator *Coordinator) RefreshBoundPromptAgentWorkflows(
	ctx context.Context,
	index BoundPromptAgentIndex,
	catalog workflowcatalog.API,
	authoring workflowcatalog.VersionAuthoringAPI,
	authorities ManagedBuiltinAuthorityProvider,
	support BuiltinSupport,
) error {
	if coordinator == nil || index == nil || catalog == nil ||
		authoring == nil || authorities == nil || support == nil {
		return fmt.Errorf(
			"prompt-agent Workflow Catalog authoring is required: %w",
			workflowcatalog.ErrUnavailable,
		)
	}
	workspaces, err := index.ListWorkspaceKeys(ctx)
	if err != nil {
		return fmt.Errorf("list workspaces for prompt-agent workflow refresh: %w", err)
	}
	var refreshErrs []error
	for _, value := range workspaces {
		workspace := strings.TrimSpace(value)
		if workspace == "" {
			continue
		}
		enabled, err := index.HasEnabledPromptAgentBinding(ctx, workspace)
		if err != nil {
			refreshErrs = append(
				refreshErrs,
				fmt.Errorf("list prompt-agent bindings in workspace %q: %w", workspace, err),
			)
			continue
		}
		if !enabled {
			continue
		}
		if err := coordinator.ensureBuiltin(
			ctx,
			catalog,
			authoring,
			authorities,
			support,
			workspace,
			workflowcatalog.BuiltinPromptAgentWorkflowName,
			true,
		); err != nil {
			refreshErrs = append(
				refreshErrs,
				fmt.Errorf("refresh bound prompt-agent workflow in workspace %q: %w", workspace, err),
			)
		}
	}
	return errors.Join(refreshErrs...)
}

//nolint:funlen // Reuse is an exhaustive digest, trust, lifecycle, and management-policy decision.
func builtinReuseDecision(
	ctx context.Context,
	catalog workflowcatalog.API,
	support BuiltinSupport,
	workspace,
	name string,
	freshRunners []RunnerSpec,
) (
	reuse bool,
	registeredDigest string,
	missing []string,
	preserveOperatorVersion bool,
	expectedRevision uint64,
	err error,
) {
	driverRecord, err := catalog.GetDriver(ctx, workspace, name)
	if errors.Is(err, workflowcatalog.ErrNotFound) {
		return false, "", nil, false, 0, nil
	}
	if err != nil {
		return false, "", nil, false, 0, err
	}
	if driverRecord == nil || driverRecord.Revision == 0 {
		return false, "", nil, false, 0, workflowcatalog.ErrInvalidPersistedState
	}
	expectedRevision = driverRecord.Revision
	if strings.TrimSpace(driverRecord.ActiveVersionID) == "" {
		return false, "", nil, false, expectedRevision, nil
	}
	version, err := catalog.GetVersion(ctx, workspace, driverRecord.ActiveVersionID)
	if errors.Is(err, workflowcatalog.ErrNotFound) {
		return false, "", nil, false, expectedRevision, nil
	}
	if err != nil {
		return false, "", nil, false, expectedRevision, err
	}
	if version == nil || version.DriverID != driverRecord.DriverID ||
		version.VersionID != driverRecord.ActiveVersionID {
		return false, "", nil, false, expectedRevision, workflowcatalog.ErrInvalidPersistedState
	}
	builtinPrefix := "builtin://workflows/" + strings.TrimSpace(name) + "/versions/"
	managed := strings.HasPrefix(strings.TrimSpace(version.SourceRef), builtinPrefix) &&
		(strings.TrimSpace(version.CreatedBy) == "system" ||
			version.Manifest["provenance"] == workflowcatalog.ManagedBuiltinProvenance)
	registeredDigest = strings.TrimSpace(version.SourceDigest)
	assessment := support.AssessVersion(version, freshRunners)
	if !managed {
		if !assessment.BundleAvailable {
			return false, registeredDigest, nil, false, expectedRevision,
				fmt.Errorf(
					"operator-managed active version for built-in workflow %q has no usable staged bundle; refusing to replace it",
					name,
				)
		}
		return true, registeredDigest, nil, true, expectedRevision, nil
	}
	if !assessment.BundleAvailable || assessment.RunnerListStale {
		return false, registeredDigest, nil, false, expectedRevision, nil
	}
	if len(assessment.MissingRunners) > 0 {
		return false, registeredDigest, append([]string(nil), assessment.MissingRunners...), false, expectedRevision, nil
	}
	return true, registeredDigest, nil, false, expectedRevision, nil
}

func handleBuiltinRegistrationError(
	err error,
	name,
	workspace,
	current,
	digest string,
	reuse bool,
	reuseMissingRunners []string,
	requireManagedRefresh bool,
) error {
	if reuse && errors.Is(err, ErrBuildToolchainUnavailable) {
		if requireManagedRefresh {
			return fmt.Errorf("refresh managed built-in workflow %q to the embedded digest: %w", name, err)
		}
		slog.Warn(
			"builtin digest refresh unavailable; reusing registered version",
			"workflow",
			name,
			"workspace",
			workspace,
			"registered_digest",
			current,
			"embedded_digest",
			digest,
			"err",
			err.Error(),
		)
		return nil
	}
	if len(reuseMissingRunners) > 0 {
		if requireManagedRefresh {
			return fmt.Errorf("refresh managed built-in workflow %q runner manifest: %w", name, err)
		}
		slog.Warn(
			"builtin runner manifest is missing runners and re-register failed; reusing the registered version",
			"workflow",
			name,
			"workspace",
			workspace,
			"missing_runners",
			strings.Join(reuseMissingRunners, ","),
			"err",
			err.Error(),
		)
		return nil
	}
	return fmt.Errorf("register built-in workflow %q: %w", name, err)
}
