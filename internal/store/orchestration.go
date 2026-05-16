package store

import (
	"context"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// OrchestrationSessionFor returns the most recent active orchestration
// session for a lead agent, or (nil, nil) if none exists.
//
// This is the join that replaces direct reads of
// domain.Agent.OrchestratorSessionID - the cached field on the agent
// record was a denormalization of the relationship represented natively
// by AgentSession{Kind=orchestration, AgentID=<lead-name>}. Reading via
// the join makes AgentSession the single source of truth and avoids the
// half-deprecation that arose when commit 9aef2ae5 stopped writing the
// cache field on FleetDB.
//
// Returns (nil, nil) when no orchestration session exists for the agent.
// Returns the most-recently-updated session when multiple match - which
// shouldn't happen in normal operation but is a defensive choice over
// returning a random one.
func OrchestrationSessionFor(ctx context.Context, s Store, workspaceKey, agentID string) (*domain.AgentSession, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, nil
	}
	sessions, err := s.AgentSessions().List(ctx, workspaceKey, AgentSessionFilter{
		AgentID: agentID,
		Kind:    domain.AgentSessionKindOrchestration,
		Limit:   8,
	})
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, nil
	}
	best := sessions[0]
	for _, sess := range sessions[1:] {
		if sess == nil {
			continue
		}
		if sess.UpdatedAt.After(best.UpdatedAt) {
			best = sess
		}
	}
	return best, nil
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
