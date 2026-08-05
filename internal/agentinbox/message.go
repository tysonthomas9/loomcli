package agentinbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

type MessageOptions struct {
	SessionID         string
	SourceKind        string
	SourceRef         string
	DriverRunID       string
	TaskRunID         string
	TriggerEventID    string
	TriggerDeliveryID string
	DedupeKey         string
}

func Enqueue(
	ctx context.Context,
	enqueuer interaction.InboxEnqueuer,
	workspace,
	targetAgentID,
	body string,
	opts MessageOptions,
) (*domain.AgentInboxMessage, error) {
	workspace = strings.TrimSpace(workspace)
	targetAgentID = strings.TrimSpace(targetAgentID)
	body = strings.TrimSpace(body)
	if workspace == "" {
		return nil, fmt.Errorf("workspace required: %w", domain.ErrInvalid)
	}
	if targetAgentID == "" {
		return nil, fmt.Errorf("target agent required: %w", domain.ErrInvalid)
	}
	if body == "" {
		return nil, fmt.Errorf("message body required: %w", domain.ErrInvalid)
	}
	if enqueuer == nil {
		return nil, fmt.Errorf("interaction inbox commands are not configured: %w", domain.ErrInvalid)
	}
	value, err := enqueuer.Enqueue(ctx, interaction.EnqueueInboxCommand{
		WorkspaceKey:      workspace,
		MessageID:         uuid.NewString(),
		TargetAgentID:     targetAgentID,
		SessionID:         strings.TrimSpace(opts.SessionID),
		Body:              body,
		SourceKind:        strings.TrimSpace(opts.SourceKind),
		SourceRef:         strings.TrimSpace(opts.SourceRef),
		DriverRunID:       strings.TrimSpace(opts.DriverRunID),
		TaskRunID:         strings.TrimSpace(opts.TaskRunID),
		TriggerEventID:    strings.TrimSpace(opts.TriggerEventID),
		TriggerDeliveryID: strings.TrimSpace(opts.TriggerDeliveryID),
		DedupeKey:         strings.TrimSpace(opts.DedupeKey),
	})
	if err != nil {
		return nil, err
	}
	return domainInboxMessage(value), nil
}

func domainInboxMessage(value *interaction.InboxMessage) *domain.AgentInboxMessage {
	if value == nil {
		return nil
	}
	result := &domain.AgentInboxMessage{
		WorkspaceKey: value.WorkspaceKey, InboxMessageID: value.MessageID,
		Cursor: value.Cursor, TargetAgentID: value.TargetAgentID,
		SessionID: value.SessionID, Body: value.Body,
		Status:     domain.AgentInboxMessageStatus(value.Status),
		SourceKind: value.SourceKind, SourceRef: value.SourceRef,
		DriverRunID: value.DriverRunID, TaskRunID: value.TaskRunID,
		TriggerEventID: value.TriggerEventID, TriggerDeliveryID: value.TriggerDeliveryID,
		DedupeKey: value.DedupeKey, Attempt: value.Attempt,
		ClaimedBy: value.ClaimedBy, ClaimExpiresAt: value.ClaimExpiresAt,
		ErrorClass: value.ErrorClass, DeliveredThreadID: value.DeliveredThreadID,
		DeliveredAt: value.DeliveredAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
	return result
}

func ContentDedupeKey(prefix string, parts ...string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "agent-message"
	}
	hash := sha256.New()
	for _, part := range parts {
		hash.Write([]byte(strings.TrimSpace(part)))
		hash.Write([]byte{0})
	}
	return prefix + ":" + hex.EncodeToString(hash.Sum(nil))
}
