package interaction

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

const (
	SessionRecoveryComponentID   platformruntime.ComponentID = "serve-interaction-session-recovery"
	InboxDeliveryComponentID     platformruntime.ComponentID = "serve-interaction-inbox-delivery"
	TerminalLifecycleComponentID platformruntime.ComponentID = "serve-interaction-terminal-lifecycle"
	SessionRecoveryCadence                                   = 10 * time.Second
	SessionRecoveryTimeout                                   = 30 * time.Second
)

type RuntimeAuthorityProvider interface {
	AuthorityForInteractionRuntime(
		context.Context,
		platformruntime.ComponentID,
		string,
		authority.Action,
	) (authority.SystemAuthority, error)
}

type RuntimeWorkspaceLister interface {
	ListWorkspaceKeys(context.Context) ([]string, error)
}

type RuntimeConfig struct {
	WorkspaceKey    string
	WorkspaceLister RuntimeWorkspaceLister
}

type sessionRecoveryComponent struct {
	commands   API
	authority  RuntimeAuthorityProvider
	workspaces runtimeWorkspaceScope
}

var _ platformruntime.Component = (*sessionRecoveryComponent)(nil)

func (*sessionRecoveryComponent) ID() platformruntime.ComponentID {
	return SessionRecoveryComponentID
}

func (component *sessionRecoveryComponent) RunOnce(ctx context.Context, now time.Time) error {
	if component == nil || component.commands == nil || component.authority == nil {
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
		auth, err := component.authority.AuthorityForInteractionRuntime(
			ctx,
			component.ID(),
			workspace,
			ActionReconcileSessions,
		)
		if err != nil {
			errs = append(errs, fmt.Errorf("derive Interaction recovery authority for %q: %w", workspace, err))
			continue
		}
		if _, err := component.commands.ReconcileSessions(ctx, auth, workspace, now); err != nil {
			errs = append(errs, fmt.Errorf("reconcile AgentSessions in %q: %w", workspace, err))
		}
	}
	return errors.Join(errs...)
}

func RuntimeRegistration(
	commands API,
	authorityProvider RuntimeAuthorityProvider,
	config RuntimeConfig,
) (platformruntime.Registration, error) {
	if commands == nil || authorityProvider == nil {
		return platformruntime.Registration{}, fmt.Errorf("interaction runtime API and authority provider are required: %w", ErrUnavailable)
	}
	workspaces, err := newRuntimeWorkspaceScope(config.WorkspaceKey, config.WorkspaceLister)
	if err != nil {
		return platformruntime.Registration{}, err
	}
	return platformruntime.Registration{
		Component: &sessionRecoveryComponent{
			commands:   commands,
			authority:  authorityProvider,
			workspaces: workspaces,
		},
		Policy: platformruntime.Policy{
			Cadence:   SessionRecoveryCadence,
			Immediate: true,
			Timeout:   SessionRecoveryTimeout,
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
	lister RuntimeWorkspaceLister
}

func newRuntimeWorkspaceScope(workspace string, lister RuntimeWorkspaceLister) (runtimeWorkspaceScope, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" && lister == nil {
		return runtimeWorkspaceScope{}, fmt.Errorf("workspace lister is required for unscoped Interaction runtime: %w", ErrUnavailable)
	}
	return runtimeWorkspaceScope{fixed: workspace, lister: lister}, nil
}

func (scope runtimeWorkspaceScope) list(ctx context.Context) ([]string, error) {
	if scope.fixed != "" {
		return []string{scope.fixed}, nil
	}
	workspaces, err := scope.lister.ListWorkspaceKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Interaction runtime workspaces: %w", err)
	}
	seen := make(map[string]struct{}, len(workspaces))
	for _, workspace := range workspaces {
		if workspace == "" || workspace != strings.TrimSpace(workspace) {
			return nil, fmt.Errorf("workspace list contains an invalid key: %w", ErrInvalidPersistedState)
		}
		if _, duplicate := seen[workspace]; duplicate {
			return nil, fmt.Errorf("workspace list contains duplicate %q: %w", workspace, ErrInvalidPersistedState)
		}
		seen[workspace] = struct{}{}
	}
	out := append([]string(nil), workspaces...)
	sort.Strings(out)
	return out, nil
}
