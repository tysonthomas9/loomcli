package store

import (
	"context"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// OrchestrationSessionFor returns the most recent active interactive session
// for a lead agent, or (nil, nil) if none exists. Phase 4 and older records
// used kind=orchestration, so the lookup falls back to that legacy kind only
// when no active kind=interactive record exists.
//
// This join is the only durable representation of the relationship:
// AgentSession{Kind=interactive, AgentID=<lead-name>}. Reading via
// the join makes AgentSession the single source of truth and avoids the
// a join avoids a second identity-owned cache.
//
// Returns (nil, nil) when no active session exists for the agent.
// Returns the most-recently-updated session when multiple match - which
// shouldn't happen in normal operation but is a defensive choice over
// returning a random one.
func OrchestrationSessionFor(ctx context.Context, s Store, workspaceKey, agentID string) (*domain.AgentSession, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, nil
	}

	for _, kind := range []domain.AgentSessionKind{
		domain.AgentSessionKindInteractive,
		domain.AgentSessionKindOrchestration,
	} {
		sessions, err := s.AgentSessions().List(ctx, workspaceKey, AgentSessionFilter{
			AgentID: agentID,
			Kind:    kind,
			Limit:   8,
		})
		if err != nil {
			return nil, err
		}
		if best := mostRecentActiveSession(sessions); best != nil {
			return best, nil
		}
	}
	return nil, nil
}

// OrchestrationSessionIDFor is a convenience wrapper around
// OrchestrationSessionFor that returns just the session id ("" when
// there is no orchestration session). Designed as a drop-in for
// `agent.OrchestratorSessionID` reads.
func OrchestrationSessionIDFor(ctx context.Context, s Store, workspaceKey, agentID string) (string, error) {
	sess, err := OrchestrationSessionFor(ctx, s, workspaceKey, agentID)
	if err != nil || sess == nil {
		return "", err
	}
	return sess.SessionID, nil
}

func mostRecentActiveSession(sessions []*domain.AgentSession) *domain.AgentSession {
	var best *domain.AgentSession
	for _, session := range sessions {
		if !activeInteractiveSession(session) {
			continue
		}
		if best == nil || session.UpdatedAt.After(best.UpdatedAt) {
			best = session
		}
	}
	return best
}

func activeInteractiveSession(sess *domain.AgentSession) bool {
	if sess == nil || sess.FinishedAt != nil {
		return false
	}
	switch sess.Status {
	case "", domain.AgentSessionLeased, domain.AgentSessionStarting, domain.AgentSessionRunning, domain.AgentSessionIdle, domain.AgentSessionYielded:
		return true
	default:
		return false
	}
}
