package terminal

import (
	"context"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

// PresentationState owns only the user's active-tab preference. It cannot
// create, infer, or mutate an Interaction terminal lifecycle.
type PresentationState interface {
	GetActiveTab(context.Context, string) (string, error)
	SetActiveTab(context.Context, string, string) error
}

type redisPresentationState struct {
	client *redis.Client
}

func NewPresentationState(client *redis.Client) PresentationState {
	if client == nil {
		return nil
	}
	return &redisPresentationState{client: client}
}

func terminalUIStateKey(workspaceKey string) string {
	return "terminal:ui-state:" + workspaceKey
}

func (state *redisPresentationState) GetActiveTab(ctx context.Context, workspaceKey string) (string, error) {
	values, err := state.client.HGetAll(ctx, terminalUIStateKey(workspaceKey)).Result()
	if err != nil {
		slog.Warn("failed to get terminal presentation state", "err", err)
		return "", nil
	}
	activeTab := values["active_tab"]
	return activeTab, nil
}

func (state *redisPresentationState) SetActiveTab(ctx context.Context, workspaceKey, activeTab string) error {
	if err := state.client.HSet(ctx, terminalUIStateKey(workspaceKey), "active_tab", activeTab).Err(); err != nil {
		return apperrors.ErrInternal("failed to save terminal presentation state", err)
	}
	return nil
}
