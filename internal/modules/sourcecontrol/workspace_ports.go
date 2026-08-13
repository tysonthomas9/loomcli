package sourcecontrol

import (
	"context"
	"fmt"
)

// WorkspacePorts is the composition result for Source Control's three public
// workspace-facing capabilities. Consumers receive only the port they need.
type WorkspacePorts struct {
	Browse   Browse
	Mutate   Mutate
	Checkout Checkout
}

type workspaceOperations struct {
	*browseModule
	*checkoutOperations
	files      workspaceFileAdapter
	accessSeal *accessGrantSeal
}

var _ Browse = (*workspaceOperations)(nil)

// NewWorkspacePorts composes Source Control policy over Workspace-owned
// placement and private machine-local mechanics.
func NewWorkspacePorts(
	grants AccessGrantIssuer,
	layout CheckoutLayout,
	git GitBrowseMechanics,
	files workspaceFileAdapter,
	branches BranchMechanics,
	forge ForgePublication,
) (WorkspacePorts, error) {
	if grants.seal == nil || layout == nil || git == nil || files == nil || branches == nil || forge == nil {
		return WorkspacePorts{}, fmt.Errorf("compose Source Control workspace ports: layout, Git browse, file, branch, and forge adapters are required: %w", ErrUnavailable)
	}
	browse, err := newDiffBrowse(layout, git)
	if err != nil {
		return WorkspacePorts{}, err
	}
	checkout, err := newCheckoutOperations(layout, branches, forge)
	if err != nil {
		return WorkspacePorts{}, err
	}
	operations := &workspaceOperations{
		browseModule:       browse,
		checkoutOperations: checkout,
		files:              files,
		accessSeal:         grants.seal,
	}
	return WorkspacePorts{
		Browse:   operations,
		Mutate:   operations,
		Checkout: operations,
	}, nil
}

func (operations *workspaceOperations) ListDirectory(ctx context.Context, query PathQuery) (*FileTreeResult, error) {
	if err := requireReadGrant(operations.accessSeal, query.Grant); err != nil {
		return nil, err
	}
	return operations.files.ListDirectoryAuthorized(
		ctx,
		query.Location.WorkspaceKey, query.Location.Scope, query.Location.Target,
		query.Location.Repository, query.Path, query.Grant.sensitive,
	)
}

func (operations *workspaceOperations) ReadFile(ctx context.Context, query PathQuery) (*FileReadResult, error) {
	if err := requireReadGrant(operations.accessSeal, query.Grant); err != nil {
		return nil, err
	}
	return operations.files.ReadFileAuthorized(
		ctx,
		query.Location.WorkspaceKey, query.Location.Scope, query.Location.Target,
		query.Location.Repository, query.Path, query.Grant.sensitive,
	)
}

func (operations *workspaceOperations) StatPath(ctx context.Context, query PathQuery) (*FileStatResult, error) {
	if err := requireReadGrant(operations.accessSeal, query.Grant); err != nil {
		return nil, err
	}
	return operations.files.StatPathAuthorized(
		ctx,
		query.Location.WorkspaceKey, query.Location.Scope, query.Location.Target,
		query.Location.Repository, query.Path, query.Grant.sensitive,
	)
}

func (operations *workspaceOperations) ReadFileAtRevision(ctx context.Context, query RevisionQuery) (*FileReadResult, error) {
	if err := requireReadGrant(operations.accessSeal, query.Grant); err != nil {
		return nil, err
	}
	return operations.files.ReadFileAtRevisionAuthorized(
		ctx,
		query.Location.WorkspaceKey, query.Location.Scope, query.Location.Target,
		query.Location.Repository, query.Path, query.Revision, query.Grant.sensitive,
	)
}

func (operations *workspaceOperations) IndexFiles(ctx context.Context, query LocationQuery) (*FileIndexResult, error) {
	if err := requireReadGrant(operations.accessSeal, query.Grant); err != nil {
		return nil, err
	}
	return operations.files.IndexFilesAuthorized(
		ctx,
		query.Location.WorkspaceKey, query.Location.Scope, query.Location.Target, query.Location.Repository,
		query.Grant.sensitive,
	)
}

func (operations *workspaceOperations) SearchFiles(ctx context.Context, query SearchQuery) (*FileSearchResult, error) {
	if err := requireReadGrant(operations.accessSeal, query.Grant); err != nil {
		return nil, err
	}
	return operations.files.SearchFilesAuthorized(
		ctx,
		query.Location.WorkspaceKey, query.Location.Scope, query.Location.Target,
		query.Location.Repository, query.Search, query.Grant.sensitive,
	)
}

func (operations *workspaceOperations) DiffPath(ctx context.Context, query PathDiffQuery) (*FileDiffResult, error) {
	if err := requireReadGrant(operations.accessSeal, query.Grant); err != nil {
		return nil, err
	}
	return operations.files.DiffPathAuthorized(
		ctx,
		query.Location.WorkspaceKey, query.Location.Scope, query.Location.Target,
		query.Location.Repository, query.Path, query.From, query.To, query.Grant.sensitive,
	)
}

func (operations *workspaceOperations) BlamePath(ctx context.Context, query PathQuery) (*FileBlameResult, error) {
	if err := requireReadGrant(operations.accessSeal, query.Grant); err != nil {
		return nil, err
	}
	return operations.files.BlamePathAuthorized(
		ctx,
		query.Location.WorkspaceKey, query.Location.Scope, query.Location.Target,
		query.Location.Repository, query.Path, query.Grant.sensitive,
	)
}

func (operations *workspaceOperations) PathHistory(ctx context.Context, query PathQuery) (*FileHistoryResult, error) {
	if err := requireReadGrant(operations.accessSeal, query.Grant); err != nil {
		return nil, err
	}
	return operations.files.PathHistoryAuthorized(
		ctx,
		query.Location.WorkspaceKey, query.Location.Scope, query.Location.Target,
		query.Location.Repository, query.Path, query.Grant.sensitive,
	)
}

func (operations *workspaceOperations) WriteFile(ctx context.Context, command WriteCommand) (*FileMutationResult, error) {
	if err := requireWriteGrant(operations.accessSeal, command.Grant); err != nil {
		return nil, err
	}
	return operations.files.WriteFileAuthorized(
		ctx,
		command.Location.WorkspaceKey, command.Location.Scope, command.Location.Target,
		command.Location.Repository, command.Path, command.Content,
		FileWritePreconditions{IfMatch: command.ExpectedVersion, IfNoneMatch: command.CreateOnly}, command.Grant.sensitive,
	)
}

func (operations *workspaceOperations) DeletePath(ctx context.Context, command DeleteCommand) error {
	if err := requireWriteGrant(operations.accessSeal, command.Grant); err != nil {
		return err
	}
	return operations.files.DeletePathAuthorized(
		ctx,
		command.Location.WorkspaceKey, command.Location.Scope, command.Location.Target,
		command.Location.Repository, command.Path, command.Recursive, command.ExpectedVersion, command.Grant.sensitive,
	)
}

func (operations *workspaceOperations) CreateDirectory(ctx context.Context, command CreateDirectoryCommand) error {
	if err := requireWriteGrant(operations.accessSeal, command.Grant); err != nil {
		return err
	}
	return operations.files.CreateDirectoryAuthorized(
		ctx,
		command.Location.WorkspaceKey, command.Location.Scope, command.Location.Target,
		command.Location.Repository, command.Path, command.Grant.sensitive,
	)
}

func (operations *workspaceOperations) MovePath(ctx context.Context, command MoveCommand) (*FileMutationResult, error) {
	if err := requireWriteGrant(operations.accessSeal, command.Grant); err != nil {
		return nil, err
	}
	return operations.files.MovePathAuthorized(
		ctx,
		command.Location.WorkspaceKey, command.Location.Scope, command.Location.Target,
		command.Location.Repository, command.From, command.To, command.Overwrite,
		command.ExpectedSourceVersion, command.ExpectedDestinationVersion, command.Grant.sensitive,
	)
}

func (operations *workspaceOperations) Status(ctx context.Context, query LocationQuery) (FileGitStatusResult, error) {
	if err := requireReadGrant(operations.accessSeal, query.Grant); err != nil {
		return FileGitStatusResult{}, err
	}
	return operations.files.StatusAuthorized(
		ctx,
		query.Location.WorkspaceKey, query.Location.Scope, query.Location.Target, query.Location.Repository,
		query.Grant.sensitive,
	)
}

func (operations *workspaceOperations) ListCheckouts(ctx context.Context, query WorkspaceQuery) (*FileCheckoutsResult, error) {
	if err := requireReadGrant(operations.accessSeal, query.Grant); err != nil {
		return nil, err
	}
	return operations.files.ListCheckoutsAuthorized(ctx, query.WorkspaceKey, query.Grant.sensitive)
}

func (operations *workspaceOperations) Repair(ctx context.Context, command RepairCommand) (*RepairResult, error) {
	if err := requireWriteGrant(operations.accessSeal, command.Grant); err != nil {
		return nil, err
	}
	return operations.files.RepairCheckoutAuthorized(
		ctx,
		command.Location.WorkspaceKey,
		FileCheckoutRepairRequest{
			Scope: string(command.Location.Scope), Target: command.Location.Target,
			Repo: command.Location.Repository, Force: command.Force,
		},
	)
}
