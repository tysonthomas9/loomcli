// Package agentinbox enqueues a message into a target agent's fleet-db inbox
// (store.AgentInboxMessages) and derives content-hash dedupe keys so a redelivered
// message collapses onto one row. Called by internal/leadcontrol to hand epic
// assignments and replies to a lead, and by internal/driver's outbox dispatcher.
package agentinbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
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

func Enqueue(ctx context.Context, st store.Store, workspace, targetAgentID, body string, opts MessageOptions) (*domain.AgentInboxMessage, error) {
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
	if st == nil || st.AgentInboxMessages() == nil {
		return nil, fmt.Errorf("agent inbox store is not configured: %w", domain.ErrInvalid)
	}
	return st.AgentInboxMessages().Create(ctx, store.AgentInboxMessageCreate{
		WorkspaceKey:      workspace,
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
