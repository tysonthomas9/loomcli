package app

import (
	"strings"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// classifyFromWorkspaceConfig returns a terminal.AgentKind classifier backed
// by configByIDFn. The classifier inspects the named workspace's
// AgentKind field; missing / empty / unrecognized values default to
// AgentEphemeral, which is the regression-safe behavior. A nil configByIDFn
// (e.g., single-repo mode) makes the classifier always return ephemeral.
//
// This is the only piece of plumbing that wires the per-workspace metadata
// added in plan-rbp.5 into the dispatch factory. Keeping it in a small file
// next to server_app.go avoids leaking app-package-specific assumptions
// into the terminal package while still giving operators a single place to
// look for "how is the kind determined?".
func classifyFromWorkspaceConfig(configByIDFn func(string) (*ops.WorkspaceData, error)) func(terminal.SessionKey) terminal.AgentKind {
	if configByIDFn == nil {
		return func(terminal.SessionKey) terminal.AgentKind { return terminal.AgentEphemeral }
	}
	return func(key terminal.SessionKey) terminal.AgentKind {
		if key.Workspace == "" {
			return terminal.AgentEphemeral
		}
		wsData, err := configByIDFn(key.Workspace)
		if err != nil || wsData == nil {
			return terminal.AgentEphemeral
		}
		// configByIDFn returns the *full* WorkspaceData payload; the matching
		// summary entry (whose AgentKind field we look at) is the one whose
		// ID equals the requested workspace.
		for _, ws := range wsData.Workspaces {
			if ws.ID != key.Workspace {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(ws.AgentKind)) {
			case "persistent":
				return terminal.AgentPersistent
			default:
				return terminal.AgentEphemeral
			}
		}
		return terminal.AgentEphemeral
	}
}
