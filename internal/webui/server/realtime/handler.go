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

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// tracerName is the stable instrumentation library name for this package.
const tracerName = "github.com/tysonthomas9/loomcli/internal/webui/server/realtime"

const (
	disconnectReasonClientClose = "client_close"
	disconnectReasonServerClose = "server_close"
	disconnectReasonError       = "error"

	catchUpPageLimit      = 100
	defaultCatchUpTimeout = 5 * time.Second
)

type mutationPageFn func(context.Context, string, string, int) (backend.MutationPage, error)
type boundedMutationPageFn func(context.Context, string, string, string, int) (backend.MutationPage, error)

// HandlerConfig configures the SSE Handler.
type HandlerConfig struct {
	Hub                    *Hub
	GetMutationPage        func(context.Context, string, string, int) (backend.MutationPage, error)
	GetMutationPageThrough boundedMutationPageFn
	WorkspaceFromCtx       func(context.Context) string
	TokenStore             *TokenStore // nil = open mode (no auth required)
	// OnAuthenticated activates the workspace subscriber and returns its
	// ready head. It runs only after the client has been synchronously registered.
	OnAuthenticated func(context.Context, string) (string, error)
}

type frameWriter interface {
	WriteRetry(int) error
	WriteEventID(string, string, string) error
	WriteEventNoID(string, string) error
	WriteResync(string, string) error
	WriteComment(string) error
}

// Handler is an http.Handler for the SSE endpoint with configurable heartbeat.
type Handler struct {
	hub                    *Hub
	getMutationPage        mutationPageFn
	getMutationPageThrough boundedMutationPageFn
	heartbeatInterval      time.Duration
	tokenStore             *TokenStore
	workspaceFromCtx       func(context.Context) string
	onAuthenticated        func(context.Context, string) (string, error)
	clientIDCounter        atomic.Int64

	catchUpTimeout time.Duration
	writerFactory  func(http.ResponseWriter) (frameWriter, error)
}

// NewHandler creates an SSE Handler from the given config.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		hub:                    cfg.Hub,
		getMutationPage:        cfg.GetMutationPage,
		getMutationPageThrough: cfg.GetMutationPageThrough,
		heartbeatInterval:      HeartbeatInterval,
		tokenStore:             cfg.TokenStore,
		workspaceFromCtx:       cfg.WorkspaceFromCtx,
		onAuthenticated:        cfg.OnAuthenticated,
		catchUpTimeout:         defaultCatchUpTimeout,
		writerFactory: func(w http.ResponseWriter) (frameWriter, error) {
			return NewWriter(w)
		},
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.validateAuth(w, r) {
		return
	}
	if h.hub == nil {
		jsonError(w, http.StatusServiceUnavailable, "stream_unavailable")
		return
	}

	clientID := h.clientIDCounter.Add(1)
	lastSince := ParseLastSince(r)
	sourceRepos := ParseSourceRepos(r.URL.Query().Get("source_repos"))
	workspaceID := ""
	if h.workspaceFromCtx != nil {
		workspaceID = h.workspaceFromCtx(r.Context())
	}
	if workspaceID == "" {
		slog.Warn("SSE client connected with empty workspace_id — will not receive mutations (fail-closed)",
			"client_id", clientID, "remote_addr", r.RemoteAddr)
	}

	// Keep the handshake span short; the long-lived event loop is represented
	// by a linked disconnect span instead of one multi-hour span.
	handshakeCtx, handshakeSpan := otel.Tracer(tracerName).Start(r.Context(), "sse.handshake",
		trace.WithAttributes(
			attribute.String("loom.workspace", workspaceID),
			attribute.String("network.peer.address", r.RemoteAddr),
		),
	)
	client := NewClient(clientID, ClientSendBuf, lastSince, sourceRepos, workspaceID)
	client.authoritative = h.getMutationPage != nil || h.getMutationPageThrough != nil
	if err := h.hub.RegisterClient(r.Context(), client); err != nil {
		handshakeSpan.RecordError(err)
		handshakeSpan.SetStatus(codes.Error, "registration")
		handshakeSpan.End()
		if r.Context().Err() == nil {
			jsonError(w, http.StatusServiceUnavailable, "stream_unavailable")
		}
		return
	}
	defer func() {
		h.hub.UnregisterClient(client)
		close(client.done)
	}()

	if h.onAuthenticated != nil && workspaceID != "" {
		head, err := h.onAuthenticated(r.Context(), workspaceID)
		if err != nil {
			slog.Warn("SSE subscriber activation failed", "workspace", workspaceID, "err", err)
			handshakeSpan.RecordError(err)
			handshakeSpan.SetStatus(codes.Error, "activation")
			handshakeSpan.End()
			jsonError(w, http.StatusServiceUnavailable, "subscription_unavailable")
			return
		}
		handshakeSpan.SetAttributes(attribute.String("loom.subscription.head", head))
	}

	if client.authoritative {
		h.serveAuthoritative(w, r, client, lastSince, handshakeSpan)
		return
	}

	sw, err := h.writerFactory(w)
	if err != nil {
		handshakeSpan.RecordError(err)
		handshakeSpan.SetStatus(codes.Error, "writer")
		handshakeSpan.End()
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	slog.Info("SSE client connected", "client_id", client.id, "remote_addr", r.RemoteAddr,
		"since", lastSince, "repos", sourceRepos, "workspace_id", workspaceID)
	if err := sw.WriteRetry(RetryMs); err != nil {
		h.endHandshakeWriteError(handshakeSpan, client, err, "retry")
		return
	}
	if err := sw.WriteEventNoID("connected", fmt.Sprintf(`{"clientId":%d}`, client.id)); err != nil {
		h.endHandshakeWriteError(handshakeSpan, client, err, "connected")
		return
	}
	handshakeSpan.End()

	reason, loopErr := h.streamLoop(sw, client, r.Context(), nil)
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

func (h *Handler) endHandshakeWriteError(span trace.Span, client *Client, err error, frame string) {
	slog.Error("SSE client handshake write failed", "client_id", client.id, "frame", frame, "err", err)
	span.RecordError(err)
	span.SetStatus(codes.Error, "network")
	span.End()
}

// streamLoop pumps live events until the client, hub, or network closes. The
// catch-up cursor set remains intact for the whole connection so interleaved
// live events cannot make a later queued duplicate visible.
func (h *Handler) streamLoop(
	sw frameWriter,
	client *Client,
	ctx context.Context,
	catchUpCursors map[string]struct{},
) (string, error) {
	interval := h.heartbeatInterval
	if interval == 0 {
		interval = HeartbeatInterval
	}
	heartbeatTicker := time.NewTicker(interval)
	defer heartbeatTicker.Stop()
	var resyncSeq uint64
	for {
		if dropped, pending := client.beginResync(); pending {
			closed, err := h.writeOverflowResync(sw, client, nil, dropped, &resyncSeq)
			if err != nil {
				slog.Error("SSE client resync write failed", "client_id", client.id, "err", err)
				return disconnectReasonError, err
			}
			if closed {
				return disconnectReasonServerClose, nil
			}
			continue
		}
		select {
		case mutation, ok := <-client.send:
			if !ok {
				return disconnectReasonServerClose, nil
			}
			closed, err := h.writeLiveMutation(sw, client, mutation, catchUpCursors, &resyncSeq)
			if err != nil {
				return disconnectReasonError, err
			}
			if closed {
				return disconnectReasonServerClose, nil
			}
		case <-heartbeatTicker.C:
			if err := sw.WriteComment("heartbeat"); err != nil {
				slog.Error("SSE client heartbeat failed", "client_id", client.id, "err", err)
				return disconnectReasonError, err
			}
		case <-ctx.Done():
			slog.Info("SSE client disconnected", "client_id", client.id)
			if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				return disconnectReasonError, err
			}
			return disconnectReasonClientClose, nil
		}
	}
}

func (h *Handler) writeLiveMutation(
	sw frameWriter,
	client *Client,
	mutation *MutationPayload,
	catchUpCursors map[string]struct{},
	resyncSeq *uint64,
) (bool, error) {
	if dropped, pending := client.beginResync(); pending {
		closed, err := h.writeOverflowResync(sw, client, mutation, dropped, resyncSeq)
		if err != nil {
			slog.Error("SSE client resync write failed", "client_id", client.id, "err", err)
		}
		return closed, err
	}
	if mutation.deliverySeq != 0 && mutation.deliverySeq <= *resyncSeq {
		return false, nil
	}
	if mutation.Cursor != "" {
		if _, duplicate := catchUpCursors[mutation.Cursor]; duplicate {
			return false, nil
		}
	}
	if err := writeSSEEvent(sw, mutation); err != nil {
		slog.Error("SSE client write failed", "client_id", client.id, "err", err)
		return false, err
	}
	return false, nil
}

func (h *Handler) writeOverflowResync(
	sw frameWriter,
	client *Client,
	first *MutationPayload,
	dropped resyncPoint,
	resyncSeq *uint64,
) (bool, error) {
	highest := dropped
	consider := func(mutation *MutationPayload) {
		if mutation != nil && mutation.deliverySeq > highest.seq {
			highest = resyncPoint{seq: mutation.deliverySeq, cursor: mutation.deliveryCursor}
		}
	}
	consider(first)
	closed := false
	for {
		select {
		case mutation, ok := <-client.send:
			if !ok {
				closed = true
				goto drained
			}
			consider(mutation)
		default:
			goto drained
		}
	}

drained:
	var err error
	if highest.cursor == "" {
		// A transient notification cannot authorize a new durable checkpoint.
		// Omit the id field; an explicit empty id would clear resume state.
		err = sw.WriteEventNoID("resync", `{"reason":"overflow"}`)
	} else {
		err = sw.WriteResync(highest.cursor, "overflow")
	}
	if err != nil {
		return closed, err
	}
	if highest.seq > *resyncSeq {
		*resyncSeq = highest.seq
	}
	return closed, nil
}

func writeSSEEvent(sw frameWriter, mutation *MutationPayload) error {
	data, err := json.Marshal(mutation)
	if err != nil {
		slog.Error("SSE marshal error", "err", err)
		return nil
	}
	id := eventIDForMutation(mutation)
	if id == "" {
		return sw.WriteEventNoID("mutation", string(data))
	}
	return sw.WriteEventID(id, "mutation", string(data))
}

// Only durable source cursors may advance the browser resume checkpoint.
func eventIDForMutation(mutation *MutationPayload) string {
	if mutation == nil {
		return ""
	}
	return mutation.Cursor
}

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

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
