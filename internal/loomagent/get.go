package loomagent

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// WaitingMessage is one sender's message waiting for the agent (§4.9).
// Completions are the task_completed records in Text, and Message is Text
// without them; both are set only when Text has such records.
type WaitingMessage struct {
	Sender, Text, Since string
	Message             string
	Completions         []Completion
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
	if a.DeletedAt != nil {
		a.State = StateDeleted
	}
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
	notices, err := s.store.WaitingNotices(ctx, agentID)
	if err != nil {
		return AgentInfo{}, err
	}
	for _, sl := range slots {
		if sl.State == loomstore.SlotWaiting {
			w := WaitingMessage{Sender: sl.Sender, Text: sl.Body, Since: deref(sl.QueuedAt)}
			n, err := s.slotNotices(ctx, agentID, sl.Sender, sl.Body, notices[sl.Sender])
			if err != nil {
				return AgentInfo{}, err
			}
			if msg, done := completionsIn(sl.Body, n); len(done) > 0 {
				w.Message, w.Completions = msg, done
			}
			out.WaitingMessages = append(out.WaitingMessages, w)
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

// List returns one page of agents matching f and the next page's cursor. It
// reads only the registry and never calls a harness (design v2 §4.5).
func (s *Service) List(ctx context.Context, f loomstore.AgentFilter) ([]AgentInfo, string, error) {
	rows, next, err := s.store.ListAgents(ctx, f)
	if err != nil {
		return nil, "", err
	}
	out := make([]AgentInfo, len(rows))
	for i, a := range rows {
		out[i] = info(a)
	}
	return out, next, nil
}
