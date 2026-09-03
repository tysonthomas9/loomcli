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

	catchUpPageLimit       = 100
	defaultCatchUpMaxPages = 10
	defaultCatchUpTimeout  = 5 * time.Second
)

type mutationPageFn func(context.Context, string, string, int) (backend.MutationPage, error)

// HandlerConfig configures the SSE Handler.
type HandlerConfig struct {
	Hub              *Hub
	GetMutationPage  func(context.Context, string, string, int) (backend.MutationPage, error)
	WorkspaceFromCtx func(context.Context) string
	TokenStore       *TokenStore // nil = open mode (no auth required)
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

type preparedMutation struct {
	id      string
	payload *MutationPayload
}

type resyncInstruction struct {
	cursor string
	reason string
}

// Handler is an http.Handler for the SSE endpoint with configurable heartbeat.
type Handler struct {
	hub               *Hub
	getMutationPage   mutationPageFn
	heartbeatInterval time.Duration
	tokenStore        *TokenStore
	workspaceFromCtx  func(context.Context) string
	onAuthenticated   func(context.Context, string) (string, error)
	clientIDCounter   atomic.Int64

	catchUpMaxPages int
	catchUpTimeout  time.Duration
	writerFactory   func(http.ResponseWriter) (frameWriter, error)
}

// NewHandler creates an SSE Handler from the given config.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		hub:               cfg.Hub,
		getMutationPage:   cfg.GetMutationPage,
		heartbeatInterval: HeartbeatInterval,
		tokenStore:        cfg.TokenStore,
		workspaceFromCtx:  cfg.WorkspaceFromCtx,
		onAuthenticated:   cfg.OnAuthenticated,
		catchUpMaxPages:   defaultCatchUpMaxPages,
		catchUpTimeout:    defaultCatchUpTimeout,
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

	catchUp, catchUpCursors, resync, err := h.fetchCatchUp(r.Context(), lastSince, workspaceID, sourceRepos)
	if err != nil {
		slog.Warn("SSE catch-up requires client resync", "workspace", workspaceID, "reason", resync.reason, "err", err)
		handshakeSpan.RecordError(err)
		handshakeSpan.SetAttributes(attribute.String("loom.resync.reason", resync.reason))
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
	if resync != nil {
		if err := sw.WriteResync(resync.cursor, resync.reason); err != nil {
			h.endHandshakeWriteError(handshakeSpan, client, err, "resync")
			return
		}
	} else {
		for _, mutation := range catchUp {
			if err := writePreparedMutation(sw, mutation); err != nil {
				h.endHandshakeWriteError(handshakeSpan, client, err, "catch-up")
				return
			}
		}
	}
	if err := sw.WriteRetry(RetryMs); err != nil {
		h.endHandshakeWriteError(handshakeSpan, client, err, "retry")
		return
	}
	if err := sw.WriteEventNoID("connected", fmt.Sprintf(`{"clientId":%d}`, client.id)); err != nil {
		h.endHandshakeWriteError(handshakeSpan, client, err, "connected")
		return
	}
	handshakeSpan.End()

	reason, loopErr := h.streamLoop(sw, client, r.Context(), catchUpCursors)
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

func (h *Handler) fetchCatchUp(
	requestCtx context.Context,
	since string,
	workspaceID string,
	sourceRepos []string,
) ([]preparedMutation, map[string]struct{}, *resyncInstruction, error) {
	seen := make(map[string]struct{})
	if since == "" || h.getMutationPage == nil || workspaceID == "" {
		return nil, seen, nil, nil
	}
	ctx, cancel := context.WithTimeout(requestCtx, h.catchUpTimeout)
	defer cancel()
	cursor := since
	mutations := make([]preparedMutation, 0, catchUpPageLimit)
	for pageNumber := 1; pageNumber <= h.catchUpMaxPages; pageNumber++ {
		page, err := h.getMutationPage(ctx, workspaceID, cursor, catchUpPageLimit)
		if err != nil {
			reason := "error"
			resyncCursor := since
			if errors.Is(err, backend.ErrMutationCursorExpired) {
				reason = "expired"
				var backendErr *backend.BackendError
				if errors.As(err, &backendErr) && backendErr.Meta["cursor"] != "" {
					resyncCursor = backendErr.Meta["cursor"]
				}
			} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				reason = "cap"
				resyncCursor = cursor
			}
			return mutations, seen, &resyncInstruction{cursor: resyncCursor, reason: reason}, fmt.Errorf("catch-up page %d: %w", pageNumber, err)
		}
		if err := ctx.Err(); err != nil {
			return mutations, seen, &resyncInstruction{cursor: cursor, reason: "cap"}, fmt.Errorf("catch-up exceeded time budget: %w", err)
		}
		if page.Cursor == "" {
			page.Cursor = cursor
		}
		for _, mutation := range page.Events {
			payload := BackendMutationToPayload(mutation, workspaceID)
			if !MatchesSourceRepoFilter(sourceRepos, payload.SourceRepo) {
				continue
			}
			id := eventIDForMutation(payload)
			mutations = append(mutations, preparedMutation{id: id, payload: payload})
			if payload.Cursor != "" {
				seen[payload.Cursor] = struct{}{}
			}
		}
		cursor = page.Cursor
		if !page.HasMore {
			return mutations, seen, nil, nil
		}
		if pageNumber == h.catchUpMaxPages {
			return mutations, seen, &resyncInstruction{cursor: cursor, reason: "cap"}, fmt.Errorf("catch-up exceeded %d pages", h.catchUpMaxPages)
		}
		if err := ctx.Err(); err != nil {
			return mutations, seen, &resyncInstruction{cursor: cursor, reason: "cap"}, fmt.Errorf("catch-up exceeded time budget: %w", err)
		}
	}
	return mutations, seen, &resyncInstruction{cursor: cursor, reason: "cap"}, fmt.Errorf("catch-up exceeded page budget")
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
			if dropped, pending := client.beginResync(); pending {
				closed, err := h.writeOverflowResync(sw, client, mutation, dropped, &resyncSeq)
				if err != nil {
					slog.Error("SSE client resync write failed", "client_id", client.id, "err", err)
					return disconnectReasonError, err
				}
				if closed {
					return disconnectReasonServerClose, nil
				}
				continue
			}
			if mutation.deliverySeq != 0 && mutation.deliverySeq <= resyncSeq {
				continue
			}
			if mutation.Cursor != "" {
				if _, duplicate := catchUpCursors[mutation.Cursor]; duplicate {
					continue
				}
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
			if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				return disconnectReasonError, err
			}
			return disconnectReasonClientClose, nil
		}
	}
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
	if err := sw.WriteResync(highest.cursor, "overflow"); err != nil {
		return closed, err
	}
	if highest.seq > *resyncSeq {
		*resyncSeq = highest.seq
	}
	return closed, nil
}

func writePreparedMutation(sw frameWriter, mutation preparedMutation) error {
	data, err := json.Marshal(mutation.payload)
	if err != nil {
		return nil
	}
	return sw.WriteEventID(mutation.id, "mutation", string(data))
}

func writeSSEEvent(sw frameWriter, mutation *MutationPayload) error {
	data, err := json.Marshal(mutation)
	if err != nil {
		slog.Error("SSE marshal error", "err", err)
		return nil
	}
	return sw.WriteEventID(eventIDForMutation(mutation), "mutation", string(data))
}

func eventIDForMutation(mutation *MutationPayload) string {
	if mutation != nil && mutation.deliveryCursor != "" {
		return mutation.deliveryCursor
	}
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
