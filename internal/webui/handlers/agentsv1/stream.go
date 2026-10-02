package agentsv1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// The stream's SSE event names. Every saved event and live-only notice is
// one StreamEvent frame with its kind in the data, so a reader needs no list
// of kinds. StreamError is the last frame when the stream ends on an error,
// for example subscriber_lagged; its data is an Error.
const (
	StreamEvent = "event"
	StreamError = "error"
)

// stream serves Subscribe as server-sent events (design v2 §9.1):
// GET …/v1/events?agents=a,b&after=a:12&types=k1,k2&deltas=true. An agent
// with an after cursor replays its committed events after that seq, then
// gets live ones; an agent without one gets new events only. Each saved
// event is one StreamEvent frame with id "<agent_id>:<seq>"; live-only
// notices (delta, feed.gap) carry no id. The data is an Event.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sub := h.subscribe(ctx, w, r)
	if sub == nil {
		return
	}
	sw, err := realtime.NewWriter(w)
	if err != nil {
		handler.RespondError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_ = rc.Flush() // the client sees the stream open; Subscribe already registered
	pump(ctx, sw, sub)
}

// subscribe checks the token, then starts the subscription; on failure it
// writes the error and returns nil.
func (h *Handler) subscribe(ctx context.Context, w http.ResponseWriter, r *http.Request) *loomagent.Subscription {
	ws := middleware.WorkspaceFromContext(r.Context())
	if h.validateToken != nil {
		token := r.URL.Query().Get("token")
		if token == "" {
			handler.RespondError(w, http.StatusUnauthorized, "authentication required")
			return nil
		}
		if _, err := h.validateToken(token, ws); err != nil {
			handler.RespondError(w, http.StatusUnauthorized, "invalid or expired token")
			return nil
		}
	}
	s := h.services(ws)
	if s == nil {
		handler.RespondError(w, http.StatusNotFound, "agent API not available in this workspace")
		return nil
	}
	// The stream is workspace-wide, so an agent bridge, which may see only its
	// own children, never subscribes; a bad or expired bridge token fails.
	if _, ok := bridgeToken(r); ok {
		if c, ok := h.caller(r, s); !ok || c.Kind == "agent" {
			handler.RespondError(w, http.StatusUnauthorized, "invalid agent token")
			return nil
		}
	}
	req, err := subscribeRequest(r)
	for _, id := range req.AgentIDs {
		if _, ok := req.Cursors[id]; !ok && err == nil {
			_, err = s.Get(ctx, id) // Subscribe checks only agents with a cursor
		}
	}
	if err == nil {
		var sub *loomagent.Subscription
		if sub, err = s.Subscribe(ctx, req); err == nil {
			return sub
		}
	}
	writeError(w, err)
	return nil
}

// pump writes sub's events, and a heartbeat comment when idle, until sub
// ends or a write fails. A sub ended by an Agent API error (subscriber_lagged)
// gets a last error frame.
func pump(ctx context.Context, sw *realtime.Writer, sub *loomagent.Subscription) {
	var err error
	beat := time.NewTicker(realtime.HeartbeatInterval)
	defer beat.Stop()
	for {
		select {
		case e, ok := <-sub.C:
			if !ok {
				var ae *loomagent.Error
				if err := sub.Err(); errors.As(err, &ae) {
					_ = sw.WriteEventNoID(StreamError, mustJSON(Error{Error: ae.Message, Code: ae.Code}))
				} else if err != nil && ctx.Err() == nil {
					_ = sw.WriteEventNoID(StreamError, mustJSON(Error{Error: "internal server error"}))
				}
				return
			}
			data := mustJSON(eventOut(e))
			if e.Seq == 0 {
				err = sw.WriteEventNoID(StreamEvent, data)
			} else {
				err = sw.WriteEventID(e.AgentID+":"+strconv.FormatInt(e.Seq, 10), StreamEvent, data)
			}
		case <-beat.C:
			err = sw.WriteComment("ping")
		}
		if err != nil {
			return
		}
	}
}

// subscribeRequest reads agents, after, types and deltas.
func subscribeRequest(r *http.Request) (loomagent.SubscribeRequest, error) {
	q := r.URL.Query()
	req := loomagent.SubscribeRequest{AgentIDs: handler.ParseArrayParam(q, "agents"),
		Kinds: handler.ParseArrayParam(q, "types"), Deltas: q.Get("deltas") == "true", Cursors: map[string]int64{}}
	if len(req.AgentIDs) == 0 {
		return req, service.ErrValidation("agents is required")
	}
	for _, c := range handler.ParseArrayParam(q, "after") {
		i := strings.LastIndexByte(c, ':')
		if i < 0 {
			return req, service.ErrValidation("invalid after: " + c)
		}
		n, err := intParam(c[i+1:])
		if err != nil {
			return req, err
		}
		req.Cursors[c[:i]] = n
	}
	return req, nil
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
