package interaction

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
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
	messageID, err := NewUUID()
	if err != nil {
		return nil, fmt.Errorf("generate inbox message ID: %w", err)
	}
	command.MessageID = messageID
	return enqueuer.Enqueue(ctx, command)
}

// NewUUID returns an Interaction-owned cryptographically random RFC 4122
// version 4 identifier for sessions, leases, terminals, and inbox messages.
func NewUUID() (string, error) {
	return newUUID(rand.Reader)
}

func newUUID(random io.Reader) (string, error) {
	if random == nil {
		return "", fmt.Errorf("generate UUID: random source is required")
	}
	var value [16]byte
	if _, err := io.ReadFull(random, value[:]); err != nil {
		return "", fmt.Errorf("generate UUID: %w", err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80

	var encoded [36]byte
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded[:]), nil
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
