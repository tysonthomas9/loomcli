package leadclient

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type agentSessionStore struct {
	client *Client
}

func (s agentSessionStore) Create(ctx context.Context, in store.AgentSessionCreate) (*domain.AgentSession, error) {
	terminalID := strings.TrimSpace(in.TerminalID)
	metadata := copyStringMap(in.Metadata)
	params := sessionParams{}
	if terminalID != "" {
		params.TerminalID = &terminalID
	}
	if metadata != nil {
		params.Metadata = &metadata
	}
	var out sessionEnvelope
	if err := s.client.do(ctx, "session-ensure", params, &out); err != nil {
		return nil, err
	}
	session := s.client.domainSession(out.Session)
	if session == nil {
		return nil, fmt.Errorf("session-ensure returned no session: %w", domain.ErrNotFound)
	}
	return session, nil
}

func (s agentSessionStore) Get(ctx context.Context, workspaceKey, sessionID string) (*domain.AgentSession, error) {
	var out sessionEnvelope
	if err := s.client.do(ctx, "session-get", map[string]any{}, &out); err != nil {
		return nil, err
	}
	session := s.client.domainSession(out.Session)
	if session == nil {
		return nil, fmt.Errorf("lead session not found: %w", domain.ErrNotFound)
	}
	return session, nil
}

func (s agentSessionStore) List(ctx context.Context, workspaceKey string, filter store.AgentSessionFilter) ([]*domain.AgentSession, error) {
	var out sessionEnvelope
	if err := s.client.do(ctx, "session-get", map[string]any{}, &out); err != nil {
		return nil, err
	}
	session := s.client.domainSession(out.Session)
	if session == nil || !sessionMatchesFilter(session, filter) {
		return nil, nil
	}
	return []*domain.AgentSession{session}, nil
}

func (s agentSessionStore) Heartbeat(ctx context.Context, workspaceKey, sessionID string) (*domain.AgentSession, error) {
	var out heartbeatEnvelope
	if err := s.client.do(ctx, "heartbeat", map[string]any{}, &out); err != nil {
		return nil, err
	}
	session := s.client.domainSession(&out.Session)
	if session == nil || session.SessionID == "" {
		return nil, fmt.Errorf("heartbeat returned no session: %w", domain.ErrNotFound)
	}
	return session, nil
}

func (s agentSessionStore) Update(ctx context.Context, workspaceKey, sessionID string, patch store.AgentSessionUpdate) (*domain.AgentSession, error) {
	params := sessionParams{}
	if patch.TerminalID != nil {
		terminalID := strings.TrimSpace(*patch.TerminalID)
		params.TerminalID = &terminalID
	}
	if patch.Status != nil {
		status := string(*patch.Status)
		params.Status = &status
	}
	if patch.FinishedAt != nil {
		params.FinishedAt = *patch.FinishedAt
	}
	if patch.Metadata != nil {
		metadata := copyStringMap(*patch.Metadata)
		params.Metadata = &metadata
	}
	var out sessionEnvelope
	if err := s.client.do(ctx, "session-update", params, &out); err != nil {
		return nil, err
	}
	session := s.client.domainSession(out.Session)
	if session == nil {
		return nil, fmt.Errorf("session-update returned no session: %w", domain.ErrNotFound)
	}
	return session, nil
}

func sessionMatchesFilter(session *domain.AgentSession, filter store.AgentSessionFilter) bool {
	if filter.Kind != "" && session.Kind != filter.Kind {
		return false
	}
	if filter.Status != "" && session.Status != filter.Status {
		return false
	}
	return true
}

type agentInboxMessageStore struct {
	client *Client
}

func (s agentInboxMessageStore) Create(ctx context.Context, in store.AgentInboxMessageCreate) (*domain.AgentInboxMessage, error) {
	return nil, unsupported("AgentInboxMessages.Create")
}

func (s agentInboxMessageStore) Get(ctx context.Context, workspaceKey, inboxMessageID string) (*domain.AgentInboxMessage, error) {
	return nil, unsupported("AgentInboxMessages.Get")
}

func (s agentInboxMessageStore) List(ctx context.Context, workspaceKey string, filter store.AgentInboxMessageFilter) ([]*domain.AgentInboxMessage, error) {
	params := inboxListParams{}
	if filter.Status != "" {
		status := string(filter.Status)
		params.Status = &status
	}
	if filter.Limit > 0 {
		limit := filter.Limit
		params.Limit = &limit
	}
	var out inboxListEnvelope
	if err := s.client.do(ctx, "inbox-list", params, &out); err != nil {
		return nil, err
	}
	messages := make([]*domain.AgentInboxMessage, 0, len(out.Messages))
	for i := range out.Messages {
		messages = append(messages, s.client.domainInboxMessage(&out.Messages[i]))
	}
	return messages, nil
}

func (s agentInboxMessageStore) ClaimNext(ctx context.Context, in store.AgentInboxMessageClaim) (*domain.AgentInboxMessage, error) {
	params := inboxClaimParams{}
	if claimedBy := strings.TrimSpace(in.ClaimedBy); claimedBy != "" {
		params.ClaimedBy = &claimedBy
	}
	if in.LeaseTTL > 0 {
		seconds := int((in.LeaseTTL + time.Second - 1) / time.Second)
		params.LeaseTTLSeconds = &seconds
	}
	var out inboxMessageEnvelope
	if err := s.client.do(ctx, "inbox-claim", params, &out); err != nil {
		return nil, err
	}
	message := s.client.domainInboxMessage(out.Message)
	if message == nil {
		return nil, fmt.Errorf("lead inbox message not found: %w", domain.ErrNotFound)
	}
	return message, nil
}

func (s agentInboxMessageStore) Complete(ctx context.Context, workspaceKey, inboxMessageID string, update store.AgentInboxMessageComplete) (*domain.AgentInboxMessage, error) {
	params := inboxCompleteParams{
		InboxMessageID:    strings.TrimSpace(inboxMessageID),
		Outcome:           strings.TrimSpace(update.Outcome),
		DeliveredThreadID: strings.TrimSpace(update.DeliveredThreadID),
		ErrorClass:        strings.TrimSpace(update.ErrorClass),
		Error:             strings.TrimSpace(update.Error),
	}
	var out inboxMessageEnvelope
	if err := s.client.do(ctx, "inbox-complete", params, &out); err != nil {
		return nil, err
	}
	message := s.client.domainInboxMessage(out.Message)
	if message == nil {
		return nil, fmt.Errorf("inbox-complete returned no message: %w", domain.ErrNotFound)
	}
	return message, nil
}

type agentStore struct {
	client *Client
}

func (s agentStore) Create(ctx context.Context, in store.AgentCreate) (*domain.Agent, error) {
	return nil, unsupported("Agents.Create")
}

func (s agentStore) Get(ctx context.Context, workspaceKey, name string) (*domain.Agent, error) {
	var out agentEnvelope
	if err := s.client.do(ctx, "agent-get", map[string]any{}, &out); err != nil {
		return nil, err
	}
	agent := s.client.domainAgent(out.Agent)
	if agent == nil {
		return nil, fmt.Errorf("lead agent not found: %w", domain.ErrNotFound)
	}
	return agent, nil
}

func (s agentStore) List(ctx context.Context, workspaceKey string) ([]*domain.Agent, error) {
	return nil, unsupported("Agents.List")
}

func (s agentStore) Update(ctx context.Context, workspaceKey, name string, patch store.AgentUpdate) (*domain.Agent, error) {
	return nil, unsupported("Agents.Update")
}

func (s agentStore) Delete(ctx context.Context, workspaceKey, name string) error {
	return unsupported("Agents.Delete")
}
