package authoring

import (
	"context"
	"fmt"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type ManagedBuiltinAuthorityProvider = appworkflowauthoring.ManagedBuiltinAuthorityProvider

// BoundPromptAgentIndex is the legacy composition-facing read handle. The
// adapter in builtin_support.go narrows it to the application workflow's two
// neutral query methods.
type BoundPromptAgentIndex interface {
	Workspaces() store.WorkspaceStore
	TriggerBindings() store.TriggerBindingStore
}

// EnsureBuiltinWorkflowAuthored is a compatibility facade for callers that
// have not yet assembled the app coordinator explicitly. Catalog policy and
// command sequencing live in internal/app/workflowauthoring.
func EnsureBuiltinWorkflowAuthored(
	ctx context.Context,
	catalog workflowcatalog.API,
	authoring workflowcatalog.VersionAuthoringAPI,
	authorities ManagedBuiltinAuthorityProvider,
	workspace,
	name string,
) error {
	coordinator, err := appworkflowauthoring.New(NewBundleStager())
	if err != nil {
		return err
	}
	return coordinator.EnsureBuiltin(
		ctx,
		catalog,
		authoring,
		authorities,
		NewBuiltinSupport(),
		workspace,
		name,
	)
}

// EnsureBoundPromptAgentWorkflowsAuthored is the legacy composition facade for
// the app-owned startup refresh workflow.
func EnsureBoundPromptAgentWorkflowsAuthored(
	ctx context.Context,
	index BoundPromptAgentIndex,
	catalog workflowcatalog.API,
	authoring workflowcatalog.VersionAuthoringAPI,
	authorities ManagedBuiltinAuthorityProvider,
) error {
	if index == nil {
		return fmt.Errorf(
			"prompt-agent Workflow Catalog authoring is required: %w",
			workflowcatalog.ErrUnavailable,
		)
	}
	coordinator, err := appworkflowauthoring.New(NewBundleStager())
	if err != nil {
		return err
	}
	return coordinator.RefreshBoundPromptAgentWorkflows(
		ctx,
		legacyBoundPromptAgentIndex{index: index},
		catalog,
		authoring,
		authorities,
		NewBuiltinSupport(),
	)
}
