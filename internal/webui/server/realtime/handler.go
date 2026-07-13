package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// tracerName is the instrumentation library name reported on every span
// emitted from this package. Stable so dashboards filtering on it don't
// break.
const tracerName = "github.com/tysonthomas9/loomcli/internal/webui/server/realtime"

// Disconnect reasons reported on `sse.disconnect` spans. Bounded enum so the
// `disconnect.reason` attribute stays low-cardinality.
const (
	disconnectReasonClientClose = "client_close"
	disconnectReasonServerClose = "server_close"
	disconnectReasonError       = "error"
)

// HandlerConfig configures the SSE Handler.
type HandlerConfig struct {
	Hub               *Hub
	GetMutationsSince func(wsID string, since string) []rpc.MutationEvent
	WorkspaceFromCtx  func(context.Context) string
	TokenStore        *TokenStore // nil = open mode (no auth required)
	// OnAuthenticated runs after the stream request passes handler-level auth.
	OnAuthenticated func(context.Context, string)
}

// Handler is an http.Handler for the SSE endpoint with configurable heartbeat.
type Handler struct {
	hub               *Hub
	getMutationsSince func(wsID string, since string) []rpc.MutationEvent
	heartbeatInterval time.Duration
	tokenStore        *TokenStore
	workspaceFromCtx  func(context.Context) string
	onAuthenticated   func(context.Context, string)
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
		onAuthenticated:   cfg.OnAuthenticated,
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
	if h.onAuthenticated != nil && workspaceID != "" {
		h.onAuthenticated(r.Context(), workspaceID)
	}

	// Short-lived child span covering the SSE handshake — catch-up replay
	// + retry frame + connected event. End BEFORE entering the long-lived
	// streamLoop so we don't keep a multi-minute (or multi-hour) span open
	// in Jaeger. The streamLoop itself is unspanned; per-event spans would
	// flood the collector. See docs/observability/tracing-contract.md §3.
	handshakeCtx, handshakeSpan := otel.Tracer(tracerName).Start(r.Context(), "sse.handshake",
		trace.WithAttributes(
			attribute.String("loom.workspace", workspaceID),
			attribute.String("network.peer.address", r.RemoteAddr),
		),
	)

	client := NewClient(clientID, ClientSendBuf, lastSince, sourceRepos, workspaceID)

	// Check if shutting down before registering
	select {
	case <-r.Context().Done():
		handshakeSpan.SetAttributes(attribute.String("disconnect.reason", disconnectReasonClientClose))
		handshakeSpan.End()
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
		handshakeSpan.RecordError(err)
		handshakeSpan.SetStatus(codes.Error, "network")
		handshakeSpan.End()
		return
	}
	if err := sw.WriteRetry(RetryMs); err != nil {
		slog.Error("SSE client retry write failed", "client_id", client.id, "err", err)
		handshakeSpan.RecordError(err)
		handshakeSpan.SetStatus(codes.Error, "network")
		handshakeSpan.End()
		return
	}
	// Connected event has no id: field -- it's a control event, not a mutation.
	if err := sw.WriteEventNoID("connected", fmt.Sprintf(`{"clientId":%d}`, client.id)); err != nil {
		slog.Error("SSE client connected event write failed", "client_id", client.id, "err", err)
		handshakeSpan.RecordError(err)
		handshakeSpan.SetStatus(codes.Error, "network")
		handshakeSpan.End()
		return
	}
	// Handshake complete — end the span before the long-lived stream loop.
	handshakeSpan.End()
	_ = handshakeCtx

	reason, loopErr := h.streamLoop(sw, client, r.Context())

	// Short-lived sibling span at disconnect so we record duration of the
	// connection (via the gap between handshake.end and disconnect.start)
	// without holding a span open for the lifetime of the stream.
	_, discSpan := otel.Tracer(tracerName).Start(context.Background(), "sse.disconnect",
		trace.WithLinks(trace.LinkFromContext(handshakeCtx)),
		trace.WithAttributes(
			attribute.String("loom.workspace", workspaceID),
			attribute.String("disconnect.reason", reason),
		),
	)
	if loopErr != nil {
		discSpan.RecordError(loopErr)
		discSpan.SetStatus(codes.Error, "network")
	}
	discSpan.End()
}

func (h *Handler) sendCatchUp(sw *Writer, since string, workspaceID string, sourceRepos []string) error {
	if since == "" || h.getMutationsSince == nil || workspaceID == "" {
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

// streamLoop runs the long-lived event pump and returns the disconnect
// reason (one of the bounded disconnectReason* enum values) plus any
// non-cancellation error encountered. The reason is reported on the
// `sse.disconnect` span so dashboards can group disconnects by cause
// without keeping the span open for the connection lifetime.
func (h *Handler) streamLoop(sw *Writer, client *Client, ctx context.Context) (string, error) {
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
				// Hub-side close: client.send was closed by UnregisterClient
				// (server shutdown or hub-driven eviction).
				return disconnectReasonServerClose, nil
			}
			if err := writeSSEEvent(sw, mutation); err != nil {
				slog.Error("SSE client write failed", "client_id", client.id, "err", err)
				return disconnectReasonError, err
			}
		case <-heartbeatTicker.C:
			if err := sw.WriteComment("heartbeat"); err != nil {
				slog.Error("SSE client heartbeat failed", "client_id", client.id, "err", err)
				return disconnectReasonError, err
			}
		case <-ctx.Done():
			slog.Info("SSE client disconnected", "client_id", client.id)
			// Cancellation is the normal close path (browser navigated away
			// or shutdown); per the trace contract §7 it is NOT an error.
			if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				return disconnectReasonError, err
			}
			return disconnectReasonClientClose, nil
		}
	}
}

func writeSSEEvent(sw *Writer, mutation *MutationPayload) error {
	data, err := json.Marshal(mutation)
	if err != nil {
		slog.Error("SSE marshal error", "err", err)
		return nil // marshal error is not a write error -- skip this event
	}
	return sw.WriteEventID(eventIDForMutation(mutation), "mutation", string(data))
}

func eventIDForMutation(mutation *MutationPayload) string {
	if mutation != nil && mutation.Cursor != "" {
		return mutation.Cursor
	}
	if mutation == nil || mutation.Timestamp == "" {
		return fmt.Sprintf("%d", NextEventID())
	}
	ts, err := time.Parse(time.RFC3339Nano, mutation.Timestamp)
	if err != nil {
		return fmt.Sprintf("%d", NextEventID())
	}
	tsMs := ts.UnixMilli()
	for {
		current := eventIDCounter.Load()
		if tsMs <= current {
			return fmt.Sprintf("%d", NextEventID())
		}
		if eventIDCounter.CompareAndSwap(current, tsMs) {
			return fmt.Sprintf("%d", tsMs)
		}
	}
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
		Cursor:     m.Cursor,
		Type:       m.Type,
		EntityType: m.EntityType,
		EntityID:   m.EntityID,
		Action:     m.Action,
		IssueID:    m.IssueID,
		Title:      m.Title,
		Assignee:   m.Assignee,
		Actor:      m.Actor,
		Timestamp:  m.Timestamp.UTC().Format(time.RFC3339Nano),
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
