package agentprovisioning

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

const (
	RecoveryComponentID platformruntime.ComponentID = "serve-agent-provisioning-recovery"

	DefaultRecoveryLimit   = 50
	MaxRecoveryLimit       = 500
	DefaultRecoveryCadence = 10 * time.Second
	DefaultRecoveryTimeout = 30 * time.Second
)

type RecoveryCommands interface {
	Recover(context.Context, string, int) (int, error)
}

type WorkspaceLister interface {
	ListWorkspaceKeys(context.Context) ([]string, error)
}

type RuntimeConfig struct {
	WorkspaceKey    string
	WorkspaceLister WorkspaceLister
	Limit           int
}

type recoveryComponent struct {
	commands   RecoveryCommands
	workspaces runtimeWorkspaceScope
	limit      int
}

var _ RecoveryCommands = (*Manager)(nil)
var _ platformruntime.Component = (*recoveryComponent)(nil)

func (*recoveryComponent) ID() platformruntime.ComponentID {
	return RecoveryComponentID
}

func (component *recoveryComponent) RunOnce(ctx context.Context, _ time.Time) error {
	if component == nil || component.commands == nil {
		return ErrUnavailable
	}
	workspaces, err := component.workspaces.list(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, workspace := range workspaces {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if _, err := component.commands.Recover(ctx, workspace, component.limit); err != nil {
			errs = append(errs, fmt.Errorf("recover AgentProvisioning in %q: %w", workspace, err))
		}
	}
	return errors.Join(errs...)
}

func RuntimeRegistration(commands RecoveryCommands, config RuntimeConfig) (platformruntime.Registration, error) {
	if commands == nil {
		return platformruntime.Registration{}, fmt.Errorf("agent provisioning recovery commands are required: %w", ErrUnavailable)
	}
	workspaces, err := newRuntimeWorkspaceScope(config.WorkspaceKey, config.WorkspaceLister)
	if err != nil {
		return platformruntime.Registration{}, err
	}
	limit, err := normalizeRuntimeLimit(config.Limit)
	if err != nil {
		return platformruntime.Registration{}, err
	}
	return platformruntime.Registration{
		Component: &recoveryComponent{commands: commands, workspaces: workspaces, limit: limit},
		Policy: platformruntime.Policy{
			Cadence:   DefaultRecoveryCadence,
			Immediate: true,
			Timeout:   DefaultRecoveryTimeout,
			FailureBackoff: platformruntime.Backoff{
				Initial:    time.Second,
				Max:        time.Minute,
				Multiplier: 2,
			},
		},
	}, nil
}

type runtimeWorkspaceScope struct {
	fixed  string
	lister WorkspaceLister
}

func newRuntimeWorkspaceScope(workspace string, lister WorkspaceLister) (runtimeWorkspaceScope, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" && lister == nil {
		return runtimeWorkspaceScope{}, fmt.Errorf("workspace lister is required for unscoped AgentProvisioning recovery: %w", ErrUnavailable)
	}
	return runtimeWorkspaceScope{fixed: workspace, lister: lister}, nil
}

func (scope runtimeWorkspaceScope) list(ctx context.Context) ([]string, error) {
	if scope.fixed != "" {
		return []string{scope.fixed}, nil
	}
	workspaces, err := scope.lister.ListWorkspaceKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list AgentProvisioning recovery workspaces: %w", err)
	}
	seen := make(map[string]struct{}, len(workspaces))
	for _, workspace := range workspaces {
		if workspace == "" || workspace != strings.TrimSpace(workspace) {
			return nil, fmt.Errorf("workspace list contains an invalid key: %w", ErrConflict)
		}
		if _, duplicate := seen[workspace]; duplicate {
			return nil, fmt.Errorf("workspace list contains duplicate %q: %w", workspace, ErrConflict)
		}
		seen[workspace] = struct{}{}
	}
	out := append([]string(nil), workspaces...)
	sort.Strings(out)
	return out, nil
}

func normalizeRuntimeLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultRecoveryLimit, nil
	}
	if limit < 0 || limit > MaxRecoveryLimit {
		return 0, fmt.Errorf("recovery limit must be between 1 and %d: %w", MaxRecoveryLimit, ErrInvalid)
	}
	return limit, nil
}
