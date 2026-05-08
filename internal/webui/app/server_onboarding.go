package app

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/onboarding"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// onboardingAdapter implements onboarding.Deps over the server's typed
// dependencies. Constructed once per server.
type onboardingAdapter struct {
	workspaceSvc service.WorkspaceService
	backendOps   ops.BackendOps
	issueCounter onboarding.IssueBackendCounter
}

func (a *onboardingAdapter) HasAnyWorkspace(ctx context.Context) (bool, error) {
	if a.workspaceSvc == nil {
		return false, nil
	}
	items, err := a.workspaceSvc.ListWorkspaces(ctx)
	if err != nil {
		return false, err
	}
	return len(items) > 0, nil
}

func (a *onboardingAdapter) GetWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	return a.workspaceSvc.GetWorkspace(ctx, wsID)
}

func (a *onboardingAdapter) BackendsHealth(_ context.Context) ([]ops.BackendHealth, error) {
	if a.backendOps == nil {
		return nil, nil
	}
	return a.backendOps.ListBackendsHealth()
}

func (a *onboardingAdapter) IssueCount(ctx context.Context) (int, error) {
	return a.issueCounter.Count(ctx)
}

// buildOnboardingHandler wires the onboarding endpoint handler from the
// server's existing dependencies. Returns nil when workspaceSvc is
// unavailable; callers gate route registration on the nil result.
func (app *Server) buildOnboardingHandler() http.HandlerFunc {
	if app.workspaceSvc == nil {
		return nil
	}
	deps := &onboardingAdapter{
		workspaceSvc: app.workspaceSvc,
		backendOps:   app.config.BackendOps,
		issueCounter: onboarding.IssueBackendCounter{
			Resolve: func(ctx context.Context) backend.IssueBackend {
				if app.config.IssueBackendFn == nil {
					return nil
				}
				return app.config.IssueBackendFn(ctx)
			},
		},
	}
	return onboarding.HandleStatus(deps)
}
