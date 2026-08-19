package stacksvc

import (
	"context"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

var _ service.StackService = (*Service)(nil)

type storeProvider func() stackstore.Store

// Service is the read-only stack lineage implementation for the web UI.
type Service struct {
	storeProvider storeProvider
}

// New creates the read-only stack lineage service.
func New() service.StackService {
	return newWithProvider(driverpkg.DefaultStackStore)
}

func newWithProvider(provider storeProvider) service.StackService {
	if provider == nil {
		provider = func() stackstore.Store { return nil }
	}
	return &Service{storeProvider: provider}
}

func (s *Service) ListStacks(ctx context.Context, wsID string) (*service.WorkspaceStacksResult, error) {
	store := s.storeProvider()
	if store == nil {
		return &service.WorkspaceStacksResult{Stacks: []service.WorkspaceStack{}}, nil
	}
	stacks, err := store.ListStacks(ctx, wsID)
	if err != nil {
		return nil, service.ErrInternal("failed to list stacks", err)
	}
	out := make([]service.WorkspaceStack, 0, len(stacks))
	for _, stack := range stacks {
		nodes, err := store.ListNodes(ctx, wsID, stack.ID)
		if err != nil {
			return nil, service.ErrInternal("failed to list stack nodes", err)
		}
		byTask := stacklineage.ByTask(nodes)
		apiNodes := make([]service.WorkspaceStackNode, 0, len(nodes))
		for i, node := range nodes {
			baseRef, err := stacklineage.BaseBranchSliding(stack, node, byTask)
			if err != nil {
				baseRef = ""
			}
			apiNodes = append(apiNodes, service.WorkspaceStackNode{
				TaskID:       node.TaskID,
				BaseTaskID:   node.BaseTaskID,
				OutputBranch: node.OutputBranch,
				BaseRef:      baseRef,
				Position:     i,
			})
		}
		out = append(out, service.WorkspaceStack{
			ID:       string(stack.ID),
			Repo:     stack.RepoName,
			RootBase: stack.RootBase,
			Nodes:    apiNodes,
		})
	}
	return &service.WorkspaceStacksResult{Stacks: out}, nil
}
