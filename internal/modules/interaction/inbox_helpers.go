package interaction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/identity"
)

// EnqueueGenerated validates the caller-owned delivery coordinates, assigns a
// new opaque message ID, and submits the command through the narrow runtime
// inbox port.
func EnqueueGenerated(
	ctx context.Context,
	enqueuer InboxEnqueuer,
	command EnqueueInboxCommand,
) (*InboxMessage, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.TargetAgentID = strings.TrimSpace(command.TargetAgentID)
	command.Body = strings.TrimSpace(command.Body)
	if command.WorkspaceKey == "" {
		return nil, fmt.Errorf("workspace required: %w", ErrInvalid)
	}
	if command.TargetAgentID == "" {
		return nil, fmt.Errorf("target agent required: %w", ErrInvalid)
	}
	if command.Body == "" {
		return nil, fmt.Errorf("message body required: %w", ErrInvalid)
	}
	if enqueuer == nil {
		return nil, fmt.Errorf("interaction inbox commands are not configured: %w", ErrUnavailable)
	}
	messageID, err := identity.NewUUID()
	if err != nil {
		return nil, fmt.Errorf("generate inbox message ID: %w", err)
	}
	command.MessageID = messageID
	return enqueuer.Enqueue(ctx, command)
}

// ContentDedupeKey returns a stable content-derived key for one logical inbox
// delivery. It contains no product state and is safe to recompute on retries.
func ContentDedupeKey(prefix string, parts ...string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "agent-message"
	}
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(strings.TrimSpace(part)))
		_, _ = hash.Write([]byte{0})
	}
	return prefix + ":" + hex.EncodeToString(hash.Sum(nil))
}
