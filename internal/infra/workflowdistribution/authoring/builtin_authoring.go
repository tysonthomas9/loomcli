package authoring

import (
	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
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
