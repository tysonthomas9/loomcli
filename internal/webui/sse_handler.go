package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// SSEWriter centralizes SSE wire-format concerns.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func newSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("ResponseWriter does not implement http.Flusher")
	}
	return &SSEWriter{w: w, flusher: flusher}, nil
}

func (sw *SSEWriter) WriteRetry(ms int) error {
	_, err := fmt.Fprintf(sw.w, "retry: %d\n\n", ms)
	sw.flusher.Flush()
	return err
}

func (sw *SSEWriter) WriteEvent(id int64, event, data string) error {
	_, err := io.WriteString(sw.w, fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", id, event, data))
	sw.flusher.Flush()
	return err
}

func (sw *SSEWriter) WriteComment(text string) error {
	_, err := fmt.Fprintf(sw.w, ": %s\n\n", text)
	sw.flusher.Flush()
	return err
}

// SSEHandler is an http.Handler for the SSE endpoint with configurable heartbeat.
// sseAuth is optional: when non-nil (external auth mode), each connection must
// present a valid short-lived opaque token obtained from the /events/token exchange.
// When nil (open mode), connections are allowed without authentication.
type SSEHandler struct {
	hub               *SSEHub
	getMutationsSince func(wsID string, since int64) []rpc.MutationEvent
	heartbeatInterval time.Duration
	sseAuth           *sseTokenStore
	clientIDCounter   atomic.Int64
}

// NewSSEHandler creates an SSEHandler in open mode (no auth required).
func NewSSEHandler(hub *SSEHub, getMutationsSince func(wsID string, since int64) []rpc.MutationEvent) *SSEHandler {
	return &SSEHandler{
		hub:               hub,
		getMutationsSince: getMutationsSince,
		heartbeatInterval: sseHeartbeatInterval,
	}
}

// NewSSEHandlerWithAuth creates an SSEHandler in external auth mode.
// Connections must present a valid opaque token from the /events/token exchange.
func NewSSEHandlerWithAuth(hub *SSEHub, getMutationsSince func(wsID string, since int64) []rpc.MutationEvent, sseAuth *sseTokenStore) *SSEHandler {
	h := NewSSEHandler(hub, getMutationsSince)
	h.sseAuth = sseAuth
	return h
}

func (h *SSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Handler-level auth: validate opaque token before streaming.
	// Matches the terminalAuth pattern in handleTerminalWS.
	if !validateSSEAuth(w, r, h.sseAuth) {
		return
	}

	clientID := h.clientIDCounter.Add(1)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sw, err := newSSEWriter(w)
	if err != nil {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Error("SSE: failed to disable write deadline", "err", err)
	}

	lastSince := parseLastSince(r)
	sourceRepos := parseSourceRepos(r.URL.Query().Get("source_repos"))
	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		slog.Warn("SSE client connected with empty workspace_id — will not receive mutations (fail-closed)", "client_id", clientID, "remote_addr", r.RemoteAddr)
	}

	client := &SSEClient{
		id:          clientID,
		send:        make(chan *MutationPayload, sseClientSendBuf),
		done:        make(chan struct{}),
		lastSince:   lastSince,
		sourceRepos: sourceRepos,
		workspaceID: workspaceID,
	}

	// Check if shutting down before registering
	select {
	case <-r.Context().Done():
		return
	default:
	}

	h.hub.RegisterClient(client)
	defer func() {
		h.hub.UnregisterClient(client)
		close(client.done)
	}()

	slog.Info("SSE client connected", "client_id", client.id, "remote_addr", r.RemoteAddr, "since", lastSince, "repos", sourceRepos, "workspace_id", workspaceID)

	if err := h.sendCatchUp(sw, lastSince, workspaceID, sourceRepos); err != nil {
		slog.Error("SSE client catch-up write failed", "client_id", client.id, "err", err)
		return
	}
	if err := sw.WriteRetry(sseRetryMs); err != nil {
		slog.Error("SSE client retry write failed", "client_id", client.id, "err", err)
		return
	}
	// Connected event has no id: field — it's a control event, not a mutation
	connFrame := fmt.Sprintf("event: connected\ndata: {\"clientId\":%d}\n\n", client.id)
	if _, err := io.WriteString(sw.w, connFrame); err != nil {
		slog.Error("SSE client connected event write failed", "client_id", client.id, "err", err)
		return
	}
	sw.flusher.Flush()
	h.streamLoop(sw, client, r.Context())
}

func (h *SSEHandler) sendCatchUp(sw *SSEWriter, since int64, workspaceID string, sourceRepos []string) error {
	if since <= 0 || h.getMutationsSince == nil || workspaceID == "" {
		return nil
	}
	for _, m := range h.getMutationsSince(workspaceID, since) {
		payload := rpcMutationToPayload(m)
		payload.WorkspaceID = workspaceID
		if !matchesSourceRepoFilter(sourceRepos, payload.SourceRepo) {
			continue
		}
		if err := writeSSEEvent(sw, payload); err != nil {
			return err
		}
	}
	return nil
}

func (h *SSEHandler) streamLoop(sw *SSEWriter, client *SSEClient, ctx context.Context) {
	interval := h.heartbeatInterval
	if interval == 0 {
		interval = sseHeartbeatInterval
	}
	heartbeatTicker := time.NewTicker(interval)
	defer heartbeatTicker.Stop()
	for {
		select {
		case mutation, ok := <-client.send:
			if !ok {
				return
			}
			if err := writeSSEEvent(sw, mutation); err != nil {
				slog.Error("SSE client write failed", "client_id", client.id, "err", err)
				return
			}
		case <-heartbeatTicker.C:
			if err := sw.WriteComment("heartbeat"); err != nil {
				slog.Error("SSE client heartbeat failed", "client_id", client.id, "err", err)
				return
			}
		case <-ctx.Done():
			slog.Info("SSE client disconnected", "client_id", client.id)
			return
		}
	}
}

func writeSSEEvent(sw *SSEWriter, mutation *MutationPayload) error {
	data, err := json.Marshal(mutation)
	if err != nil {
		slog.Error("SSE marshal error", "err", err)
		return nil // marshal error is not a write error — skip this event
	}
	return sw.WriteEvent(sseEventIDCounter.Add(1), "mutation", string(data))
}

// rpcMutationToPayload converts an RPC mutation event to a payload.
func rpcMutationToPayload(m rpc.MutationEvent) *MutationPayload {
	return &MutationPayload{
		Type:       m.Type,
		IssueID:    m.IssueID,
		Title:      m.Title,
		Assignee:   m.Assignee,
		Actor:      m.Actor,
		Timestamp:  m.Timestamp.UTC().Format(time.RFC3339),
		OldStatus:  m.OldStatus,
		NewStatus:  m.NewStatus,
		ParentID:   m.ParentID,
		StepCount:  m.StepCount,
		SourceRepo: m.SourceRepo,
	}
}
