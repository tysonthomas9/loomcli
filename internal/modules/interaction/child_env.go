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

var childBaseAllowed = map[string]struct{}{
	"PATH": {}, "HOME": {}, "PWD": {}, "OLDPWD": {}, "TMPDIR": {}, "TMP": {}, "TEMP": {},
	"TERM": {}, "USER": {}, "LOGNAME": {}, "SHELL": {}, "TZ": {}, "LANG": {},
	"NO_COLOR": {}, "FORCE_COLOR": {}, "CLICOLOR": {}, "CLICOLOR_FORCE": {},
	"HTTP_PROXY": {}, "HTTPS_PROXY": {}, "NO_PROXY": {},
	"http_proxy": {}, "https_proxy": {}, "no_proxy": {},
	"CODEX_HOME": {}, "CLAUDE_CONFIG_DIR": {}, "TMUX_TMPDIR": {},
	"GIT_AUTHOR_NAME": {}, "GIT_AUTHOR_EMAIL": {},
	"GIT_COMMITTER_NAME": {}, "GIT_COMMITTER_EMAIL": {},
}

var childLaunchAllowed = map[string]struct{}{
	"LOOM_WORKSPACE": {}, "LOOM_AGENT_NAME": {}, "LOOM_AGENT_ROLE": {},
	"LOOM_CONFIG_DIR":        {},
	"LOOM_AGENT_TERMINAL_ID": {}, "LOOM_BACKEND": {},
	"LOOM_ORCHESTRATOR_SESSION_ID": {}, "LOOM_WORKTREE_PATH": {},
	EnvSessionWorkspace: {}, EnvSessionID: {}, EnvSessionAgentID: {},
	EnvSessionTerminalID: {}, EnvSessionNodeID: {}, EnvSessionLeaseID: {},
	EnvSessionFence: {}, EnvSessionToken: {}, EnvInteractionAPIURL: {},
}

// FilterChildBaseEnv removes ambient operator, forge, FleetDB, daemon, cloud,
// and provider credentials before any Interaction-owned child is launched.
// Authentication homes needed by local CLIs are admitted by exact path keys;
// no broad LOOM_ or cloud prefix exists.
func FilterChildBaseEnv(base []string) []string {
	out := make([]string, 0, len(base))
	for _, entry := range base {
		name, _, ok := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" || childEnvironmentSensitive(name) {
			continue
		}
		if _, allowed := childBaseAllowed[name]; allowed || strings.HasPrefix(name, "LC_") {
			out = append(out, entry)
		}
	}
	return out
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

func childEnvironmentSensitive(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if upper == "" {
		return true
	}
	if _, allowed := childBaseAllowed[upper]; allowed {
		return false
	}
	for _, prefix := range []string{
		"LOOM_", "FLEET_", "AWS_", "AZURE_", "GCP_", "GOOGLE_", "GIT_CONFIG_", "SSH_",
	} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	for _, fragment := range []string{
		"SECRET", "TOKEN", "PASSWORD", "PRIVATE_KEY", "ACCESS_KEY", "API_KEY", "CREDENTIAL",
	} {
		if strings.Contains(upper, fragment) {
			return true
		}
	}
	return false
}
