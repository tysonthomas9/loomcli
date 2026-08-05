package sourcecontrol

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

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

// API is the minimal Phase 5 Source Control materialization surface.
type API interface {
	MaterializeWorkspace(context.Context, authority.SystemAuthority, MaterializeCommand) (*Materialization, error)
	FetchRepositoryRef(context.Context, authority.SystemAuthority, FetchRefCommand) (*FetchedRef, error)
}

// Materializer is the authority-free application port used by task execution
// and PR review. Serve composition owns authority issuance; callers provide
// only their typed durable product coordinates. Generic repository checkout is
// deliberately absent so these consumers cannot guess or replay a Workspace
// admission operation ID.
type Materializer interface {
	PrepareTaskCheckout(context.Context, TaskCheckoutCommand) (*TaskCheckout, error)
	PreparePullRequestCheckout(context.Context, PullRequestCheckoutCommand) (*PullRequestCheckout, error)
}

// RepositoryAdmissionMaterializer is the Workspace-only application port that
// materializes one member of a durable FleetDB admission batch. The command
// contains only the opaque admission ID and repository reference. It is
// deliberately not included in Materializer, which is shared with task
// execution and PR review.
type RepositoryAdmissionMaterializer interface {
	PrepareRepositoryAdmissionCheckout(
		context.Context,
		RepositoryAdmissionCheckoutCommand,
	) (*PreparedRepositoryCheckout, error)
}

// TaskOutcomeRecorder is the narrow Source Control application port used by
// task execution's finalize barrier. The capability owns stack-lineage
// persistence; the executor supplies only trusted task/repository coordinates
// and runner evidence.
type TaskOutcomeRecorder interface {
	RecordTaskOutcome(context.Context, TaskOutcomeCommand) (bool, error)
}

type TaskOutcomeCommand struct {
	WorkspaceKey string
	Repository   string
	TaskID       string
	Metadata     map[string]string
}
