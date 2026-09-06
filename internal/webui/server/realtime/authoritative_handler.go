package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type authoritativeSession struct {
	handler   *Handler
	client    *Client
	writer    frameWriter
	reader    *authoritativeReader
	source    MutationSource
	ctx       context.Context
	fence     string
	passReady bool
	fresh     bool
	principal string
}

// serveAuthoritative uses one source cursor for replay and live reconciliation.
// A fresh head means subscribe-from, not acknowledgment of a query snapshot.
func (h *Handler) serveAuthoritative(w http.ResponseWriter, r *http.Request, client *Client, cursor, principal string, handshake trace.Span) {
	sw, err := h.writerFactory(w)
	if err != nil {
		handshake.End()
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	session := authoritativeSession{handler: h, client: client, writer: sw, ctx: r.Context(), principal: principal}
	if err := session.initialize(cursor); err != nil {
		session.fail(err)
		handshake.End()
		return
	}
	interval := h.heartbeatInterval
	if interval <= 0 {
		interval = HeartbeatInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	if err := session.catchUp(ticker.C); err != nil {
		session.fail(err)
		handshake.End()
		return
	}
	if err := sw.WriteRetry(RetryMs); err != nil {
		handshake.End()
		return
	}
	if err := sw.WriteEventNoID("connected", fmt.Sprintf(`{"clientId":%d}`, client.id)); err != nil {
		handshake.End()
		return
	}
	handshake.End()
	for {
		if err := session.wait(ticker.C, false); err != nil {
			return
		}
		if err := session.catchUp(ticker.C); err != nil {
			session.fail(err)
			return
		}
	}
}

func (s *authoritativeSession) captureFence() error {
	ctx, cancel := context.WithTimeout(s.ctx, s.handler.catchUpTimeout)
	defer cancel()
	page, err := s.source.ReadHead(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if page.Cursor == "" || page.Cursor == "$" || page.HasMore || len(page.Events) != 0 {
		return errors.New("invalid authoritative head")
	}
	if err := validateFrame(&page.Cursor, nil, nil); err != nil {
		return err
	}
	s.fence = page.Cursor
	s.passReady = true
	return nil
}

func (s *authoritativeSession) initialize(cursor string) error {
	if s.client.workspaceID == "" {
		return errors.New("missing workspace")
	}
	if s.handler.openMutationSource == nil {
		return errors.New("bounded authoritative mutation source required")
	}
	ctx, cancel := context.WithTimeout(s.ctx, s.handler.catchUpTimeout)
	source, err := s.handler.openMutationSource(ctx, s.client.workspaceID)
	contextErr := ctx.Err()
	cancel()
	if err != nil {
		return err
	}
	if contextErr != nil {
		return contextErr
	}
	if source == nil {
		return errors.New("authoritative mutation source is nil")
	}
	s.source = source
	if err := s.captureFence(); err != nil {
		return err
	}
	s.fresh = cursor == ""
	if s.fresh {
		cursor = s.fence
	}
	reader, err := newAuthoritativeReader(s.client.workspaceID, cursor, s.client.sourceRepos,
		func(ctx context.Context, _ string, since string, limit int) (backend.MutationPage, error) {
			return s.source.ReadPage(ctx, since, s.fence, limit)
		})
	s.reader = reader
	return err
}

func (s *authoritativeSession) fail(err error) {
	if s.ctx.Err() != nil || isAuthoritativeWriteError(err) {
		return
	}
	reason := "error"
	if errors.Is(err, backend.ErrMutationCursorExpired) {
		reason = "expired"
	}
	payload := struct {
		Reason   string          `json:"reason"`
		Recovery *RecoveryHandle `json:"recovery,omitempty"`
	}{Reason: reason}
	if reason == "expired" && s.principal != "" && s.handler.recoveryRegistry != nil {
		if reader, ok := s.source.(backend.IssueRecoveryBackend); ok {
			if handle, err := s.handler.recoveryRegistry.Register(s.principal, s.client.workspaceID, s.client.sourceRepos, reader); err == nil {
				payload.Recovery = &handle
			}
		}
	}
	data, _ := json.Marshal(payload)
	_ = s.writer.WriteEventNoID("resync", string(data))
}

func (s *authoritativeSession) catchUp(ticks <-chan time.Time) error {
	if !s.passReady {
		if err := s.captureFence(); err != nil {
			return err
		}
	}
	s.reader.through = s.fence
	for {
		ctx, cancel := context.WithTimeout(s.ctx, s.handler.catchUpTimeout)
		more, err := s.reader.readPage(ctx, s.writer, catchUpPageLimit)
		cancel()
		if err != nil {
			return err
		}
		if !more {
			if s.fresh {
				if err := s.writer.WriteEventID(s.reader.cursor, "checkpoint", "{}"); err != nil {
					return &authoritativeWriteError{cause: err}
				}
				s.fresh = false
			}
			s.passReady = false
			return nil
		}
		// Each bounded page yields to control and transient work without skipping.
		if err := s.wait(ticks, true); err != nil {
			return err
		}
	}
}

// wait services hints and control frames. In yielding mode an idle queue returns
// immediately so continuous source pagination can continue on the next turn.
func (s *authoritativeSession) wait(ticks <-chan time.Time, yielding bool) error {
	if _, dropped := s.client.beginResync(); dropped {
		if err := s.writer.WriteEventNoID("resync", `{"reason":"overflow"}`); err != nil {
			return &authoritativeWriteError{cause: err}
		}
	}
	var idle <-chan struct{}
	if yielding {
		ready := make(chan struct{})
		close(ready)
		idle = ready
	}
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-s.handler.hub.done:
		return &authoritativeWriteError{cause: errors.New("hub stopped")}
	case <-s.client.wake:
		return nil
	case mutation, ok := <-s.client.send:
		if !ok {
			return &authoritativeWriteError{cause: errors.New("client closed")}
		}
		if mutation.Cursor != "" {
			return nil
		}
		if err := writeSSEEvent(s.writer, mutation); err != nil {
			return &authoritativeWriteError{cause: err}
		}
	case <-ticks:
		if err := s.writer.WriteComment("heartbeat"); err != nil {
			return &authoritativeWriteError{cause: err}
		}
	case <-idle:
	}
	return nil
}
