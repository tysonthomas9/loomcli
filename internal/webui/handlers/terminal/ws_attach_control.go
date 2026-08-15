package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// replacedReasonServerRestart is the only reason value the server writes.
// The OpenAPI schema declares replaced_reason as a single-value enum, and
// clients cannot set it, so widening it is a deliberate server change.
const replacedReasonServerRestart = "server_restart"

// attachControlMessage is the JSON control frame sent to the client right
// after the scrollback replay. The server sends PTY output exclusively as
// binary frames, so a text frame on this socket is unambiguously control.
type attachControlMessage struct {
	Type           string `json:"type"`
	Reattached     bool   `json:"reattached"`
	Replaced       bool   `json:"replaced"`
	ReplacedAt     string `json:"replaced_at,omitempty"`
	ReplacedReason string `json:"replaced_reason,omitempty"`
}

// attachControlFrame builds the attach control frame. Replaced is true only
// when THIS attach is the replacement; on a reattach the frame still carries
// the stored replaced_at, so a client that joined late learns about the
// replacement without a REST round-trip.
func attachControlFrame(reattached bool, meta *tabmeta.TabMetadata) ([]byte, error) {
	msg := attachControlMessage{Type: "attach", Reattached: reattached}
	if meta != nil && !meta.ReplacedAt.IsZero() {
		msg.Replaced = !reattached
		msg.ReplacedAt = meta.ReplacedAt.UTC().Format(time.RFC3339)
		msg.ReplacedReason = meta.ReplacedReason
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal attach control frame: %w", err)
	}
	return raw, nil
}

// markSessionReplaced decides whether this attach replaced a shell that died
// with a previous server process, and if so persists the marker onto meta and
// into Redis. It reports whether the session was marked.
//
// A fresh tab created inside this server process is NOT a replacement even
// though it also spawns — the same discrimination ptyAttachable makes. Zero
// timestamps on either side mean we cannot tell, so we do not mark.
func markSessionReplaced(
	ctx context.Context,
	store *tabmeta.Store,
	workspace, session string,
	meta *tabmeta.TabMetadata,
	startedAt, now time.Time,
) (bool, error) {
	// A nil store is auth-only/test wiring: there is nothing to persist into,
	// and an unpersisted marker would vanish on the next read, so report not
	// replaced rather than announce something no other client can see.
	if store == nil || meta == nil || startedAt.IsZero() || meta.CreatedAt.IsZero() {
		return false, nil
	}
	if !meta.CreatedAt.Before(startedAt) {
		return false, nil
	}

	slog.Info("terminal session stale across server restart; spawning fresh",
		"session", session, "workspace", workspace, "created_at", meta.CreatedAt)

	// Truncated to the second so the in-memory marker matches the RFC3339 form
	// that lands in Redis and in the control frame.
	meta.ReplacedAt = now.UTC().Truncate(time.Second)
	meta.ReplacedReason = replacedReasonServerRestart

	fields := map[string]string{
		"replaced_at":     meta.ReplacedAt.Format(time.RFC3339),
		"replaced_reason": meta.ReplacedReason,
	}
	if _, err := store.Patch(ctx, workspace, session, fields); err != nil {
		return true, fmt.Errorf("persist session replacement marker: %w", err)
	}
	return true, nil
}
