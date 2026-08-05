package automationcomposition

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type automationWorkspaceLister struct {
	workspaces store.WorkspaceStore
}

var _ automation.WorkspaceLister = (*automationWorkspaceLister)(nil)

func newAutomationWorkspaceLister(workspaces store.WorkspaceStore) automation.WorkspaceLister {
	if workspaces == nil {
		return nil
	}
	return &automationWorkspaceLister{workspaces: workspaces}
}

func NewAutomationWorkspaceLister(workspaces store.WorkspaceStore) automation.WorkspaceLister {
	return newAutomationWorkspaceLister(workspaces)
}

func (lister *automationWorkspaceLister) ListWorkspaceKeys(ctx context.Context) ([]string, error) {
	if lister == nil || lister.workspaces == nil {
		return nil, automation.ErrUnavailable
	}
	values, err := lister.workspaces.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Automation workspaces: %w", err)
	}
	keys := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == nil {
			return nil, automation.ErrInvalidPersistedState
		}
		key := strings.TrimSpace(value.Key)
		if key == "" || key != value.Key {
			return nil, automation.ErrInvalidPersistedState
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, automation.ErrInvalidPersistedState
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}
