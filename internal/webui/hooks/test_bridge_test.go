package hooks

import (
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
)

// MultiWorkspaceSubscriber → subscription.MultiWorkspaceSubscriber
type MultiWorkspaceSubscriber = subscription.MultiWorkspaceSubscriber

// NewMultiWorkspaceSubscriber → subscription.NewMultiWorkspaceSubscriber
var NewMultiWorkspaceSubscriber = subscription.NewMultiWorkspaceSubscriber

func regCtx(wsID, path string) *coordinator.RegistrationContext {
	return &coordinator.RegistrationContext{WorkspaceID: wsID, WorkspacePath: path, Logger: slog.Default()}
}

func deregCtx(wsID string) coordinator.DeregistrationContext {
	return coordinator.DeregistrationContext{WorkspaceID: wsID, Logger: slog.Default()}
}
