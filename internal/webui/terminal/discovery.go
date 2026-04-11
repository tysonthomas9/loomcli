package terminal

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/workspace"
)

// tmuxSessionMeta is a lightweight pair of (tmux session name, creation time)
// used by the discovery helpers — listTmuxSessions,
// FindLatestAgentSession, KillWorkspaceSessions, and ListWorkspaceSessions —
// that need to enumerate tmux's view of the world rather than rely on
// TerminalManager's in-memory state alone.
type tmuxSessionMeta struct {
	name    string
	created int64
}

// listTmuxSessions returns every tmux session visible to the loom user on
// the configured socket, regardless of which workspace or server instance
// owns it. Callers are expected to filter by prefix or regex.
//
// Returns (nil, nil) when no tmux server is running or no sessions exist —
// this is the normal state for a fresh boot and is not an error.
func (m *TerminalManager) listTmuxSessions() ([]tmuxSessionMeta, error) {
	cmd := m.tmuxCmd("list-sessions", "-F", "#{session_name}\t#{session_created}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.ToLower(string(out))
		// No tmux server/sessions is a normal state for archive fallback.
		if strings.Contains(msg, "failed to connect to server") || strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting to") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	var sessions []tmuxSessionMeta
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		created, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		sessions = append(sessions, tmuxSessionMeta{name: name, created: created})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

// FindLatestAgentSession returns the newest tmux session matching the auto-mode
// naming convention for an agent: loom-<wsPrefix>-<role>-<agent>-<pid>.
// When wsID is non-empty, only sessions for that workspace are matched.
// When wsID is empty, returns no match (fail-closed).
//
// Agent sessions are created outside TerminalManager (by automode_tmux.go in
// the CLI), so this method bypasses the server-instance prefix scheme that
// TerminalManager-created sessions use. It pattern-matches the raw tmux
// session name directly.
func (m *TerminalManager) FindLatestAgentSession(wsID, agentName string) (string, bool, error) {
	if !validSessionName.MatchString(agentName) {
		return "", false, fmt.Errorf("invalid agent name %q", agentName)
	}

	sessions, err := m.listTmuxSessions()
	if err != nil {
		return "", false, err
	}

	// When workspace ID is empty, fail closed — no match rather than match-all.
	if wsID == "" {
		return "", false, nil
	}
	wsPrefix := workspace.ShortWorkspaceID(wsID)
	pattern := regexp.MustCompile(fmt.Sprintf(`^loom-%s-[a-zA-Z0-9_-]+-%s-[0-9]+$`, regexp.QuoteMeta(wsPrefix), regexp.QuoteMeta(agentName)))

	var bestName string
	var bestCreated int64
	found := false
	for _, session := range sessions {
		if !pattern.MatchString(session.name) {
			continue
		}
		if !found || session.created > bestCreated || (session.created == bestCreated && session.name > bestName) {
			bestName = session.name
			bestCreated = session.created
			found = true
		}
	}
	if !found {
		return "", false, nil
	}
	return bestName, true, nil
}
