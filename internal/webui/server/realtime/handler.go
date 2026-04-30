package realtime

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
)

// HandlerConfig configures the SSE Handler.
type HandlerConfig struct {
	Hub               *Hub
	GetMutationsSince func(wsID string, since int64) []rpc.MutationEvent
	WorkspaceFromCtx  func(context.Context) string
	TokenStore        *TokenStore // nil = open mode (no auth required)
}

// Handler is an http.Handler for the SSE endpoint with configurable heartbeat.
type Handler struct {
	hub               *Hub
	getMutationsSince func(wsID string, since int64) []rpc.MutationEvent
	heartbeatInterval time.Duration
	tokenStore        *TokenStore
	workspaceFromCtx  func(context.Context) string
	clientIDCounter   atomic.Int64
}

// NewHandler creates an SSE Handler from the given config.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		hub:               cfg.Hub,
		getMutationsSince: cfg.GetMutationsSince,
		heartbeatInterval: HeartbeatInterval,
		tokenStore:        cfg.TokenStore,
		workspaceFromCtx:  cfg.WorkspaceFromCtx,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Handler-level auth: validate opaque token before streaming.
	if !h.validateAuth(w, r) {
		return
	}

	clientID := h.clientIDCounter.Add(1)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sw, err := NewWriter(w)
	if err != nil {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Error("SSE: failed to disable write deadline", "err", err)
	}

	lastSince := ParseLastSince(r)
	sourceRepos := ParseSourceRepos(r.URL.Query().Get("source_repos"))
	workspaceID := ""
	if h.workspaceFromCtx != nil {
		workspaceID = h.workspaceFromCtx(r.Context())
	}
	if workspaceID == "" {
		slog.Warn("SSE client connected with empty workspace_id — will not receive mutations (fail-closed)", "client_id", clientID, "remote_addr", r.RemoteAddr)
	}

	client := NewClient(clientID, ClientSendBuf, lastSince, sourceRepos, workspaceID)

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
	if err := sw.WriteRetry(RetryMs); err != nil {
		slog.Error("SSE client retry write failed", "client_id", client.id, "err", err)
		return
	}
	// Connected event has no id: field -- it's a control event, not a mutation
	connFrame := fmt.Sprintf("event: connected\ndata: {\"clientId\":%d}\n\n", client.id)
	if _, err := io.WriteString(sw.W, connFrame); err != nil {
		slog.Error("SSE client connected event write failed", "client_id", client.id, "err", err)
		return
	}
	sw.Flusher.Flush()
	h.streamLoop(sw, client, r.Context())
}

func (h *Handler) sendCatchUp(sw *Writer, since int64, workspaceID string, sourceRepos []string) error {
	if since <= 0 || h.getMutationsSince == nil || workspaceID == "" {
		return nil
	}
	for _, m := range h.getMutationsSince(workspaceID, since) {
		payload := RPCMutationToPayload(m)
		payload.WorkspaceID = workspaceID
		if !MatchesSourceRepoFilter(sourceRepos, payload.SourceRepo) {
			continue
		}
		if err := writeSSEEvent(sw, payload); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) streamLoop(sw *Writer, client *Client, ctx context.Context) {
	interval := h.heartbeatInterval
	if interval == 0 {
		interval = HeartbeatInterval
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

func writeSSEEvent(sw *Writer, mutation *MutationPayload) error {
	data, err := json.Marshal(mutation)
	if err != nil {
		slog.Error("SSE marshal error", "err", err)
		return nil // marshal error is not a write error -- skip this event
	}
	return sw.WriteEvent(eventIDForMutation(mutation), "mutation", string(data))
}

func eventIDForMutation(mutation *MutationPayload) int64 {
	if mutation == nil || mutation.Timestamp == "" {
		return NextEventID()
	}
	ts, err := time.Parse(time.RFC3339Nano, mutation.Timestamp)
	if err != nil {
		return NextEventID()
	}
	return ts.UnixMilli()
}

// validateAuth checks the opaque token from the query parameter when auth
// is required (tokenStore non-nil). Returns true if the request should proceed.
func (h *Handler) validateAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.tokenStore == nil {
		return true
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		jsonError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	expectedWS := ""
	if h.workspaceFromCtx != nil {
		expectedWS = h.workspaceFromCtx(r.Context())
	}
	if _, err := h.tokenStore.Validate(token, expectedWS); err != nil {
		jsonError(w, http.StatusUnauthorized, "invalid or expired token")
		return false
	}
	return true
}

// RPCMutationToPayload converts an RPC mutation event to a payload.
func RPCMutationToPayload(m rpc.MutationEvent) *MutationPayload {
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

// jsonError writes a JSON error response. Minimal helper to avoid importing webui.
func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
