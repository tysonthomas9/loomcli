package terminal

import (
	"context"
)

// StateQueries is the single presentation query Terminal delivery needs from
// Workspace. Interaction owns every terminal, Agent-session, and local
// placement decision behind TerminalTabs.
type StateQueries interface {
	ResolveWorkspaceName(context.Context, string) (string, error)
}
