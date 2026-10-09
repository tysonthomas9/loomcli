package stackstore

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
)

// A dependent starts while its blocker's code awaits review only when that
// blocker is its base in a stack, so it is built on the blocker's frozen
// revision (Tyson, 2026-10-09). The FleetDB backend asks this store.
func init() {
	fleet.SetStackBaseLookup(func(ctx context.Context, workspace, taskID string) (string, error) {
		store, err := Default()
		if err != nil {
			return "", nil // No loom directory: no stacks.
		}
		return store.StackBase(ctx, workspace, taskID)
	})
}

// StackBase returns the task taskID is based on in its stack in workspace,
// or "" when it is in no stack or is a stack root.
func (s *LocalStore) StackBase(ctx context.Context, workspace, taskID string) (string, error) {
	stacks, err := s.ListStacks(ctx, workspace)
	if err != nil {
		return "", err
	}
	for _, stack := range stacks {
		nodes, err := s.ListNodes(ctx, workspace, stack.ID)
		if err != nil {
			return "", err
		}
		for _, node := range nodes {
			if node.TaskID == taskID {
				return node.BaseTaskID, nil
			}
		}
	}
	return "", nil
}
