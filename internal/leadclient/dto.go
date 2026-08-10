package leadclient

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
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

type heartbeatEnvelope struct {
	Session       sessionResult `json:"session"`
	OccupantToken string        `json:"occupantToken,omitempty"`
}

type sessionResult struct {
	SessionID       string                    `json:"sessionId"`
	AgentID         string                    `json:"agentId"`
	NodeID          string                    `json:"nodeId,omitempty"`
	Kind            domain.AgentSessionKind   `json:"kind,omitempty"`
	TaskID          string                    `json:"taskId,omitempty"`
	TerminalID      string                    `json:"terminalId,omitempty"`
	ParentSessionID string                    `json:"parentSessionId,omitempty"`
	Status          domain.AgentSessionStatus `json:"status,omitempty"`
	Phase           string                    `json:"phase,omitempty"`
	Attempt         int                       `json:"attempt,omitempty"`
	StartedAt       *time.Time                `json:"startedAt,omitempty"`
	LastHeartbeat   time.Time                 `json:"lastHeartbeat"`
	FinishedAt      *time.Time                `json:"finishedAt,omitempty"`
	Summary         string                    `json:"summary,omitempty"`
	ErrorClass      string                    `json:"errorClass,omitempty"`
	ExitCode        *int                      `json:"exitCode,omitempty"`
	Metadata        map[string]string         `json:"metadata,omitempty"`
	CreatedAt       *time.Time                `json:"createdAt,omitempty"`
	UpdatedAt       *time.Time                `json:"updatedAt,omitempty"`
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

func (c *Client) domainSession(result *sessionResult) *domain.AgentSession {
	if result == nil {
		return nil
	}
	return &domain.AgentSession{
		WorkspaceKey:    c.workspaceKey,
		SessionID:       result.SessionID,
		AgentID:         result.AgentID,
		NodeID:          result.NodeID,
		Kind:            result.Kind,
		TaskID:          result.TaskID,
		TerminalID:      result.TerminalID,
		ParentSessionID: result.ParentSessionID,
		Status:          result.Status,
		Phase:           result.Phase,
		Attempt:         result.Attempt,
		StartedAt:       timeValue(result.StartedAt),
		LastHeartbeat:   result.LastHeartbeat,
		FinishedAt:      cloneTimePtr(result.FinishedAt),
		Summary:         result.Summary,
		ErrorClass:      result.ErrorClass,
		ExitCode:        cloneIntPtr(result.ExitCode),
		Metadata:        copyStringMap(result.Metadata),
		CreatedAt:       timeValue(result.CreatedAt),
		UpdatedAt:       timeValue(result.UpdatedAt),
	}
}

func (c *Client) domainAgent(result *agentResult) *domain.Agent {
	if result == nil {
		return nil
	}
	return &domain.Agent{
		WorkspaceKey:     c.workspaceKey,
		Name:             result.Name,
		RoleName:         result.RoleName,
		Auto:             result.Auto,
		Backend:          result.Backend,
		FallbackBackends: append([]string(nil), result.FallbackBackends...),
		RuntimeProvider:  result.RuntimeProvider,
		Repos:            append([]string(nil), result.Repos...),
		RepoGroups:       append([]string(nil), result.RepoGroups...),
		CrossRepo:        result.CrossRepo,
		Parent:           result.Parent,
		State:            result.State,
		Mode:             result.Mode,
		TaskFilter:       result.TaskFilter,
		MaxConcurrency:   result.MaxConcurrency,
		BudgetPolicy:     result.BudgetPolicy,
		DesiredState:     result.DesiredState,
		CreatedAt:        timeValue(result.CreatedAt),
		UpdatedAt:        timeValue(result.UpdatedAt),
	}
}

func (c *Client) domainInboxMessage(result *inboxMessageResult) *domain.AgentInboxMessage {
	if result == nil {
		return nil
	}
	return &domain.AgentInboxMessage{
		WorkspaceKey:      c.workspaceKey,
		InboxMessageID:    result.InboxMessageID,
		Cursor:            result.Cursor,
		TargetAgentID:     result.TargetAgentID,
		SessionID:         result.SessionID,
		Body:              result.Body,
		Status:            result.Status,
		SourceKind:        result.SourceKind,
		SourceRef:         result.SourceRef,
		DriverRunID:       result.DriverRunID,
		TaskRunID:         result.TaskRunID,
		TriggerEventID:    result.TriggerEventID,
		TriggerDeliveryID: result.TriggerDeliveryID,
		DedupeKey:         result.DedupeKey,
		Attempt:           result.Attempt,
		ClaimedBy:         result.ClaimedBy,
		ClaimExpiresAt:    cloneTimePtr(result.ClaimExpiresAt),
		LastError:         result.LastError,
		ErrorClass:        result.ErrorClass,
		DeliveredThreadID: result.DeliveredThreadID,
		DeliveredAt:       cloneTimePtr(result.DeliveredAt),
		CreatedAt:         timeValue(result.CreatedAt),
		UpdatedAt:         timeValue(result.UpdatedAt),
	}
}

func timeValue(in *time.Time) time.Time {
	if in == nil {
		return time.Time{}
	}
	return *in
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
