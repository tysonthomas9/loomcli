package loomagent

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// WaitingMessage is one sender's message waiting for the agent (§4.9).
type WaitingMessage struct {
	Sender, Text, Since string
}

// AgentInfo is what Get and List return (design v2 §4.5). HarnessSessionID is
// always nil: the harness session id is never returned.
type AgentInfo struct {
	loomstore.Agent
	Compute         string           // always "local" in Phase 1
	WaitingMessages []WaitingMessage // Get only
	OpenAsks        []Ask            // Get only
	ModelUnverified bool             // the model was not in the harness's catalog when chosen (MCS1)
}

func info(a loomstore.Agent) AgentInfo {
	a.HarnessSessionID, a.HarnessSessionRoot = nil, nil
	cfg, _ := loadConfig(a)
	return AgentInfo{Agent: a, Compute: "local", ModelUnverified: cfg.ModelUnverified}
}

// Get returns the agent with its waiting messages. It reads only the registry.
func (s *Service) Get(ctx context.Context, agentID string) (AgentInfo, error) {
	a, err := s.agent(ctx, agentID)
	if err != nil {
		return AgentInfo{}, err
	}
	slots, err := s.store.Slots(ctx, agentID)
	if err != nil {
		return AgentInfo{}, err
	}
	out := info(a)
	out.OpenAsks = s.openAsks(agentID)
	for _, sl := range slots {
		if sl.State == loomstore.SlotWaiting {
			out.WaitingMessages = append(out.WaitingMessages, WaitingMessage{Sender: sl.Sender, Text: sl.Body, Since: deref(sl.QueuedAt)})
		}
	}
	return out, nil
}

// agent reads agentID's row, mapping a missing row, or another workspace's
// agent, to agent_not_found.
func (s *Service) agent(ctx context.Context, agentID string) (loomstore.Agent, error) {
	a, err := s.store.GetAgent(ctx, agentID)
	if errors.Is(err, loomstore.ErrNotFound) || (err == nil && a.WorkspaceID != s.workspaceID) {
		return a, &Error{Code: CodeAgentNotFound, Message: agentID}
	}
	return a, err
}
