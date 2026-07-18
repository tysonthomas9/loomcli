package driver

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/store"
)

// DefaultStaleTaskRunMaxAge is retained as host configuration for Execution's
// typed stale-recovery runtime.
const DefaultStaleTaskRunMaxAge = 20 * time.Minute

// resolveSweepWorkspaces is the shared read-only workspace projection used by
// background reconcilers. It performs no Execution aggregate mutation.
func resolveSweepWorkspaces(ctx context.Context, source store.Store, configured, label string) ([]string, error) {
	if configured != "" {
		return []string{configured}, nil
	}
	workspaces, err := source.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces for %s: %w", label, err)
	}
	keys := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		if workspace != nil {
			keys = append(keys, workspace.Key)
		}
	}
	return keys, nil
}
