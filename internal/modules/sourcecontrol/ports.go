package sourcecontrol

import "context"

// RepositoryResolver resolves one opaque Workspace-owned repository reference
// into the machine-local checkout projection needed by Source Control. The
// implementation belongs to composition/Workspace; callers cannot supply a
// remote URL through MaterializeCommand. materializationID is part of the
// lookup coordinate: a Workspace admission projection may be visible only to
// the exact operation that registered it, while ordinary task and PR
// materializers continue to resolve committed Repository records only.
type RepositoryResolver interface {
	ResolveRepositoryCheckout(
		context.Context,
		string,
		string,
		string,
	) (RepositoryCheckout, error)
	RecordRepositoryCheckout(context.Context, RepositoryCheckout, string) error
}

// RepositoryAdmissionLocalResolver returns only the machine-local coordinates
// needed to join an opaque admission ID to FleetDB's durable admission record.
// Implementations must survive serve restart and fail closed on divergent
// operation, fingerprint, or checkout-root bindings.
type RepositoryAdmissionLocalResolver interface {
	ResolveLocalRepositoryAdmission(
		context.Context,
		string,
	) (RepositoryAdmissionLocalProjection, error)
}

// GitReadBroker is Source Control's credential-free outbound port. The
// Connectors adapter executes the authenticated read instead of returning
// plaintext credentials or a generally reusable helper.
type GitReadBroker interface {
	Clone(context.Context, GitCloneRequest) (GitCloneReceipt, error)
	FetchRef(context.Context, GitFetchRequest) (GitFetchReceipt, error)
}

// CheckoutInspector compares an existing checkout to one expected remote and
// returns only a match classification. It must not return the observed remote,
// which could contain legacy userinfo or another embedded secret.
type CheckoutInspector interface {
	// CanonicalTarget validates that workspacePath is a real directory, that
	// targetPath and every existing parent below it are non-symlink paths
	// contained by that workspace, and returns a stable resolved lock identity.
	CanonicalTarget(context.Context, string, string) (string, error)
	MatchRemote(context.Context, string, string, string) (CheckoutMatch, error)
	ResolveCommit(context.Context, string, string) (string, error)
}

// TaskOutcomeStore is the Source Control persistence port for the narrow
// stack-lineage transition used by Execution's finalize barrier.
type TaskOutcomeStore interface {
	ListTaskStacks(context.Context, string) ([]TaskStack, error)
	ListTaskStackNodes(context.Context, string, string) ([]TaskStackNode, error)
	UpdateTaskStackOutcome(context.Context, string, string, string, TaskStackOutcomeMutation) error
}

// StackLifecycleStore is the persistence port for Source Control-owned stack
// topology. Its implementation may retain the legacy local JSON format while
// callers migrate, but all policy and public mutations enter through
// StackLifecycle.
type StackLifecycleStore interface {
	EnsureStackRecord(context.Context, Stack) error
	GetStackRecord(context.Context, string, string) (*Stack, error)
	ListStackRecords(context.Context, string) ([]Stack, error)
	ListStackNodeRecords(context.Context, string, string) ([]StackNode, error)
	AddStackNodeRecord(context.Context, string, string, string, string, string) (StackNode, error)
	MoveStackNodeRecord(context.Context, string, string, string, string) error
	SetStackNodeBaseRecord(context.Context, string, string, string, string) error
	RemoveStackNodeRecord(context.Context, string, string, string) error
	UpdateStackNodePublicationRecord(context.Context, string, string, string, StackNodePublicationMutation) error
}
