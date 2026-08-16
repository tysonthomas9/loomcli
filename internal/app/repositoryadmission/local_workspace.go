package repositoryadmission

import "context"

// OwnershipCheck revalidates the exact durable owner generation immediately
// around each externally visible materialization step.
type OwnershipCheck func(context.Context) error

type CreatePlan struct {
	WorkspaceKey  string
	WorkspacePath string
	Repositories  []RepositorySpec
	CloneURLs     []string
}

type AddPlan struct {
	WorkspaceKey   string
	WorkspacePath  string
	Branch         string
	Repositories   []RepositorySpec
	CloneURLs      []string
	LocalRepoPaths []string
}

type MaterializationResult struct {
	Repositories  []RepositoryPlacement
	DefaultBranch string
}

// LocalWorkspace is the machine-local adapter used by the durable workflow.
// It owns placement, checkout, local-cache, and role-seeding mechanics but no
// durable admission state, owner generation, replay decision, or status.
type LocalWorkspace interface {
	CreateEmpty(context.Context, CreateCommand) (Result, error)
	AddWithoutAdmission(context.Context, AddRepositoriesCommand) (Result, error)
	PlanCreate(context.Context, CreateCommand) (CreatePlan, error)
	PlanAdd(context.Context, AddRepositoriesCommand) (AddPlan, error)
	MaterializeCreate(context.Context, CreateCommand, CreatePlan, *Record, OwnershipCheck) (MaterializationResult, error)
	MaterializeAdd(context.Context, AddRepositoriesCommand, AddPlan, *Record, OwnershipCheck) (MaterializationResult, error)
	Replay(context.Context, *Record, string, bool) (Result, error)
	VerifyRecoveryIntent(context.Context, LocalIntent) error
}
