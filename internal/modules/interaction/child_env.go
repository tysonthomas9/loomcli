package interaction

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	EnvSessionWorkspace  = "LOOM_SESSION_WORKSPACE"
	EnvSessionID         = "LOOM_SESSION_ID"
	EnvSessionAgentID    = "LOOM_SESSION_AGENT_ID"
	EnvSessionTerminalID = "LOOM_SESSION_TERMINAL_ID"
	EnvSessionNodeID     = "LOOM_SESSION_NODE_ID"
	EnvSessionLeaseID    = "LOOM_SESSION_LEASE_ID"
	EnvSessionFence      = "LOOM_SESSION_FENCING_TOKEN"
	EnvSessionToken      = "LOOM_SESSION_AUTH_TOKEN" //nolint:gosec // environment variable name, not a credential
	EnvInteractionAPIURL = "LOOM_INTERACTION_API_URL"
)

var childLaunchAllowed = map[string]struct{}{
	"LOOM_WORKSPACE": {}, "LOOM_AGENT_NAME": {}, "LOOM_AGENT_ROLE": {},
	"LOOM_CONFIG_DIR":        {},
	"LOOM_AGENT_TERMINAL_ID": {}, "LOOM_BACKEND": {},
	"LOOM_ORCHESTRATOR_SESSION_ID": {}, "LOOM_WORKTREE_PATH": {},
	EnvSessionWorkspace: {}, EnvSessionID: {}, EnvSessionAgentID: {},
	EnvSessionTerminalID: {}, EnvSessionNodeID: {}, EnvSessionLeaseID: {},
	EnvSessionFence: {}, EnvSessionToken: {}, EnvInteractionAPIURL: {},
}

// ChildLaunchEnvAllowed reports whether a server-owned launch overlay may set
// name after the ambient environment has been filtered.
func ChildLaunchEnvAllowed(name string) bool {
	_, ok := childLaunchAllowed[strings.TrimSpace(name)]
	return ok
}

// SessionEnvelope returns the exact least-privilege variables for one
// interactive child. The caller must Close token immediately after the
// subprocess has copied its environment.
func SessionEnvelope(auth authority.SessionAuthority, token *LeaseToken) (map[string]string, error) {
	if token == nil || len(token.value) == 0 || auth.Workspace() == "" ||
		auth.SessionID() == "" || auth.AgentID() == "" || auth.NodeID() == "" ||
		auth.LeaseID() == "" || auth.FencingToken() <= 0 {
		return nil, fmt.Errorf("complete session authority and one-time token are required: %w", ErrInvalid)
	}
	return map[string]string{
		EnvSessionWorkspace:  auth.Workspace(),
		EnvSessionID:         auth.SessionID(),
		EnvSessionAgentID:    auth.AgentID(),
		EnvSessionTerminalID: auth.TerminalID(),
		EnvSessionNodeID:     auth.NodeID(),
		EnvSessionLeaseID:    auth.LeaseID(),
		EnvSessionFence:      strconv.FormatInt(auth.FencingToken(), 10),
		EnvSessionToken:      string(token.value),
	}, nil
}
