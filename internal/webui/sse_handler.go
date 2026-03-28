package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
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
		log.Printf("SSE: failed to disable write deadline: %v", err)
	}

	lastSince := parseLastSince(r)
	sourceRepos := parseSourceRepos(r.URL.Query().Get("source_repos"))
	workspaceID := WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		log.Printf("SSE: WARNING client %d from %s connected with empty workspaceID — will not receive mutations (fail-closed)", clientID, r.RemoteAddr)
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

	log.Printf("SSE client %d connected from %s (since=%d, repos=%v, workspace=%s)", client.id, r.RemoteAddr, lastSince, sourceRepos, workspaceID)

	if err := h.sendCatchUp(sw, lastSince, workspaceID, sourceRepos); err != nil {
		log.Printf("SSE client %d catch-up write failed: %v", client.id, err)
		return
	}
	if err := sw.WriteRetry(sseRetryMs); err != nil {
		log.Printf("SSE client %d retry write failed: %v", client.id, err)
		return
	}
	// Connected event has no id: field — it's a control event, not a mutation
	connFrame := fmt.Sprintf("event: connected\ndata: {\"clientId\":%d}\n\n", client.id)
	if _, err := io.WriteString(sw.w, connFrame); err != nil {
		log.Printf("SSE client %d connected event write failed: %v", client.id, err)
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
				log.Printf("SSE client %d write failed: %v", client.id, err)
				return
			}
		case <-heartbeatTicker.C:
			if err := sw.WriteComment("heartbeat"); err != nil {
				log.Printf("SSE client %d heartbeat failed: %v", client.id, err)
				return
			}
		case <-ctx.Done():
			log.Printf("SSE client %d disconnected", client.id)
			return
		}
	}
}

func writeSSEEvent(sw *SSEWriter, mutation *MutationPayload) error {
	data, err := json.Marshal(mutation)
	if err != nil {
		log.Printf("SSE marshal error: %v", err)
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
