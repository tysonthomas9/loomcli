package leadapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type sessionParams struct {
	TerminalID *string            `json:"terminalId,omitempty"`
	Status     *string            `json:"status,omitempty"`
	FinishedAt *time.Time         `json:"finishedAt,omitempty"`
	Metadata   *map[string]string `json:"metadata,omitempty"`
}

type sessionEnvelope struct {
	Session *sessionResult `json:"session"`
}

type agentEnvelope struct {
	Agent *agentResult `json:"agent"`
}

type agentResult struct {
	Name             string                   `json:"name"`
	RoleName         string                   `json:"roleName"`
	Auto             bool                     `json:"auto,omitempty"`
	Backend          string                   `json:"backend,omitempty"`
	FallbackBackends []string                 `json:"fallbackBackends,omitempty"`
	RuntimeProvider  domain.RuntimeProvider   `json:"runtimeProvider,omitempty"`
	Repos            []string                 `json:"repos,omitempty"`
	RepoGroups       []string                 `json:"repoGroups,omitempty"`
	CrossRepo        bool                     `json:"crossRepo,omitempty"`
	Parent           string                   `json:"parent,omitempty"`
	State            domain.AgentState        `json:"state,omitempty"`
	Mode             domain.AgentMode         `json:"mode,omitempty"`
	TaskFilter       string                   `json:"taskFilter,omitempty"`
	MaxConcurrency   int                      `json:"maxConcurrency,omitempty"`
	BudgetPolicy     string                   `json:"budgetPolicy,omitempty"`
	DesiredState     domain.AgentDesiredState `json:"desiredState,omitempty"`
	CreatedAt        *time.Time               `json:"createdAt,omitempty"`
	UpdatedAt        *time.Time               `json:"updatedAt,omitempty"`
}

type inboxListParams struct {
	Status *string `json:"status,omitempty"`
	Limit  *int    `json:"limit,omitempty"`
}

type inboxClaimParams struct {
	ClaimedBy       *string `json:"claimedBy,omitempty"`
	LeaseTTLSeconds *int    `json:"leaseTtlSeconds,omitempty"`
}

type inboxCompleteParams struct {
	InboxMessageID    string `json:"inboxMessageId"`
	Outcome           string `json:"outcome"`
	DeliveredThreadID string `json:"deliveredThreadId,omitempty"`
	ErrorClass        string `json:"errorClass,omitempty"`
	Error             string `json:"error,omitempty"`
}

type inboxListEnvelope struct {
	Messages []inboxMessageResult `json:"messages"`
}

type inboxMessageEnvelope struct {
	Message *inboxMessageResult `json:"message"`
}

type inboxMessageResult struct {
	InboxMessageID    string                         `json:"inboxMessageId"`
	Cursor            int64                          `json:"cursor,omitempty"`
	TargetAgentID     string                         `json:"targetAgentId"`
	SessionID         string                         `json:"sessionId,omitempty"`
	Body              string                         `json:"body"`
	Status            domain.AgentInboxMessageStatus `json:"status"`
	SourceKind        string                         `json:"sourceKind,omitempty"`
	SourceRef         string                         `json:"sourceRef,omitempty"`
	DriverRunID       string                         `json:"driverRunId,omitempty"`
	TaskRunID         string                         `json:"taskRunId,omitempty"`
	TriggerEventID    string                         `json:"triggerEventId,omitempty"`
	TriggerDeliveryID string                         `json:"triggerDeliveryId,omitempty"`
	DedupeKey         string                         `json:"dedupeKey,omitempty"`
	Attempt           int                            `json:"attempt,omitempty"`
	ClaimedBy         string                         `json:"claimedBy,omitempty"`
	ClaimExpiresAt    *time.Time                     `json:"claimExpiresAt,omitempty"`
	LastError         string                         `json:"lastError,omitempty"`
	ErrorClass        string                         `json:"errorClass,omitempty"`
	DeliveredThreadID string                         `json:"deliveredThreadId,omitempty"`
	DeliveredAt       *time.Time                     `json:"deliveredAt,omitempty"`
	CreatedAt         *time.Time                     `json:"createdAt,omitempty"`
	UpdatedAt         *time.Time                     `json:"updatedAt,omitempty"`
}

func (m *Module) sessionEnsure(ctx context.Context, ws string, id occupantIdentity, body []byte) (any, error) {
	var params sessionParams
	if err := decodeLeadParams(body, &params); err != nil {
		return nil, err
	}
	m.sessionEnsureMu.Lock()
	defer m.sessionEnsureMu.Unlock()

	session, err := m.sessionEnsureLocked(ctx, ws, id.node, params)
	if err != nil {
		return nil, err
	}
	return sessionEnvelope{Session: newSessionResult(session)}, nil
}

// sessionEnsureLocked adopts the placement's existing orchestration session or
// creates one. It runs under sessionEnsureMu, which is load-bearing: Create
// dedupes only on the server-minted UUID SessionID, so two concurrent creates
// would produce two distinct sessions that both match {NodeID, orchestration}
// and permanently trip the single-active-session invariant. The mutex is the
// only guard against that, which is sound for single-instance serve (ticket 06);
// the ErrAlreadyExists branch below is a belt-and-suspenders path that never
// fires today (UUIDs never collide). Horizontal-scale serve would need a
// store-level uniqueness constraint on active (NodeID, Kind=orchestration).
func (m *Module) sessionEnsureLocked(ctx context.Context, ws string, node *domain.Node, params sessionParams) (*domain.AgentSession, error) {
	session, err := m.resolveLeadSession(ctx, ws, node)
	switch {
	case err == nil:
		return m.updateAdoptedSession(ctx, ws, session, params)
	case !errors.Is(err, domain.ErrNotFound):
		return nil, err
	}

	created, err := m.createLeadSessionForPlacement(ctx, ws, node, params)
	if errors.Is(err, domain.ErrAlreadyExists) {
		session, err = m.resolveLeadSession(ctx, ws, node)
		if err != nil {
			return nil, err
		}
		return m.updateAdoptedSession(ctx, ws, session, params)
	}
	return created, err
}

func (m *Module) createLeadSessionForPlacement(ctx context.Context, ws string, node *domain.Node, params sessionParams) (*domain.AgentSession, error) {
	agentName, err := requirePlacementAgentName(node)
	if err != nil {
		return nil, err
	}
	terminalID := ""
	if params.TerminalID != nil {
		terminalID = strings.TrimSpace(*params.TerminalID)
	}
	created, err := m.store.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: ws,
		SessionID:    "lead-" + uuid.NewString(),
		AgentID:      agentName,
		NodeID:       node.NodeID,
		Kind:         domain.AgentSessionKindOrchestration,
		TerminalID:   terminalID,
		Status:       domain.AgentSessionRunning,
		Metadata:     copyStringMapPtr(params.Metadata),
	})
	return created, err
}

func (m *Module) sessionUpdate(ctx context.Context, ws string, id occupantIdentity, body []byte) (any, error) {
	var params sessionParams
	if err := decodeLeadParams(body, &params); err != nil {
		return nil, err
	}
	session, err := m.resolveLeadSession(ctx, ws, id.node)
	if err != nil {
		return nil, err
	}
	patch := store.AgentSessionUpdate{}
	apply := false
	if params.Status != nil {
		status := domain.AgentSessionStatus(strings.TrimSpace(*params.Status))
		patch.Status = &status
		apply = true
	}
	if params.TerminalID != nil {
		terminalID := strings.TrimSpace(*params.TerminalID)
		patch.TerminalID = &terminalID
		apply = true
	}
	if params.FinishedAt != nil {
		finishedAt := params.FinishedAt
		patch.FinishedAt = &finishedAt
		apply = true
	}
	if params.Metadata != nil {
		metadata := copyStringMap(*params.Metadata)
		patch.Metadata = &metadata
		apply = true
	}
	if apply {
		session, err = m.store.AgentSessions().Update(ctx, ws, session.SessionID, patch)
		if err != nil {
			return nil, err
		}
	}
	return sessionEnvelope{Session: newSessionResult(session)}, nil
}

func (m *Module) sessionGet(ctx context.Context, ws string, id occupantIdentity, body []byte) (any, error) {
	if err := decodeEmptyParams(body); err != nil {
		return nil, err
	}
	session, err := m.resolveLeadSession(ctx, ws, id.node)
	if errors.Is(err, domain.ErrNotFound) {
		return sessionEnvelope{Session: nil}, nil
	}
	if err != nil {
		return nil, err
	}
	return sessionEnvelope{Session: newSessionResult(session)}, nil
}

func (m *Module) agentGet(ctx context.Context, ws string, id occupantIdentity, body []byte) (any, error) {
	if err := decodeEmptyParams(body); err != nil {
		return nil, err
	}
	agentName, err := requirePlacementAgentName(id.node)
	if err != nil {
		return nil, err
	}
	agent, err := m.store.Agents().Get(ctx, ws, agentName)
	if errors.Is(err, domain.ErrNotFound) {
		return agentEnvelope{Agent: nil}, nil
	}
	if err != nil {
		return nil, err
	}
	return agentEnvelope{Agent: newAgentResult(agent)}, nil
}

func (m *Module) inboxList(ctx context.Context, ws string, id occupantIdentity, body []byte) (any, error) {
	var params inboxListParams
	if err := decodeLeadParams(body, &params); err != nil {
		return nil, err
	}
	target, err := requirePlacementAgentName(id.node)
	if err != nil {
		return nil, err
	}
	status := domain.AgentInboxMessageQueued
	if params.Status != nil && strings.TrimSpace(*params.Status) != "" {
		status = domain.AgentInboxMessageStatus(strings.TrimSpace(*params.Status))
	}
	limit := 0
	if params.Limit != nil && *params.Limit > 0 {
		limit = *params.Limit
	}
	messages, err := m.store.AgentInboxMessages().List(ctx, ws, store.AgentInboxMessageFilter{
		TargetAgentID: target,
		Status:        status,
		Limit:         limit,
	})
	if err != nil {
		return nil, err
	}
	return inboxListEnvelope{Messages: newInboxMessageResults(messages)}, nil
}

func (m *Module) inboxClaim(ctx context.Context, ws string, id occupantIdentity, body []byte) (any, error) {
	var params inboxClaimParams
	if err := decodeLeadParams(body, &params); err != nil {
		return nil, err
	}
	target, err := requirePlacementAgentName(id.node)
	if err != nil {
		return nil, err
	}
	session, err := m.resolveLeadSession(ctx, ws, id.node)
	if err != nil {
		return nil, err
	}
	claimedBy := target
	if params.ClaimedBy != nil && strings.TrimSpace(*params.ClaimedBy) != "" {
		claimedBy = strings.TrimSpace(*params.ClaimedBy)
	}
	var leaseTTL time.Duration
	if params.LeaseTTLSeconds != nil && *params.LeaseTTLSeconds > 0 {
		leaseTTL = time.Duration(*params.LeaseTTLSeconds) * time.Second
	}
	message, err := m.store.AgentInboxMessages().ClaimNext(ctx, store.AgentInboxMessageClaim{
		WorkspaceKey:  ws,
		TargetAgentID: target,
		SessionID:     session.SessionID,
		ClaimedBy:     claimedBy,
		LeaseTTL:      leaseTTL,
	})
	if errors.Is(err, domain.ErrNotFound) {
		return inboxMessageEnvelope{Message: nil}, nil
	}
	if err != nil {
		return nil, err
	}
	return inboxMessageEnvelope{Message: newInboxMessageResult(message)}, nil
}

func (m *Module) inboxComplete(ctx context.Context, ws string, id occupantIdentity, body []byte) (any, error) {
	var params inboxCompleteParams
	if err := decodeLeadParams(body, &params); err != nil {
		return nil, err
	}
	target, err := requirePlacementAgentName(id.node)
	if err != nil {
		return nil, err
	}
	inboxMessageID := strings.TrimSpace(params.InboxMessageID)
	outcome := strings.TrimSpace(params.Outcome)
	if inboxMessageID == "" {
		return nil, fmt.Errorf("inboxMessageId required: %w", domain.ErrInvalid)
	}
	if outcome == "" {
		return nil, fmt.Errorf("outcome required: %w", domain.ErrInvalid)
	}
	message, err := m.store.AgentInboxMessages().Get(ctx, ws, inboxMessageID)
	if err != nil {
		return nil, err
	}
	if message.TargetAgentID != target {
		return nil, fmt.Errorf("inbox message %q belongs to agent %q, not %q: %w", inboxMessageID, message.TargetAgentID, target, domain.ErrNotOwner)
	}
	message, err = m.store.AgentInboxMessages().Complete(ctx, ws, inboxMessageID, store.AgentInboxMessageComplete{
		Outcome:           outcome,
		DeliveredThreadID: strings.TrimSpace(params.DeliveredThreadID),
		ErrorClass:        strings.TrimSpace(params.ErrorClass),
		Error:             strings.TrimSpace(params.Error),
	})
	if err != nil {
		return nil, err
	}
	return inboxMessageEnvelope{Message: newInboxMessageResult(message)}, nil
}

func (m *Module) updateAdoptedSession(ctx context.Context, ws string, session *domain.AgentSession, params sessionParams) (*domain.AgentSession, error) {
	patch := store.AgentSessionUpdate{}
	apply := false
	if params.TerminalID != nil {
		terminalID := strings.TrimSpace(*params.TerminalID)
		patch.TerminalID = &terminalID
		apply = true
	}
	if params.Metadata != nil {
		metadata := copyStringMap(session.Metadata)
		if metadata == nil {
			metadata = make(map[string]string, len(*params.Metadata))
		}
		for key, value := range *params.Metadata {
			metadata[key] = value
		}
		patch.Metadata = &metadata
		apply = true
	}
	if !apply {
		return session, nil
	}
	return m.store.AgentSessions().Update(ctx, ws, session.SessionID, patch)
}

func decodeLeadParams(body []byte, out any) error {
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode lead op params: %s: %w", err.Error(), domain.ErrInvalid)
	}
	return nil
}

func requirePlacementAgentName(node *domain.Node) (string, error) {
	name := placementAgentName(node)
	if name == "" {
		return "", fmt.Errorf("placement agent name missing: %w", domain.ErrInvalid)
	}
	return name, nil
}

func placementAgentName(node *domain.Node) string {
	if node == nil {
		return ""
	}
	if name, ok := strings.CutPrefix(node.OwnerActor, "agent:"); ok {
		return strings.TrimSpace(name)
	}
	for _, label := range node.Labels {
		if name, ok := strings.CutPrefix(label, "loom-agent="); ok {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

func newSessionResult(session *domain.AgentSession) *sessionResult {
	if session == nil {
		return nil
	}
	return &sessionResult{
		SessionID:       session.SessionID,
		AgentID:         session.AgentID,
		NodeID:          session.NodeID,
		Kind:            session.Kind,
		TaskID:          session.TaskID,
		TerminalID:      session.TerminalID,
		ParentSessionID: session.ParentSessionID,
		Status:          session.Status,
		Phase:           session.Phase,
		Attempt:         session.Attempt,
		StartedAt:       timePtr(session.StartedAt),
		LastHeartbeat:   session.LastHeartbeat,
		FinishedAt:      cloneTimePtr(session.FinishedAt),
		Summary:         session.Summary,
		ErrorClass:      session.ErrorClass,
		ExitCode:        cloneIntPtr(session.ExitCode),
		Metadata:        copyStringMap(session.Metadata),
		CreatedAt:       timePtr(session.CreatedAt),
		UpdatedAt:       timePtr(session.UpdatedAt),
	}
}

func newAgentResult(agent *domain.Agent) *agentResult {
	if agent == nil {
		return nil
	}
	return &agentResult{
		Name:             agent.Name,
		RoleName:         agent.RoleName,
		Auto:             agent.Auto,
		Backend:          agent.Backend,
		FallbackBackends: append([]string(nil), agent.FallbackBackends...),
		RuntimeProvider:  agent.RuntimeProvider,
		Repos:            append([]string(nil), agent.Repos...),
		RepoGroups:       append([]string(nil), agent.RepoGroups...),
		CrossRepo:        agent.CrossRepo,
		Parent:           agent.Parent,
		State:            agent.State,
		Mode:             agent.Mode,
		TaskFilter:       agent.TaskFilter,
		MaxConcurrency:   agent.MaxConcurrency,
		BudgetPolicy:     agent.BudgetPolicy,
		DesiredState:     agent.DesiredState,
		CreatedAt:        timePtr(agent.CreatedAt),
		UpdatedAt:        timePtr(agent.UpdatedAt),
	}
}

func newInboxMessageResults(messages []*domain.AgentInboxMessage) []inboxMessageResult {
	results := make([]inboxMessageResult, 0, len(messages))
	for _, message := range messages {
		if result := newInboxMessageResult(message); result != nil {
			results = append(results, *result)
		}
	}
	return results
}

func newInboxMessageResult(message *domain.AgentInboxMessage) *inboxMessageResult {
	if message == nil {
		return nil
	}
	return &inboxMessageResult{
		InboxMessageID:    message.InboxMessageID,
		Cursor:            message.Cursor,
		TargetAgentID:     message.TargetAgentID,
		SessionID:         message.SessionID,
		Body:              message.Body,
		Status:            message.Status,
		SourceKind:        message.SourceKind,
		SourceRef:         message.SourceRef,
		DriverRunID:       message.DriverRunID,
		TaskRunID:         message.TaskRunID,
		TriggerEventID:    message.TriggerEventID,
		TriggerDeliveryID: message.TriggerDeliveryID,
		DedupeKey:         message.DedupeKey,
		Attempt:           message.Attempt,
		ClaimedBy:         message.ClaimedBy,
		ClaimExpiresAt:    cloneTimePtr(message.ClaimExpiresAt),
		LastError:         message.LastError,
		ErrorClass:        message.ErrorClass,
		DeliveredThreadID: message.DeliveredThreadID,
		DeliveredAt:       cloneTimePtr(message.DeliveredAt),
		CreatedAt:         timePtr(message.CreatedAt),
		UpdatedAt:         timePtr(message.UpdatedAt),
	}
}

func copyStringMapPtr(in *map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	return copyStringMap(*in)
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func timePtr(in time.Time) *time.Time {
	if in.IsZero() {
		return nil
	}
	out := in
	return &out
}

func cloneTimePtr(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneIntPtr(in *int) *int {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
