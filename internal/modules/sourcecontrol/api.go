package sourcecontrol

import "github.com/tysonthomas9/loomcli/internal/platform/authority"

const (
	// ActionMaterializeWorkspace authorizes one serve-owned checkout
	// materialization request. Credentials are not part of this API.
	ActionMaterializeWorkspace authority.Action = "sourcecontrol.materialize-workspace"
	// ActionFetchRepositoryRef authorizes one exact read-only ref fetch into a
	// Source-Control-owned refs/loom destination.
	ActionFetchRepositoryRef authority.Action = "sourcecontrol.fetch-repository-ref"
)

// OperationRules is the default-deny Source Control operation registry for the
// minimal Phase 5 materializer. Interactive and execution callers reach this
// through registered server workflows; they do not receive filesystem or Git
// authority directly.
func OperationRules() []authority.OperationRule {
	return []authority.OperationRule{
		authority.Allow(ActionMaterializeWorkspace, authority.ClassSystem),
		authority.Allow(ActionFetchRepositoryRef, authority.ClassSystem),
	}
}

type TaskOutcomeCommand struct {
	WorkspaceKey string
	Repository   string
	TaskID       string
	Metadata     map[string]string
}
